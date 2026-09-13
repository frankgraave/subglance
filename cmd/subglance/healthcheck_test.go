package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr string
		want string
	}{
		{"port only", ":8080", "http://127.0.0.1:8080/api/v1/ready"},
		{"all interfaces", "0.0.0.0:8080", "http://127.0.0.1:8080/api/v1/ready"},
		{"explicit host is kept", "127.0.0.1:9000", "http://127.0.0.1:9000/api/v1/ready"},
		{"named host is kept", "localhost:8080", "http://localhost:8080/api/v1/ready"},
		{"ipv6 wildcard", "[::]:8080", "http://[::1]:8080/api/v1/ready"},
		{"not host:port", "garbage", "http://garbage/api/v1/ready"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := healthcheckURL(tc.addr); got != tc.want {
				t.Errorf("healthcheckURL(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

// listenAddr turns a test server URL back into the --addr form the real flag
// takes, so the test exercises config loading rather than bypassing it.
func listenAddr(t *testing.T, serverURL string) string {
	t.Helper()
	return strings.TrimPrefix(serverURL, "http://")
}

func TestRunHealthcheckReady(t *testing.T) {
	t.Parallel()

	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready","database":"up"}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runHealthcheck([]string{"--addr", listenAddr(t, srv.URL)}, &out); err != nil {
		t.Fatalf("runHealthcheck on a ready server: %v", err)
	}

	// The endpoint is part of the contract: probing /health instead would
	// report healthy while the database is still down.
	if path != "/api/v1/ready" {
		t.Errorf("probed %q, want /api/v1/ready", path)
	}
	if !strings.HasPrefix(out.String(), "ok ") {
		t.Errorf("output = %q, want it to start with \"ok \"", out.String())
	}
}

func TestRunHealthcheckNotReady(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable","database":"down"}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	err := runHealthcheck([]string{"--addr", listenAddr(t, srv.URL)}, &out)
	if err == nil {
		t.Fatal("runHealthcheck against a 503 returned nil; the container would be reported healthy")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want it to name the status", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q on failure, want nothing on stdout", out.String())
	}
}

func TestRunHealthcheckNothingListening(t *testing.T) {
	t.Parallel()

	// A port that was just closed: the connection is refused rather than
	// hanging, which is the case a starting container is in.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := listenAddr(t, srv.URL)
	srv.Close()

	var out bytes.Buffer
	if err := runHealthcheck([]string{"--addr", addr}, &out); err == nil {
		t.Fatal("runHealthcheck against a closed port returned nil")
	}
}
