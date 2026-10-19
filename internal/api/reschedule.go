package api

// Rescheduler is told that the set of scheduled monitors has changed, so the
// change is checked now rather than at the scheduler's next reload tick.
//
// The API depends on this interface rather than on the monitor package, as it
// does for Prober, so tests can count the calls without a running scheduler.
// Nil means nothing is told, and the tick, every 30 seconds, picks the change
// up on its own.
type Rescheduler interface {
	// ReloadSoon asks for the monitor set to be read again shortly. It must
	// not block and cannot fail: it is called on the request's path, after
	// the write, and the request has succeeded whatever happens next.
	ReloadSoon()
}

// WithRescheduler attaches the scheduler that is told about monitor writes.
func (s *Server) WithRescheduler(r Rescheduler) *Server {
	s.rescheduler = r
	return s
}

// monitorsChanged tells the scheduler that a write changed what it checks:
// a monitor created, edited, deleted, paused or resumed, an import, a reset.
//
// Called once per request, after the write, whether the request wrote one
// monitor or a hundred; the scheduler folds requests that arrive close
// together into one read.
func (s *Server) monitorsChanged() {
	if s.rescheduler != nil {
		s.rescheduler.ReloadSoon()
	}
}
