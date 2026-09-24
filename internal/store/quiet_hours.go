package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// What a channel does with an alert that arrives during its quiet hours.
const (
	// QuietHold keeps the alert and sends it, in one digest with everything
	// else that waited, when the window ends. The default, because it is the
	// only choice that cannot lose an alert.
	QuietHold = "hold"
	// QuietDrop discards the alert. Offered for channels that are worthless
	// after the fact, never chosen for anyone.
	QuietDrop = "drop"
)

// QuietHours is a daily window during which a channel does not deliver.
//
// The bounds are wall-clock times in the channel's own timezone, so "23:00 to
// 07:00" keeps meaning the same night across a DST change. An end earlier than
// the start runs past midnight.
type QuietHours struct {
	ChannelID int64  `json:"-"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Timezone  string `json:"timezone"`
	During    string `json:"during"`
}

// Validate reports the first problem with a quiet-hours definition.
func (q QuietHours) Validate() error {
	start, err := parseClock(q.Start)
	if err != nil {
		return fmt.Errorf("start must be HH:MM")
	}
	end, err := parseClock(q.End)
	if err != nil {
		return fmt.Errorf("end must be HH:MM")
	}
	if start == end {
		// Equal bounds would mean either "never" or "always", and a
		// channel that is always quiet is a channel that is off. Disabling
		// a channel already has its own switch.
		return fmt.Errorf("start and end must differ")
	}
	if q.Timezone == "" || q.Timezone == "Local" {
		return fmt.Errorf("timezone must be an explicit IANA name")
	}
	if _, err := time.LoadLocation(q.Timezone); err != nil {
		return fmt.Errorf("unknown IANA timezone")
	}
	if q.During != QuietHold && q.During != QuietDrop {
		return fmt.Errorf("during must be hold or drop")
	}
	return nil
}

// parseClock turns "HH:MM" into minutes after midnight.
func parseClock(s string) (int, error) {
	if len(s) != 5 {
		return 0, errors.New("not HH:MM")
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Active reports whether at falls inside the window. The window is [start,
// end): an alert at exactly the end time is delivered, not held for a day.
//
// An invalid definition is never active. Quiet hours can only ever delay an
// alert, so the failure that matters is one that holds deliveries by mistake.
func (q QuietHours) Active(at time.Time) bool {
	if q.Validate() != nil {
		return false
	}
	loc, _ := time.LoadLocation(q.Timezone)
	start, _ := parseClock(q.Start)
	end, _ := parseClock(q.End)

	local := at.In(loc)
	now := local.Hour()*60 + local.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}

// EndAfter returns the first moment at or after at when the window ends,
// which is when held deliveries are released.
//
// Computed from the local calendar date rather than by adding hours, so a
// window ending at 07:00 ends at 07:00 on the night the clocks change. A
// wall-clock end that does not exist that night (inside a spring-forward gap)
// resolves to the first valid instant after the gap. time.Date alone does not
// guarantee that: it may pick the offset from before the transition and land
// an hour early, still inside the window, which would hold alerts a day more.
func (q QuietHours) EndAfter(at time.Time) time.Time {
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return at
	}
	end, err := parseClock(q.End)
	if err != nil {
		return at
	}
	local := at.In(loc)
	for day := 0; day <= 2; day++ {
		want := time.Date(local.Year(), local.Month(), local.Day()+day, end/60, end%60, 0, 0, time.UTC)
		t := time.Date(local.Year(), local.Month(), local.Day()+day, end/60, end%60, 0, 0, loc)
		for wallClock(t.In(loc)).Before(want) {
			t = t.Add(time.Minute)
		}
		if !t.Before(at) {
			return t
		}
	}
	return at
}

// wallClock returns t's local date and time to the minute, re-labelled as UTC
// so two wall-clock readings compare without offsets getting in the way.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
}

// GetQuietHours returns a channel's quiet hours. The boolean is false when the
// channel has none.
func (db *DB) GetQuietHours(ctx context.Context, channelID int64) (QuietHours, bool, error) {
	q := QuietHours{ChannelID: channelID}
	err := db.Reader.QueryRowContext(ctx, `
		SELECT starts_at, ends_at, timezone, during
		  FROM notif_quiet_hours
		 WHERE channel_id = ?`, channelID).Scan(&q.Start, &q.End, &q.Timezone, &q.During)
	if errors.Is(err, sql.ErrNoRows) {
		return QuietHours{}, false, nil
	}
	if err != nil {
		return QuietHours{}, false, fmt.Errorf("get quiet hours for channel %d: %w", channelID, err)
	}
	return q, true, nil
}

// SetQuietHours creates or replaces a channel's quiet hours. It reports
// ErrNotFound when the channel does not exist.
//
// Anything already held for the channel is released for re-evaluation in the
// same transaction. A held delivery carries the end of the window that held
// it; after an edit that moment may be hours away from the new end, and an
// alert must not sit out a window nobody configured any more.
func (db *DB) SetQuietHours(ctx context.Context, q QuietHours) error {
	if err := q.Validate(); err != nil {
		return err
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set quiet hours: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM notif_channels WHERE id = ?)`, q.ChannelID).Scan(&exists); err != nil {
		return fmt.Errorf("set quiet hours: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: channel %d", ErrNotFound, q.ChannelID)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notif_quiet_hours (channel_id, starts_at, ends_at, timezone, during)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			starts_at = excluded.starts_at, ends_at = excluded.ends_at,
			timezone = excluded.timezone, during = excluded.during`,
		q.ChannelID, q.Start, q.End, q.Timezone, q.During); err != nil {
		return fmt.Errorf("set quiet hours: %w", err)
	}
	if err := releaseQuietHeld(ctx, tx, q.ChannelID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearQuietHours removes a channel's quiet hours and releases whatever they
// were holding. It reports ErrNotFound when the channel has none.
func (db *DB) ClearQuietHours(ctx context.Context, channelID int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clear quiet hours: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM notif_quiet_hours WHERE channel_id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("clear quiet hours: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("clear quiet hours: %w", err)
	} else if n == 0 {
		return fmt.Errorf("%w: quiet hours for channel %d", ErrNotFound, channelID)
	}
	if err := releaseQuietHeld(ctx, tx, channelID); err != nil {
		return err
	}
	return tx.Commit()
}

// releaseQuietHeld makes a channel's held deliveries due now. They keep their
// held mark, so the worker still folds them into one digest rather than
// sending the night's alerts one by one.
func releaseQuietHeld(ctx context.Context, tx *sql.Tx, channelID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET next_attempt_at = ?, updated_at = ?
		 WHERE channel_id = ? AND quiet_held = 1 AND status = ? AND suppressed = 0`,
		time.Now().Unix(), time.Now().Unix(), channelID, OutboxPending); err != nil {
		return fmt.Errorf("release held deliveries: %w", err)
	}
	return nil
}

// HoldDelivery parks a pending delivery until a quiet-hours window ends.
//
// No attempt is charged: nothing was sent, so the channel's retry budget is
// still whole when the window opens.
func (db *DB) HoldDelivery(ctx context.Context, id int64, until time.Time) error {
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET quiet_held = 1, next_attempt_at = ?, last_error = 'held for quiet hours', updated_at = ?
		 WHERE id = ? AND status = ?`, until.Unix(), time.Now().Unix(), id, OutboxPending)
	if err != nil {
		return fmt.Errorf("hold delivery %d: %w", id, err)
	}
	return nil
}

// DropDelivery terminates a delivery that arrived during quiet hours on a
// channel set to drop. The row stays, marked, so the interface can still say
// what was not sent and why.
func (db *DB) DropDelivery(ctx context.Context, id int64) error {
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET suppressed = 1, last_error = 'dropped during quiet hours', updated_at = ?
		 WHERE id = ? AND status = ?`, time.Now().Unix(), id, OutboxPending)
	if err != nil {
		return fmt.Errorf("drop delivery %d: %w", id, err)
	}
	return nil
}

// StillHeld reports whether a delivery is still waiting on quiet hours.
//
// A sweep loads a batch of due rows before it acts on any of them, and the
// first held row of a channel to be attempted folds the rest into its digest.
// The others are still in that batch; this is how their attempt learns that
// their content has already gone out and must not be sent a second time.
func (db *DB) StillHeld(ctx context.Context, id int64) (bool, error) {
	var held bool
	err := db.Writer.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM notif_outbox
		               WHERE id = ? AND quiet_held = 1 AND status = ? AND suppressed = 0)`,
		id, OutboxPending).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("check held delivery %d: %w", id, err)
	}
	return held, nil
}

// ListQuietHours returns every channel's quiet hours, keyed by channel.
func (db *DB) ListQuietHours(ctx context.Context) (map[int64]QuietHours, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id, starts_at, ends_at, timezone, during FROM notif_quiet_hours`)
	if err != nil {
		return nil, fmt.Errorf("list quiet hours: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]QuietHours{}
	for rows.Next() {
		var q QuietHours
		if err := rows.Scan(&q.ChannelID, &q.Start, &q.End, &q.Timezone, &q.During); err != nil {
			return nil, err
		}
		out[q.ChannelID] = q
	}
	return out, rows.Err()
}

// HeldDeliveries returns every delivery a channel's quiet hours are holding,
// oldest first, whether or not its release time has come.
func (db *DB) HeldDeliveries(ctx context.Context, channelID int64) ([]Delivery, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		  FROM notif_outbox
		 WHERE channel_id = ? AND quiet_held = 1 AND status = ? AND suppressed = 0
		 ORDER BY created_at, id`, channelID, OutboxPending)
	if err != nil {
		return nil, fmt.Errorf("query held deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FoldDigest turns a channel's held deliveries into one.
//
// The row in keep takes the digest payload and becomes an ordinary pending
// delivery, due at due, with the normal retry schedule behind it. The rows in
// folded are closed in the same transaction, marked with where their content
// went, so a crash cannot leave the digest queued and its parts queued again
// beside it.
func (db *DB) FoldDigest(ctx context.Context, keep int64, event, payload string, folded []int64, due time.Time) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fold digest: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET payload_json = ?, event = ?, quiet_held = 0,
		       next_attempt_at = ?, last_error = '', updated_at = ?
		 WHERE id = ?`, payload, event, due.Unix(), now, keep); err != nil {
		return fmt.Errorf("fold digest: %w", err)
	}
	for _, id := range folded {
		if _, err := tx.ExecContext(ctx, `
			UPDATE notif_outbox
			   SET suppressed = 1, quiet_held = 0,
			       last_error = ?, updated_at = ?
			 WHERE id = ?`, fmt.Sprintf("sent in quiet-hours digest %d", keep), now, id); err != nil {
			return fmt.Errorf("fold digest: %w", err)
		}
	}
	return tx.Commit()
}
