package api

import (
	"net/http"
	"runtime"
	"time"

	"github.com/frankgraave/subglance/internal/buildinfo"
)

// diagnosticsResponse is the instance card on the settings page: what is
// running, since when, on what database, and whether the check pipeline is
// keeping up. Everything in it is read-only and describes this process.
type diagnosticsResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`

	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`

	Database diagnosticsDatabase `json:"database"`

	// Scheduler is null when no check pipeline is attached, rather than a
	// block of zeros: zero workers busy is a healthy reading, and an API
	// without a scheduler behind it must not report one.
	Scheduler *diagnosticsScheduler `json:"scheduler"`
}

type diagnosticsDatabase struct {
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	WALBytes    int64  `json:"wal_bytes"`
	JournalMode string `json:"journal_mode"`
}

type diagnosticsScheduler struct {
	Workers        int    `json:"workers"`
	Busy           int    `json:"busy"`
	QueueDepth     int    `json:"queue_depth"`
	Scheduled      int    `json:"scheduled"`
	SkippedChecks  uint64 `json:"skipped_checks"`
	ChecksRecorded uint64 `json:"checks_recorded"`
	WriteFailures  uint64 `json:"heartbeat_write_failures"`
}

// handleDiagnostics serves the instance card.
//
// Administrators only. The database path and the runtime describe the host
// rather than the monitors, and the realistic viewer is someone who may look
// at outages without being told where the server keeps its files. /metrics
// stays readable by any role: its counters say nothing about the host.
//
// The worker readings come from the same runner as /metrics, so the page and
// a scrape taken at the same moment cannot disagree.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")

	file, err := s.db.FileStats(r.Context())
	if err != nil {
		s.log.Error("read database file stats", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the database file")
		return
	}

	resp := diagnosticsResponse{
		Version:       buildinfo.Version,
		Commit:        buildinfo.ShortCommit(),
		GoVersion:     runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:     s.startedAt.UTC(),
		UptimeSeconds: int64(time.Since(s.startedAt) / time.Second),
		Database: diagnosticsDatabase{
			Path:        file.Path,
			Bytes:       file.Bytes,
			WALBytes:    file.WALBytes,
			JournalMode: file.JournalMode,
		},
	}
	if s.metrics != nil {
		m := s.metrics.Metrics()
		resp.Scheduler = &diagnosticsScheduler{
			Workers:        m.Workers,
			Busy:           m.Busy,
			QueueDepth:     m.QueueDepth,
			Scheduled:      m.Scheduled,
			SkippedChecks:  m.SkippedChecks,
			ChecksRecorded: m.ChecksRecorded,
			WriteFailures:  m.HeartbeatWriteFailures,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
