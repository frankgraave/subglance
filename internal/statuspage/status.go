package statuspage

// Live is what the caller knows about a monitor right now. The fields are
// the facts the dashboard's status is derived from, not the dashboard's
// word for it, so the two mappings cannot silently share a mistake.
type Live struct {
	// Enabled is false for a paused monitor.
	Enabled bool
	// Checked is true once the monitor has any result at all. Whether the
	// last one passed is deliberately not asked: until an incident is
	// confirmed, a failed check changes nothing a visitor sees.
	Checked bool
	// Confirmed is true while a confirmed incident is open.
	Confirmed bool
	// Recovering is true while that incident sees passing checks but has
	// not met its recovery threshold yet.
	Recovering bool
	// Notice is true while the open incident is a certificate notice: the
	// service answers, and its certificate expires soon. It is never an
	// outage, so Confirmed is false with it.
	Notice bool
}

// PublicStatus maps a monitor's live state to its public word (design §1.3).
//
// The page states only confirmed facts. A failed check that has not crossed
// the failure threshold is the dashboard's amber "warning", there so the
// operator can look early; in public it stays "up", or the page would
// announce every blip the retry setting exists to absorb. It turns "down" at
// the moment the incident is confirmed, which is the moment the operator's
// own notification goes out.
//
// A recovering monitor is "degraded", not "up": its outage is still open,
// and the page must not call it over before the operator's channels do.
func PublicStatus(l Live) Status {
	switch {
	case !l.Enabled:
		return StatusNotMonitored
	case l.Recovering && l.Confirmed:
		return StatusDegraded
	case l.Confirmed:
		return StatusDown
	case !l.Checked:
		return StatusNoData
	default:
		return StatusUp
	}
}
