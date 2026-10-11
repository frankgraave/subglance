package store

import (
	"context"
	"database/sql"
	"fmt"
)

// A delivery's monitor_id column names one monitor, and deleting that monitor
// deletes the row (ON DELETE CASCADE). For a message about one monitor that
// is right: it goes with the monitor, as its incidents and heartbeats do.
//
// A grouped alert, a grouped recovery or a quiet-hours digest is one row per
// channel that speaks for several monitors; the columns carry only the first
// monitor and incident, and the others are named in the payload's members.
// Left to the cascade, deleting that first monitor would take the message
// about the others with it: a queued alert would never be sent, and an
// incident of a monitor that still exists would lose the record of where it
// was reported.
//
// So a message lives as long as any monitor it speaks for. The helpers below
// read what a payload speaks for from every "monitor_id" and "incident_id" in
// it, at any depth, which covers the top level, members and a digest's
// entries alike without depending on how deep a given kind of message nests
// them.

// The first id a payload names under a key that is still a row, in document
// order. json_tree refuses a document that is not valid JSON outright, so
// each caller makes sure it only reads valid ones.
const (
	// In an UPDATE of notif_outbox: the row's own payload, leaving out the
	// monitor (or that monitor's incidents) given as the one parameter.
	otherMonitorNamed = `SELECT t.value FROM json_tree(notif_outbox.payload_json) t
	         WHERE t.key = 'monitor_id' AND t.type = 'integer'
	           AND t.value IN (SELECT id FROM monitors WHERE id != ?)
	         ORDER BY t.id LIMIT 1`
	otherIncidentNamed = `SELECT t.value FROM json_tree(notif_outbox.payload_json) t
	         WHERE t.key = 'incident_id' AND t.type = 'integer'
	           AND t.value IN (SELECT id FROM incidents WHERE monitor_id != ?)
	         ORDER BY t.id LIMIT 1`

	// In EnqueueDelivery: the payload being written, as the CTE doc(json).
	monitorNamed = `SELECT t.value FROM doc, json_tree(doc.json) t
	         WHERE t.key = 'monitor_id' AND t.type = 'integer'
	           AND t.value IN (SELECT id FROM monitors)
	         ORDER BY t.id LIMIT 1`
	incidentNamed = `SELECT t.value FROM doc, json_tree(doc.json) t
	         WHERE t.key = 'incident_id' AND t.type = 'integer'
	           AND t.value IN (SELECT id FROM incidents)
	         ORDER BY t.id LIMIT 1`
)

// keepSharedDeliveries moves every delivery of the monitor about to be
// deleted that also speaks for another monitor that still exists onto that
// monitor, and its incident column, when it names the deleted monitor's
// incident, onto the first incident the message names that is not, so the
// cascade leaves the row alone. A
// row that speaks for no other monitor is not touched and goes with the
// deletion, as before.
//
// The payload is not rewritten: it is what was true when the alert fired
// (see EnqueueDelivery), and a grouped alert that named the deleted monitor
// still did.
func keepSharedDeliveries(ctx context.Context, tx *sql.Tx, monitorID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE notif_outbox
		   SET incident_id = COALESCE(
		           (SELECT id FROM incidents WHERE id = notif_outbox.incident_id AND monitor_id != ?),
		           (`+otherIncidentNamed+`)),
		       monitor_id = (`+otherMonitorNamed+`)
		 WHERE monitor_id = ? AND json_valid(payload_json)
		   AND EXISTS (`+otherMonitorNamed+`)`,
		monitorID, monitorID, monitorID, monitorID, monitorID); err != nil {
		return fmt.Errorf("keep deliveries shared with monitor %d: %w", monitorID, err)
	}
	return nil
}
