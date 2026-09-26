package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// This file is a minimal AWS Signature Version 4 signer for S3 requests.
//
// # Why not an SDK
//
// SubGlance ships as one static binary, and its size is part of what it
// promises. The AWS SDK brings dozens of packages to sign four kinds of
// request; minio-go is lighter but still a dependency tree for the same four.
// Signing is a few HMACs over a canonical form of the request, the form is
// specified exactly, and AWS publishes worked examples to test against — see
// sigv4_test.go, which checks this code byte for byte against them.

const (
	sigAlgorithm = "AWS4-HMAC-SHA256"
	amzDate      = "20060102T150405Z"
	scopeDate    = "20060102"

	// emptySHA256 is the payload hash of a request with no body, which is
	// every request here except the upload.
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// credentials are the static keys a request is signed with.
type credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// sign adds the x-amz-date, x-amz-content-sha256 and Authorization headers to
// req. payloadHash is the lowercase hex SHA-256 of the body.
//
// Every header already on the request is signed, plus Host. That is the
// simplest rule that is also safe: a header that is sent but not signed can be
// altered in transit, and the only headers set here are ones that matter.
// Headers the transport adds later (User-Agent, Accept-Encoding,
// Content-Length) are not in req.Header yet, so they are not signed, which is
// what S3 expects.
func sign(req *http.Request, creds credentials, region string, payloadHash string, now time.Time) {
	now = now.UTC()
	req.Header.Set("X-Amz-Date", now.Format(amzDate))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	names, canonHeaders := canonicalHeaders(req)
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		canonicalPath(req.URL.Path),
		canonicalQuery(req.URL.Query()),
		canonHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := now.Format(scopeDate) + "/" + region + "/s3/aws4_request"
	stringToSign := sigAlgorithm + "\n" + now.Format(amzDate) + "\n" + scope + "\n" + hexSHA256([]byte(canonical))

	key := hmacSHA256([]byte("AWS4"+creds.SecretAccessKey), now.Format(scopeDate))
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", sigAlgorithm+
		" Credential="+creds.AccessKeyID+"/"+scope+
		",SignedHeaders="+signedHeaders+
		",Signature="+signature)
}

// canonicalHeaders returns the sorted, lowercased header names to sign and
// their canonical block: one "name:value\n" line per header, values trimmed
// and inner whitespace runs collapsed, as the specification asks.
func canonicalHeaders(req *http.Request) ([]string, string) {
	values := map[string]string{}
	for name, vv := range req.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" {
			continue
		}
		trimmed := make([]string, len(vv))
		for i, v := range vv {
			trimmed[i] = strings.Join(strings.Fields(v), " ")
		}
		values[lower] = strings.Join(trimmed, ",")
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values["host"] = host

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(values[name])
		b.WriteByte('\n')
	}
	return names, b.String()
}

// canonicalPath URI-encodes each segment of the path and keeps the slashes.
// S3 is the one AWS service that does not encode the path a second time.
func canonicalPath(path string) string {
	if path == "" {
		return "/"
	}
	return uriEncode(path, false)
}

// canonicalQuery sorts the parameters by name and encodes both halves.
func canonicalQuery(q map[string][]string) string {
	if len(q) == 0 {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for k, vv := range q {
		for _, v := range vv {
			pairs = append(pairs, pair{uriEncode(k, true), uriEncode(v, true)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

// uriEncode percent-encodes everything except the unreserved characters. A
// slash is kept as-is in a path and encoded in a query value.
//
// url.PathEscape and url.QueryEscape are close but not this: QueryEscape turns
// a space into "+", and PathEscape leaves characters such as "$" and ":"
// alone. Either difference produces a signature S3 rejects.
func uriEncode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
