package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// seen records the request headers each server received.
type seen struct {
	mu  sync.Mutex
	hdr http.Header
}

func (s *seen) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hdr = r.Header.Clone()
}

func (s *seen) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hdr == nil {
		return "<no request>"
	}
	return s.hdr.Get(key)
}

// hostURL rewrites a httptest URL onto another host name for the same
// listener: 127.0.0.1 and localhost are two hosts without any network.
func hostURL(t *testing.T, raw, host string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = host + ":" + u.Port()
	return u.String()
}

func monitorWithSecrets(target string) Monitor {
	m := monitor(target)
	m.Headers = map[string]string{
		"Authorization":  "Bearer secret",
		"X-Api-Key":      "secret-key",
		"X-Custom-Token": "tok",
	}
	return m
}

var secretHeaders = []string{"Authorization", "X-Api-Key", "X-Custom-Token"}

func TestCustomHeadersStayOnTheMonitoredOrigin(t *testing.T) {
	var got seen
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	cases := []struct {
		name   string
		target string // where the redirector sends the check
		want   bool   // whether the custom headers should arrive
	}{
		{"another host", hostURL(t, final.URL, "localhost"), false},
		{"same host, another port", final.URL, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = seen{}
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.target, http.StatusFound)
			}))
			defer redirector.Close()

			res := testChecker().Check(context.Background(), monitorWithSecrets(redirector.URL))
			if !res.OK {
				t.Fatalf("check failed: %s", res.Error)
			}
			for _, h := range secretHeaders {
				if v := got.get(h); v != "" {
					t.Errorf("%s reached another origin: %q", h, v)
				}
			}
			if ua := got.get("User-Agent"); !strings.HasPrefix(ua, "SubGlance/") {
				t.Errorf("User-Agent on the other origin = %q, want SubGlance's own", ua)
			}
		})
	}
}

func TestCustomHeadersFollowASameOriginRedirect(t *testing.T) {
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		got.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), monitorWithSecrets(srv.URL+"/start"))
	if !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	for _, h := range secretHeaders {
		if got.get(h) == "" {
			t.Errorf("%s was dropped on a same-origin redirect", h)
		}
	}
}

// A hop away and back again: the headers return with the original origin.
// The monitored origin is named localhost and the other one 127.0.0.1, because
// net/http strips Authorization only when the host name changes, and it keeps
// stripping it on every later hop.
func TestCustomHeadersReturnToTheMonitoredOrigin(t *testing.T) {
	var got seen
	var homeURL string
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range secretHeaders {
			if v := r.Header.Get(h); v != "" {
				t.Errorf("%s reached the other origin: %q", h, v)
			}
		}
		http.Redirect(w, r, homeURL+"/end", http.StatusFound)
	}))
	defer away.Close()
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, away.URL, http.StatusFound)
			return
		}
		got.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer home.Close()
	homeURL = hostURL(t, home.URL, "localhost")

	res := testChecker().Check(context.Background(), monitorWithSecrets(homeURL+"/start"))
	if !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	for _, h := range secretHeaders {
		if got.get(h) == "" {
			t.Errorf("%s was not restored on the monitored origin", h)
		}
	}
	if got.get("Authorization") != "Bearer secret" {
		t.Errorf("Authorization back on the monitored origin = %q, want Bearer secret", got.get("Authorization"))
	}
}

// A monitor that overrides the checker's own User-Agent keeps a User-Agent on
// the other origin: dropping the override must not leave the hop without one.
func TestOverriddenUserAgentFallsBackOnAnotherOrigin(t *testing.T) {
	var got seen
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	m := monitor(redirector.URL)
	m.Headers = map[string]string{"User-Agent": "my-probe/1.0", "Accept": "application/json"}
	res := testChecker().Check(context.Background(), m)
	if !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	if ua := got.get("User-Agent"); !strings.HasPrefix(ua, "SubGlance/") {
		t.Errorf("User-Agent = %q, want SubGlance's own", ua)
	}
	if a := got.get("Accept"); a != "*/*" {
		t.Errorf("Accept = %q, want */*", a)
	}
}

// A check that fails after its headers were withheld says so, instead of
// leaving a bare 401 that points at credentials that are fine.
func TestWithheldHeadersExplainTheFailure(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	res := testChecker().Check(context.Background(), monitorWithSecrets(redirector.URL))
	if res.OK || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ok=%v status=%d, want a failed 401", res.OK, res.StatusCode)
	}
	for _, want := range []string{"status 401", "not sent to " + final.URL, "left " + redirector.URL, "final URL"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("error %q does not contain %q", res.Error, want)
		}
	}
}

// Without custom headers nothing is withheld, and the error stays as it was.
func TestNoCustomHeadersLeavesTheErrorAlone(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer final.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	res := testChecker().Check(context.Background(), monitor(redirector.URL))
	if res.Error != "status 401, expected 200-299" {
		t.Errorf("error = %q", res.Error)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"http://example.com/a", "http://example.com/b", true},
		{"http://example.com/", "http://example.com:80/", true},
		{"https://Example.com/", "https://example.com:443/x", true},
		// The upgrade: same host name, default ports, http to https.
		{"http://example.com/", "https://example.com/", true},
		{"http://example.com:80/", "https://example.com:443/", true},
		// Downgrades and anything off the default ports are other origins.
		{"https://example.com/", "http://example.com/", false},
		{"http://example.com:8080/", "https://example.com/", false},
		{"http://example.com/", "https://example.com:8443/", false},
		{"http://example.com/", "http://example.com:8080/", false},
		{"http://example.com/", "http://api.example.com/", false},
		{"http://example.com/", "https://www.example.com/", false},
	}
	for _, tc := range cases {
		from, _ := url.Parse(tc.from)
		to, _ := url.Parse(tc.to)
		if got := sameOrigin(from, to); got != tc.want {
			t.Errorf("sameOrigin(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// A chain across two other origins names the one whose response failed, not
// the first it passed through.
func TestWithheldHeadersNameTheLastOrigin(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer final.Close()
	middle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer middle.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, middle.URL, http.StatusFound)
	}))
	defer start.Close()

	res := testChecker().Check(context.Background(), monitorWithSecrets(start.URL))
	if res.OK {
		t.Fatal("check passed, want a failed 401")
	}
	if !strings.Contains(res.Error, "not sent to "+final.URL) {
		t.Errorf("error %q does not name the final origin %s", res.Error, final.URL)
	}
}

// A failure back on the monitored origin had its headers, so it carries no
// explanation about another origin.
func TestReturnedHeadersLeaveTheErrorAlone(t *testing.T) {
	var away *httptest.Server
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, away.URL, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer home.Close()
	away = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, home.URL+"/end", http.StatusFound)
	}))
	defer away.Close()

	res := testChecker().Check(context.Background(), monitorWithSecrets(home.URL+"/start"))
	if res.Error != "status 503, expected 200-299" {
		t.Errorf("error = %q", res.Error)
	}
}
