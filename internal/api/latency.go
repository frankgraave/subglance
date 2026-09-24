package api

import (
	"errors"
	"net/http"
	"time"
)

// latencyWindows are the periods the latency chart offers. Unlike uptime, the
// window is a closed list rather than any duration: the step each window is
// drawn at is part of the answer, and a free-form window would need a rule
// for picking it that no client has asked for.
var latencyWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

// latencySteps is the ladder a window's step is picked from. Every rung
// divides a day evenly, so steps line up with calendar hours and UTC days
// rather than with whenever the request happened to arrive.
var latencySteps = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// maxLatencyPoints bounds how many steps one window is split into. About two
// hundred is more columns than a detail page is wide in useful pixels, and it
// keeps the payload small enough to refetch every minute.
const maxLatencyPoints = 200

// latencyStep picks the finest rung that keeps the window under
// maxLatencyPoints steps.
func latencyStep(window time.Duration) time.Duration {
	for _, step := range latencySteps {
		if window/step <= maxLatencyPoints {
			return step
		}
	}
	return latencySteps[len(latencySteps)-1]
}

// latencyPoint is one step of the series.
//
// The three latency fields are pointers because a step whose checks all
// failed has no latency at all, and 0ms would draw it as the fastest answer
// the service ever gave.
type latencyPoint struct {
	T       time.Time `json:"t"`
	Checks  int       `json:"checks"`
	Samples int       `json:"samples"`
	Down    int       `json:"down"`
	AvgMS   *int      `json:"avg_ms"`
	MinMS   *int      `json:"min_ms"`
	MaxMS   *int      `json:"max_ms"`
}

func (s *Server) handleMonitorLatency(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.requireMonitor(w, r, id) {
		return
	}

	spec := r.URL.Query().Get("window")
	if spec == "" {
		spec = "24h"
	}
	window, ok := latencyWindows[spec]
	if !ok {
		writeError(w, http.StatusBadRequest, errLatencyWindow.Error())
		return
	}
	step := latencyStep(window)

	// The range ends at the end of the current step and starts a whole number
	// of steps before it, so the first step is as complete as the data allows
	// and the newest one is the step still filling up.
	until := time.Now().UTC().Truncate(step).Add(step)
	since := until.Add(-window)

	points, err := s.db.LatencySeries(r.Context(), id, since, until, step)
	if err != nil {
		s.log.Error("latency series", "monitor_id", id, "window", spec, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load latency history")
		return
	}

	out := make([]latencyPoint, 0, len(points))
	for _, p := range points {
		lp := latencyPoint{T: p.Start, Checks: p.Checks, Samples: p.Samples, Down: p.Down}
		if p.Samples > 0 {
			avg, lo, hi := p.AvgMS, p.MinMS, p.MaxMS
			lp.AvgMS, lp.MinMS, lp.MaxMS = &avg, &lo, &hi
		}
		out = append(out, lp)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id": id,
		"window":     spec,
		"step_s":     int(step.Seconds()),
		"from":       since,
		"to":         until,
		"points":     out,
	})
}

var errLatencyWindow = errors.New("window must be one of 24h, 7d, 30d or 90d")
