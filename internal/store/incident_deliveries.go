package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// GetIncident returns one incident. It reports ErrNotFound when absent.
func (db *DB) GetIncident(ctx context.Context, id int64) (Incident, error) {
	row := db.Reader.QueryRowContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents WHERE id = ?", id)
	inc, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, fmt.Errorf("%w: incident %d", ErrNotFound, id)
	}
	if err != nil {
		return Incident{}, fmt.Errorf("read incident %d: %w", id, err)
	}
	return inc, nil
}

// IncidentDeliveries returns every outbox row whose message speaks about the
// incident, oldest first.
//
// The incident_id column alone does not answer that. A message about several
// monitors at once is one row per channel, and it carries the id of the first
// incident in it only; the others are named in the payload's members. So the
// match is on the payload: the alert's own incident_id, or any member's. A
// quiet-hours digest names its incidents the same way.
//
// Only rows queued at or after the incident started are read: nothing can be
// sent about an incident before it exists, and the bound keeps the JSON
// match to the rows that could hold it. A digest that carries this incident
// but was queued earlier, as the first alert of the night it folded, is
// still reachable: the rows it folded name it in their last_error, and
// MergedInto reads that back.
//
// A row whose payload is not valid JSON is matched on its incident_id column,
// so a corrupt payload is listed rather than hidden.
func (db *DB) IncidentDeliveries(ctx context.Context, inc Incident) ([]Delivery, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		  FROM notif_outbox
		 WHERE created_at >= ?
		   AND CASE WHEN json_valid(payload_json) THEN
		         json_extract(payload_json, '$.incident_id') = ?
		         OR EXISTS (SELECT 1 FROM json_each(payload_json, '$.members')
		                     WHERE json_extract(value, '$.incident_id') = ?)
		       ELSE incident_id = ? END
		 ORDER BY created_at, id`,
		inc.StartedAt.Unix(), inc.ID, inc.ID, inc.ID)
	if err != nil {
		return nil, fmt.Errorf("query incident %d deliveries: %w", inc.ID, err)
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

// Carrier returns the id of the delivery that carried a merged delivery's
// news (see MergedInto), or zero when the delivery was not merged or the
// reason no longer names one.
func (d Delivery) Carrier() int64 {
	var rest string
	switch d.MergedInto() {
	case MergedIntoDigest:
		rest = strings.TrimPrefix(d.LastError, foldedIntoDigest)
	case MergedIntoRecovery:
		rest = strings.TrimPrefix(d.LastError, replacedByRecovery)
	default:
		return 0
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end == -1 {
		end = len(rest)
	}
	id, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
