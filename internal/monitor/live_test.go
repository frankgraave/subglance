package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/store"
)

// A monitor created while the runner is live must actually get checked, and
// the result must reach the bus. This covers the whole path — scheduler,
// checker, store, bus — that the unit tests each cover only a slice of.
func TestNewMonitorGetsCheckedAndPublished(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	db := testDB(t)
	bus := events.NewBus(64)
	sub := bus.Subscribe()
	defer sub.Close()

	r := New(Options{
		DB:                  db,
		AllowPrivateTargets: true,
		Bus:                 bus,
		// Short reload so the test does not sit through the 30s default.
		ReloadInterval: 200 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	if _, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name:      "target",
		Type:      "http",
		Target:    target.URL,
		IntervalS: 3600,
		TimeoutS:  5,
		Enabled:   true,
		Retries:   1,
	}); err != nil {
		t.Fatalf("creating monitor: %v", err)
	}

	// The interval is an hour, so anything arriving here proves the new-monitor
	// fast path ran — not that we waited out a short interval.
	select {
	case e := <-sub.C():
		if e.Kind != events.KindHeartbeat {
			t.Fatalf("first event = %q, want heartbeat", e.Kind)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no heartbeat published within 15s: a newly created monitor " +
			"must be checked promptly, not one full interval later")
	}
}
