package api

import (
	"errors"
	"net/http"
	"strconv"
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
func (s *Server) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
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
	writeJSON(w, http.StatusOK, resp)
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
func (s *Server) handleSetRetention(w http.ResponseWriter, r *http.Request) {
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

	err := s.db.SetRetention(r.Context(), raw, rollup, s.retentionPins)
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
	w.Header().Set("Cache-Control", "private, no-store")
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
