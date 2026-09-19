package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// CaptureReason records why a particular failure did not store a response.
// The zero value is unknown, never a deduction from current configuration.
type CaptureReason string

const (
	CaptureDisabled CaptureReason = "disabled"
	CaptureFlapping CaptureReason = "flapping"
	CaptureBudget   CaptureReason = "budget"
)

// RecordHeartbeatWithCaptureReason persists the runner's actual decision.
// A suppressed response has no snapshot, so heartbeat and reason fit in one
// atomic INSERT. The ordinary snapshot transaction stays in RecordHeartbeat.
func (db *DB) RecordHeartbeatWithCaptureReason(ctx context.Context, hb Heartbeat, reason CaptureReason) error {
	if reason == "" {
		return db.RecordHeartbeat(ctx, hb)
	}
	if hb.OK || hb.Response != nil {
		return fmt.Errorf("capture reason requires a failed heartbeat without a snapshot")
	}
	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error, response_capture_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hb.MonitorID, hb.TS.Unix(), hb.OK, nullInt(hb.LatencyMS),
		nullInt(hb.StatusCode), nullString(hb.Error), reason)
	if err != nil {
		return fmt.Errorf("insert heartbeat capture reason for monitor %d: %w", hb.MonitorID, err)
	}
	return nil
}

// HeartbeatCaptureReasons loads only the requested page's historical decisions.
// Kept off bulk dashboard reads just like response bodies. No current monitor
// settings or engine state participate in this read.
func (db *DB) HeartbeatCaptureReasons(ctx context.Context, hbs []Heartbeat) (map[int64]CaptureReason, error) {
	out := make(map[int64]CaptureReason)
	ids := make([]int64, 0, len(hbs))
	for _, hb := range hbs {
		if !hb.OK && hb.Response == nil {
			ids = append(ids, hb.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode heartbeat ids: %w", err)
	}
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT id, response_capture_reason FROM heartbeats
		WHERE id IN (SELECT value FROM json_each(?)) AND response_capture_reason IS NOT NULL`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("query capture reasons: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var reason CaptureReason
		if err := rows.Scan(&id, &reason); err != nil {
			return nil, fmt.Errorf("scan capture reason: %w", err)
		}
		out[id] = reason
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query capture reasons: %w", err)
	}
	return out, nil
}
