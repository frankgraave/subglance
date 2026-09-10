package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// Prober runs a single check on demand, outside the schedule.
//
// The API depends on this interface rather than on the monitor package so the
// two stay decoupled and so tests can substitute a probe that never touches
// the network. A nil Prober disables the endpoint instead of crashing it,
// matching how the event bus is treated.
type Prober interface {
	// CheckNow probes the monitor once and returns the result.
	//
	// Implementations must record the result exactly as a scheduled check
	// would when m.Enabled is true, and must record nothing when it is
	// false. The error is reserved for "the probe could not be run at all"
	// — an unknown monitor type, say. A target that is down is a Result
	// with OK false, not an error.
	CheckNow(ctx context.Context, m store.Monitor) (checker.Result, error)
}

// manualCheckCooldown is the minimum spacing between manual checks of the same
// monitor.
//
// Without it, "check now" is a button that turns one authenticated click into
// unbounded outbound traffic: held down, it would hammer someone else's server
// from our IP and stuff the heartbeat table with results nobody asked for.
// Five seconds is short enough that a human retrying a fix never notices, long
// enough that a stuck key or a loop in a script cannot become an attack.
const manualCheckCooldown = 5 * time.Second

// checkResponse is the wire shape of a manual check.
type checkResponse struct {
	MonitorID int64     `json:"monitor_id"`
	OK        bool      `json:"ok"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMS int       `json:"latency_ms"`

	StatusCode int    `json:"status_code,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Error      string `json:"error,omitempty"`

	CertExpiry *time.Time `json:"cert_expiry,omitempty"`

	// Recorded says whether this result became part of the monitor's
	// history. It is false for paused monitors, and the UI needs to know:
	// a green result that did not move the dashboard would otherwise look
	// like a bug.
	Recorded bool `json:"recorded"`
}

// handleCheckMonitor runs one check immediately and returns its result.
//
// Why this exists: without it the only way to find out whether a fix worked is
// to wait out the interval, which for a monitor checked every fifteen minutes
// makes the feedback loop useless precisely when someone is mid-incident.
//
// Two decisions are worth stating, because they are not the only defensible
// ones:
//
//  1. For an enabled monitor the result goes through the ordinary recording
//     path — heartbeat, state engine, incident bookkeeping, live stream. The
//     point of the button is that a monitor you have just fixed goes green
//     now instead of at the next tick, and that only happens if the result
//     counts. It really is an observation of the target, so counting it
//     toward uptime is honest rather than a distortion.
//
//  2. For a paused monitor nothing is recorded. A paused monitor is one we
//     have promised not to watch; writing heartbeats into that gap would
//     misrepresent an unmonitored period as a monitored one, and any incident
//     opened would be closed again by the reconciliation pass anyway. The
//     probe still runs and the result is still returned, because "is it back
//     yet?" is exactly what someone asks before un-pausing.
func (s *Server) handleCheckMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if s.prober == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this instance runs without a checker, so manual checks are unavailable")
		return
	}

	m, err := s.db.GetMonitor(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.log.Error("check monitor: load", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}

	if wait, ok := s.manualChecks.reserve(id, time.Now(), manualCheckCooldown); !ok {
		// Retry-After is integer seconds, and rounding down would advertise
		// a moment that is still too early — so round up, and never to 0.
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests,
			"a check for this monitor ran moments ago; try again in a few seconds")
		return
	}

	res, err := s.prober.CheckNow(r.Context(), m)
	if err != nil {
		s.log.Error("manual check failed to run", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not run the check")
		return
	}

	s.log.Info("manual check", "monitor_id", id, "monitor", m.Name,
		"ok", res.OK, "recorded", m.Enabled)

	resp := checkResponse{
		MonitorID:  m.ID,
		OK:         res.OK,
		CheckedAt:  res.CheckedAt,
		LatencyMS:  int(res.Latency.Milliseconds()),
		StatusCode: res.StatusCode,
		Kind:       string(res.Kind),
		Error:      res.Error,
		Recorded:   m.Enabled,
	}
	if !res.CertExpiry.IsZero() {
		expiry := res.CertExpiry
		resp.CertExpiry = &expiry
	}
	if resp.CheckedAt.IsZero() {
		resp.CheckedAt = time.Now()
	}

	writeJSON(w, http.StatusOK, resp)
}

// cooldown tracks the last accepted action per key.
type cooldown struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

// reserve claims the next slot for key. It reports how long the caller must
// wait when the slot is not free yet.
//
// Claiming and checking happen under one lock on purpose: two concurrent
// requests that both read "last was long ago" before either writes would both
// be let through, which is the whole failure this guards against.
func (c *cooldown) reserve(key int64, now time.Time, window time.Duration) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.last == nil {
		c.last = make(map[int64]time.Time)
	}
	if prev, seen := c.last[key]; seen {
		if elapsed := now.Sub(prev); elapsed < window {
			return window - elapsed, false
		}
	}
	c.last[key] = now
	return 0, true
}

// forget drops a key's cooldown. Used when a monitor is deleted, so the map
// cannot grow without bound across a long-lived process.
func (c *cooldown) forget(key int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, key)
}
