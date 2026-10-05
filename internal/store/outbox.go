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

// PendingBefore returns a channel's deliveries that were queued before the
// one with the given id and have not gone out yet, oldest first.
//
// A row held for quiet hours or waiting for a retry is included: it is still
// going to be sent. A suppressed row is not, since it never will be, and nor
// is a failed one. That is how an alert that gave up stops holding back the
// recovery queued after it.
//
// Queue order is row order. Rows are written in the order the notifier
// flushes them, and it flushes a channel's alerts before its recoveries.
func (db *DB) PendingBefore(ctx context.Context, channelID, id int64) ([]Delivery, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		  FROM notif_outbox
		 WHERE channel_id = ? AND id < ? AND status = ? AND suppressed = 0
		 ORDER BY id`, channelID, id, OutboxPending)
	if err != nil {
		return nil, fmt.Errorf("query earlier deliveries: %w", err)
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

// Replaced is an earlier delivery a recovery stands in for, in part or whole.
//
// Payload is what the row still has to say once the outage that recovered is
// taken out of it. Empty means nothing is left, and the row is closed.
type Replaced struct {
	ID      int64
	Payload string
}

// ReplaceWithRecovery folds a recovery and the alerts it closes into one
// delivery, in one transaction.
//
// The recovery takes the payload given and the retry state of from, the
// alert it replaces: its attempts, its last error and its next attempt, so
// the merged message is tried when the alert would have been and gives up
// when the alert would have, instead of starting a schedule of its own. Each
// replaced row keeps what it still has to say, or is closed, marked with
// where its news went, the way a quiet-hours digest closes the rows it
// folds. A crash cannot leave the merged recovery queued with the alert
// still queued beside it.
//
// Every write is conditional on the row still pending, so a row that was
// delivered or gave up in the meantime is left as it is.
// If the recovery itself is no longer pending, nothing is written and
// ErrNotFound is reported.
func (db *DB) ReplaceWithRecovery(ctx context.Context, recovery int64, payload string, from Delivery, replaced []Replaced) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace with recovery %d: %w", recovery, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET payload_json = ?, attempts = ?, last_error = ?, next_attempt_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		payload, from.Attempts, from.LastError, from.NextAttemptAt.Unix(), now, recovery, OutboxPending)
	if err != nil {
		return fmt.Errorf("replace with recovery %d: %w", recovery, err)
	}
	// The alerts are closed only because the recovery carries their news. A
	// recovery that is gone, deleted with its monitor or sent meanwhile,
	// carries nothing, so they stay as they are.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replace with recovery %d: %w", recovery, err)
	}
	if n == 0 {
		return fmt.Errorf("replace with recovery %d: %w: no pending recovery", recovery, ErrNotFound)
	}
	for _, r := range replaced {
		if r.Payload != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE notif_outbox
				   SET payload_json = ?, updated_at = ?
				 WHERE id = ? AND status = ? AND suppressed = 0`,
				r.Payload, now, r.ID, OutboxPending); err != nil {
				return fmt.Errorf("replace with recovery %d: %w", recovery, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE notif_outbox
			   SET suppressed = 1, last_error = ?, updated_at = ?
			 WHERE id = ? AND status = ? AND suppressed = 0`,
			fmt.Sprintf("sent as part of recovery %d: the monitor was back up before this alert went out", recovery),
			now, r.ID, OutboxPending); err != nil {
			return fmt.Errorf("replace with recovery %d: %w", recovery, err)
		}
	}
	return tx.Commit()
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

// WithholdDelivery ends a pending delivery that its channel declined to send,
// such as an SMS over the channel's hourly limit. Like a delivery dropped in
// quiet hours, the row stays, marked, with the reason, so the delivery log can
// say what was not sent and why; it is not retried and not counted as pending.
func (db *DB) WithholdDelivery(ctx context.Context, id int64, reason string) error {
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET suppressed = 1, last_error = ?, updated_at = ?
		 WHERE id = ? AND status = ?`, reason, time.Now().Unix(), id, OutboxPending)
	if err != nil {
		return fmt.Errorf("withhold delivery %d: %w", id, err)
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

	// Pending counts the deliveries still waiting to be sent, whatever
	// their age: a queued alert is work, not history, so no window hides it.
	Pending int
	// Retrying is the part of Pending that has already failed at least one
	// attempt. A delivery queued a moment ago and one that has been bounced
	// three times are both pending; only the second says the channel is in
	// trouble.
	Retrying int
	// Failed counts the deliveries that gave up inside the window.
	Failed int

	// LastError is the most recent failure inside the window, from a
	// delivery that gave up or one still being retried. It answers "why is
	// this channel not delivering" without sending the operator to the log.
	LastError string
	// LastFailedAt and LastDeliveredAt are the newest outcome of each kind
	// inside the window, zero when there was none. Which of the two is newer
	// is what says whether a channel is failing now or failed once and has
	// delivered since.
	LastFailedAt    time.Time
	LastDeliveredAt time.Time
}

// ChannelHealthSince reports, per channel, the outcomes of deliveries that
// finished at or after since, and every delivery still pending.
//
// Scoped to a window rather than all time because the question the interface
// asks is "is this channel working now", and a webhook that failed once last
// month should not wear a red badge forever. The window is on when the
// delivery finished (updated_at), not when it was queued: that is the clock
// the retention pass prunes delivered rows by, so a window no longer than
// DeliveryLogRetention never compares a failure it can see with a success
// that has already been deleted.
//
// A channel with no deliveries in the window has no entry.
func (db *DB) ChannelHealthSince(ctx context.Context, since time.Time) (map[int64]ChannelHealth, error) {
	at := since.Unix()
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id,
		       SUM(CASE WHEN status = 'pending' AND suppressed = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'pending' AND suppressed = 0 AND attempts > 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status = 'failed' AND updated_at >= ? THEN 1 ELSE 0 END),
		       COALESCE(MAX(CASE WHEN status = 'failed' AND updated_at >= ? THEN updated_at END), 0),
		       COALESCE(MAX(CASE WHEN status = 'delivered' AND updated_at >= ? THEN updated_at END), 0)
		  FROM notif_outbox
		 WHERE status = 'pending' OR updated_at >= ?
		 GROUP BY channel_id`, at, at, at, at)
	if err != nil {
		return nil, fmt.Errorf("query channel health: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]ChannelHealth{}
	for rows.Next() {
		var (
			h                         ChannelHealth
			lastFailed, lastDelivered int64
		)
		if err := rows.Scan(&h.ChannelID, &h.Pending, &h.Retrying, &h.Failed, &lastFailed, &lastDelivered); err != nil {
			return nil, err
		}
		if lastFailed > 0 {
			h.LastFailedAt = time.Unix(lastFailed, 0).UTC()
		}
		if lastDelivered > 0 {
			h.LastDeliveredAt = time.Unix(lastDelivered, 0).UTC()
		}
		out[h.ChannelID] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The most recent failure message per channel, fetched separately: doing
	// it in the aggregate above would need a window function, and SQLite's
	// support for those is newer than the oldest build this has to run on.
	// A delivery still being retried counts: its error is the newest thing
	// known about a channel that is failing right now.
	errRows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id, last_error
		  FROM notif_outbox
		 WHERE last_error <> ''
		   AND ((status = 'failed' AND updated_at >= ?)
		     OR (status = 'pending' AND suppressed = 0 AND attempts > 0))
		 ORDER BY channel_id, updated_at DESC, id DESC`, at)
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
// A failed row is evidence the operator may not have seen yet, and it stays
// even when it also carries the maintenance flag: a delivery can fail after it
// was suppressed, and the failure is the part worth keeping. Unsuppressed
// pending rows are still work; other maintenance-suppressed rows are terminal.
func (db *DB) PruneDeliveries(ctx context.Context, before time.Time) (int64, error) {
	res, err := db.Writer.ExecContext(ctx, `
		DELETE FROM notif_outbox
		 WHERE (status = ? OR suppressed = 1)
		   AND status <> ?
		   AND updated_at < ?`, OutboxDelivered, OutboxFailed, before.Unix())
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
