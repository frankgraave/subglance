package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/backup"
)

// BackupStatusSource reports on scheduled backups.
type BackupStatusSource interface {
	Status() backup.Status
}

// WithBackups attaches the scheduled backup runner. A nil source means backups
// are not configured, which the endpoint reports as such; never calling this
// means the state is unknown (503), the same split as WithWatchdog.
func (s *Server) WithBackups(b BackupStatusSource) *Server {
	s.backupsWired = true
	s.backups = b
	return s
}

// backupResponse is the wire shape of GET /api/v1/backup. The nullable
// fields are pointers so "never happened" reads as null rather than as the
// zero time, which a client would render as 1970.
type backupResponse struct {
	Configured    bool       `json:"configured"`
	Target        *string    `json:"target"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastObject    *string    `json:"last_object"`
	LastSizeBytes *int64     `json:"last_size_bytes"`
	LastError     *string    `json:"last_error"`
	LastErrorAt   *time.Time `json:"last_error_at"`
	Failures      uint64     `json:"failures"`
}

// handleBackup serves the backup status for the diagnostics section of the
// settings page.
//
// Administrators only. The target names a bucket, and the last error can quote
// the service's reply about it; neither is something a viewer needs, and a
// bucket name is half of what an attacker needs to go looking for it.
func (s *Server) handleBackup(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !s.backupsWired {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup state unavailable"})
		return
	}
	if s.backups == nil {
		writeJSON(w, http.StatusOK, backupResponse{})
		return
	}
	st := s.backups.Status()
	out := backupResponse{Configured: true, Target: &st.Target, Failures: st.Failures}
	if !st.LastSuccess.IsZero() {
		at := st.LastSuccess.UTC()
		out.LastSuccessAt, out.LastObject, out.LastSizeBytes = &at, &st.LastObject, &st.LastSize
	}
	if st.LastError != "" {
		at := st.LastErrorAt.UTC()
		out.LastError, out.LastErrorAt = &st.LastError, &at
	}
	writeJSON(w, http.StatusOK, out)
}

// writeBackupMetrics adds the backup series to /metrics when backups are on.
//
// Nothing is written when they are off, rather than zeros: a
// last-success timestamp of 0 would trip every "backup older than a day"
// alert on an instance that never asked for backups.
func (s *Server) writeBackupMetrics(b *strings.Builder) {
	if s.backups == nil {
		return
	}
	st := s.backups.Status()
	var last uint64
	if !st.LastSuccess.IsZero() {
		last = uint64(st.LastSuccess.Unix())
	}
	gauge(b, "subglance_backup_last_success_timestamp_seconds",
		"Unix time of the last backup that reached the bucket, 0 if none has since start.", last)
	gauge(b, "subglance_backup_last_size_bytes",
		"Compressed size of the last successful backup.", uint64(max(st.LastSize, 0)))
	counter(b, "subglance_backup_failures_total",
		"Scheduled or manual backups that failed.", st.Failures)
}
