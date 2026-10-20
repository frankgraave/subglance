package monitor

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The confirmation of an outage whose alert is about to be sent marks the
// incident as owed that alert, in the same write, so a process that ends
// before the notifier has kept the alert on disk leaves a record that it is
// owed (see store.Incident.AlertOwed). An outage that no alert will be sent
// for at that moment is not marked: the next start would otherwise announce
// what maintenance, or a log-only deployment, had decided not to.
func TestConfirmMarksTheAlertOwedOnlyWhenOneIsSent(t *testing.T) {
	for _, c := range []struct {
		name     string
		notify   bool
		maintain bool
		retries  int
		want     bool
	}{
		{"alert handed to the notifier", true, false, 2, true},
		{"confirmed on its first failure", true, false, 1, true},
		{"no notifier", false, false, 2, false},
		{"in maintenance", true, true, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := t.Context()
			db := testDB(t)
			m, err := db.CreateMonitor(ctx, store.Monitor{
				Name: "api", Type: "http", Target: "https://example.invalid",
				IntervalS: 20, TimeoutS: 5, Retries: c.retries, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if c.maintain {
				now := time.Now().UTC()
				if _, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{
					Name: "deploy", MonitorID: m.ID, StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
				}); err != nil {
					t.Fatal(err)
				}
			}
			alerts := &alertRecorder{}
			opts := Options{DB: db, Log: quietLogger()}
			if c.notify {
				// Records the alert and keeps nothing on disk, as a
				// notifier does that is killed before its write.
				opts.Notify = alerts.record
			}
			r := New(opts)
			r.now = func() time.Time { return time.Now().Add(time.Minute) }
			for range c.retries {
				r.record(outcomeFor(m, m.Target, false))
			}

			inc, err := db.OpenIncidentFor(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !inc.Confirmed() {
				t.Fatalf("precondition: the outage was not confirmed: %+v", inc)
			}
			if inc.AlertOwed != c.want {
				t.Fatalf("alert owed = %v, want %v", inc.AlertOwed, c.want)
			}
			if c.want {
				if got := alerts.alerts; len(got) != 1 || !got[0].Incident.AlertOwed {
					t.Fatalf("the notifier was handed %+v, want the incident as owed, so it clears the mark", got)
				}
			}
		})
	}
}
