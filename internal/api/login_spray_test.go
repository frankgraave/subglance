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

	// One host, one connection, a fresh forwarded address per request.
	assertSprayStoppedByIPKey(t, srv.Handler(), "203.0.113.9:40000", "", func(i int) string {
		return "198.51.100." + strconv.Itoa(i%250+1)
	}, "a forwarded header from an untrusted peer must not reset the limiter")
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

	assertSprayStoppedByIPKey(t, srv.Handler(), "127.0.0.1:40000", "", func(i int) string {
		return "198.51.100." + strconv.Itoa(i%250+1) + ", 203.0.113.9"
	}, "the address the client wrote must not pick the limiter's bucket")
}

// TestCredentialSprayingBehindARealIPProxyIsLimited is the spray from behind
// a trusted proxy that sets X-Real-Ip and leaves X-Forwarded-For alone, as
// nginx does with only `proxy_set_header X-Real-IP $remote_addr;`. The
// visitor's own X-Forwarded-For then arrives untouched beside the real
// address, and a chain that always wins hands the visitor its bucket.
func TestCredentialSprayingBehindARealIPProxyIsLimited(t *testing.T) {
	srv, _ := testServerWithDB(t)
	if _, err := srv.WithTrustedProxies("127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	assertSprayStoppedByIPKey(t, srv.Handler(), "127.0.0.1:40000", "203.0.113.9", func(i int) string {
		return "198.51.100." + strconv.Itoa(i%250+1)
	}, "an X-Forwarded-For the proxy did not write must not pick the limiter's bucket")
}

// assertSprayStoppedByIPKey sends maxLoginAttempts failed sign-ins, each for a
// different account and with the X-Forwarded-For value xff(i) (and realIP as
// X-Real-Ip when it is not empty), and then one more. Every account is fresh, so only the per-IP key can refuse the last
// one: the spray holds that key when the client address does not follow the
// forwarded value.
//
// The count stays far below loginFloodBurst on purpose. The global flood
// limiter runs first in handleLogin and answers with its own 429, so a spray
// long enough to reach it would pass whether or not the IP key ever filled.
// The Retry-After of 900 tells the keyed limiter's refusal apart from the
// flood limiter's 1.
func assertSprayStoppedByIPKey(t *testing.T, h http.Handler, remoteAddr, realIP string, xff func(int) string, why string) {
	t.Helper()
	if maxLoginAttempts+1 >= loginFloodBurst {
		t.Fatalf("the spray of %d attempts would reach the flood burst of %v", maxLoginAttempts+1, loginFloodBurst)
	}

	for i := range maxLoginAttempts + 1 {
		req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf(`{"email":"victim%d@example.com","password":"wrong-password-entirely"}`, i))
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Forwarded-For", xff(i))
		if realIP != "" {
			req.Header.Set("X-Real-Ip", realIP)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if i < maxLoginAttempts {
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: status %d, want %d before the IP key is full",
					i+1, rec.Code, http.StatusUnauthorized)
			}
			continue
		}
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" {
			t.Errorf("attempt %d from one host: status %d, Retry-After %q, want %d with the keyed limiter's 900; %s",
				i+1, rec.Code, rec.Header().Get("Retry-After"), http.StatusTooManyRequests, why)
		}
	}
}
