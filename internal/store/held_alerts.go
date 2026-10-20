package store

import (
	"context"
	"fmt"
	"time"
)

// HeldAlert is one alert waiting in a grouping window for one channel.
//
// The notifier holds an alert in memory for up to its grouping window before
// it writes the batch to the outbox. The row is the same alert on disk, so a
// process that is killed inside the window does not take the alert with it:
// the next start reads it back (see ListHeldAlerts) and sends it.
type HeldAlert struct {
	ID        int64
	ChannelID int64
	MonitorID int64
	// Payload is the single alert, rendered as the outbox stores one.
	Payload string
	// Due is when the window of the batch it waits in closes.
	Due time.Time
}

// HoldAlert records an alert that has joined a batch and returns its id.
func (db *DB) HoldAlert(ctx context.Context, h HeldAlert) (int64, error) {
	var id int64
	err := db.Writer.QueryRowContext(ctx, `
		INSERT INTO notif_held (channel_id, monitor_id, payload_json, due_at, created_at)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id`,
		h.ChannelID, h.MonitorID, h.Payload, h.Due.Unix(), time.Now().Unix()).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("hold alert: %w", err)
	}
	return id, nil
}

// ListHeldAlerts returns every alert still waiting, in the order they were
// held. Only a process that ended without its final flush leaves any.
func (db *DB) ListHeldAlerts(ctx context.Context) ([]HeldAlert, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT id, channel_id, monitor_id, payload_json, due_at
		  FROM notif_held
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list held alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []HeldAlert
	for rows.Next() {
		var (
			h   HeldAlert
			due int64
		)
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.MonitorID, &h.Payload, &due); err != nil {
			return nil, fmt.Errorf("list held alerts: %w", err)
		}
		h.Due = time.Unix(due, 0).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list held alerts: %w", err)
	}
	return out, nil
}

// EnqueueReleasing writes a batch to the outbox and removes the held alerts it
// carries, in one transaction, so each of them is in exactly one of the two
// tables at any moment.
//
// When nothing is written the transaction is rolled back and the alerts stay
// held, so a write that failed is tried again on the next start. That includes
// a batch dropped because its channel or every monitor it speaks for was
// deleted while it waited (ErrUnknownChannel, ErrNotFound), which leaves
// nothing behind either: the deletion took the held rows with it, as it takes
// queued ones.
func (db *DB) EnqueueReleasing(ctx context.Context, d Delivery, held []int64) (Delivery, error) {
	if len(held) == 0 {
		return db.EnqueueDelivery(ctx, d)
	}
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, fmt.Errorf("enqueue delivery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	out, err := enqueueDelivery(ctx, tx, d)
	if err != nil {
		return Delivery{}, err
	}
	for _, id := range held {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notif_held WHERE id = ?`, id); err != nil {
			return Delivery{}, fmt.Errorf("release held alert %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Delivery{}, fmt.Errorf("enqueue delivery: commit: %w", err)
	}
	return out, nil
}

// ReleaseHeldAlerts removes held alerts without writing anything to the
// outbox, for a row that cannot be read back into an alert.
func (db *DB) ReleaseHeldAlerts(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := db.Writer.ExecContext(ctx, `DELETE FROM notif_held WHERE id = ?`, id); err != nil {
			return fmt.Errorf("release held alert %d: %w", id, err)
		}
	}
	return nil
}
