package notifier

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// The acceptance criteria of SUB-212, against a real database: a channel that
// dead-letters a real alert produces exactly one channel_failing notice
// through another channel, the next dead letter on the same channel produces
// none until the channel has delivered again, and a channel with nowhere to
// report it stays quiet rather than looping.

// brokenHook is the webhook URL the routing sender refuses. It carries a
// credential in its path, the way a Slack or Discord webhook does.
const brokenHook = "https://hooks.example.com/services/T000/B000/verysecret"

// routingSender refuses deliveries to brokenHook while broken is set, and
// records everything else by URL.
type routingSender struct {
	mu     sync.Mutex
	broken bool
	sent   map[string][]Alert
	// tried holds every alert offered to brokenHook, refused or not.
	tried []Alert
}

func (r *routingSender) Send(_ context.Context, cfg map[string]string, a Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg["url"] == brokenHook {
		r.tried = append(r.tried, a)
	}
	if r.broken && cfg["url"] == brokenHook {
		// What a revoked webhook answers, with the URL in the message the
		// way Go's HTTP client writes it.
		return errors.New(`Post "` + brokenHook + `": endpoint rejected the alert (404)`)
	}
	if r.sent == nil {
		r.sent = map[string][]Alert{}
	}
	r.sent[cfg["url"]] = append(r.sent[cfg["url"]], a)
	return nil
}

func (r *routingSender) Validate(map[string]string) error { return nil }

func (r *routingSender) setBroken(b bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broken = b
}

// notices returns the channel_failing notices delivered to url.
func (r *routingSender) notices(url string) []Alert {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Alert
	for _, a := range r.sent[url] {
		if a.Event == EventChannelFailing {
			out = append(out, a)
		}
	}
	return out
}

type failingFixture struct {
	db      *store.DB
	n       *Notifier
	sender  *routingSender
	m       store.Monitor
	broken  store.Channel
	backup  store.Channel
	clock   time.Time
	backURL string
	inc     store.Incident
}

// newFailingFixture builds a monitor alerting through a channel whose webhook
// is revoked, and a second, working channel that is the instance default.
func newFailingFixture(t *testing.T) *failingFixture {
	t.Helper()
	db, m, ch := testDB(t)
	ctx := context.Background()
	ch.Config = map[string]string{"url": brokenHook}
	ch, err := db.UpdateChannel(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	const backURL = "https://example.com/backup-hook"
	backup, err := db.CreateChannel(ctx, store.Channel{Name: "backup", Type: store.ChannelWebhook,
		Config: map[string]string{"url": backURL}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultChannel(ctx, backup.ID); err != nil {
		t.Fatal(err)
	}
	f := &failingFixture{db: db, sender: &routingSender{broken: true}, m: m, broken: ch, backup: backup,
		clock: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), backURL: backURL}
	f.n = newTestNotifier(t, db, f.sender, func() time.Time { return f.clock })
	return f
}

// alert queues one real alert for the monitor and runs one pass of the
// notifier's loop: a sweep, then the failure report.
func (f *failingFixture) alert(t *testing.T, event state.Event) {
	t.Helper()
	ctx := context.Background()
	// One incident for the whole test: the alerts are about delivery, and a
	// monitor may have only one open incident at a time.
	if f.inc.ID == 0 {
		f.inc = openIncident(t, f.db, f.m.ID, f.clock, "connection refused")
	}
	if err := f.n.Enqueue(ctx, f.m, f.inc, event, f.clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	f.pass(t)
}

func (f *failingFixture) pass(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.n.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	f.n.reportFailures(ctx)
}

func TestADeadLetteredChannelIsReportedOnceThroughAnother(t *testing.T) {
	f := newFailingFixture(t)

	f.alert(t, state.EventIncidentConfirmed)
	got := f.sender.notices(f.backURL)
	if len(got) != 1 {
		t.Fatalf("notices through the backup channel = %d, want 1", len(got))
	}
	notice := got[0]
	if notice.Title() != "SubGlance cannot deliver alerts to ops (webhook)" || !notice.Down() {
		t.Errorf("notice title = %q (down %v)", notice.Title(), notice.Down())
	}
	// The notice names the error, without the credential the error carried.
	if strings.Contains(notice.LastError, "verysecret") || strings.Contains(notice.Body(), "verysecret") {
		t.Fatalf("the failing channel's credential reached another channel: %q", notice.LastError)
	}
	if !strings.Contains(notice.LastError, "rejected the alert (404)") {
		t.Errorf("the notice lost the reason: %q", notice.LastError)
	}
	if !notice.StartedAt.Equal(f.clock) {
		t.Errorf("started_at = %v, want the moment the alert gave up (%v)", notice.StartedAt, f.clock)
	}

	// The spell records the notice, and which channel carried it.
	failures, err := f.db.ChannelFailures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fl := failures[f.broken.ID]; fl.NoticedAt.IsZero() || fl.NoticeChannelID != f.backup.ID {
		t.Errorf("failure record = %+v, want noticed through %d", fl, f.backup.ID)
	}

	// A second dead letter on the same channel, a minute later: no second
	// notice: notices about notices must not become noise.
	f.clock = f.clock.Add(2 * time.Minute)
	f.alert(t, state.EventIncidentResolved)
	if got := f.sender.notices(f.backURL); len(got) != 1 {
		t.Fatalf("after a second dead letter: %d notices, want still 1", len(got))
	}

	// The channel delivers again: the spell is over. The next failure is a
	// new spell and earns a notice of its own.
	f.sender.setBroken(false)
	f.clock = f.clock.Add(2 * time.Minute)
	f.alert(t, state.EventIncidentConfirmed)
	if failures, _ := f.db.ChannelFailures(context.Background()); len(failures) != 0 {
		t.Fatalf("a delivery arrived and the channel still reads failing: %+v", failures)
	}
	f.sender.setBroken(true)
	f.clock = f.clock.Add(2 * time.Minute)
	f.alert(t, state.EventIncidentResolved)
	if got := f.sender.notices(f.backURL); len(got) != 2 {
		t.Fatalf("a new spell of failures: %d notices, want 2", len(got))
	}
}

func TestAFailingChannelWithNoOtherChannelStaysOnScreen(t *testing.T) {
	f := newFailingFixture(t)
	ctx := context.Background()
	if err := f.db.DeleteChannel(ctx, f.backup.ID); err != nil {
		t.Fatal(err)
	}

	f.alert(t, state.EventIncidentConfirmed)
	failures, err := f.db.ChannelFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fl, ok := failures[f.broken.ID]
	if !ok || !fl.NoticedAt.IsZero() {
		t.Fatalf("failure record = %+v (present %v), want an open, unreported spell", fl, ok)
	}
	if len(f.sender.sent) != 0 {
		t.Fatalf("sent %v with no other channel to send through", f.sender.sent)
	}

	// A channel added later carries the notice on the next check.
	added, err := f.db.CreateChannel(ctx, store.Channel{Name: "late", Type: store.ChannelWebhook,
		Config: map[string]string{"url": f.backURL}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(failureRecheck)
	f.pass(t)
	if got := f.sender.notices(f.backURL); len(got) != 1 {
		t.Fatalf("after %s was added: %d notices, want 1", added.Name, len(got))
	}
}

func TestAFailureNoticeWaitsOutQuietHoursAndSkipsAFailingDefault(t *testing.T) {
	f := newFailingFixture(t)
	ctx := context.Background()
	// The default is in its quiet hours at noon UTC.
	if err := f.db.SetQuietHours(ctx, store.QuietHours{ChannelID: f.backup.ID, Start: "11:00", End: "13:00",
		Timezone: "UTC", During: store.QuietHold}); err != nil {
		t.Fatal(err)
	}

	f.alert(t, state.EventIncidentConfirmed)
	if got := f.sender.notices(f.backURL); len(got) != 0 {
		t.Fatalf("a failure notice went into quiet hours: %d", len(got))
	}

	// The window ends; the next check sends it.
	f.clock = time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
	f.pass(t)
	if got := f.sender.notices(f.backURL); len(got) != 1 {
		t.Fatalf("after the quiet hours: %d notices, want 1", len(got))
	}
}

func TestAFailingDefaultIsReportedThroughTheNextChannel(t *testing.T) {
	f := newFailingFixture(t)
	ctx := context.Background()
	// The failing channel is itself the default; the notice must go
	// elsewhere, never into the channel that is failing.
	if err := f.db.SetDefaultChannel(ctx, f.broken.ID); err != nil {
		t.Fatal(err)
	}
	f.alert(t, state.EventIncidentConfirmed)
	if got := f.sender.notices(f.backURL); len(got) != 1 {
		t.Fatalf("notices through the other channel = %d, want 1", len(got))
	}
	for _, a := range f.sender.tried {
		if a.Event == EventChannelFailing {
			t.Fatal("a notice was offered to the failing channel itself")
		}
	}
}

func TestATestSendNeitherOpensNorLeavesAFailure(t *testing.T) {
	f := newFailingFixture(t)
	ctx := context.Background()

	// A failed test is the operator watching it fail: not a spell, no notice.
	if err := f.n.Test(ctx, f.broken); err == nil {
		t.Fatal("the test of a revoked webhook succeeded")
	}
	if failures, _ := f.db.ChannelFailures(ctx); len(failures) != 0 {
		t.Fatalf("a failed test opened a spell: %+v", failures)
	}

	// A real alert gives up; a test that arrives afterwards ends the spell.
	f.alert(t, state.EventIncidentConfirmed)
	f.sender.setBroken(false)
	if err := f.n.Test(ctx, f.broken); err != nil {
		t.Fatal(err)
	}
	if failures, _ := f.db.ChannelFailures(ctx); len(failures) != 0 {
		t.Fatalf("a test arrived and the channel still reads failing: %+v", failures)
	}
}

func TestADisabledChannelIsNotAFailingOne(t *testing.T) {
	f := newFailingFixture(t)
	ctx := context.Background()
	inc := openIncident(t, f.db, f.m.ID, f.clock, "connection refused")
	if err := f.n.Enqueue(ctx, f.m, inc, state.EventIncidentConfirmed, f.clock); err != nil {
		t.Fatal(err)
	}
	// Switched off after the alert was queued: it is dead-lettered, but
	// nothing is wrong with a channel somebody chose to turn off.
	f.broken.Enabled = false
	if _, err := f.db.UpdateChannel(ctx, f.broken); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	if failures, _ := f.db.ChannelFailures(ctx); len(failures) != 0 {
		t.Fatalf("a disabled channel was recorded as failing: %+v", failures)
	}
}

func TestDeliveryCountsByTypeAndOutcome(t *testing.T) {
	f := newFailingFixture(t)
	counts := func() map[string]uint64 {
		out := map[string]uint64{}
		for _, c := range f.n.DeliveryCounts() {
			out[c.ChannelType+"/"+c.Outcome] = c.Count
		}
		return out
	}
	// Every series exists before anything happened, at zero.
	if got := counts(); len(got) != 3 || got["webhook/failed"] != 0 {
		t.Fatalf("before any delivery: %v, want three zero series", got)
	}

	f.alert(t, state.EventIncidentConfirmed)
	f.sender.setBroken(false)
	f.alert(t, state.EventIncidentResolved)
	// The notice through the backup channel is not an alert delivery.
	if got := counts(); got["webhook/failed"] != 1 || got["webhook/delivered"] != 1 || got["webhook/retried"] != 0 {
		t.Fatalf("counts = %v, want 1 failed and 1 delivered", got)
	}

	// A retryable failure counts as retried, not failed.
	f.n.senders[store.ChannelWebhook] = &fakeSender{err: retryable("endpoint returned 503")}
	f.alert(t, state.EventIncidentConfirmed)
	if got := counts(); got["webhook/retried"] != 1 || got["webhook/failed"] != 1 {
		t.Fatalf("counts = %v, want 1 retried", got)
	}
}

func TestChannelFailingNoticeReadsAsTextEverywhere(t *testing.T) {
	ch := store.Channel{Name: "ops", Type: store.ChannelSlack,
		Config: map[string]string{"url": brokenHook}}
	at := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	a := ChannelFailingNotice(ch, store.ChannelFailure{FailedAt: at,
		LastError: `Post "` + brokenHook + `": no such host`}, at.Add(time.Minute))
	if strings.Contains(a.Body(), "verysecret") {
		t.Fatalf("body carries the credential: %q", a.Body())
	}
	if !strings.Contains(a.Body(), "2026-10-05 03:00 UTC") {
		t.Errorf("body does not say when: %q", a.Body())
	}
	sms := smsText(a, "UTC", 160)
	if !strings.HasPrefix(sms, "FAILING SubGlance channel ops (slack):") || strings.Contains(sms, "verysecret") {
		t.Errorf("sms = %q", sms)
	}
}
