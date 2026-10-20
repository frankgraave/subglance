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

func TestRedirectsAreNotFollowed(t *testing.T) {
	var mu sync.Mutex
	var reached bool
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		reached = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	w := newTestWatchdog(t, redirector.URL, func() Liveness { return Liveness{Scheduled: 1, ChecksCompleted: 1} })
	w.send(context.Background(), "alive", "alive")

	mu.Lock()
	defer mu.Unlock()
	if reached {
		t.Fatal("watchdog followed a redirect to a second host; a configured endpoint must not be able to steer the request")
	}
}

func TestSuppliedClientKeepsItsRedirectPolicy(t *testing.T) {
	// A caller who hands in a client with its own CheckRedirect keeps it:
	// only the default client is hardened.
	sentinel := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	supplied := &http.Client{CheckRedirect: sentinel}
	w, err := New(Options{
		URL:      "https://example.com/ping",
		Liveness: func() Liveness { return Liveness{} },
		Log:      quiet(),
		Client:   supplied,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.client != supplied {
		t.Fatal("a client supplied with a redirect policy should be used as-is")
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{
		"https://hc-ping.com/abc",
		"http://192.168.1.9:8080/api/push/xyz",
		"http://subglance-b:3000/api/push/xyz",
		"http://[2001:db8::1]:8080/ping",
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
		// Parse, and fail every ping before it leaves the process.
		"https://hc-ping.com:99999/abc",
		"https://hc-ping.com:0/abc",
		"https://hc-p!ng.com/abc",
		"https://-hc-ping.com/abc",
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

// An instance whose monitors are all checked less often than the watchdog
// pings records nothing for most intervals while working perfectly. Silence
// shorter than the schedule allows is not a stall, and withholding the ping
// for it raised the alarm at the other end for a healthy instance.
func TestPingsBetweenChecksOfASlowSchedule(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	reading := Liveness{ChecksCompleted: 5, Scheduled: 1, CheckGap: time.Hour}
	w := newTestWatchdog(t, srv.URL, func() Liveness { return reading })
	w.last = reading
	w.recorded = time.Now().Add(-10 * time.Minute)
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 1 || got[0] != "alive" {
		t.Fatalf("a pipeline ten minutes into an hourly schedule must ping, got %v", got)
	}
	if w.Snapshot().Suppressed {
		t.Fatal("a ping within the schedule's gap is reported as withheld")
	}
}

// Past the gap the schedule allows, silence is the stall it always was.
func TestStaysSilentPastTheScheduleGap(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	reading := Liveness{ChecksCompleted: 5, Scheduled: 1, CheckGap: time.Hour}
	w := newTestWatchdog(t, srv.URL, func() Liveness { return reading })
	w.last = reading
	w.recorded = time.Now().Add(-61 * time.Minute)
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("a pipeline silent for longer than its schedule allows must not ping, got %v", got)
	}
	if !w.Snapshot().Suppressed {
		t.Fatal("a withheld ping is not reported as withheld")
	}
}

// A failed write is evidence, and it does not wait out the gap: a full disk on
// an instance of hourly monitors would otherwise be reported healthy for most
// of an hour. The ping stays withheld until a check is recorded again.
func TestFailedWriteWithholdsThePingInsideTheGap(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	reading := Liveness{ChecksCompleted: 5, Scheduled: 1, CheckGap: time.Hour, WriteFailures: 1}
	w := newTestWatchdog(t, srv.URL, func() Liveness { return reading })
	w.last = Liveness{ChecksCompleted: 5, Scheduled: 1, CheckGap: time.Hour}
	w.recorded = time.Now().Add(-time.Minute)

	w.tick(context.Background())
	w.tick(context.Background()) // nothing moved: still the failed write
	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("a failed write with no recorded check since must withhold the ping, got %v", got)
	}

	reading.ChecksCompleted = 6
	w.tick(context.Background())
	if got := rec.seen(); len(got) != 1 || got[0] != "alive" {
		t.Fatalf("a recorded check after the failure must ping again, got %v", got)
	}

	// And the failure is over: the next quiet tick inside the gap is a
	// pipeline between checks again, not one whose writes are failing.
	w.tick(context.Background())
	if got := rec.seen(); len(got) != 2 {
		t.Fatalf("a quiet tick after the recovery must ping, got %v", got)
	}
}

// A failed write beside a recorded one is one monitor's trouble, not a stalled
// pipeline, and pings as the recorded check says.
func TestFailedWriteBesideARecordedCheckStillPings(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	w := newTestWatchdog(t, srv.URL, func() Liveness {
		return Liveness{ChecksCompleted: 6, Scheduled: 2, CheckGap: time.Hour, WriteFailures: 1}
	})
	w.last = Liveness{ChecksCompleted: 5, Scheduled: 2, CheckGap: time.Hour}
	w.tick(context.Background())

	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("want one alive ping, got %v", got)
	}
}

// A monitor added between two ticks shortens the gap at once, but a pipeline
// quiet by the old schedule's measure is not late by the new one until the new
// monitor has had a tick to be checked in.
func TestAShortenedGapWaitsOneTick(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	reading := Liveness{ChecksCompleted: 5, Scheduled: 2, CheckGap: 30 * time.Second}
	w := newTestWatchdog(t, srv.URL, func() Liveness { return reading })
	w.last = Liveness{ChecksCompleted: 5, Scheduled: 1, CheckGap: time.Hour}
	w.recorded = time.Now().Add(-10 * time.Minute)

	w.tick(context.Background())
	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("the tick that first sees the shorter gap must still ping, got %v", got)
	}
	w.tick(context.Background())
	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("the next tick, still with nothing recorded, must not ping, got %v", got)
	}
}

// The quiet spell starts when Run does: a restart gets one gap to record its
// first check, rather than being read as silent since the zero time.
func TestRunStartsTheQuietSpell(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	reading := Liveness{ChecksCompleted: 3, Scheduled: 1, CheckGap: time.Hour}
	w := newTestWatchdog(t, srv.URL, func() Liveness { return reading })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx) // sends the stopped ping and returns

	w.tick(context.Background())
	if got := rec.seen(); len(got) != 2 || got[1] != "alive" {
		t.Fatalf("a tick within the first gap after Run must ping, got %v", got)
	}
}
