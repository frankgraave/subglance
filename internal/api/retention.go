package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// WithRetentionPins records which retention windows were fixed by a flag or
// an environment variable, so the settings endpoints can show them as
// read-only and refuse to store a value underneath them.
func (s *Server) WithRetentionPins(p store.RetentionPins) *Server {
	s.retentionPins = p
	return s
}

// retentionWindowJSON is one window on the wire. Seconds of zero means the
// data is kept forever, the same convention as the flags.
type retentionWindowJSON struct {
	Seconds  int64   `json:"seconds"`
	Source   string  `json:"source"`
	PinnedBy *string `json:"pinned_by"`
	// SetAsideSeconds is the stored value that a pinned window made invalid
	// and that is therefore not in force. Null when nothing was set aside.
	SetAsideSeconds *int64 `json:"set_aside_seconds"`
}

type retentionTableJSON struct {
	Name       string  `json:"name"`
	Rows       int64   `json:"rows"`
	Bytes      *int64  `json:"bytes"`
	RowsPerDay float64 `json:"rows_per_day"`
}

type retentionResponse struct {
	Raw               retentionWindowJSON  `json:"raw"`
	Rollup            retentionWindowJSON  `json:"rollup"`
	MinimumRawSeconds int64                `json:"minimum_raw_seconds"`
	RunAt             retentionRunAtJSON   `json:"run_at"`
	MaxDatabaseSize   maxDatabaseSizeJSON  `json:"max_database_size"`
	Tables            []retentionTableJSON `json:"tables"`
	// LastPass is the most recent recorded pass, or null when none has
	// been recorded or the record cannot be read.
	LastPass *retentionPassJSON `json:"last_pass"`
	// Running is true while a pass is in progress.
	Running bool `json:"running"`
	// Compact is what compacting the database would do, or null when that
	// cannot be worked out (the platform's free disk space is unknown).
	Compact *compactPlanJSON `json:"compact"`
}

// retentionRunAtJSON is the time of day the daily pass runs.
type retentionRunAtJSON struct {
	Value    string  `json:"value"`
	Source   string  `json:"source"`
	PinnedBy *string `json:"pinned_by"`
}

// maxDatabaseSizeJSON is the database size limit. Bytes of zero means none.
type maxDatabaseSizeJSON struct {
	Bytes        int64   `json:"bytes"`
	Source       string  `json:"source"`
	PinnedBy     *string `json:"pinned_by"`
	MinimumBytes int64   `json:"minimum_bytes"`
}

// retentionPassJSON is the record of one pass on the wire. The rows each
// step removed are what the windows did; SizeCap is what the size limit did
// beyond that, and is null when no limit was set.
type retentionPassJSON struct {
	StartedAt     time.Time    `json:"started_at"`
	DurationMS    int64        `json:"duration_ms"`
	Trigger       string       `json:"trigger"`
	Heartbeats    int64        `json:"heartbeats"`
	HourlyBuckets int64        `json:"hourly_buckets"`
	Incidents     int64        `json:"incidents"`
	Deliveries    int64        `json:"deliveries"`
	FreedBytes    int64        `json:"freed_bytes"`
	SizeCap       *sizeCapJSON `json:"size_cap"`
	Error         *string      `json:"error"`
}

// sizeCapJSON is what the size limit did on one pass. The two "since"
// times are null when the limit did not move that boundary.
type sizeCapJSON struct {
	LimitBytes    int64      `json:"limit_bytes"`
	BeforeBytes   int64      `json:"before_bytes"`
	AfterBytes    int64      `json:"after_bytes"`
	Heartbeats    int64      `json:"heartbeats"`
	HourlyBuckets int64      `json:"hourly_buckets"`
	RawSince      *time.Time `json:"raw_since"`
	HourlySince   *time.Time `json:"hourly_since"`
	AtFloor       bool       `json:"at_floor"`
}

// compactPlanJSON is what the settings page needs to offer a compaction:
// whether it is worth it, how long writes wait for it, and whether the disk
// can hold it.
type compactPlanJSON struct {
	SizeBytes       int64  `json:"size_bytes"`
	FreeBytes       int64  `json:"free_bytes"`
	AutoVacuum      string `json:"auto_vacuum"`
	Recommended     bool   `json:"recommended"`
	EstimateSeconds int64  `json:"estimate_seconds"`
	// DiskShortfall is null when the disk has room. The directory is left
	// out: this response is readable by every role, and a path on the
	// server is not something a viewer needs. The refusal an administrator
	// gets when starting a compaction names it.
	DiskShortfall *diskShortfallJSON `json:"disk_shortfall"`
	Running       bool               `json:"running"`
	// Last is how the most recent compaction started from the settings
	// page went, since the server started; null before the first.
	Last *compactOutcomeJSON `json:"last"`
}

type diskShortfallJSON struct {
	NeedBytes int64 `json:"need_bytes"`
	FreeBytes int64 `json:"free_bytes"`
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

func pinnedBy(by string) *string {
	if by == "" {
		return nil
	}
	return &by
}

func passJSON(p *store.RetentionPass) *retentionPassJSON {
	if p == nil {
		return nil
	}
	out := &retentionPassJSON{
		StartedAt:     p.StartedAt.UTC(),
		DurationMS:    p.Duration.Milliseconds(),
		Trigger:       p.Trigger,
		Heartbeats:    p.Heartbeats,
		HourlyBuckets: p.HourlyBuckets,
		Incidents:     p.Incidents,
		Deliveries:    p.Deliveries,
		FreedBytes:    p.FreedBytes,
	}
	if p.Error != "" {
		msg := p.Error
		out.Error = &msg
	}
	if c := p.SizeCap; c != nil {
		out.SizeCap = &sizeCapJSON{
			LimitBytes: c.Limit, BeforeBytes: c.Before, AfterBytes: c.After,
			Heartbeats: c.Heartbeats, HourlyBuckets: c.HourlyBuckets,
			RawSince: optionalTime(c.RawSince), HourlySince: optionalTime(c.HourlySince),
			AtFloor: c.AtFloor,
		}
	}
	return out
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func windowJSON(w store.RetentionWindow) retentionWindowJSON {
	out := retentionWindowJSON{Seconds: seconds(w.Value), Source: w.Source}
	if w.PinnedBy != "" {
		by := w.PinnedBy
		out.PinnedBy = &by
	}
	if w.Raised != nil {
		was := seconds(*w.Raised)
		out.SetAsideSeconds = &was
	}
	return out
}

// handleGetRetention reports the windows in force, where each came from, and
// what the tables they govern cost today.
//
// The response carries the windows' version as a weak ETag. It is read before
// the windows: a save landing in between then pairs new windows with the old
// version, which can only make the caller's next save fail needlessly, never
// let it overwrite something it did not see.
func (s *Server) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	version, err := s.db.RetentionVersion(r.Context())
	if err != nil {
		s.log.Error("read retention version", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read retention settings")
		return
	}
	resp, err := s.retentionWindows(r)
	if err != nil {
		s.log.Error("resolve retention", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read retention settings")
		return
	}
	if resp.Tables, err = s.retentionTables(r); err != nil {
		s.log.Error("measure retention tables", "error", err)
		writeError(w, http.StatusInternalServerError, "could not measure the database")
		return
	}
	w.Header().Set("ETag", retentionETag(version))
	writeJSON(w, http.StatusOK, resp)
}

// retentionETag renders the windows' version as a weak entity tag. Weak for
// the same reason as a monitor's: it versions the stored windows, not the
// bytes of a response that also carries live table measurements.
func retentionETag(version int64) string {
	return `W/"` + strconv.FormatInt(version, 10) + `"`
}

// retentionVersions converts the tags a client offered into the versions they
// denote, dropping any tag this API could never have issued. Only the
// canonical spelling counts: an entity tag is opaque, and "01" is not a tag
// that was handed out.
func retentionVersions(tags []string) []int64 {
	versions := make([]int64, 0, len(tags))
	for _, tag := range tags {
		v, err := strconv.ParseInt(tag, 10, 64)
		if err != nil || v < 0 || strconv.FormatInt(v, 10) != tag {
			continue
		}
		versions = append(versions, v)
	}
	return versions
}

// retentionWindows is the policy half of the response: the settings in force
// and where each came from, what the last pass did and whether one is
// running, with no table measurements yet.
//
// Only the settings the page edits fail the call. The last pass and the
// compaction plan are reports about the database, and one that cannot be read
// is left out (and logged) rather than locking an administrator out of the
// settings it sits next to.
func (s *Server) retentionWindows(r *http.Request) (retentionResponse, error) {
	ctx := r.Context()
	eff, err := s.db.ResolveRetention(ctx, s.retentionPins)
	if err != nil {
		return retentionResponse{}, err
	}
	runAt, err := s.db.ResolveRetentionRunAt(ctx, s.retentionPins.RunAt)
	if err != nil {
		return retentionResponse{}, err
	}
	limit, err := s.db.ResolveMaxDatabaseSize(ctx, s.retentionPins.MaxSize)
	if err != nil {
		return retentionResponse{}, err
	}
	resp := retentionResponse{
		Raw:               windowJSON(eff.Raw),
		Rollup:            windowJSON(eff.Rollup),
		MinimumRawSeconds: seconds(store.MinRawRetention),
		RunAt: retentionRunAtJSON{
			Value: runAt.Value.String(), Source: runAt.Source, PinnedBy: pinnedBy(runAt.PinnedBy),
		},
		MaxDatabaseSize: maxDatabaseSizeJSON{
			Bytes: limit.Value, Source: limit.Source, PinnedBy: pinnedBy(limit.PinnedBy),
			MinimumBytes: store.MinMaxDatabaseSize,
		},
		Tables: []retentionTableJSON{},
	}
	if s.passes != nil {
		resp.Running = s.passes.Running()
	}
	if last, err := s.db.LastRetentionPass(ctx); err != nil {
		s.log.Error("read the last retention pass", "error", err)
	} else {
		resp.LastPass = passJSON(last)
	}
	if plan, err := s.compactor().PlanCompact(ctx); err != nil {
		s.log.Warn("plan database compaction", "error", err)
	} else {
		resp.Compact = &compactPlanJSON{
			SizeBytes: plan.SizeBytes, FreeBytes: plan.FreeBytes, AutoVacuum: plan.AutoVacuum,
			Recommended: plan.Recommended, EstimateSeconds: seconds(plan.Estimate),
			Running: s.compacting.Load(), Last: s.lastCompaction(),
		}
		if d := plan.Disk; d != nil {
			resp.Compact.DiskShortfall = &diskShortfallJSON{NeedBytes: d.NeedBytes, FreeBytes: d.FreeBytes}
		}
	}
	return resp, nil
}

// retentionTables measures the tables the windows govern.
func (s *Server) retentionTables(r *http.Request) ([]retentionTableJSON, error) {
	tables, err := s.db.RetentionTables(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]retentionTableJSON, 0, len(tables))
	for _, t := range tables {
		row := retentionTableJSON{Name: t.Name, Rows: t.Rows, RowsPerDay: t.RowsPerDay}
		if t.Bytes >= 0 {
			b := t.Bytes
			row.Bytes = &b
		}
		out = append(out, row)
	}
	return out, nil
}

// retentionRequest is the body of PUT /api/v1/settings/retention. An absent
// field is left as it is. A window of zero means forever, a size limit of
// zero means none.
type retentionRequest struct {
	RawSeconds       *int64  `json:"raw_seconds"`
	RollupSeconds    *int64  `json:"rollup_seconds"`
	RunAt            *string `json:"run_at"`
	MaxDatabaseBytes *int64  `json:"max_database_bytes"`
}

// maxRetentionSeconds keeps a window inside what time.Duration can hold. It
// is not a policy limit — a hundred years is still "no artificial cap" — it
// is the point past which the arithmetic would overflow into a negative
// window, which is the one input that would delete everything.
const maxRetentionSeconds = 100 * 365 * 24 * 3600

func retentionField(window string) string { return window + "_seconds" }

// handleSetRetention stores the windows chosen on the settings page. They
// take effect on the next maintenance pass, without a restart.
//
// An If-Match header makes the save conditional on nobody having saved since
// the caller read the windows. That matters more here than it looks: the page
// only previews a change that shortens a window, so an editor who lengthened
// 30 days to 60 while another administrator saved 90 would, unconditionally,
// shorten retention without anyone having seen what it removes. Without the
// header the save stays last-write-wins, like a monitor PATCH, so a script
// written against the unconditional endpoint keeps working.
func (s *Server) handleSetRetention(w http.ResponseWriter, r *http.Request) {
	// Presence, not value, decides: an If-Match sent empty is a malformed
	// precondition, and treating it as absent would save unconditionally.
	ifMatchValues, hasIfMatch := r.Header["If-Match"]
	ifMatch := strings.Join(ifMatchValues, ", ")
	var (
		wantTags []string
		wantAny  bool
	)
	if hasIfMatch {
		var valid bool
		if wantTags, wantAny, valid = parseIfMatch(ifMatch); !valid {
			writeError(w, http.StatusBadRequest, "malformed If-Match header")
			return
		}
	}

	var req retentionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.RawSeconds == nil && req.RollupSeconds == nil && req.RunAt == nil && req.MaxDatabaseBytes == nil {
		writeProblem(w, http.StatusBadRequest,
			bodyProblem("send at least one of raw_seconds, rollup_seconds, run_at and max_database_bytes"))
		return
	}

	raw, status, p := retentionInput("raw", req.RawSeconds, s.retentionPins.Raw)
	if !p.ok() {
		writeProblem(w, status, p)
		return
	}
	rollup, status, p := retentionInput("rollup", req.RollupSeconds, s.retentionPins.Rollup)
	if !p.ok() {
		writeProblem(w, status, p)
		return
	}
	runAt, status, p := runAtInput(req.RunAt, s.retentionPins.RunAt)
	if !p.ok() {
		writeProblem(w, status, p)
		return
	}
	maxBytes, status, p := maxDatabaseSizeInput(req.MaxDatabaseBytes, s.retentionPins.MaxSize)
	if !p.ok() {
		writeProblem(w, status, p)
		return
	}
	change := store.RetentionChange{Raw: raw, Rollup: rollup, RunAt: runAt, MaxBytes: maxBytes}

	// `*` asks only that the settings exist, and they always do.
	var (
		version int64
		err     error
	)
	if hasIfMatch && !wantAny {
		version, err = s.db.SaveRetentionSettingsIfVersion(r.Context(), change, s.retentionPins, retentionVersions(wantTags))
	} else {
		version, err = s.db.SaveRetentionSettings(r.Context(), change, s.retentionPins)
	}
	if errors.Is(err, store.ErrRetentionVersion) {
		// No fresh ETag: the caller has to look at what the other
		// administrator saved, and re-preview, before its change can mean
		// anything. A validator to retry with would invite the overwrite
		// this refused.
		writeError(w, http.StatusPreconditionFailed,
			"retention was changed by someone else since you read it; load it again and reapply your change")
		return
	}
	var bad *store.RetentionError
	if errors.As(err, &bad) {
		writeProblem(w, http.StatusBadRequest, fieldProblem(retentionField(bad.Window), bad.Msg))
		return
	}
	if err != nil {
		s.log.Error("save retention", "error", err)
		writeError(w, http.StatusInternalServerError, "could not save retention settings")
		return
	}
	s.log.Info("retention settings changed", "raw_seconds", req.RawSeconds, "rollup_seconds", req.RollupSeconds,
		"run_at", req.RunAt, "max_database_bytes", req.MaxDatabaseBytes)

	// The windows are committed at this point, so nothing below may answer
	// with an error: a 500 would tell the client the save failed when it did
	// not. What cannot be read back is left out instead.
	//
	// The version is the one this save wrote. Windows read back below could
	// in principle be newer still, which again only errs towards a refused
	// next save.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", retentionETag(version))
	resp, err := s.retentionWindows(r)
	if err != nil {
		s.log.Error("resolve retention after save", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if tables, err := s.retentionTables(r); err != nil {
		s.log.Error("measure retention tables after save", "error", err)
	} else {
		resp.Tables = tables
	}
	writeJSON(w, http.StatusOK, resp)
}

// retentionInput checks one window of a PUT. A window set by a flag is a
// conflict with the server's configuration (409), anything else out of range
// is a bad request (400).
func retentionInput(window string, v *int64, pin *store.RetentionPin) (*time.Duration, int, problem) {
	if v == nil {
		return nil, 0, problem{}
	}
	field := retentionField(window)
	if pin != nil {
		// Storing a value under a pin would look like it worked and then
		// change nothing, and would surface later as a surprise when the
		// flag is removed. Refusing says where the value really lives.
		return nil, http.StatusConflict, fieldProblem(field,
			"this window is set by "+pin.By+" and can only be changed there")
	}
	if *v < 0 || *v > maxRetentionSeconds {
		return nil, http.StatusBadRequest, fieldProblem(field,
			window+" retention must be between 0 (forever) and 100 years, in seconds")
	}
	d := time.Duration(*v) * time.Second
	return &d, 0, problem{}
}

// runAtInput checks the time of day in a PUT.
func runAtInput(v *string, pin *store.RetentionRunAtPin) (*store.ClockTime, int, problem) {
	if v == nil {
		return nil, 0, problem{}
	}
	if pin != nil {
		return nil, http.StatusConflict, fieldProblem("run_at",
			"the time of day is set by "+pin.By+" and can only be changed there")
	}
	c, err := store.ParseClockTime(*v)
	if err != nil {
		return nil, http.StatusBadRequest, fieldProblem("run_at",
			"run_at must be a 24-hour time of day written HH:MM, such as 03:30")
	}
	return &c, 0, problem{}
}

// maxDatabaseSizeInput checks the size limit in a PUT.
func maxDatabaseSizeInput(v *int64, pin *store.MaxDatabaseSizePin) (*int64, int, problem) {
	if v == nil {
		return nil, 0, problem{}
	}
	if pin != nil {
		return nil, http.StatusConflict, fieldProblem("max_database_bytes",
			"the database size limit is set by "+pin.By+" and can only be changed there")
	}
	if *v != 0 && *v < store.MinMaxDatabaseSize {
		return nil, http.StatusBadRequest, fieldProblem("max_database_bytes",
			"the database size limit must be 0 (no limit) or at least "+store.FormatByteSize(store.MinMaxDatabaseSize))
	}
	return v, 0, problem{}
}

type retentionPreviewResponse struct {
	Heartbeats    int64 `json:"heartbeats"`
	HourlyBuckets int64 `json:"hourly_buckets"`
	Incidents     int64 `json:"incidents"`
}

// handlePreviewRetention counts what a pass under the proposed windows would
// remove if it ran now. Absent parameters take the window in force, so a form
// that changes one field can ask about exactly that change.
func (s *Server) handlePreviewRetention(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	eff, err := s.db.ResolveRetention(r.Context(), s.retentionPins)
	if err != nil {
		s.log.Error("resolve retention", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read retention settings")
		return
	}
	policy := eff.Policy()
	for _, q := range []struct {
		window string
		dst    *time.Duration
	}{{"raw", &policy.Raw}, {"rollup", &policy.Rollup}} {
		v := r.URL.Query().Get(retentionField(q.window))
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > maxRetentionSeconds {
			writeProblem(w, http.StatusBadRequest, fieldProblem(retentionField(q.window),
				q.window+" retention must be between 0 (forever) and 100 years, in seconds"))
			return
		}
		*q.dst = time.Duration(n) * time.Second
	}
	if err := policy.Validate(); err != nil {
		var bad *store.RetentionError
		if errors.As(err, &bad) {
			writeProblem(w, http.StatusBadRequest, fieldProblem(retentionField(bad.Window), bad.Msg))
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	impact, err := s.db.PreviewRetention(r.Context(), policy)
	if err != nil {
		s.log.Error("preview retention", "error", err)
		writeError(w, http.StatusInternalServerError, "could not preview retention")
		return
	}
	writeJSON(w, http.StatusOK, retentionPreviewResponse{
		Heartbeats: impact.Heartbeats, HourlyBuckets: impact.HourlyBuckets, Incidents: impact.Incidents,
	})
}
