package monitor

// Flapping exposes the same live suppression state consulted by the reminder
// loop. The API reads this facet of its existing prober; it does not infer
// suppression from historic heartbeats or keep a second cache of engine state.
func (r *Runner) Flapping(monitorID int64) bool {
	return r.engine.Flapping(monitorID)
}
