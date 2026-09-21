package checker

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- RFC 6960 section 4.2.1 mandates SHA-1 for the responder byKey identifier.
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"golang.org/x/crypto/ocsp"
)

const (
	maxOCSPResponseBytes = 64 << 10
	ocspLookupTimeout    = 2 * time.Second
	ocspAttemptTimeout   = time.Second
	maxOCSPResponders    = 3
)

func newOCSPClient() *http.Client {
	// A certificate is not operator input. Never inherit the monitor's private
	// target opt-in, environment proxies or credentials, or follow redirects.
	dialer := &net.Dialer{Control: NewGuard(false).ControlFunc()}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:            dialer.DialContext,
			DisableKeepAlives:      true,
			DisableCompression:     true,
			MaxResponseHeaderBytes: 16 << 10,
			TLSHandshakeTimeout:    ocspAttemptTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *SSLChecker) revocationStatus(ctx context.Context, staple []byte, leaf, issuer *x509.Certificate) int {
	if status := authenticatedOCSP(staple, leaf, issuer, c.now()); status != ocsp.Unknown {
		return status
	}
	ctx, cancel := context.WithTimeout(ctx, ocspLookupTimeout)
	defer cancel()
	request, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		return ocsp.Unknown
	}
	for i, rawURL := range leaf.OCSPServer {
		if i >= maxOCSPResponders || ctx.Err() != nil {
			break
		}
		der := c.fetchOCSP(ctx, rawURL, request)
		if status := authenticatedOCSP(der, leaf, issuer, c.now()); status != ocsp.Unknown {
			return status
		}
	}
	return ocsp.Unknown
}

func (c *SSLChecker) fetchOCSP(ctx context.Context, rawURL string, request []byte) []byte {
	if len(rawURL) > 2048 {
		return nil
	}
	target, err := url.Parse(rawURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ocspAttemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(request))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/ocsp-request")
	req.Header.Set("Accept", "application/ocsp-response")
	response, err := c.ocspClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxOCSPResponseBytes {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/ocsp-response" {
		return nil
	}
	der, err := io.ReadAll(io.LimitReader(response.Body, maxOCSPResponseBytes+1))
	if err != nil || len(der) > maxOCSPResponseBytes {
		return nil
	}
	return der
}

// authenticatedOCSP returns Unknown for missing, untrusted or stale evidence.
// ParseResponseForCert selects the serial and checks embedded signatures.
// Explicit verification below also supports an issuer-signed response carrying
// the issuing intermediate itself (which is not signed by its own key).
// Issuer identity, signer authorization and freshness follow RFC 6960 §3.2.
func authenticatedOCSP(der []byte, leaf, issuer *x509.Certificate, now time.Time) int {
	if len(der) == 0 || len(der) > maxOCSPResponseBytes {
		return ocsp.Unknown
	}
	response, err := ocsp.ParseResponseForCert(der, leaf, nil)
	if err != nil {
		return ocsp.Unknown
	}

	signer := issuer
	if response.Certificate != nil {
		signer = response.Certificate
		if !bytes.Equal(signer.Raw, issuer.Raw) {
			if err := signer.CheckSignatureFrom(issuer); err != nil {
				return ocsp.Unknown
			}
			if !slices.Contains(signer.ExtKeyUsage, x509.ExtKeyUsageOCSPSigning) ||
				(signer.KeyUsage != 0 && signer.KeyUsage&x509.KeyUsageDigitalSignature == 0) ||
				len(signer.UnhandledCriticalExtensions) > 0 ||
				now.Before(signer.NotBefore) || now.After(signer.NotAfter) ||
				response.ProducedAt.Before(signer.NotBefore) || response.ProducedAt.After(signer.NotAfter) {
				return ocsp.Unknown
			}
		}
	}
	if err := response.CheckSignatureFrom(signer); err != nil {
		return ocsp.Unknown
	}
	if len(response.RawResponderName) > 0 {
		if !bytes.Equal(response.RawResponderName, signer.RawSubject) {
			return ocsp.Unknown
		}
	} else {
		key, ok := ocspPublicKey(signer)
		if !ok {
			return ocsp.Unknown
		}
		// x/crypto/ocsp parses this identifier but does not authenticate it.
		// The signer and response signature were independently verified above.
		hash := sha1.Sum(key) // #nosec G401 -- RFC 6960 byKey identifier, not signature security.
		if !bytes.Equal(response.ResponderKeyHash, hash[:]) {
			return ocsp.Unknown
		}
	}

	// Permit five minutes of positive clock skew, but never extend nextUpdate.
	// Responses without nextUpdate get one day; all others at most seven days.
	const skew = 5 * time.Minute
	maxAge := 7 * 24 * time.Hour
	if response.NextUpdate.IsZero() {
		maxAge = 24 * time.Hour
	} else if !response.NextUpdate.After(now) || response.NextUpdate.Before(response.ThisUpdate) {
		return ocsp.Unknown
	}
	if response.ThisUpdate.IsZero() || response.ProducedAt.IsZero() ||
		response.ThisUpdate.After(now.Add(skew)) || response.ProducedAt.After(now.Add(skew)) ||
		now.Sub(response.ThisUpdate) > maxAge {
		return ocsp.Unknown
	}
	if response.Status == ocsp.Revoked && (response.RevokedAt.IsZero() || response.RevokedAt.After(now.Add(skew))) {
		return ocsp.Unknown
	}

	// x/crypto does not expose or compare the CertID issuer hashes. Decode only
	// the signed identity fields here, after the library has checked the DER.
	var data ocspResponseIdentity
	if rest, err := asn1.Unmarshal(response.TBSResponseData, &data); err != nil || len(rest) != 0 || data.Version != 0 {
		return ocsp.Unknown
	}
	for _, ext := range data.Extensions {
		if ext.Critical {
			return ocsp.Unknown
		}
	}
	requestDER, err := ocsp.CreateRequest(leaf, issuer, &ocsp.RequestOptions{Hash: response.IssuerHash})
	if err != nil {
		return ocsp.Unknown
	}
	request, err := ocsp.ParseRequest(requestDER)
	if err != nil {
		return ocsp.Unknown
	}
	for _, single := range data.Responses {
		if single.ID.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
			if !bytes.Equal(single.ID.NameHash, request.IssuerNameHash) || !bytes.Equal(single.ID.KeyHash, request.IssuerKeyHash) {
				return ocsp.Unknown
			}
			return response.Status
		}
	}
	return ocsp.Unknown
}

type ocspResponseIdentity struct {
	Version    int `asn1:"optional,default:0,explicit,tag:0"`
	Responder  asn1.RawValue
	ProducedAt time.Time `asn1:"generalized"`
	Responses  []struct {
		ID struct {
			Algorithm    pkix.AlgorithmIdentifier
			NameHash     []byte
			KeyHash      []byte
			SerialNumber *big.Int
		}
	}
	Extensions []pkix.Extension `asn1:"optional,explicit,tag:1"`
}

func ocspPublicKey(cert *x509.Certificate) ([]byte, bool) {
	var info struct {
		Algorithm pkix.AlgorithmIdentifier
		Key       asn1.BitString
	}
	rest, err := asn1.Unmarshal(cert.RawSubjectPublicKeyInfo, &info)
	return info.Key.RightAlign(), err == nil && len(rest) == 0
}
