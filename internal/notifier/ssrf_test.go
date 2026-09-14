package notifier

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// These tests reproduce the attack the guard exists to stop: a channel is a
// URL somebody typed, and delivery makes this process connect to it. Without
// a guard, anyone who can write a channel can point it at the cloud metadata
// service or a Docker socket and read the answer back out of the outbox's
// error column.

// blockedTargets are the two addresses that make the point: the loopback
// interface, where unauthenticated admin sockets live, and the link-local
// address every cloud provider serves credentials from.
var blockedTargets = []struct {
	name string
	url  string
}{
	{"loopback docker socket", "http://127.0.0.1:2375/containers/json"},
	{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
}

func TestWebhookRefusesInternalAddresses(t *testing.T) {
	guard := checker.NewGuard(false)
	s := NewWebhookSender(guard)

	for _, tc := range blockedTargets {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Send(context.Background(),
				map[string]string{"url": tc.url},
				Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
			if err == nil {
				t.Fatalf("%s was delivered; SubGlance is an SSRF proxy", tc.url)
			}
			if !errors.Is(err, checker.ErrPrivateTarget) {
				t.Fatalf("error does not name the guard: %v", err)
			}

			// A refused address is refused on every attempt. Marking
			// it retryable would keep the delivery in the queue for
			// half an hour and hide the real cause behind a
			// "gave up after 6 attempts" message.
			var r *Retryable
			if errors.As(err, &r) {
				t.Errorf("blocked address reported as retryable: %v", err)
			}
		})
	}
}

// TestWebhookAllowsInternalAddressesWhenOptedIn is the other half of the
// promise: somebody running ntfy on their own LAN asked for this, and the
// guard must not lock them out of their own network.
func TestWebhookAllowsInternalAddressesWhenOptedIn(t *testing.T) {
	var got bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// httptest listens on loopback, which is exactly what the guard blocks
	// above, so this is the same delivery with the flag flipped.
	s := NewWebhookSender(checker.NewGuard(true))
	if err := s.Send(context.Background(),
		map[string]string{"url": srv.URL},
		Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)}); err != nil {
		t.Fatalf("--allow-private-targets did not allow a private target: %v", err)
	}
	if !got {
		t.Fatal("the endpoint was never called")
	}
}

// TestTestButtonIsGuarded matters as much as the delivery path. The test
// button is unauthenticated-by-configuration in the sense that anyone who can
// edit a channel can press it, and a button that returns the response from an
// arbitrary internal address is a ready-made SSRF probe with a UI.
func TestTestButtonIsGuarded(t *testing.T) {
	db, _, _ := testDB(t)

	n := New(Options{
		DB:    db,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Guard: checker.NewGuard(false),
	})

	err := n.Test(context.Background(), store.Channel{
		Name:    "probe",
		Type:    store.ChannelWebhook,
		Config:  map[string]string{"url": "http://169.254.169.254/latest/meta-data/"},
		Enabled: true,
	})
	if err == nil {
		t.Fatal("the test button reached the metadata service")
	}
	if !errors.Is(err, checker.ErrPrivateTarget) {
		t.Fatalf("error does not name the guard: %v", err)
	}
}

// TestBlockedDeliveryIsDeadLetteredNotRetried walks the real queue path, so
// this covers the classification as the notifier applies it rather than as a
// sender returns it.
func TestBlockedDeliveryIsDeadLetteredNotRetried(t *testing.T) {
	db, m, ch := testDB(t)

	ch.Config = map[string]string{"url": "http://127.0.0.1:2375/containers/json"}
	if _, err := db.UpdateChannel(context.Background(), ch); err != nil {
		t.Fatalf("update channel: %v", err)
	}

	clock := time.Now()
	n := New(Options{
		DB:    db,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Guard: checker.NewGuard(false),
		Now:   func() time.Time { return clock },
	})

	ctx := context.Background()
	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// Far enough ahead that any retry schedule would have come due.
	clock = clock.Add(24 * time.Hour)
	due, err := db.DueDeliveries(ctx, clock, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatal("a blocked address was queued for retry; it will never become reachable")
	}
}

// TestEmailRefusesInternalSMTPHost covers the channel that does not speak
// HTTP. The SMTP host is the same kind of operator-supplied target, and a
// dialer without the guard would happily open a connection to a port that has
// nothing to do with mail.
func TestEmailRefusesInternalSMTPHost(t *testing.T) {
	s := NewEmailSender(checker.NewGuard(false))

	err := s.Send(context.Background(), map[string]string{
		"host": "127.0.0.1",
		"port": "2375",
		"from": "subglance@example.com",
		"to":   "ops@example.com",
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
	if err == nil {
		t.Fatal("an internal SMTP host was dialled")
	}
	if !errors.Is(err, checker.ErrPrivateTarget) {
		t.Fatalf("error does not name the guard: %v", err)
	}
	var r *Retryable
	if errors.As(err, &r) {
		t.Errorf("blocked SMTP host reported as retryable: %v", err)
	}
}

// TestEmailAllowsInternalSMTPHostWhenOptedIn: a relay on localhost is the most
// common self-hosted mail setup there is, so opting in has to keep working.
//
// The listener answers a greeting and then a 421, which is enough to prove the
// connection was made without implementing a mail server.
func TestEmailAllowsInternalSMTPHostWhenOptedIn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("421 go away\r\n"))
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	s := NewEmailSender(checker.NewGuard(true))
	err = s.Send(context.Background(), map[string]string{
		"host": host,
		"port": port,
		"from": "subglance@example.com",
		"to":   "ops@example.com",
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})

	// The send fails — the listener is not a mail server — but it must
	// fail on the SMTP conversation, not on the guard.
	if errors.Is(err, checker.ErrPrivateTarget) {
		t.Fatalf("--allow-private-targets did not allow a local relay: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "delivery blocked") {
		t.Fatalf("delivery was blocked despite the opt-in: %v", err)
	}
}

// TestNilGuardStillDelivers keeps the optional field honest: a deployment
// built without the checker pipeline, and every test that passes no guard,
// must still be able to send.
func TestNilGuardStillDelivers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewWebhookSender(nil)
	if err := s.Send(context.Background(),
		map[string]string{"url": srv.URL},
		Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)}); err != nil {
		t.Fatalf("a nil guard blocked a delivery: %v", err)
	}
}
