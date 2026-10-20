package store

import (
	"context"
	"errors"
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
	// Created is when it joined its batch. Read back by IncidentNotifications
	// only; HoldAlerts stamps it.
	Created time.Time
}

// HoldAlert records an alert that has joined a batch and returns its id.
func (db *DB) HoldAlert(ctx context.Context, h HeldAlert) (int64, error) {
	ids, err := db.HoldAlerts(ctx, []HeldAlert{h}, 0)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// HoldAlerts records one alert that has joined a batch on each of its
// channels, in one transaction, and returns the rows' ids in the order given.
//
// owed, when not zero, is the incident whose confirmation the alert
// announces. Its AlertOwed mark is cleared in the same transaction, so the
// alert is on disk as owed or as held at every moment, never as neither.
func (db *DB) HoldAlerts(ctx context.Context, held []HeldAlert, owed int64) ([]int64, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("hold alert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	ids := make([]int64, 0, len(held))
	for _, h := range held {
		var id int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO notif_held (channel_id, monitor_id, payload_json, due_at, created_at)
			VALUES (?, ?, ?, ?, ?)
			RETURNING id`,
			h.ChannelID, h.MonitorID, h.Payload, h.Due.Unix(), now).Scan(&id); err != nil {
			return nil, fmt.Errorf("hold alert: %w", err)
		}
		ids = append(ids, id)
	}
	if err := clearAlertOwed(ctx, tx, []int64{owed}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("hold alert: commit: %w", err)
	}
	return ids, nil
}

// heldColumns is a notif_held row as scanHeld reads it.
const heldColumns = `id, channel_id, monitor_id, payload_json, due_at, created_at`

// incidentHeldAlerts returns the alerts about one incident that are still
// waiting in their grouping windows, one per channel, in the order they were
// held. A waiting alert names a single incident: it is grouped with others
// only when its batch is written to the outbox.
func incidentHeldAlerts(ctx context.Context, q queryer, incidentID int64) ([]HeldAlert, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+heldColumns+`
		  FROM notif_held
		 WHERE json_valid(payload_json)
		   AND json_extract(payload_json, '$.incident_id') = ?
		 ORDER BY id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list held alerts for incident %d: %w", incidentID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []HeldAlert
	for rows.Next() {
		var (
			h            HeldAlert
			due, created int64
		)
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.MonitorID, &h.Payload, &due, &created); err != nil {
			return nil, fmt.Errorf("list held alerts for incident %d: %w", incidentID, err)
		}
		h.Due = time.Unix(due, 0).UTC()
		h.Created = time.Unix(created, 0).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list held alerts for incident %d: %w", incidentID, err)
	}
	return out, nil
}

// OwedIncidents returns every open outage that was confirmed with its alert
// owed and whose alert was never kept on disk (see Incident.AlertOwed),
// oldest first. Only a process that ended between the two writes, or a write
// of the alert that failed, leaves any.
func (db *DB) OwedIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+incidentColumns+`
		  FROM incidents
		 WHERE alert_owed = 1 AND resolved_at IS NULL AND confirmed_at IS NOT NULL
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query owed incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIncidents(rows)
}

// ClearAlertOwed records that a confirmed outage needs no alert after all:
// it has no channel to go to, every one is switched off, or a maintenance
// window took it over.
func (db *DB) ClearAlertOwed(ctx context.Context, incidentID int64) error {
	return clearAlertOwed(ctx, db.Writer, []int64{incidentID})
}

// DeferOwedToMaintenance hands a confirmed outage's alert to maintenance,
// which announces it when a check after the window still sees it down (see
// SetMaintenancePending), and clears its AlertOwed mark in the same write.
func (db *DB) DeferOwedToMaintenance(ctx context.Context, incidentID int64) error {
	if _, err := db.Writer.ExecContext(ctx,
		`UPDATE incidents SET maintenance_pending = 1, alert_owed = 0 WHERE id = ?`, incidentID); err != nil {
		return fmt.Errorf("defer incident %d to maintenance: %w", incidentID, err)
	}
	return nil
}

// clearAlertOwed clears the AlertOwed mark of each incident named; a zero id
// is skipped, so a caller passes what it has without checking.
func clearAlertOwed(ctx context.Context, q execer, incidents []int64) error {
	for _, id := range incidents {
		if id == 0 {
			continue
		}
		if _, err := q.ExecContext(ctx, `UPDATE incidents SET alert_owed = 0 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("clear owed alert of incident %d: %w", id, err)
		}
	}
	return nil
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

// BatchWrite is one batch of alerts on its way to the outbox: the row that
// carries it and the held alerts it releases.
type BatchWrite struct {
	Delivery Delivery
	Held     []int64
}

// EnqueueBatches writes batches to the outbox and removes the held alerts each
// carries, and clears the AlertOwed mark of each incident in owed, in one
// transaction. Every alert is then held, queued or owed at any moment, and
// never in two of those at once or in none.
//
// A batch whose channel, or every monitor it speaks for, was deleted while it
// waited writes nothing, and its entry in the returned slice is
// ErrUnknownChannel or ErrNotFound; the others are still written. The deletion
// took its held rows with it, as it takes queued ones, and a channel that is
// gone will never take the alert, so the owed marks are cleared all the same.
//
// Any other failure rolls everything back: the alerts stay held and the
// incidents owed, so the next start sends them.
func (db *DB) EnqueueBatches(ctx context.Context, writes []BatchWrite, owed []int64) ([]error, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("enqueue delivery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	dropped := make([]error, len(writes))
	for i, w := range writes {
		if _, err := enqueueDelivery(ctx, tx, w.Delivery); err != nil {
			// Neither refusal is an SQL error, so the transaction can
			// carry the other batches.
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnknownChannel) {
				dropped[i] = err
				continue
			}
			return nil, err
		}
		for _, id := range w.Held {
			if _, err := tx.ExecContext(ctx, `DELETE FROM notif_held WHERE id = ?`, id); err != nil {
				return nil, fmt.Errorf("release held alert %d: %w", id, err)
			}
		}
	}
	if err := clearAlertOwed(ctx, tx, owed); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("enqueue delivery: commit: %w", err)
	}
	return dropped, nil
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
