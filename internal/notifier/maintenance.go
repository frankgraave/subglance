package notifier

import (
	"context"

	"github.com/frankgraave/subglance/internal/state"
)

func (n *Notifier) filterMaintenance(ctx context.Context, a Alert, channelID int64) (Alert, bool, []int64, error) {
	var held []int64
	members := a.Members
	if len(members) == 0 {
		// Pre-upgrade groups have names but no member identities. If any window
		// is active, hold the whole legacy group rather than guess its scope.
		if a.Grouped() {
			windows, err := n.db.ListMaintenance(ctx)
			if err != nil {
				return a, false, held, err
			}
			for _, w := range windows {
				if w.Active(n.now()) {
					return a, false, held, nil
				}
			}
			return a, true, held, nil
		}
		members = []Alert{a}
	}
	kept := make([]Alert, 0, len(members))
	for _, member := range members {
		muted, err := n.db.InMaintenance(ctx, member.MonitorID, n.now())
		if err != nil {
			return a, false, held, err
		}
		if muted {
			if state.Event(member.Event) == state.EventIncidentConfirmed && member.IncidentID != 0 {
				held = append(held, member.IncidentID)
			}
			continue
		}
		// A recovery may have been queued before its grouped down alert was
		// suppressed. Consult the durable intent again at delivery time.
		if (state.Event(member.Event) == state.EventIncidentResolved || state.Event(member.Event) == state.EventIncidentReminder) && member.IncidentID != 0 {
			pending, err := n.db.MaintenanceRecoverySuppressed(ctx, member.IncidentID, channelID)
			if err != nil {
				return a, false, held, err
			}
			if pending {
				continue
			}
		}
		kept = append(kept, member)
	}
	if len(kept) == 0 {
		return a, false, held, nil
	}
	if a.Digest {
		return BuildDigest(kept, a.DigestZone, a.At), true, held, nil
	}
	return Summarise(kept), true, held, nil
}
