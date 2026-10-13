package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// cutSecret stands in for the notifier's RedactError: it replaces the word
// "secret" and counts its calls, so the test can see which rows it was given.
type cutSecret struct{ calls int }

func (c *cutSecret) redact(msg string, _ map[string]string) string {
	c.calls++
	return strings.ReplaceAll(msg, "secret", "****")
}

func TestRedactDeliveryErrorsRepeatsAndLeavesOwnReasonsAlone(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{Name: "api", Type: "http",
		Target: "https://example.com", IntervalS: 60, TimeoutS: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.CreateChannel(ctx, Channel{Name: "ops", Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/hook/secret"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func() Delivery {
		t.Helper()
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch.ID, MonitorID: m.ID,
			Event: "incident_confirmed", Payload: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	failed := enqueue()
	if err := db.MarkFailed(ctx, failed.ID, `Post "https://example.com/hook/secret": refused`); err != nil {
		t.Fatal(err)
	}
	retrying := enqueue()
	if err := db.MarkRetry(ctx, retrying.ID, `Post "https://example.com/hook/secret": timeout`, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A reason SubGlance wrote itself, on a suppressed row. It holds the
	// word the stand-in replaces, to prove the row is never offered.
	withheld := enqueue()
	const ownReason = "not sent: the secret hourly limit was reached"
	if err := db.WithholdDelivery(ctx, withheld.ID, ownReason); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordChannelFailure(ctx, ch.ID, time.Now(), `Post "https://example.com/hook/secret": refused`); err != nil {
		t.Fatal(err)
	}
	// Dated well back, so that a rewrite stamping the current time shows.
	if _, err := db.Writer.ExecContext(ctx, `UPDATE notif_outbox SET updated_at = 1000000000 WHERE id = ?`, failed.ID); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetDelivery(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}

	cut := &cutSecret{}
	n, err := db.RedactDeliveryErrors(ctx, cut.redact)
	if err != nil {
		t.Fatalf("RedactDeliveryErrors: %v", err)
	}
	if n != 3 {
		t.Errorf("rows changed = %d, want 3 (failed, retrying, failure record)", n)
	}
	if cut.calls != 3 {
		t.Errorf("redact called %d times, want 3: a suppressed row was offered", cut.calls)
	}

	for _, id := range []int64{failed.ID, retrying.ID} {
		d, err := db.GetDelivery(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(d.LastError, "secret") || !strings.Contains(d.LastError, "example.com") {
			t.Errorf("delivery %d last_error = %q", id, d.LastError)
		}
	}
	after, err := db.GetDelivery(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at moved from %v to %v; retention dates a delivery by it", before.UpdatedAt, after.UpdatedAt)
	}
	w, err := db.GetDelivery(ctx, withheld.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w.LastError != ownReason {
		t.Errorf("suppressed row rewritten to %q", w.LastError)
	}
	failures, err := db.ChannelFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := failures[ch.ID].LastError; strings.Contains(got, "secret") {
		t.Errorf("channel failure last_error = %q", got)
	}

	// A second start over rows that are already clean reads them and writes
	// nothing.
	again := &cutSecret{}
	n, err = db.RedactDeliveryErrors(ctx, again.redact)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || again.calls != 3 {
		t.Errorf("second run changed %d rows with %d calls, want 0 rows and 3 calls", n, again.calls)
	}

	// Every start, not once per database: a row an older version writes in
	// full after the first pass, after a downgrade or from a second copy,
	// is cleaned by the next start.
	if err := db.MarkFailed(ctx, retrying.ID, "secret again"); err != nil {
		t.Fatal(err)
	}
	third := &cutSecret{}
	n, err = db.RedactDeliveryErrors(ctx, third.redact)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("run after a full-text write changed %d rows, want 1", n)
	}
	d, err := db.GetDelivery(ctx, retrying.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.LastError, "secret") {
		t.Errorf("row written after the first pass kept its secret: %q", d.LastError)
	}
}
