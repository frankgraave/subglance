package api

import (
	"errors"
	"net/http"
	"time"
)

// standardUptimeWindows are the periods a dashboard almost always shows at
// once. Returning all three from one request keeps the common case to a single
// round trip; a caller that wants something else asks for it with ?window=.
var standardUptimeWindows = []string{"24h", "7d", "30d"}

const (
	minUptimeWindow = time.Minute
	maxUptimeWindow = 90 * 24 * time.Hour
)

// uptimeWindow is availability over one period.
//
// Uptime is a pointer because "no samples" and "0% up" are different facts and
// must not share one number. A monitor created a minute ago has no data; a
// monitor that failed every check has 0%. Collapsing them would make a brand
// new monitor look like a total outage, which is exactly the kind of confident
// lie this product exists to avoid.
type uptimeWindow struct {
	Window       string   `json:"window"`
	WindowS      int      `json:"window_s"`
	Total        int      `json:"total"`
	Up           int      `json:"up"`
	Down         int      `json:"down"`
	Uptime       *float64 `json:"uptime"`
	AvgLatencyMS int      `json:"avg_latency_ms,omitempty"`
}

func (s *Server) handleMonitorUptime(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if !s.requireMonitor(w, r, id) {
		return
	}
	ctx := r.Context()

	requested := standardUptimeWindows
	if v := r.URL.Query().Get("window"); v != "" {
		requested = []string{v}
	}

	windows := make([]uptimeWindow, 0, len(requested))
	for _, spec := range requested {
		d, err := parseUptimeWindow(spec)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		stats, err := s.db.Uptime(ctx, id, d)
		if err != nil {
			s.log.Error("uptime", "monitor_id", id, "window", spec, "error", err)
			writeError(w, http.StatusInternalServerError, "could not compute uptime")
			return
		}

		out := uptimeWindow{
			Window:       spec,
			WindowS:      int(d.Seconds()),
			Total:        stats.Total,
			Up:           stats.Up,
			Down:         stats.Down,
			AvgLatencyMS: stats.AvgLatency,
		}
		if stats.Total > 0 {
			pct := stats.Percentage
			out.Uptime = &pct
		}
		windows = append(windows, out)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id": id,
		"windows":    windows,
	})
}

// parseUptimeWindow accepts Go duration syntax plus a "d" suffix for days,
// which time.ParseDuration does not support but every dashboard asks for.
func parseUptimeWindow(spec string) (time.Duration, error) {
	var (
		d   time.Duration
		err error
	)
	if days, ok := parseDaySuffix(spec); ok {
		d = time.Duration(days) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(spec)
		if err != nil {
			return 0, errors.New("window must be a duration such as 24h, 7d or 30m")
		}
	}
	if d < minUptimeWindow || d > maxUptimeWindow {
		return 0, errors.New("window must be between 1m and 90d")
	}
	return d, nil
}

// parseDaySuffix reads "7d" as 7 days. It reports false for anything else,
// including "7d12h": mixing the day suffix with other units would need its own
// parser, and no caller has asked for one.
func parseDaySuffix(spec string) (int, bool) {
	if len(spec) < 2 || spec[len(spec)-1] != 'd' {
		return 0, false
	}
	days := 0
	for _, c := range spec[:len(spec)-1] {
		if c < '0' || c > '9' {
			return 0, false
		}
		days = days*10 + int(c-'0')
		if days > 10000 {
			return 0, false
		}
	}
	return days, true
}
