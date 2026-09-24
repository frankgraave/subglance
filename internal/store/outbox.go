package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Outbox delivery statuses.
const (
	// OutboxPending is waiting for its next attempt.
	OutboxPending = "pending"
	// OutboxDelivered reached the channel and is done.
	OutboxDelivered = "delivered"
	// OutboxFailed exhausted its attempts. Terminal: the dead letter.
	OutboxFailed = "failed"
)

// Delivery is one queued notification: one alert aimed at one channel.
//
// An alert for a monitor with three channels becomes three rows. Keeping them
// separate means a broken Slack webhook cannot hold up the e-mail that was
// going to the person actually on call, and each gets its own retry schedule.
type Delivery struct {
	ID int64

	ChannelID  int64
	MonitorID  int64
	IncidentID int64 // zero when the alert has no incident behind it

	Event   string
	Payload string

	Status    string
	Attempts  int
	LastError string

	// QuietHeld is true while the delivery waits for its channel's quiet
	// hours to end, rather than for a retry.
	QuietHeld bool

	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const deliveryColumns = `id, channel_id, monitor_id, incident_id, event,
	payload_json, status, attempts, last_error, next_attempt_at, created_at, updated_at,
	quiet_held`

// EnqueueDelivery adds one notification to the outbox, due immediately.
//
// It takes the payload already rendered. The queue stores what should be said,
// not the ingredients to work it out later: a retry an hour after the fact has
// to repeat the original message, not recompute a fresher one.
func (db *DB) EnqueueDelivery(ctx context.Context, d Delivery) (Delivery, error) {
	now := time.Now().Unix()
	due := d.NextAttemptAt
	if due.IsZero() {
		due = time.Unix(now, 0)
	}

	var incidentID any
	if d.IncidentID != 0 {
		incidentID = d.IncidentID
	}

	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO notif_outbox
			(channel_id, monitor_id, incident_id, event, payload_json,
			 status, attempts, next_attempt_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		d.ChannelID, d.MonitorID, incidentID, d.Event, d.Payload,
		OutboxPending, due.Unix(), now, now)
	if err != nil {
		return Delivery{}, fmt.Errorf("enqueue delivery: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Delivery{}, fmt.Errorf("last insert id: %w", err)
	}

	d.ID = id
	d.Status = OutboxPending
	d.NextAttemptAt = time.Unix(due.Unix(), 0).UTC()
	d.CreatedAt = time.Unix(now, 0).UTC()
	d.UpdatedAt = d.CreatedAt
	return d, nil
}

// DueDeliveries returns pending deliveries whose next attempt has come,
// oldest first, at most limit rows.
//
// The limit exists so a backlog built up during a long outage drains in
// batches instead of being loaded into memory at once — the moment a
// monitoring tool is least able to afford a large allocation is right after
// the thing it monitors came back.
func (db *DB) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		  FROM notif_outbox
		 WHERE status = ? AND suppressed = 0 AND next_attempt_at <= ?
		 ORDER BY next_attempt_at, id
		 LIMIT ?`, OutboxPending, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("query due deliveries: %w", err)
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

// GetDelivery returns one delivery. It reports ErrNotFound when absent.
func (db *DB) GetDelivery(ctx context.Context, id int64) (Delivery, error) {
	row := db.Reader.QueryRowContext(ctx,
		"SELECT "+deliveryColumns+" FROM notif_outbox WHERE id = ?", id)
	d, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, fmt.Errorf("%w: delivery %d", ErrNotFound, id)
	}
	return d, err
}

// MarkDelivered records a successful send.
func (db *DB) MarkDelivered(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET status = ?, attempts = attempts + 1, last_error = '', updated_at = ?
		 WHERE id = ?`, OutboxDelivered, now, id)
	if err != nil {
		return fmt.Errorf("mark delivered %d: %w", id, err)
	}
	return nil
}

// MarkRetry records a failed attempt and schedules the next one.
//
// The caller decides the delay, because the backoff belongs with the policy in
// the notifier, not with the storage.
func (db *DB) MarkRetry(ctx context.Context, id int64, cause string, next time.Time) error {
	now := time.Now().Unix()
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET attempts = attempts + 1, last_error = ?, next_attempt_at = ?, updated_at = ?
		 WHERE id = ?`, cause, next.Unix(), now, id)
	if err != nil {
		return fmt.Errorf("mark retry %d: %w", id, err)
	}
	return nil
}

// DeferDelivery postpones a pending delivery whose prerequisites could not be
// evaluated. No send was attempted, so the channel's retry budget is preserved.
func (db *DB) DeferDelivery(ctx context.Context, id int64, cause string, next time.Time) error {
	_, err := db.Writer.ExecContext(ctx, `
  UPDATE notif_outbox
     SET last_error = ?, next_attempt_at = ?, updated_at = ?
   WHERE id = ? AND status = ?`, cause, next.Unix(), time.Now().Unix(), id, OutboxPending)
	if err != nil {
		return fmt.Errorf("defer delivery %d: %w", id, err)
	}
	return nil
}

// MarkFailed moves a delivery to the dead letter: its attempts ran out.
func (db *DB) MarkFailed(ctx context.Context, id int64, cause string) error {
	now := time.Now().Unix()
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET status = ?, attempts = attempts + 1, last_error = ?, updated_at = ?
		 WHERE id = ?`, OutboxFailed, cause, now, id)
	if err != nil {
		return fmt.Errorf("mark failed %d: %w", id, err)
	}
	return nil
}

// ChannelHealth summarises how one channel's deliveries are going.
type ChannelHealth struct {
	ChannelID int64
	Pending   int
	Failed    int

	// LastError is the most recent failure, empty when the last attempts
	// went through. It answers "why is this channel not delivering" without
	// sending the operator to the log.
	LastError    string
	LastFailedAt time.Time
}

// ChannelHealthSince reports pending and failed counts per channel for
// deliveries created at or after since.
//
// Scoped to a window rather than all time because the question the interface
// asks is "is this channel working now", and a webhook that failed once last
// month should not wear a red badge forever.
func (db *DB) ChannelHealthSince(ctx context.Context, since time.Time) (map[int64]ChannelHealth, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id,
		       SUM(CASE WHEN status = 'pending' AND suppressed = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'failed'  THEN 1 ELSE 0 END),
		       COALESCE(MAX(CASE WHEN status = 'failed' THEN updated_at END), 0)
		  FROM notif_outbox
		 WHERE created_at >= ?
		 GROUP BY channel_id`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query channel health: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]ChannelHealth{}
	for rows.Next() {
		var (
			h            ChannelHealth
			lastFailedAt int64
		)
		if err := rows.Scan(&h.ChannelID, &h.Pending, &h.Failed, &lastFailedAt); err != nil {
			return nil, err
		}
		if lastFailedAt > 0 {
			h.LastFailedAt = time.Unix(lastFailedAt, 0).UTC()
		}
		out[h.ChannelID] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The most recent failure message per channel, fetched separately: doing
	// it in the aggregate above would need a window function, and SQLite's
	// support for those is newer than the oldest build this has to run on.
	errRows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id, last_error
		  FROM notif_outbox
		 WHERE status = 'failed' AND created_at >= ? AND last_error <> ''
		 ORDER BY channel_id, updated_at DESC`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query channel errors: %w", err)
	}
	defer func() { _ = errRows.Close() }()

	for errRows.Next() {
		var (
			id  int64
			msg string
		)
		if err := errRows.Scan(&id, &msg); err != nil {
			return nil, err
		}
		h, ok := out[id]
		if !ok || h.LastError != "" {
			continue // ORDER BY put the newest first; keep it
		}
		h.LastError = msg
		out[id] = h
	}
	return out, errRows.Err()
}

// PruneDeliveries removes delivered or suppressed rows older than before, and reports how
// many went.
//
// A failed row is evidence the operator may not have seen yet. Unsuppressed
// pending rows are still work; maintenance-suppressed rows are terminal.
func (db *DB) PruneDeliveries(ctx context.Context, before time.Time) (int64, error) {
	res, err := db.Writer.ExecContext(ctx, `
		DELETE FROM notif_outbox
		 WHERE (status = ? OR suppressed = 1) AND updated_at < ?`, OutboxDelivered, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune deliveries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune deliveries: %w", err)
	}
	return n, nil
}

func scanDelivery(s scanner) (Delivery, error) {
	var (
		d          Delivery
		incidentID sql.NullInt64
		next       int64
		created    int64
		updated    int64
	)
	if err := s.Scan(&d.ID, &d.ChannelID, &d.MonitorID, &incidentID, &d.Event,
		&d.Payload, &d.Status, &d.Attempts, &d.LastError,
		&next, &created, &updated, &d.QuietHeld); err != nil {
		return Delivery{}, err
	}

	if incidentID.Valid {
		d.IncidentID = incidentID.Int64
	}
	d.NextAttemptAt = time.Unix(next, 0).UTC()
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.UpdatedAt = time.Unix(updated, 0).UTC()
	return d, nil
}
