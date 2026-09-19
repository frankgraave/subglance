package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// This file holds the bulk writes that only the demo-data seeder needs.
//
// They exist because the ordinary write paths are shaped for the product: a
// heartbeat arrives one at a time, from a check that has just finished, and
// RecordHeartbeat gives each one its own transaction so a crash can lose at
// most the beat being written. A seeder writes tens of thousands of beats that
// all already happened, and one transaction per beat turns a two-second job
// into a two-minute one for a guarantee nobody wants here.
//
// They are deliberately narrow and deliberately named. Nothing in the running
// server calls them, and a future reader who finds SeedHeartbeats in a trace
// of production should treat that as the bug it is.

// HourlyBucket is one row of the rolled-up heartbeat table.
//
// The rollup is normally produced by RollupHeartbeats from raw beats that have
// aged past the retention horizon. A seeder has no aged beats to fold — it is
// inventing history — so it writes the buckets directly, which is also the
// only way to give a demo more past than its raw retention window allows.
type HourlyBucket struct {
	MonitorID int64
	// Bucket is the hour the counts belong to. It is truncated to the hour
	// on the way in, because that is what the primary key assumes and a
	// stray minute would quietly create a second bucket for one hour.
	Bucket time.Time

	Up   int
	Down int

	LatencyMin   int
	LatencyMax   int
	LatencyAvg   int
	LatencyCount int
}

// SeedHeartbeats writes many heartbeats, and their response snapshots, in one
// transaction.
//
// Rows are inserted in the order given, so a caller that hands over beats
// oldest first gets ids that ascend with time — which is what any later
// tiebreak on id will assume.
func (db *DB) SeedHeartbeats(ctx context.Context, hbs []Heartbeat) error {
	if len(hbs) == 0 {
		return nil
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed heartbeats: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// One prepared statement for the whole batch: the beats differ only in
	// their values, and re-parsing the same INSERT tens of thousands of
	// times is most of the cost of a seed run.
	beat, err := tx.PrepareContext(ctx, `
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare heartbeat insert: %w", err)
	}
	defer func() { _ = beat.Close() }()

	snap, err := tx.PrepareContext(ctx, `
		INSERT INTO heartbeat_responses (heartbeat_id, body, headers_json, truncated)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare response snapshot insert: %w", err)
	}
	defer func() { _ = snap.Close() }()

	for _, hb := range hbs {
		res, err := beat.ExecContext(ctx,
			hb.MonitorID, hb.TS.Unix(), hb.OK,
			nullInt(hb.LatencyMS), nullInt(hb.StatusCode), nullString(hb.Error))
		if err != nil {
			return fmt.Errorf("insert heartbeat for monitor %d: %w", hb.MonitorID, err)
		}
		if hb.Response == nil {
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("heartbeat id for monitor %d: %w", hb.MonitorID, err)
		}
		var headersJSON any
		if len(hb.Response.Headers) > 0 {
			b, err := json.Marshal(hb.Response.Headers)
			if err != nil {
				return fmt.Errorf("encode response headers: %w", err)
			}
			headersJSON = string(b)
		}
		if _, err := snap.ExecContext(ctx,
			id, hb.Response.Body, headersJSON, hb.Response.Truncated); err != nil {
			return fmt.Errorf("insert response snapshot for monitor %d: %w", hb.MonitorID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed heartbeats: %w", err)
	}
	return nil
}

// SeedHourlyBuckets writes rolled-up hourly rows in one transaction.
//
// Counts are added to whatever the bucket already holds, exactly as
// RollupHeartbeats does: seeding the same hour twice must not silently discard
// the first pass, and merging is the behaviour the rest of the system already
// relies on.
func (db *DB) SeedHourlyBuckets(ctx context.Context, buckets []HourlyBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed buckets: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO heartbeat_hourly (
			monitor_id, bucket, up_count, down_count,
			latency_min, latency_max, latency_avg, latency_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (monitor_id, bucket) DO UPDATE SET
			up_count   = heartbeat_hourly.up_count   + excluded.up_count,
			down_count = heartbeat_hourly.down_count + excluded.down_count,
			latency_min = min(
				COALESCE(heartbeat_hourly.latency_min, excluded.latency_min),
				COALESCE(excluded.latency_min, heartbeat_hourly.latency_min)
			),
			latency_max = max(
				COALESCE(heartbeat_hourly.latency_max, excluded.latency_max),
				COALESCE(excluded.latency_max, heartbeat_hourly.latency_max)
			),
			latency_avg = CASE
				WHEN heartbeat_hourly.latency_count + excluded.latency_count = 0
					THEN NULL
				ELSE CAST(
					(COALESCE(heartbeat_hourly.latency_avg, 0) * heartbeat_hourly.latency_count
					 + COALESCE(excluded.latency_avg, 0) * excluded.latency_count)
					/ (heartbeat_hourly.latency_count + excluded.latency_count)
				AS INTEGER)
			END,
			latency_count = heartbeat_hourly.latency_count + excluded.latency_count`)
	if err != nil {
		return fmt.Errorf("prepare bucket insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, b := range buckets {
		if _, err := stmt.ExecContext(ctx,
			b.MonitorID, b.Bucket.Truncate(bucketSize).Unix(), b.Up, b.Down,
			nullInt(b.LatencyMin), nullInt(b.LatencyMax),
			nullInt(b.LatencyAvg), b.LatencyCount,
		); err != nil {
			return fmt.Errorf("insert hourly bucket for monitor %d: %w", b.MonitorID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed buckets: %w", err)
	}
	return nil
}

// SeedIncident writes one incident with every lifecycle timestamp decided by
// the caller.
//
// The ordinary path spreads those timestamps over four calls — open, confirm,
// remind, resolve — because in the product they happen at four different
// moments, minutes or days apart, and each is a decision the state engine
// makes with only the present in hand. Invented history has all four moments
// available at once, and replaying them through the lifecycle API would mean
// writing "now" four times and then correcting it.
//
// The partial unique index still applies: at most one unresolved incident per
// monitor, whatever a caller believes it is seeding.
func (db *DB) SeedIncident(ctx context.Context, inc Incident) (Incident, error) {
	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO incidents (
			monitor_id, started_at, confirmed_at, resolved_at, acked_at,
			cause, last_error, reminded_at, reminder_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.MonitorID, inc.StartedAt.Unix(),
		seedTime(inc.ConfirmedAt), seedTime(inc.ResolvedAt), seedTime(inc.AckedAt),
		inc.Cause, inc.LastError, seedTime(inc.RemindedAt), inc.ReminderCount)
	if err != nil {
		if isUniqueViolation(err) {
			return Incident{}, fmt.Errorf("%w (monitor %d)", ErrIncidentAlreadyOpen, inc.MonitorID)
		}
		return Incident{}, fmt.Errorf("seed incident for monitor %d: %w", inc.MonitorID, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Incident{}, fmt.Errorf("last insert id: %w", err)
	}
	inc.ID = id
	return inc, nil
}

// SeedDelivery writes one outbox row in whatever state the caller asks for.
//
// EnqueueDelivery cannot do this: it exists to add work, so it hard-codes
// pending, zero attempts and a created_at of now. The notifications screen
// reads the other two states — a channel's health badge is built from the
// failed and pending counts in a recent window — so a demo with only pending
// rows shows every channel as permanently backed up.
func (db *DB) SeedDelivery(ctx context.Context, d Delivery) (Delivery, error) {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	if d.NextAttemptAt.IsZero() {
		d.NextAttemptAt = d.CreatedAt
	}
	if d.Status == "" {
		d.Status = OutboxPending
	}

	var incidentID any
	if d.IncidentID != 0 {
		incidentID = d.IncidentID
	}

	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO notif_outbox
			(channel_id, monitor_id, incident_id, event, payload_json,
			 status, attempts, last_error, next_attempt_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ChannelID, d.MonitorID, incidentID, d.Event, d.Payload,
		d.Status, d.Attempts, d.LastError,
		d.NextAttemptAt.Unix(), d.CreatedAt.Unix(), d.UpdatedAt.Unix())
	if err != nil {
		return Delivery{}, fmt.Errorf("seed delivery: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Delivery{}, fmt.Errorf("last insert id: %w", err)
	}
	d.ID = id
	return d, nil
}

// SeedMonitorTimestamps backdates a monitor's created_at and updated_at.
//
// A monitor with a year of history that claims to have been created this
// morning reads as a bug in the product rather than an artefact of the
// seeder, and "last changed" is shown next to it.
func (db *DB) SeedMonitorTimestamps(ctx context.Context, id int64, createdAt, updatedAt time.Time) error {
	res, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET created_at = ?, updated_at = ? WHERE id = ?",
		createdAt.Unix(), updatedAt.Unix(), id)
	if err != nil {
		return fmt.Errorf("backdate monitor %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: monitor %d", ErrNotFound, id)
	}
	return nil
}

// seedTime renders an optional timestamp for storage: the zero time means the
// moment never happened, which the schema spells NULL.
func seedTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}
