package notifier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// fakeSender records what it was asked to deliver and answers as told.
type fakeSender struct {
	mu   sync.Mutex
	sent []Alert

	// err is returned by Send. Set to a *Retryable to exercise the retry
	// path, to a plain error for the permanent one.
	err error

	// failFirst makes the first n attempts fail with err and the rest
	// succeed, which is how a real endpoint that comes back looks.
	failFirst int
	attempts  int
}

func (f *fakeSender) Send(_ context.Context, _ map[string]string, a Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.attempts++
	if f.failFirst > 0 && f.attempts <= f.failFirst {
		return f.err
	}
	if f.failFirst == 0 && f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, a)
	return nil
}

func (f *fakeSender) Validate(map[string]string) error { return nil }

func (f *fakeSender) delivered() []Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Alert(nil), f.sent...)
}

func (f *fakeSender) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// testDB builds a migrated temporary database with one monitor and one
// channel assigned to it.
func testDB(t *testing.T) (*store.DB, store.Monitor, store.Channel) {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{
		Path: filepath.Join(t.TempDir(), "notifier.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name:      "api",
		Type:      "http",
		Target:    "https://example.com/health",
		IntervalS: 60,
		TimeoutS:  10,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	ch, err := db.CreateChannel(ctx, store.Channel{
		Name:    "ops",
		Type:    store.ChannelWebhook,
		Config:  map[string]string{"url": "https://example.com/hook"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if err := db.SetMonitorChannels(ctx, m.ID, []int64{ch.ID}); err != nil {
		t.Fatalf("assign channel: %v", err)
	}
	return db, m, ch
}

// openIncident creates a real incident row.
//
// The outbox has a foreign key to incidents, which is correct: a delivery that
// names an incident that never existed is a bug, not a case to tolerate. So
// the tests use real rows rather than invented ids.
func openIncident(t *testing.T, db *store.DB, monitorID int64, at time.Time, cause string) store.Incident {
	t.Helper()
	inc, err := db.OpenIncident(context.Background(), monitorID, at, cause, cause)
	if err != nil {
		t.Fatalf("open incident: %v", err)
	}
	return inc
}

func newTestNotifier(t *testing.T, db *store.DB, sender Sender, now func() time.Time) *Notifier {
	t.Helper()
	return New(Options{
		DB:      db,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders: map[string]Sender{store.ChannelWebhook: sender},
		Now:     now,
	})
}

// TestEnqueueDoesNotBlockOnTheChannel is the point of the whole package: a
// channel that hangs must not hold up the check that produced the alert.
//
// The sender here blocks for longer than any check interval. If Enqueue
// touched it, this test would time out rather than fail — which is exactly the
// production symptom the outbox exists to prevent.
func TestEnqueueDoesNotBlockOnTheChannel(t *testing.T) {
	db, m, _ := testDB(t)

	blocked := make(chan struct{})
	defer close(blocked)

	hanging := senderFunc(func(ctx context.Context, _ map[string]string, _ Alert) error {
		<-blocked
		return nil
	})

	n := newTestNotifier(t, db, hanging, time.Now)
	inc := openIncident(t, db, m.ID, time.Now(), "connection refused")

	done := make(chan error, 1)
	go func() {
		done <- n.Enqueue(context.Background(), m, inc,
			state.EventIncidentConfirmed, time.Now())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue waited on the channel; it must only write to the outbox")
	}

	due, err := db.DueDeliveries(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 queued delivery, got %d", len(due))
	}
}

// senderFunc adapts a function to the Sender interface.
type senderFunc func(context.Context, map[string]string, Alert) error

func (f senderFunc) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	return f(ctx, cfg, a)
}
func (f senderFunc) Validate(map[string]string) error { return nil }

func TestSweepDeliversAndMarksDone(t *testing.T) {
	db, m, _ := testDB(t)
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, time.Now)
	ctx := context.Background()

	inc := openIncident(t, db, m.ID, time.Now().Add(-5*time.Minute), "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, time.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	sent := sender.delivered()
	if len(sent) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(sent))
	}
	if sent[0].MonitorName != "api" {
		t.Errorf("monitor name = %q, want api", sent[0].MonitorName)
	}
	if sent[0].Cause != "connection refused" {
		t.Errorf("cause = %q, want the incident cause", sent[0].Cause)
	}

	// Nothing should be due any more.
	due, err := db.DueDeliveries(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("delivery still pending after a successful send")
	}
}

// TestRetryableFailureIsRescheduled proves a transient failure comes back
// later rather than being dropped or retried immediately.
func TestRetryableFailureIsRescheduled(t *testing.T) {
	db, m, _ := testDB(t)
	sender := &fakeSender{err: retryable("endpoint returned 503"), failFirst: 1}

	clock := time.Now()
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })
	ctx := context.Background()

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	// Not due yet: the backoff has to have moved it into the future.
	due, err := db.DueDeliveries(ctx, clock, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatal("failed delivery is due again immediately; backoff did not apply")
	}

	// Due once enough time has passed.
	clock = clock.Add(time.Minute)
	due, err = db.DueDeliveries(ctx, clock, 10)
	if err != nil {
		t.Fatalf("due after backoff: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected the delivery to become due again, got %d", len(due))
	}
	if due[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1", due[0].Attempts)
	}
	if due[0].LastError == "" {
		t.Error("last error was not recorded; the interface would have nothing to show")
	}

	// Second sweep succeeds.
	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if got := len(sender.delivered()); got != 1 {
		t.Fatalf("expected 1 eventual delivery, got %d", got)
	}
}

// TestPermanentFailureIsNotRetried guards the distinction that keeps a bad URL
// from being retried into the void: only errors marked retryable come back.
func TestPermanentFailureIsNotRetried(t *testing.T) {
	db, m, _ := testDB(t)
	sender := &fakeSender{err: errors.New("endpoint rejected the alert (404)")}

	clock := time.Now()
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })
	ctx := context.Background()

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// Far into the future: a retryable failure would have surfaced by now.
	clock = clock.Add(24 * time.Hour)
	due, err := db.DueDeliveries(ctx, clock, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatal("a permanent failure was retried; a bad URL would be retried forever")
	}

	if sender.attemptCount() != 1 {
		t.Errorf("attempts = %d, want exactly 1", sender.attemptCount())
	}
}

// TestGivesUpAfterMaxAttempts proves the dead letter exists, so a permanently
// broken endpoint stops consuming worker time.
func TestGivesUpAfterMaxAttempts(t *testing.T) {
	db, m, ch := testDB(t)
	sender := &fakeSender{err: retryable("connection refused"), failFirst: 1000}

	clock := time.Now()
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })
	ctx := context.Background()

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	for i := 0; i < maxAttempts+2; i++ {
		if _, err := n.sweep(ctx); err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
		clock = clock.Add(time.Hour)
	}

	if got := sender.attemptCount(); got != maxAttempts {
		t.Errorf("attempts = %d, want %d then give up", got, maxAttempts)
	}

	health, err := db.ChannelHealthSince(ctx, clock.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	h := health[ch.ID]
	if h.Failed != 1 {
		t.Errorf("failed count = %d, want 1", h.Failed)
	}
	if h.LastError == "" {
		t.Error("channel health has no error to show the operator")
	}
}

// TestOneBrokenChannelDoesNotStopAnother is the isolation guarantee: a monitor
// with two channels must still reach the working one.
func TestOneBrokenChannelDoesNotStopAnother(t *testing.T) {
	db, m, webhookCh := testDB(t)
	ctx := context.Background()

	slackCh, err := db.CreateChannel(ctx, store.Channel{
		Name:    "slack",
		Type:    store.ChannelSlack,
		Config:  map[string]string{"url": "https://hooks.slack.test/x"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create slack channel: %v", err)
	}
	if err := db.SetMonitorChannels(ctx, m.ID, []int64{webhookCh.ID, slackCh.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	broken := &fakeSender{err: retryable("connection refused"), failFirst: 1000}
	working := &fakeSender{}

	n := New(Options{
		DB:  db,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders: map[string]Sender{
			store.ChannelWebhook: broken,
			store.ChannelSlack:   working,
		},
	})

	inc := openIncident(t, db, m.ID, time.Now(), "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, time.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := len(working.delivered()); got != 1 {
		t.Fatalf("working channel got %d deliveries, want 1: a broken channel blocked it", got)
	}
}

// TestPayloadIsFrozenAtEnqueue proves a retry repeats what was true when the
// alert fired, rather than recomputing something fresher and wrong.
func TestPayloadIsFrozenAtEnqueue(t *testing.T) {
	db, m, _ := testDB(t)
	sender := &fakeSender{err: retryable("503"), failFirst: 1}

	clock := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })
	ctx := context.Background()

	inc := openIncident(t, db, m.ID, clock.Add(-2*time.Minute), "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := n.sweep(ctx); err != nil { // fails, reschedules
		t.Fatalf("first sweep: %v", err)
	}

	clock = clock.Add(time.Hour) // an hour passes before the retry
	if _, err := n.sweep(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	sent := sender.delivered()
	if len(sent) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(sent))
	}
	if !sent[0].At.Equal(time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("alert time = %v, want the moment it fired, not the retry", sent[0].At)
	}
}

func TestDisabledChannelIsSkipped(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()

	ch.Enabled = false
	if _, err := db.UpdateChannel(ctx, ch); err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, time.Now)

	inc := openIncident(t, db, m.ID, time.Now(), "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, time.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	due, err := db.DueDeliveries(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatal("a disabled channel was queued")
	}
}

// TestBackoffGrowsAndIsJittered checks the schedule shape without asserting
// exact values, which would make the test a copy of the implementation.
func TestBackoffGrowsAndIsJittered(t *testing.T) {
	var last time.Duration
	for attempt := 1; attempt <= 5; attempt++ {
		d := backoff(attempt)
		if d <= last {
			t.Errorf("attempt %d delay %v is not longer than the previous %v", attempt, d, last)
		}
		last = d
	}

	// Jitter: repeated calls at the same attempt must not all agree, or
	// every monitor would retry in lockstep after a shared outage.
	seen := map[time.Duration]bool{}
	for i := 0; i < 20; i++ {
		seen[backoff(3)] = true
	}
	if len(seen) == 1 {
		t.Error("backoff is not jittered; every monitor would retry in lockstep")
	}
}

// TestHTTPStatusClassification pins the retry decision per status code. This
// is the rule that decides whether a misconfigured webhook is noticed or
// quietly retried forever.
func TestHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		wantErr   bool
		wantRetry bool
	}{
		{http.StatusOK, false, false},
		{http.StatusNoContent, false, false},
		{http.StatusTooManyRequests, true, true},
		{http.StatusInternalServerError, true, true},
		{http.StatusBadGateway, true, true},
		{http.StatusNotFound, true, false},
		{http.StatusUnauthorized, true, false},
		{http.StatusBadRequest, true, false},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			req, err := jsonRequest(context.Background(), srv.URL, map[string]string{"a": "b"})
			if err != nil {
				t.Fatalf("build: %v", err)
			}

			err = httpSend(context.Background(), srv.Client(), req)
			if tc.wantErr && err == nil {
				t.Fatalf("status %d: expected an error", tc.status)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("status %d: unexpected error %v", tc.status, err)
			}

			var r *Retryable
			if got := errors.As(err, &r); got != tc.wantRetry {
				t.Errorf("status %d: retryable = %v, want %v", tc.status, got, tc.wantRetry)
			}
		})
	}
}

// TestWebhookPostsTheAlert exercises the real webhook sender against a local
// server, so the JSON shape people build automation on is actually checked.
func TestWebhookPostsTheAlert(t *testing.T) {
	var got atomic.Pointer[Alert]

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "secret" {
			t.Errorf("custom header not sent")
		}
		body, _ := io.ReadAll(r.Body)
		a, err := DecodeAlert(string(body))
		if err != nil {
			t.Errorf("payload is not a valid alert: %v", err)
		}
		got.Store(&a)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewWebhookSender(nil)
	err := s.Send(context.Background(), map[string]string{
		"url":     srv.URL,
		"headers": "X-Token: secret",
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if a := got.Load(); a == nil || a.MonitorName != "api" {
		t.Fatal("the webhook did not receive the alert")
	}
}

func TestWebhookValidateRejectsBadConfig(t *testing.T) {
	s := NewWebhookSender(nil)

	if err := s.Validate(map[string]string{}); err == nil {
		t.Error("missing url accepted")
	}
	if err := s.Validate(map[string]string{"url": "ftp://example.com"}); err == nil {
		t.Error("non-http scheme accepted")
	}
	if err := s.Validate(map[string]string{"url": "https://x.test", "headers": "not-a-header"}); err == nil {
		t.Error("malformed header accepted; an auth header typo would fail silently")
	}
	if err := s.Validate(map[string]string{"url": "https://x.test", "headers": "X-A: b"}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestTelegramKeepsTheTokenOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := NewTelegramSender(nil)
	s.apiBase = srv.URL

	const token = "123456:SUPER-SECRET-VALUE"
	err := s.Send(context.Background(), map[string]string{
		"bot_token": token,
		"chat_id":   "42",
	}, Alert{MonitorName: "api"})

	if err == nil {
		t.Fatal("expected an error from a 401")
	}
	if contains(err.Error(), "SUPER-SECRET-VALUE") {
		t.Fatalf("the bot token leaked into the error message: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) > 0 && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestAlertTitleReadsLikeAPerson(t *testing.T) {
	cases := map[state.Event]string{
		state.EventIncidentOpened:    "api may be down",
		state.EventIncidentConfirmed: "api is down",
		state.EventIncidentReminder:  "api is still down",
		state.EventIncidentResolved:  "api is back up",
	}

	for event, want := range cases {
		a := Alert{MonitorName: "api", Event: string(event)}
		if got := a.Title(); got != want {
			t.Errorf("event %s: title = %q, want %q", event, got, want)
		}
	}
}

func TestEmailMessageResistsHeaderInjection(t *testing.T) {
	a := Alert{
		// A monitor name is operator-supplied text that lands in a
		// Subject line.
		MonitorName: "api\r\nBcc: attacker@evil.test",
		Event:       string(state.EventIncidentConfirmed),
		At:          time.Now(),
	}

	msg := buildMessage("from@test", []string{"to@test"}, a)
	if contains(msg, "Bcc: attacker@evil.test\r\n") {
		t.Fatal("a newline in the monitor name injected a header")
	}
}
