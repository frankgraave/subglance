package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestCredentialSprayingFromOneSourceIsLimited reproduces the X-Forwarded-For
// bypass.
//
// The login limiter keys on the email and on the client IP. When the client IP
// is taken from a header the caller controls, one host can rotate it per
// request and spray as many accounts as it likes without ever filling a bucket.
func TestCredentialSprayingFromOneSourceIsLimited(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := srv.Handler()

	const attempts = 40

	var lastCode int
	blocked := 0
	for i := range attempts {
		req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf(`{"email":"victim%d@example.com","password":"wrong-password-entirely"}`, i))
		// One host, one connection, a fresh forwarded address per request.
		req.RemoteAddr = "203.0.113.9:40000"
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(i%250+1))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		lastCode = rec.Code
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}

	if blocked == 0 {
		t.Errorf("%d spray attempts from one host were all served (last status %d); "+
			"a forwarded header from an untrusted peer must not reset the limiter",
			attempts, lastCode)
	}
}

// TestCredentialSprayingBehindAnAppendingProxyIsLimited is the same spray
// from behind a trusted proxy that appends to X-Forwarded-For, as nginx's
// $proxy_add_x_forwarded_for and HAProxy's forwardfor do. The client writes
// the first entry and the proxy adds the real address after it, so a limiter
// that reads the first entry gives one host a fresh bucket per request.
func TestCredentialSprayingBehindAnAppendingProxyIsLimited(t *testing.T) {
	srv, _ := testServerWithDB(t)
	if _, err := srv.WithTrustedProxies("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	const attempts = 40

	var lastCode int
	blocked := 0
	for i := range attempts {
		req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf(`{"email":"victim%d@example.com","password":"wrong-password-entirely"}`, i))
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(i%250+1)+", 203.0.113.9")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		lastCode = rec.Code
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}

	if blocked == 0 {
		t.Errorf("%d spray attempts from one host behind an appending proxy were all served "+
			"(last status %d); the address the client wrote must not pick the limiter's bucket",
			attempts, lastCode)
	}
}
