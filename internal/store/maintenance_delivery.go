package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// HasPendingMaintenanceChannels never changes the incident-wide intent: a
// channel retry must not silence recovery on a channel that already delivered.
func (db *DB) HasPendingMaintenanceChannels(ctx context.Context, incidentID int64) (bool, error) {
	var pending bool
	err := db.Reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM maintenance_channel_alerts WHERE incident_id=? AND pending=1)`, incidentID).Scan(&pending)
	return pending, err
}

func (db *DB) MaintenanceRecoverySuppressed(ctx context.Context, incidentID, channelID int64) (bool, error) {
	var pending bool
	err := db.Reader.QueryRowContext(ctx, `SELECT maintenance_pending OR EXISTS(SELECT 1 FROM maintenance_channel_alerts WHERE incident_id=incidents.id AND channel_id=? AND pending=1) FROM incidents WHERE id=?`, channelID, incidentID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return pending, err
}

// FilterMaintenanceDelivery atomically removes muted members and remembers
// their initial-alert intent for this channel. A crash cannot leave both the
// original payload and a separately releasable intent behind.
func (db *DB) FilterMaintenanceDelivery(ctx context.Context, d Delivery, payload string, keep bool, held []int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range held {
		if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_channel_alerts(incident_id,channel_id,pending) VALUES(?,?,1) ON CONFLICT(incident_id,channel_id) DO UPDATE SET pending=1`, id, d.ChannelID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE notif_outbox SET payload_json=?, suppressed=?, last_error=CASE WHEN ? THEN '' ELSE 'suppressed by scheduled maintenance' END, updated_at=? WHERE id=?`, payload, !keep, keep, time.Now().Unix(), d.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// EnqueueMaintenanceDeliveries transfers intent and all eligible channel
// deliveries in one transaction. Deferred initials bypass the in-memory group
// window. False means this is an ordinary initial alert, with no deferred intent.
func (db *DB) EnqueueMaintenanceDeliveries(ctx context.Context, incidentID int64, deliveries []Delivery) (bool, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var all, known, resolved bool
	err = tx.QueryRowContext(ctx, `SELECT maintenance_pending, EXISTS(SELECT 1 FROM maintenance_channel_alerts WHERE incident_id=incidents.id), resolved_at IS NOT NULL FROM incidents WHERE id=?`, incidentID).Scan(&all, &known, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !all && !known {
		return false, nil
	}
	if resolved {
		return true, nil
	}
	now := time.Now().Unix()
	for _, d := range deliveries {
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM maintenance_channel_alerts WHERE incident_id=? AND channel_id=? AND pending=1)`, incidentID, d.ChannelID).Scan(&pending); err != nil {
			return true, err
		}
		if !all && !pending {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO notif_outbox(channel_id,monitor_id,incident_id,event,payload_json,status,attempts,next_attempt_at,created_at,updated_at) VALUES(?,?,?,?,?,'pending',0,?,?,?)`, d.ChannelID, d.MonitorID, incidentID, d.Event, d.Payload, now, now, now); err != nil {
			return true, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_channel_alerts(incident_id,channel_id,pending) VALUES(?,?,0) ON CONFLICT(incident_id,channel_id) DO UPDATE SET pending=0`, incidentID, d.ChannelID); err != nil {
			return true, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE incidents SET maintenance_pending=0 WHERE id=?`, incidentID); err != nil {
		return true, err
	}
	return true, tx.Commit()
}
