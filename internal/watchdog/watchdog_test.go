package watchdog

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// recorder collects the pings a test server receives.
type recorder struct {
	mu     sync.Mutex
	events []string
	bodies []string
	status int
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.events = append(r.events, req.Header.Get("X-SubGlance-Event"))
		r.bodies = append(r.bodies, string(body))
		code := r.status
		r.mu.Unlock()
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	}
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestWatchdog(t *testing.T, url string, live func() Liveness) *Watchdog {
	t.Helper()
	w, err := New(Options{URL: url, Interval: time.Hour, Liveness: live, Log: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w == nil {
		t.Fatal("New returned nil for a configured URL")
	}
	return w
}

func TestDisabledWithoutURL(t *testing.T) {
	w, err := New(Options{Liveness: func() Liveness { return Liveness{} }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w != nil {
		t.Fatal("no URL should mean no watchdog")
	}
	// A nil watchdog must be safe to run, so the caller needs no nil check.
	w.Run(context.Background())
}

func TestPingsWhenChecksProgress(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var done uint64 = 7
	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: done, Scheduled: 3}
	})

	w.last = Liveness{ChecksCompleted: 5, Scheduled: 3}
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 1 || got[0] != "alive" {
		t.Fatalf("want one alive ping, got %v", got)
	}
	rec.mu.Lock()
	body := rec.bodies[0]
	rec.mu.Unlock()
	if want := "alive; 3 monitors scheduled, 7 checks completed"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

// A wedged pipeline is the exact failure this feature exists to expose: the
// process is up, so a bare ticker would keep the dead man's switch happy.
func TestStaysSilentWhenNoChecksCompleted(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: 42, Scheduled: 4}
	})
	w.last = Liveness{ChecksCompleted: 42, Scheduled: 4}
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("a stalled pipeline must not ping, got %v", got)
	}
}

// An instance with nothing to check is healthy, not stalled.
func TestPingsWhenNothingIsScheduled(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: 0, Scheduled: 0}
	})
	w.last = Liveness{ChecksCompleted: 0, Scheduled: 0}
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 1 || got[0] != "alive" {
		t.Fatalf("an idle instance should still ping, got %v", got)
	}
}

func TestShutdownSendsStoppedPing(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: 1, Scheduled: 1}
	})

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		w.Run(ctx)
	}()
	cancel()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	got := rec.seen()
	if len(got) != 1 || got[0] != "stopped" {
		t.Fatalf("want a single stopped ping, got %v", got)
	}
}

// A refusing endpoint must not take the process with it.
func TestRejectedPingIsSurvivable(t *testing.T) {
	rec := &recorder{status: http.StatusInternalServerError}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: 2, Scheduled: 1}
	})
	w.last = Liveness{ChecksCompleted: 1, Scheduled: 1}
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("want the ping attempted once, got %v", got)
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{
		"https://hc-ping.com/abc",
		"http://192.168.1.9:8080/api/push/xyz",
	}
	for _, raw := range valid {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", raw, err)
		}
	}

	invalid := []string{
		"ftp://example.com/ping",
		"/just/a/path",
		"hc-ping.com/abc",
		"://nonsense",
	}
	for _, raw := range invalid {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want an error", raw)
		}
	}
}

func TestNewRequiresLiveness(t *testing.T) {
	if _, err := New(Options{URL: "https://example.com/ping"}); err == nil {
		t.Fatal("a watchdog without liveness evidence must not be built")
	}
}

func TestDefaultInterval(t *testing.T) {
	w, err := New(Options{
		URL:      "https://example.com/ping",
		Liveness: func() Liveness { return Liveness{} },
		Log:      quiet(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.interval != DefaultInterval {
		t.Fatalf("interval = %s, want %s", w.interval, DefaultInterval)
	}
}
