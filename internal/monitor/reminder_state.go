package monitor

// Flapping exposes the same live suppression state consulted by the reminder
// loop. The API reads this facet of its existing prober; it does not infer
// suppression from historic heartbeats or keep a second cache of engine state.
func (r *Runner) Flapping(monitorID int64) bool {
	return r.engine.Flapping(monitorID)
}

// Recovery exposes a recovering monitor's passing streak, for the API to show
// "Recovering (1 of 2)". Read from the engine for the same reason as
// Flapping: the streak is in-memory state, and a second copy would drift.
func (r *Runner) Recovery(monitorID int64) (passes, threshold int, ok bool) {
	return r.engine.Recovery(monitorID)
}
