package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// downMonitor creates a monitor, takes it down, and returns it with its
// confirmed incident.
// downMonitor creates a confirmed, down monitor and syncs the test clock to
// the confirmation that was actually stored.
//
// `clock` is synced rather than left where the caller set it because the two
// timestamps come from different places: the outcome is stamped with the real
// time.Now() when the check is recorded, while `confirmed_at` is persisted as
// whole seconds (at.Unix()). A caller that captured its clock a moment earlier
// therefore has a clock that can sit *behind* the stored confirmation whenever
// a second boundary falls between the two calls — and then "confirmed + 15m"
// is not yet due at "clock + 15m", the reminder never goes out, and the test
// fails for reasons that have nothing to do with reminders. Rare locally,
// regular on a loaded CI runner under -race.
//
// Anchoring the clock to inc.ConfirmedAt also states the thing these tests are
// actually about: a reminder is due some interval after the incident was
// confirmed, not after an arbitrary moment in the test setup.
func downMonitor(t *testing.T, db *store.DB, r *Runner, repeatAfterS int, clock *time.Time) (store.Monitor, store.Incident) {
	t.Helper()
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "api", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 5, Retries: 1, Enabled: true,
		RepeatAfterS: repeatAfterS,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r.record(outcomeFor(m, "https://example.com", false))

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if !inc.Confirmed() {
		t.Fatal("expected the incident to be confirmed with retries=1")
	}
	*clock = inc.ConfirmedAt
	return m, inc
}

// The whole point of the ticket: an outage nobody answered keeps asking.
func TestUnacknowledgedIncidentIsRepeated(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }

	m, _ := downMonitor(t, db, r, 900, &clock)

	// Not yet: fourteen minutes is inside the first gap.
	clock = clock.Add(14 * time.Minute)
	r.sendDueReminders(ctx)
	if got := len(rec.events()); got != 1 {
		t.Fatalf("alerts after 14 minutes = %d, want 1 (the original) — a reminder fired early", got)
	}

	// Fifteen minutes after confirmation, the first reminder.
	clock = clock.Add(time.Minute)
	r.sendDueReminders(ctx)

	events := rec.events()
	if len(events) != 2 {
		t.Fatalf("alerts = %v, want the original plus one reminder", events)
	}
	if events[1] != state.EventIncidentReminder {
		t.Errorf("second alert = %q, want %q", events[1], state.EventIncidentReminder)
	}

	// A second sweep in the same minute must not fire again: the stamp moved
	// the schedule on to the longer second gap.
	r.sendDueReminders(ctx)
	if got := len(rec.events()); got != 2 {
		t.Errorf("alerts after an immediate second sweep = %d, want 2", got)
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if inc.ReminderCount != 1 {
		t.Errorf("ReminderCount = %d, want 1", inc.ReminderCount)
	}
	if inc.RemindedAt.IsZero() {
		t.Error("RemindedAt was not stamped")
	}

	// The reminder has to carry the duration, not just the fact. This is the
	// acceptance criterion "down since 02:40, 6h12m".
	last := rec.alerts[len(rec.alerts)-1]
	if last.At.Sub(last.Incident.StartedAt) <= 0 {
		t.Errorf("reminder carries no measurable downtime: started %v, alert at %v",
			last.Incident.StartedAt, last.At)
	}
}

// Acknowledging is documented as stopping repeat notifications. This is the
// test that makes the documentation true.
func TestAcknowledgedIncidentIsNotRepeated(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }

	_, inc := downMonitor(t, db, r, 900, &clock)

	if err := db.AckIncident(ctx, inc.ID, clock); err != nil {
		t.Fatalf("AckIncident: %v", err)
	}

	// Far past every gap in the schedule.
	clock = clock.Add(72 * time.Hour)
	r.sendDueReminders(ctx)

	if got := rec.events(); len(got) != 1 {
		t.Errorf("alerts = %v, want only the original — acknowledging did not stop reminders", got)
	}
}

// Zero means off, and off has to mean off however long the outage runs.
func TestRepeatAfterZeroDisablesReminders(t *testing.T) {
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }

	downMonitor(t, db, r, 0, &clock)

	clock = clock.Add(72 * time.Hour)
	r.sendDueReminders(context.Background())

	if got := rec.events(); len(got) != 1 {
		t.Errorf("alerts = %v, want only the original — repeat_after_s=0 still reminded", got)
	}
}

// A resolved incident must go quiet immediately. Without the resolved_at guard
// the ticker would keep reminding about an outage that is over.
func TestResolvedIncidentIsNotRepeated(t *testing.T) {
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }

	m, _ := downMonitor(t, db, r, 900, &clock)

	r.record(outcomeFor(m, "https://example.com", true)) // recovered

	before := len(rec.events())
	clock = clock.Add(72 * time.Hour)
	r.sendDueReminders(context.Background())

	if got := len(rec.events()); got != before {
		t.Errorf("alerts = %d, want %d — a resolved incident was still being repeated", got, before)
	}
}

// Flapping suppression is the louder rule. A monitor that is already being
// silenced for oscillating must not get a reminder stream instead.
func TestFlappingMonitorIsNotReminded(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{
		DB: db, Log: quietLogger(), Notify: rec.record,
		FlapWindow: 10 * time.Minute, FlapThreshold: 3,
	})
	r.now = func() time.Time { return clock }

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "flapper", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 5, Retries: 1, Enabled: true,
		RepeatAfterS: 900,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// down, up, down — three flips trips the threshold and leaves it down.
	r.record(outcomeFor(m, "https://example.com", false))
	r.record(outcomeFor(m, "https://example.com", true))
	r.record(outcomeFor(m, "https://example.com", false))

	if !r.engine.Flapping(m.ID) {
		t.Fatal("expected the monitor to be flapping")
	}

	before := len(rec.events())
	clock = clock.Add(2 * time.Hour)
	r.sendDueReminders(ctx)

	if got := len(rec.events()); got != before {
		t.Errorf("alerts = %d, want %d — a flapping monitor was reminded about", got, before)
	}
}

// The schedule lives in the database, so a restart continues it rather than
// starting over. Restarting during a three-day outage must not alert as if the
// incident were new.
func TestReminderScheduleSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	clock := time.Now()
	first := &alertRecorder{}
	r1 := New(Options{DB: db, Log: quietLogger(), Notify: first.record})
	r1.now = func() time.Time { return clock }

	m, _ := downMonitor(t, db, r1, 900, &clock)

	clock = clock.Add(15 * time.Minute)
	r1.sendDueReminders(ctx)
	if got := len(first.events()); got != 2 {
		t.Fatalf("alerts before restart = %d, want 2", got)
	}

	// A fresh Runner over the same database: the restart.
	second := &alertRecorder{}
	r2 := New(Options{DB: db, Log: quietLogger(), Notify: second.record})
	r2.now = func() time.Time { return clock }
	if err := r2.restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// The second gap is an hour, measured from the reminder the PREVIOUS
	// process sent. 55 more minutes is 70 minutes after confirmation but only
	// 55 after that reminder, so it must stay silent. Those offsets are chosen
	// to separate the two: a schedule that measured from confirmation instead
	// would have fired at 60.
	clock = clock.Add(55 * time.Minute)
	r2.sendDueReminders(ctx)
	if got := second.events(); len(got) != 0 {
		t.Fatalf("alerts = %v, want none — the restart measured the gap from "+
			"confirmation instead of from the last reminder", got)
	}

	// Five more minutes completes the hour, and produces exactly one reminder
	// rather than a catch-up burst.
	clock = clock.Add(5 * time.Minute)
	r2.sendDueReminders(ctx)
	if got := second.events(); len(got) != 1 || got[0] != state.EventIncidentReminder {
		t.Fatalf("alerts = %v, want exactly one reminder", got)
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if inc.ReminderCount != 2 {
		t.Errorf("ReminderCount = %d, want 2 — the count did not carry across the restart", inc.ReminderCount)
	}
}

// A paused monitor is not being checked, so its incident is frozen rather than
// ongoing. Reminding about it would alert on information nobody is refreshing.
func TestPausedMonitorIsNotReminded(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }

	m, _ := downMonitor(t, db, r, 900, &clock)

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}

	clock = clock.Add(2 * time.Hour)
	r.sendDueReminders(ctx)

	if got := rec.events(); len(got) != 1 {
		t.Errorf("alerts = %v, want only the original — a paused monitor was reminded about", got)
	}
}

// A nil notifier means log-only operation, which the option documents. It must
// not also mean "no reminders": the stamp and the event bus are what a
// deployment without a notifier watches.
func TestRemindersStillRunWithoutNotifier(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger()})
	r.now = func() time.Time { return clock }

	m, _ := downMonitor(t, db, r, 900, &clock)

	clock = clock.Add(15 * time.Minute)
	r.sendDueReminders(ctx)

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if inc.ReminderCount != 1 {
		t.Errorf("ReminderCount = %d, want 1 — a nil notifier skipped the reminder entirely",
			inc.ReminderCount)
	}
	if inc.RemindedAt.IsZero() {
		t.Error("RemindedAt was not stamped without a notifier")
	}
}
