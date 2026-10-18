package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestHealthcheckURLs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr string
		want []string
	}{
		{"port only", ":8080", []string{"http://127.0.0.1:8080/api/v1/ready"}},
		{"all interfaces", "0.0.0.0:8080", []string{"http://127.0.0.1:8080/api/v1/ready"}},
		{"explicit host is kept", "127.0.0.1:9000", []string{"http://127.0.0.1:9000/api/v1/ready"}},
		{"named host is kept", "localhost:8080", []string{"http://localhost:8080/api/v1/ready"}},
		{"explicit ipv6 host is kept", "[::1]:8080", []string{"http://[::1]:8080/api/v1/ready"}},
		{"ipv6 wildcard tries both loopbacks", "[::]:8080", []string{
			"http://[::1]:8080/api/v1/ready",
			"http://127.0.0.1:8080/api/v1/ready",
		}},
		{"not host:port", "garbage", []string{"http://garbage/api/v1/ready"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := healthcheckURLs(tc.addr); !slices.Equal(got, tc.want) {
				t.Errorf("healthcheckURLs(%q) = %q, want %q", tc.addr, got, tc.want)
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

// readyOn serves /api/v1/ready on host:port with the given status, and
// returns false when that exact address cannot be bound here.
func readyOn(t *testing.T, host, port string, status int) bool {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return true
}

// A server started with --addr [::]:PORT where IPv6 is switched off accepts
// on 127.0.0.1 only, because [::1] does not exist there. Probing [::1] alone
// reported it unhealthy for as long as it ran.
func TestRunHealthcheckIPv6WildcardFallsBackToIPv4(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(listenAddr(t, srv.URL))

	var out bytes.Buffer
	if err := runHealthcheck([]string{"--addr", net.JoinHostPort("::", port)}, &out); err != nil {
		t.Fatalf("runHealthcheck on [::]:%s with a ready server on 127.0.0.1: %v", port, err)
	}
	if want := "ok http://127.0.0.1:" + port + "/"; !strings.HasPrefix(out.String(), want) {
		t.Errorf("output = %q, want it to name the address that answered (%s...)", out.String(), want)
	}
}

// A server that answers is the answer: a 503 on [::1] means not ready, and
// trying 127.0.0.1 next must not turn that into healthy.
func TestRunHealthcheckAnswerOnFirstAddressIsFinal(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	if !readyOn(t, "::1", port, http.StatusServiceUnavailable) || !readyOn(t, "127.0.0.1", port, http.StatusOK) {
		t.Skipf("could not bind port %s on both loopbacks", port)
	}

	var out bytes.Buffer
	err = runHealthcheck([]string{"--addr", net.JoinHostPort("::", port)}, &out)
	if err == nil {
		t.Fatalf("runHealthcheck returned nil after [::1] answered 503; output %q", out.String())
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want the 503 from [::1]", err)
	}
}

func TestRunHealthcheckNamesEveryAddressTried(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	_, port, _ := net.SplitHostPort(listenAddr(t, srv.URL))
	srv.Close()

	err := runHealthcheck([]string{"--addr", net.JoinHostPort("::", port)}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runHealthcheck with nothing listening returned nil")
	}
	for _, want := range []string{"[::1]:" + port, "127.0.0.1:" + port} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

// restore and reset-password ask listening() whether a server still runs on
// the address. It has to find a [::]:PORT server wherever the healthcheck
// would: on 127.0.0.1 where IPv6 is off, on [::1] where the listener is IPv6
// only.
func TestListeningFindsAnIPv6WildcardServerOnEitherLoopback(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("cannot listen on %s here: %v", host, err)
			}
			defer func() { _ = ln.Close() }()
			_, port, _ := net.SplitHostPort(ln.Addr().String())

			if !listening(net.JoinHostPort("::", port)) {
				t.Errorf("listening([::]:%s) = false with a server on %s", port, host)
			}
		})
	}
}
