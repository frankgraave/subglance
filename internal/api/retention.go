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
	Tables            []retentionTableJSON `json:"tables"`
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

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

// retentionWindows is the policy half of the response: the windows in force
// and where each came from, with no table measurements yet.
func (s *Server) retentionWindows(r *http.Request) (retentionResponse, error) {
	eff, err := s.db.ResolveRetention(r.Context(), s.retentionPins)
	if err != nil {
		return retentionResponse{}, err
	}
	return retentionResponse{
		Raw:               windowJSON(eff.Raw),
		Rollup:            windowJSON(eff.Rollup),
		MinimumRawSeconds: seconds(store.MinRawRetention),
		Tables:            []retentionTableJSON{},
	}, nil
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
// window is left as it is; zero means forever.
type retentionRequest struct {
	RawSeconds    *int64 `json:"raw_seconds"`
	RollupSeconds *int64 `json:"rollup_seconds"`
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
	if req.RawSeconds == nil && req.RollupSeconds == nil {
		writeProblem(w, http.StatusBadRequest, bodyProblem("send raw_seconds, rollup_seconds, or both"))
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

	// `*` asks only that the settings exist, and they always do.
	var (
		version int64
		err     error
	)
	if hasIfMatch && !wantAny {
		version, err = s.db.SetRetentionIfVersion(r.Context(), raw, rollup, s.retentionPins, retentionVersions(wantTags))
	} else {
		version, err = s.db.SetRetention(r.Context(), raw, rollup, s.retentionPins)
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
	s.log.Info("retention settings changed", "raw_seconds", req.RawSeconds, "rollup_seconds", req.RollupSeconds)

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
