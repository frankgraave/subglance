package watchdog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotRejectionsPreserveLastSuccessAndPayload(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	w := newTestWatchdog(t, strings.Replace(srv.URL, "http://", "http://privateuser:privatepass@", 1)+"/privatepath?token=privatetoken", func() Liveness { return Liveness{} })
	w.tick(context.Background())
	success := *w.Snapshot().LastSuccessAt
	for _, code := range []int{302, 400, 500} {
		rec.mu.Lock()
		rec.status = code
		rec.mu.Unlock()
		w.tick(context.Background())
		s := w.Snapshot()
		if s.LastResult == nil || *s.LastResult != "rejected" || s.LastStatusCode == nil || *s.LastStatusCode != code || s.InFlight {
			t.Fatalf("rejection %d not recorded: %+v", code, s)
		}
		if s.LastSuccessAt == nil || !s.LastSuccessAt.Equal(success) {
			t.Fatal("a rejection erased or advanced last success")
		}
		encoded, _ := json.Marshal(s)
		for _, secret := range []string{"private", "http", "monitors", "checks"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("snapshot leaked %q: %s", secret, encoded)
			}
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, body := range rec.bodies {
		if body != "alive; 0 monitors scheduled, 0 checks completed" {
			t.Fatalf("outbound payload changed: %q", body)
		}
	}
}

func TestSnapshotSuppressionRetainsPingHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{Scheduled: 1, ChecksCompleted: 1} })
	w.tick(context.Background())
	before := w.Snapshot()
	w.tick(context.Background())
	after := w.Snapshot()
	if !after.Suppressed || after.InFlight {
		t.Fatal("stalled pipeline still looks like an active ping")
	}
	if !after.LastAttemptAt.Equal(*before.LastAttemptAt) || !after.LastSuccessAt.Equal(*before.LastSuccessAt) || *after.LastResult != "succeeded" {
		t.Fatal("suppression rewrote actual ping history")
	}
	if !after.LastDecisionAt.After(*before.LastDecisionAt) {
		t.Fatal("suppression decision not timestamped")
	}
}

func TestSnapshotFailureOutcomes(t *testing.T) {
	for _, kind := range []string{"timeout", "canceled", "transport_error", "request_error"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-release
				w.WriteHeader(204)
			}))
			defer srv.Close()
			defer close(release)
			w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{} })
			ctx := context.Background()
			var cancel context.CancelFunc
			switch kind {
			case "timeout":
				w.client.Timeout = 20 * time.Millisecond
			case "canceled":
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "transport_error":
				srv.Close()
			case "request_error":
				w.url = ":bad"
			}
			w.send(ctx, "alive", "alive")
			s := w.Snapshot()
			if s.LastResult == nil || *s.LastResult != kind || s.LastStatusCode != nil || s.LastSuccessAt != nil || s.LastAttemptAt == nil || s.InFlight {
				t.Fatalf("wrong safe outcome for %s: %+v", kind, s)
			}
		})
	}
}

func TestSnapshotInFlightIsolationOverdueAndConcurrentReads(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	defer srv.Close()
	w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{} })
	done := make(chan struct{})
	go func() { defer close(done); w.tick(context.Background()) }()
	<-entered
	before := w.Snapshot()
	if !before.InFlight || before.LastAttemptAt == nil || before.LastSuccessAt != nil {
		t.Fatal("in-flight request not represented")
	}
	*before.LastAttemptAt = time.Time{}
	if w.Snapshot().LastAttemptAt.IsZero() {
		t.Fatal("reader changed retained state")
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 1000 {
				_ = w.Snapshot()
			}
		})
	}
	close(release)
	<-done
	readers.Wait()
	w.mu.Lock()
	w.history.decision = time.Now().Add(-2*w.interval - pingTimeout)
	w.mu.Unlock()
	if !w.Snapshot().Overdue {
		t.Fatal("old successful history still looks current")
	}
	var disabled *Watchdog
	s := disabled.Snapshot()
	if s.Configured || s.IntervalSeconds != nil || s.LastAttemptAt != nil || s.LastResult != nil || s.Overdue {
		t.Fatalf("disabled state: %+v", s)
	}
}

func TestSnapshotCancellationDuringActualSend(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	defer close(release)
	w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.send(ctx, "alive", "alive") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach receiver")
	}
	if !w.Snapshot().InFlight {
		t.Fatal("actual blocked request not in flight")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop actual request")
	}
	s := w.Snapshot()
	if s.LastResult == nil || *s.LastResult != "canceled" || s.LastStatusCode != nil || s.LastSuccessAt != nil || s.InFlight {
		t.Fatalf("canceled actual request: %+v", s)
	}
}

func TestSnapshotStoppedEvent(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{} })
	w.pingStopped()
	s := w.Snapshot()
	if s.LastEvent == nil || *s.LastEvent != "stopped" || s.LastSuccessAt == nil {
		t.Fatalf("shutdown event missing: %+v", s)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 1 || rec.bodies[0] != "stopped; shutting down cleanly" {
		t.Fatalf("shutdown payload changed: %v", rec.bodies)
	}
}

// Check the JSON shape as well as the typed snapshot: unknown times are null.
func snapshotJSON(t *testing.T, w *Watchdog) map[string]any {
	t.Helper()
	data, err := json.Marshal(w.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSnapshotTracksActualSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	w := newTestWatchdog(t, srv.URL, func() Liveness { return Liveness{} })
	before := snapshotJSON(t, w)
	if before["configured"] != true || before["last_attempt_at"] != nil || before["last_success_at"] != nil || before["last_result"] != nil {
		t.Fatalf("before first ping: %v", before)
	}
	w.tick(context.Background())
	got := snapshotJSON(t, w)
	if got["last_attempt_at"] == nil || got["last_success_at"] == nil || got["last_result"] != "succeeded" || got["last_status_code"] != float64(204) || got["last_event"] != "alive" {
		t.Fatalf("actual 204 not recorded: %v", got)
	}
	if got["in_flight"] != false || got["suppressed"] != false || got["overdue"] != false {
		t.Fatalf("completed state: %v", got)
	}
}
