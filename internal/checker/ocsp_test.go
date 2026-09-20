package checker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"
)

type ocspFixture struct {
	now    time.Time
	issuer *x509.Certificate
	key    *ecdsa.PrivateKey
	cert   tls.Certificate
	roots  *x509.CertPool
}

func mintOCSPCert(t *testing.T, template, parent *x509.Certificate, pub any, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func newOCSPFixture(t *testing.T, responders ...string) ocspFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	ca = mintOCSPCert(t, ca, ca, &key.PublicKey, key)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintOCSPCert(t, &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature, OCSPServer: responders}, ca, &leafKey.PublicKey, key)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	// Root deliberately omitted from peer chain: use the verified trust chain.
	return ocspFixture{now, ca, key, tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: leafKey, Leaf: leaf}, roots}
}

func (f ocspFixture) response(t *testing.T, status int) []byte {
	t.Helper()
	der, err := ocsp.CreateResponse(f.issuer, f.issuer, ocsp.Response{Status: status, SerialNumber: f.cert.Leaf.SerialNumber, ThisUpdate: f.now.Add(-time.Minute), NextUpdate: f.now.Add(time.Hour), RevokedAt: f.now.Add(-time.Hour), RevocationReason: ocsp.KeyCompromise}, f.key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestSSLRevocationStapled(t *testing.T) {
	for _, status := range []int{ocsp.Good, ocsp.Revoked, ocsp.Unknown} {
		t.Run(map[int]string{ocsp.Good: "good", ocsp.Revoked: "revoked", ocsp.Unknown: "unknown"}[status], func(t *testing.T) {
			f := newOCSPFixture(t)
			f.cert.OCSPStaple = f.response(t, status)
			c := sslCheckerTrusting(f.roots, f.now)
			res := c.Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
			if status == ocsp.Revoked {
				if res.OK || res.Kind != FailTLS || !strings.Contains(res.Error, "certificate revoked") {
					t.Fatalf("authenticated revoked staple must fail clearly: %+v", res)
				}
			} else if !res.OK {
				t.Fatalf("good or unknown status must not cause outage: %+v", res)
			}
			if !res.CertExpiry.Equal(f.cert.Leaf.NotAfter) {
				t.Fatal("certificate expiry lost")
			}
		})
	}
}

func TestSSLRevocationRejectsInvalidEvidence(t *testing.T) {
	for _, name := range []string{"bad-signature", "wrong-serial", "wrong-issuer-name", "wrong-issuer-key", "expired", "future", "old-without-next", "inverted-window", "delegated-no-eku", "delegated-expired", "delegated-not-yet-valid", "delegated-wrong-key-usage", "delegated-valid", "delegated-wrong-issuer", "wrong-responder", "future-produced", "future-revocation"} {
		t.Run(name, func(t *testing.T) {
			f := newOCSPFixture(t)
			issuer, signer, key := f.issuer, f.issuer, f.key
			template := ocsp.Response{Status: ocsp.Revoked, SerialNumber: f.cert.Leaf.SerialNumber, ThisUpdate: f.now.Add(-time.Minute), NextUpdate: f.now.Add(time.Hour), RevokedAt: f.now.Add(-time.Hour)}
			switch name {
			case "wrong-serial":
				template.SerialNumber = big.NewInt(99)
			case "wrong-issuer-name":
				copy := *issuer
				copy.RawSubject = []byte{0x30, 0}
				issuer = &copy
			case "wrong-issuer-key":
				other := newOCSPFixture(t)
				copy := *issuer
				copy.RawSubjectPublicKeyInfo = other.issuer.RawSubjectPublicKeyInfo
				issuer = &copy
			case "expired":
				template.NextUpdate = f.now.Add(-time.Second)
			case "future-revocation":
				template.RevokedAt = f.now.Add(time.Hour)
			case "future":
				template.ThisUpdate = f.now.Add(time.Hour)
			case "old-without-next":
				template.ThisUpdate = f.now.Add(-48 * time.Hour)
				template.NextUpdate = time.Time{}
			case "inverted-window":
				template.ThisUpdate = f.now.Add(time.Minute)
				template.NextUpdate = f.now
			}
			if strings.HasPrefix(name, "delegated-") {
				var err error
				key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				delegate := &x509.Certificate{SerialNumber: big.NewInt(8), Subject: pkix.Name{CommonName: "responder"}, NotBefore: f.now.Add(-time.Hour), NotAfter: f.now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning}}
				switch name {
				case "delegated-no-eku":
					delegate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
				case "delegated-expired":
					delegate.NotAfter = f.now.Add(-time.Second)
				case "delegated-not-yet-valid":
					delegate.NotBefore = f.now.Add(time.Minute)
				case "delegated-wrong-key-usage":
					delegate.KeyUsage = x509.KeyUsageKeyEncipherment
				}
				signer = mintOCSPCert(t, delegate, f.issuer, &key.PublicKey, f.key)
				if name == "delegated-wrong-issuer" {
					other := newOCSPFixture(t)
					signer = mintOCSPCert(t, delegate, other.issuer, &key.PublicKey, other.key)
				}
				template.Certificate = signer
			}
			der, err := ocsp.CreateResponse(issuer, signer, template, key)
			if err != nil {
				t.Fatal(err)
			}
			if name == "wrong-responder" || name == "future-produced" {
				der = resignOCSP(t, der, key, func(fields []asn1.RawValue) {
					if name == "wrong-responder" {
						fields[0] = asn1.RawValue{Class: 2, Tag: 1, IsCompound: true, Bytes: []byte{0x30, 0}}
					} else {
						raw, err := asn1.MarshalWithParams(f.now.Add(time.Hour), "generalized")
						if err != nil {
							t.Fatal(err)
						}
						fields[1] = asn1.RawValue{FullBytes: raw}
					}
				})
			}
			if name == "bad-signature" {
				der[len(der)-1] ^= 1
			}
			f.cert.OCSPStaple = der
			res := sslCheckerTrusting(f.roots, f.now).Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
			if name == "delegated-valid" {
				if res.OK || !strings.Contains(res.Error, "revoked") {
					t.Fatalf("authorized responder must detect revocation: %+v", res)
				}
			} else if !res.OK {
				t.Fatalf("invalid evidence must soft-fail, not report revocation: %+v", res)
			}
		})
	}
}

// Re-sign altered response data so malformed identity/time fixtures exercise
// those validations rather than merely failing the signature check.
func resignOCSP(t *testing.T, der []byte, key *ecdsa.PrivateKey, edit func([]asn1.RawValue)) []byte {
	t.Helper()
	var envelope struct {
		Status   asn1.Enumerated
		Response struct {
			OID   asn1.ObjectIdentifier
			Bytes []byte
		} `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &envelope); err != nil {
		t.Fatal(err)
	}
	var basic struct {
		Data      asn1.RawValue
		Algorithm pkix.AlgorithmIdentifier
		Signature asn1.BitString
		Certs     []asn1.RawValue `asn1:"explicit,tag:0,optional"`
	}
	if _, err := asn1.Unmarshal(envelope.Response.Bytes, &basic); err != nil {
		t.Fatal(err)
	}
	var fields []asn1.RawValue
	if _, err := asn1.Unmarshal(basic.Data.FullBytes, &fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	raw, err := asn1.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	basic.Data = asn1.RawValue{FullBytes: raw}
	digest := sha256.Sum256(raw)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	basic.Signature = asn1.BitString{Bytes: sig, BitLength: len(sig) * 8}
	envelope.Response.Bytes, err = asn1.Marshal(basic)
	if err != nil {
		t.Fatal(err)
	}
	result, err := asn1.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSSLRevocationActiveFallback(t *testing.T) {
	for _, name := range []string{"absent", "malformed", "unknown", "stale", "good-staple", "revoked-staple", "active-good", "active-unknown", "unavailable", "bad-signature"} {
		t.Run(name, func(t *testing.T) {
			f := newOCSPFixture(t, "http://ocsp.test/status")
			response := f.response(t, ocsp.Revoked)
			wantRevoked := true
			switch name {
			case "malformed":
				f.cert.OCSPStaple = []byte("bad DER")
			case "unknown":
				f.cert.OCSPStaple = f.response(t, ocsp.Unknown)
			case "stale":
				der, err := ocsp.CreateResponse(f.issuer, f.issuer, ocsp.Response{SerialNumber: f.cert.Leaf.SerialNumber, Status: ocsp.Good, ThisUpdate: f.now.Add(-time.Hour), NextUpdate: f.now.Add(-time.Minute)}, f.key)
				if err != nil {
					t.Fatal(err)
				}
				f.cert.OCSPStaple = der
			case "good-staple":
				f.cert.OCSPStaple = f.response(t, ocsp.Good)
				wantRevoked = false
			case "revoked-staple":
				f.cert.OCSPStaple = f.response(t, ocsp.Revoked)
			case "active-good":
				response = f.response(t, ocsp.Good)
				wantRevoked = false
			case "active-unknown":
				response = f.response(t, ocsp.Unknown)
				wantRevoked = false
			case "unavailable":
				wantRevoked = false
			case "bad-signature":
				response[len(response)-1] ^= 1
				wantRevoked = false
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/ocsp-request" || r.Header.Get("Accept") != "application/ocsp-response" {
					t.Errorf("wrong OCSP request: %s %v", r.Method, r.Header)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				request, err := ocsp.ParseRequest(body)
				if err != nil {
					t.Error(err)
					return
				}
				expectedDER, err := ocsp.CreateRequest(f.cert.Leaf, f.issuer, nil)
				if err != nil {
					t.Error(err)
					return
				}
				expected, err := ocsp.ParseRequest(expectedDER)
				if err != nil {
					t.Error(err)
					return
				}
				if request.SerialNumber.Cmp(f.cert.Leaf.SerialNumber) != 0 || !bytes.Equal(request.IssuerKeyHash, expected.IssuerKeyHash) || !bytes.Equal(request.IssuerNameHash, expected.IssuerNameHash) {
					t.Error("request identified wrong certificate")
				}
				if name == "unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/ocsp-response")
				_, _ = w.Write(response)
			}))
			defer server.Close()
			c := sslCheckerTrusting(f.roots, f.now)
			// Only the socket destination changes; HTTP framing and OCSP validation
			// are real. Production private-address protection has separate tests.
			transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			c.ocspClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			res := c.Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
			if wantRevoked {
				if res.OK || res.Kind != FailTLS || !strings.Contains(res.Error, "revoked") {
					t.Errorf("revocation must be detected: %+v", res)
				}
			} else if !res.OK {
				t.Errorf("uncertainty must not cause outage: %+v", res)
			}
			wantCalls := int32(1)
			if name == "good-staple" || name == "revoked-staple" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Errorf("responder calls=%d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestOCSPFreshnessBoundaries(t *testing.T) {
	f := newOCSPFixture(t)
	for _, tc := range []struct {
		name string
		age  time.Duration
		next time.Time
		want int
	}{
		{"fresh-without-next", 24 * time.Hour, time.Time{}, ocsp.Revoked},
		{"stale-without-next", 24*time.Hour + time.Second, time.Time{}, ocsp.Unknown},
		{"seven-days", 7 * 24 * time.Hour, f.now.Add(time.Hour), ocsp.Revoked},
		{"older-than-seven-days", 7*24*time.Hour + time.Second, f.now.Add(time.Hour), ocsp.Unknown},
		{"next-update-boundary", time.Minute, f.now, ocsp.Unknown},
		{"within-clock-skew", -5 * time.Minute, f.now.Add(time.Hour), ocsp.Revoked},
		{"beyond-clock-skew", -5*time.Minute - time.Second, f.now.Add(time.Hour), ocsp.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			der, err := ocsp.CreateResponse(f.issuer, f.issuer, ocsp.Response{SerialNumber: f.cert.Leaf.SerialNumber, Status: ocsp.Revoked, ThisUpdate: f.now.Add(-tc.age), NextUpdate: tc.next, RevokedAt: f.now.Add(-time.Hour)}, f.key)
			if err != nil {
				t.Fatal(err)
			}
			if got := authenticatedOCSP(der, f.cert.Leaf, f.issuer, f.now); got != tc.want {
				t.Fatalf("status=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestOCSPResponderKeyIdentity(t *testing.T) {
	f := newOCSPFixture(t)
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "wrong"}[valid], func(t *testing.T) {
			var spki struct {
				Algorithm pkix.AlgorithmIdentifier
				Key       asn1.BitString
			}
			if _, err := asn1.Unmarshal(f.issuer.RawSubjectPublicKeyInfo, &spki); err != nil {
				t.Fatal(err)
			}
			hash := sha1.Sum(spki.Key.RightAlign())
			if !valid {
				hash[0] ^= 1
			}
			keyHash, err := asn1.Marshal(hash[:])
			if err != nil {
				t.Fatal(err)
			}
			der := resignOCSP(t, f.response(t, ocsp.Revoked), f.key, func(fields []asn1.RawValue) {
				fields[0] = asn1.RawValue{Class: 2, Tag: 2, IsCompound: true, Bytes: keyHash}
			})
			want := ocsp.Unknown
			if valid {
				want = ocsp.Revoked
			}
			if got := authenticatedOCSP(der, f.cert.Leaf, f.issuer, f.now); got != want {
				t.Fatalf("status=%d, want %d", got, want)
			}
		})
	}
}

func TestOCSPVerifiedIssuerIgnoresUnrelatedPeerCertificate(t *testing.T) {
	f := newOCSPFixture(t)
	unrelated := newOCSPFixture(t)
	f.cert.Certificate = append(f.cert.Certificate, unrelated.issuer.Raw)
	f.cert.OCSPStaple = f.response(t, ocsp.Revoked)
	result := sslCheckerTrusting(f.roots, f.now).Check(t.Context(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
	if result.OK || !strings.Contains(result.Error, "certificate revoked") {
		t.Fatalf("issuer must come from verified chain, not peer order: %+v", result)
	}
}

func TestOCSPUntrustedChainDoesNotFetch(t *testing.T) {
	f := newOCSPFixture(t, "http://ocsp.test/")
	c := sslCheckerTrusting(x509.NewCertPool(), f.now)
	c.ocspClient.Transport = ocspRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("untrusted certificate caused active lookup")
		return nil, fmt.Errorf("unexpected request")
	})
	result := c.Check(t.Context(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
	if result.OK || result.Kind != FailTLS {
		t.Fatalf("untrusted chain must still fail: %+v", result)
	}
}

func TestOCSPEmbeddedIssuingIntermediate(t *testing.T) {
	f := newOCSPFixture(t)
	root := newOCSPFixture(t)
	intermediate := *f.issuer
	intermediate.SerialNumber = big.NewInt(71)
	f.issuer = mintOCSPCert(t, &intermediate, root.issuer, &f.key.PublicKey, root.key)
	f.roots = root.roots
	f.cert.Certificate = append(f.cert.Certificate, f.issuer.Raw)
	der, err := ocsp.CreateResponse(f.issuer, f.issuer, ocsp.Response{Status: ocsp.Revoked, SerialNumber: f.cert.Leaf.SerialNumber, ThisUpdate: f.now.Add(-time.Minute), NextUpdate: f.now.Add(time.Hour), RevokedAt: f.now.Add(-time.Hour), Certificate: f.issuer}, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.cert.OCSPStaple = der
	result := sslCheckerTrusting(f.roots, f.now).Check(t.Context(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
	if result.OK || !strings.Contains(result.Error, "certificate revoked") {
		t.Fatalf("issuer-signed response embedding intermediate must detect revocation: %+v", result)
	}
}
