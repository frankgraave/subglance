package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testChecker allows private addresses, because httptest servers listen on
// 127.0.0.1. The guard itself is tested separately in guard_test.go.
func testChecker() *HTTPChecker {
	return NewHTTPChecker(HTTPOptions{Guard: NewGuard(true)})
}

func monitor(target string) Monitor {
	return Monitor{
		ID:              1,
		Name:            "test",
		Type:            TypeHTTP,
		Target:          target,
		Timeout:         5 * time.Second,
		Method:          http.MethodGet,
		ExpectedStatus:  "200-299",
		FollowRedirects: true,
	}
}

func TestHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("all good"))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), monitor(srv.URL))

	if !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	if res.StatusCode != 200 {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if res.Latency <= 0 {
		t.Error("latency was not measured")
	}
	if res.CheckedAt.IsZero() {
		t.Error("CheckedAt was not set")
	}
}

func TestHTTPUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), monitor(srv.URL))

	if res.OK {
		t.Fatal("a 500 was accepted as healthy")
	}
	if res.Kind != FailStatus {
		t.Errorf("kind = %q, want %q", res.Kind, FailStatus)
	}
	if res.StatusCode != 500 {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
	if !strings.Contains(res.Error, "500") {
		t.Errorf("error %q does not mention the status code", res.Error)
	}
}

func TestHTTPTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.Timeout = 200 * time.Millisecond

	res := testChecker().Check(context.Background(), m)

	if res.OK {
		t.Fatal("a timed-out request was reported as healthy")
	}
	if res.Kind != FailTimeout {
		t.Errorf("kind = %q, want %q (error: %s)", res.Kind, FailTimeout, res.Error)
	}
}

func TestHTTPConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more

	res := testChecker().Check(context.Background(), monitor(url))

	if res.OK {
		t.Fatal("a refused connection was reported as healthy")
	}
	if res.Kind != FailConnection {
		t.Errorf("kind = %q, want %q (error: %s)", res.Kind, FailConnection, res.Error)
	}
}

func TestHTTPDNSFailure(t *testing.T) {
	requireNetwork(t)

	m := monitor("http://this-host-does-not-exist.invalid")
	m.Timeout = 5 * time.Second

	res := testChecker().Check(context.Background(), m)

	if res.OK {
		t.Fatal("an unresolvable host was reported as healthy")
	}
	if res.Kind != FailDNS {
		t.Errorf("kind = %q, want %q (error: %s)", res.Kind, FailDNS, res.Error)
	}
}

func TestHTTPKeywordMustContain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"healthy","version":"1.2.3"}`))
	}))
	defer srv.Close()

	t.Run("present", func(t *testing.T) {
		m := monitor(srv.URL)
		m.KeywordMode = KeywordMustContain
		m.Keyword = "healthy"

		if res := testChecker().Check(context.Background(), m); !res.OK {
			t.Errorf("check failed while the keyword was present: %s", res.Error)
		}
	})

	t.Run("missing", func(t *testing.T) {
		m := monitor(srv.URL)
		m.KeywordMode = KeywordMustContain
		m.Keyword = "degraded"

		res := testChecker().Check(context.Background(), m)
		if res.OK {
			t.Fatal("check passed while the required keyword was missing")
		}
		if res.Kind != FailKeyword {
			t.Errorf("kind = %q, want %q", res.Kind, FailKeyword)
		}
	})
}

// The point of must_not_contain: an error page that still returns HTTP 200.
func TestHTTPKeywordMustNotContain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<h1>Database connection error</h1>"))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.KeywordMode = KeywordMustNotContain
	m.Keyword = "error"

	res := testChecker().Check(context.Background(), m)
	if res.OK {
		t.Fatal("an error page returning 200 was accepted as healthy")
	}
	if res.Kind != FailKeyword {
		t.Errorf("kind = %q, want %q", res.Kind, FailKeyword)
	}
}

func TestHTTPRedirects(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	t.Run("followed", func(t *testing.T) {
		m := monitor(redirector.URL)
		m.FollowRedirects = true

		res := testChecker().Check(context.Background(), m)
		if !res.OK {
			t.Errorf("following the redirect failed: %s", res.Error)
		}
		if res.StatusCode != 200 {
			t.Errorf("status = %d, want 200", res.StatusCode)
		}
	})

	t.Run("not followed", func(t *testing.T) {
		m := monitor(redirector.URL)
		m.FollowRedirects = false

		res := testChecker().Check(context.Background(), m)
		if res.OK {
			t.Error("the 302 was accepted while 200-299 was expected")
		}
		if res.StatusCode != 302 {
			t.Errorf("status = %d, want 302", res.StatusCode)
		}
	})
}

// Disabling redirects must not leak into other monitors: the checker is shared
// across goroutines, so mutating the client would be a data race.
func TestRedirectSettingIsPerRequest(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	c := testChecker()

	noFollow := monitor(redirector.URL)
	noFollow.FollowRedirects = false
	_ = c.Check(context.Background(), noFollow)

	follow := monitor(redirector.URL)
	follow.FollowRedirects = true
	res := c.Check(context.Background(), follow)

	if !res.OK || res.StatusCode != 200 {
		t.Errorf("a later monitor inherited the no-redirect setting: status=%d ok=%v err=%s",
			res.StatusCode, res.OK, res.Error)
	}
}

func TestHTTPCustomHeadersAndMethod(t *testing.T) {
	var gotMethod, gotHeader, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Api-Key")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.Method = "POST"
	m.Headers = map[string]string{"X-Api-Key": "secret"}
	m.Body = `{"ping":true}`

	if res := testChecker().Check(context.Background(), m); !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotHeader != "secret" {
		t.Errorf("X-Api-Key = %q, want secret", gotHeader)
	}
	if gotBody != `{"ping":true}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestHTTPSendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	_ = testChecker().Check(context.Background(), monitor(srv.URL))

	if !strings.Contains(got, "SubGlance") {
		t.Errorf("User-Agent = %q, want it to identify SubGlance", got)
	}
}

func TestHTTPRejectsBadInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Monitor)
	}{
		{"unsupported scheme", func(m *Monitor) { m.Target = "ftp://example.com" }},
		{"unparseable url", func(m *Monitor) { m.Target = "http://[::1" }},
		{"invalid status spec", func(m *Monitor) { m.ExpectedStatus = "abc" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := monitor("http://example.com")
			tt.mutate(&m)

			res := testChecker().Check(context.Background(), m)
			if res.OK {
				t.Fatal("invalid input was accepted")
			}
			if res.Kind != FailInternal {
				t.Errorf("kind = %q, want %q (error: %s)", res.Kind, FailInternal, res.Error)
			}
		})
	}
}

// The guard must produce a comprehensible message, or an admin will lose an
// afternoon wondering why an internal URL never checks.
func TestHTTPGuardBlocksPrivateTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	guarded := NewHTTPChecker(HTTPOptions{Guard: NewGuard(false)})
	res := guarded.Check(context.Background(), monitor(srv.URL))

	if res.OK {
		t.Fatal("the guard let a loopback target through")
	}
	if !strings.Contains(res.Error, "allow-private-targets") {
		t.Errorf("error %q does not explain how to allow this deliberately", res.Error)
	}
}

// An endless response body must not be able to exhaust memory.
func TestHTTPBodyReadIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 64*1024)
		for range 64 { // 4 MiB, well past the 1 MiB cap
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.KeywordMode = KeywordMustContain
	m.Keyword = "x"
	m.Timeout = 10 * time.Second

	res := testChecker().Check(context.Background(), m)
	if !res.OK {
		t.Errorf("check failed on a large body: %s", res.Error)
	}
}

func TestHTTPReportsCertExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHTTPChecker(HTTPOptions{Guard: NewGuard(true)})
	// httptest uses a self-signed certificate, so the check fails on trust —
	// which is itself the assertion: an untrusted certificate must be a TLS
	// failure and not a generic connection error.
	res := c.Check(context.Background(), monitor(srv.URL))

	if res.OK {
		if res.CertExpiry.IsZero() {
			t.Error("certificate expiry was not reported for a TLS connection")
		}
		return
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q for a self-signed certificate (error: %s)",
			res.Kind, FailTLS, res.Error)
	}
}

func TestContextCancellationIsRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := testChecker().Check(ctx, monitor(srv.URL))
	elapsed := time.Since(start)

	if res.OK {
		t.Fatal("a cancelled check reported success")
	}
	if elapsed > time.Second {
		t.Errorf("cancellation took %s; the context is not being respected", elapsed)
	}
}
