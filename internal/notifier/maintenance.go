package notifier

import (
	"context"
	"database/sql"
	"errors"

	"github.com/frankgraave/subglance/internal/state"
)

func (n *Notifier) filterMaintenance(ctx context.Context, a Alert) (Alert, bool, error) {
	members := a.Members
	if len(members) == 0 {
		// Pre-upgrade groups have names but no member identities. If any window
		// is active, hold the whole legacy group rather than guess its scope.
		if a.Grouped() {
			windows, err := n.db.ListMaintenance(ctx)
			if err != nil {
				return a, false, err
			}
			for _, w := range windows {
				if w.Active(n.now()) {
					return a, false, nil
				}
			}
			return a, true, nil
		}
		members = []Alert{a}
	}
	kept := make([]Alert, 0, len(members))
	for _, member := range members {
		muted, err := n.db.InMaintenance(ctx, member.MonitorID, n.now())
		if err != nil {
			return a, false, err
		}
		if muted {
			if state.Event(member.Event) == state.EventIncidentConfirmed && member.IncidentID != 0 {
				if err := n.db.SetMaintenancePending(ctx, member.IncidentID, true); err != nil {
					return a, false, err
				}
			}
			continue
		}
		// A recovery may have been queued before its grouped down alert was
		// suppressed. Consult the durable intent again at delivery time.
		if state.Event(member.Event) == state.EventIncidentResolved && member.IncidentID != 0 {
			var pending bool
			err := n.db.Reader.QueryRowContext(ctx, `SELECT maintenance_pending FROM incidents WHERE id=?`, member.IncidentID).Scan(&pending)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return a, false, err
			}
			if err == nil && pending {
				continue
			}
		}
		kept = append(kept, member)
	}
	if len(kept) == 0 {
		return a, false, nil
	}
	return Summarise(kept), true, nil
}
