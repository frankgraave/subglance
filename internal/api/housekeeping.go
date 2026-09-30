package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/housekeeping"
	"github.com/frankgraave/subglance/internal/store"
)

// RetentionPasses starts a retention pass on request. housekeeping.Retention
// implements it; the scheduler and the settings page share one, so a pass
// started by hand and a scheduled one can never run at once.
type RetentionPasses interface {
	// Begin claims and starts a pass in the background, or returns
	// housekeeping.ErrPassRunning when one has not finished yet.
	Begin(ctx context.Context, trigger string) error
	// Running reports whether a pass is in progress.
	Running() bool
}

// WithHousekeeping attaches the retention scheduler, and the context that
// bounds the passes and compactions the settings page starts: they outlive
// the request that asked for them, but should stop with the server.
func (s *Server) WithHousekeeping(ctx context.Context, p RetentionPasses) *Server {
	s.background = ctx
	s.passes = p
	return s
}

// backgroundContext is the context for work that outlives its request.
func (s *Server) backgroundContext() context.Context {
	if s.background != nil {
		return s.background
	}
	return context.Background()
}

// compactor is the part of the store a compaction needs.
type compactor interface {
	PlanCompact(ctx context.Context) (store.CompactPlan, error)
	Compact(ctx context.Context) (store.CompactResult, error)
}

func (s *Server) compactor() compactor {
	if s.compactStore != nil {
		return s.compactStore
	}
	return s.db
}

// startedJSON is the body of a 202 from the two actions below.
type startedJSON struct {
	Running bool `json:"running"`
	// EstimateSeconds is how long a compaction is expected to hold the
	// writer. Omitted for a retention pass, whose length depends on what
	// it finds.
	EstimateSeconds *int64 `json:"estimate_seconds,omitempty"`
}

// handleRunRetention starts a retention pass now, for "Run now" on the
// settings page. The pass runs in the background and is recorded like a
// scheduled one, so its outcome appears as last_pass on GET
// /api/v1/settings/retention; a pass over a large table takes longer than a
// request, or a proxy in front of one, should be kept waiting.
//
// What it removes under the windows can be counted beforehand with the
// preview endpoint. What a size limit adds to that cannot be, because it
// depends on how much space the windows free; the card says so.
func (s *Server) handleRunRetention(w http.ResponseWriter, _ *http.Request) {
	if s.passes == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this instance runs without housekeeping, so a retention pass cannot be started")
		return
	}
	// Not the request's context: that ends with this response, and would
	// cancel the pass it just started.
	if err := s.passes.Begin(s.backgroundContext(), housekeeping.TriggerManual); err != nil {
		if errors.Is(err, housekeeping.ErrPassRunning) {
			writeError(w, http.StatusConflict, "a retention pass is already running; its result will appear when it finishes")
			return
		}
		s.log.Error("start retention pass", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start a retention pass")
		return
	}
	s.log.Info("retention pass started from the settings page")
	w.Header().Set("Location", "/api/v1/settings/retention")
	writeJSON(w, http.StatusAccepted, startedJSON{Running: true})
}

// compactOutcomeJSON is how the most recent compaction went.
type compactOutcomeJSON struct {
	FinishedAt  time.Time `json:"finished_at"`
	DurationMS  int64     `json:"duration_ms"`
	BeforeBytes int64     `json:"before_bytes"`
	AfterBytes  int64     `json:"after_bytes"`
	// ShrinkPending is true when a reader kept the file at its old length
	// for now; the next checkpoint shrinks it.
	ShrinkPending bool    `json:"shrink_pending"`
	Error         *string `json:"error"`
}

// handleCompact rewrites the database with a full VACUUM, for "Compact
// database" on the settings page. It is the way out for a database too large
// to have been switched to incremental auto-vacuum at startup, whose file
// therefore never shrinks, and the shipped image has no sqlite3 to do it by
// hand.
//
// The checks that can refuse it run before the answer: a compaction already
// running is a 409, and a disk that cannot hold the copy a VACUUM writes is a
// 507 naming the directory and the numbers, so nothing is started that would
// fill the disk. The rewrite itself runs in the background, because it holds
// the only writer for as long as it takes and that can be minutes.
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	if !s.compacting.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "the database is already being compacted")
		return
	}
	started := false
	defer func() {
		if !started {
			s.compacting.Store(false)
		}
	}()

	plan, err := s.compactor().PlanCompact(r.Context())
	if err != nil {
		s.log.Error("plan database compaction", "error", err)
		writeError(w, http.StatusInternalServerError, "could not work out whether the database can be compacted")
		return
	}
	if plan.Disk != nil {
		writeError(w, http.StatusInsufficientStorage, plan.Disk.Error())
		return
	}

	started = true
	ctx := s.backgroundContext()
	go func() {
		defer s.compacting.Store(false)
		s.compact(ctx)
	}()
	s.log.Info("database compaction started from the settings page",
		"size", store.FormatByteSize(plan.SizeBytes), "estimate", plan.Estimate)
	w.Header().Set("Location", "/api/v1/settings/retention")
	est := seconds(plan.Estimate)
	writeJSON(w, http.StatusAccepted, startedJSON{Running: true, EstimateSeconds: &est})
}

// compact runs one compaction and keeps its outcome for the settings page.
func (s *Server) compact(ctx context.Context) {
	res, err := s.compactor().Compact(ctx)
	out := &compactOutcomeJSON{
		FinishedAt:    time.Now().UTC(),
		DurationMS:    res.Duration.Milliseconds(),
		BeforeBytes:   res.BeforeBytes,
		AfterBytes:    res.AfterBytes,
		ShrinkPending: res.ShrinkPending,
	}
	if err != nil {
		msg := err.Error()
		out.Error = &msg
		s.log.Error("compact the database", "error", err)
	} else {
		s.log.Info("compacted the database",
			"before", store.FormatByteSize(res.BeforeBytes), "after", store.FormatByteSize(res.AfterBytes),
			"duration", res.Duration, "auto_vacuum", res.AutoVacuum, "shrink_pending", res.ShrinkPending)
	}
	s.compactMu.Lock()
	s.lastCompact = out
	s.compactMu.Unlock()
}

// lastCompaction returns the outcome of the most recent compaction started
// since the server started, or nil.
func (s *Server) lastCompaction() *compactOutcomeJSON {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()
	return s.lastCompact
}
