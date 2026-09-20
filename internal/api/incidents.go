package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// incidentResponse is the API shape of an incident.
//
// The two timestamps are both exposed on purpose. StartedAt is when the first
// check failed; ConfirmedAt is when the failure count crossed the monitor's
// threshold and a human was told. Support conversations turn on that gap
// ("we saw it at 14:02, you alerted at 14:04"), so hiding it would be a
// disservice.
type incidentResponse struct {
	ID        int64 `json:"id"`
	MonitorID int64 `json:"monitor_id"`

	StartedAt   time.Time  `json:"started_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	AckedAt     *time.Time `json:"acked_at,omitempty"`

	Confirmed bool `json:"confirmed"`
	Resolved  bool `json:"resolved"`
	Acked     bool `json:"acked"`

	DurationS int `json:"duration_s"`

	Cause     string `json:"cause,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func toIncidentResponse(inc store.Incident) incidentResponse {
	resp := incidentResponse{
		ID:        inc.ID,
		MonitorID: inc.MonitorID,
		StartedAt: inc.StartedAt,
		Confirmed: inc.Confirmed(),
		Resolved:  inc.Resolved(),
		Acked:     !inc.AckedAt.IsZero(),
		DurationS: int(inc.Duration().Seconds()),
		Cause:     inc.Cause,
		LastError: inc.LastError,
	}

	if inc.Confirmed() {
		t := inc.ConfirmedAt
		resp.ConfirmedAt = &t
	}
	if inc.Resolved() {
		t := inc.ResolvedAt
		resp.ResolvedAt = &t
	}
	if !inc.AckedAt.IsZero() {
		t := inc.AckedAt
		resp.AckedAt = &t
	}
	return resp
}

// handleListOpenIncidents answers "what is broken right now" — the query the
// dashboard's landing view is built around.
func (s *Server) handleListOpenIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.db.ListOpenIncidents(r.Context())
	if err != nil {
		s.log.Error("list open incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, toIncidentResponse(inc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

// resolvedHistoryDefaultDays is the window the endpoint answers for when the
// caller names none. It matches what the incidents screen asks for, and the
// screen asks explicitly — this default exists for a human with curl, not as a
// second place the product's opinion is written down.
const resolvedHistoryDefaultDays = 30

// resolvedHistoryMaxDays caps the window. Two years is past the point where a
// browser should be assembling the answer at all; beyond it the honest reply
// is an export, not a scroll.
const resolvedHistoryMaxDays = 730

// resolvedCursorGrace is how far past the cap a continuation cursor may sit
// before it is treated as fabricated.
//
// It exists because the cap is measured from `now` on every request while a
// cursor's bound is fixed when the walk starts: at `days=730` the server's own
// cursor is a second past the ceiling by the time the client asks for page
// two. The one-hour grace permits a maximum-window walk to continue for up to
// an hour; after that the caller must restart without a cursor. The cap uses
// elapsed 24-hour days like the first page, not local calendar arithmetic:
// differing DST offsets would otherwise consume the entire grace period.
const resolvedCursorGrace = time.Hour

const (
	resolvedPageDefaultLimit = 50
	resolvedPageMaxLimit     = 200
)

// handleListResolvedIncidents answers "what recovered across this instance
// recently", which had no endpoint at all.
//
// The screen that asks it used to assemble the answer client-side, one request
// per monitor, capped at 24 monitors — so on anything larger the history was
// incomplete, and the card could only say so. Worse, the per-monitor endpoint
// returns a LIMIT 50 page with no completeness metadata, so a monitor with 60
// outages in the window looked exactly like one that had 50.
//
// This replaces all of it with a keyset-paginated sweep. The response carries
// has_more and, when there is more, the cursor to continue with — so a client
// that reaches the end knows it reached the end, and one that stops early
// knows it stopped. The point is that "is this list complete" stops being a
// guess: a LIMIT with no "there is more" signal would simply have moved the
// old lie from the browser to the server.
func (s *Server) handleListResolvedIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	days := resolvedHistoryDefaultDays
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > resolvedHistoryMaxDays {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("days must be between 1 and %d", resolvedHistoryMaxDays))
			return
		}
		days = n
	}

	limit := resolvedPageDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > resolvedPageMaxLimit {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("limit must be between 1 and %d", resolvedPageMaxLimit))
			return
		}
		limit = n
	}

	var cursor store.ResolvedIncidentCursor
	if v := q.Get("cursor"); v != "" {
		parsed, err := parseResolvedCursor(v)
		if err != nil {
			// A malformed cursor is refused rather than silently treated as
			// "start from the top". Quietly restarting a paged read is how a
			// client ends up looping over page one forever while believing it
			// is walking a month of history.
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		cursor = parsed
	}

	/*
	 * The window's lower bound is pinned on the first request and then
	 * carried by the cursor, rather than recomputed per request.
	 *
	 * Recomputing it looks harmless and is not. Paging walks toward *older*
	 * incidents while `now - days` walks forward, so the floor rises under a
	 * walk that is descending toward it: an incident sitting just inside the
	 * window when page one was served can be below the floor by the time the
	 * request that would have returned it arrives. The walk then ends normally
	 * — `has_more` false, no error — having skipped it.
	 *
	 * That is the exact failure this endpoint was built to remove, reappearing
	 * one level up: a history that is quietly short while reporting itself
	 * complete. A pinned bound makes every page of one walk describe the same
	 * window, which is what "complete" has to mean for the word to be worth
	 * anything.
	 */
	since := cursor.Since
	if since.IsZero() {
		since = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	} else if since.Before(time.Now().
		Add(-resolvedHistoryMaxDays * 24 * time.Hour).
		Add(-resolvedCursorGrace)) {
		/*
		 * A cursor pins the window; it does not get to widen it.
		 *
		 * `days` is capped, and moving the bound onto the cursor handed the
		 * same number back to the client as free text — `cursor=1.1.1` claims
		 * a lower bound of 1970 and walks the whole table, which is the cap
		 * removed by the very mechanism that was meant to make paging honest.
		 *
		 * Refused rather than clamped. Silently narrowing a fabricated bound
		 * would answer a different question than the one asked while calling
		 * it the same walk, and this endpoint's whole point is that a page of
		 * history means what it says.
		 *
		 * The grace is what makes the check survive its own first page. Both
		 * ends of this comparison move — the ceiling advances with the clock
		 * while the pinned bound stays where page one put it — so a walk begun
		 * at exactly the cap falls behind the ceiling a second later, and a
		 * bare comparison refused the continuation of a walk the server had
		 * just issued. Wide enough for any real walk, and still narrower by
		 * orders of magnitude than the window it guards.
		 */
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	page, err := s.db.ListResolvedIncidents(r.Context(), since, cursor, limit)
	if err != nil {
		s.log.Error("list resolved incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(page.Incidents))
	for _, inc := range page.Incidents {
		out = append(out, toIncidentResponse(inc))
	}

	body := map[string]any{
		"incidents": out,
		"has_more":  page.HasMore,
		"days":      days,
	}
	if page.HasMore {
		next := page.Next
		next.Since = since
		body["next_cursor"] = formatResolvedCursor(next)
	}
	writeJSON(w, http.StatusOK, body)
}

// formatResolvedCursor renders a cursor as "<since>.<resolved>.<id>".
//
// Plain and readable rather than base64: it is a position in a public list,
// not a secret, and an opaque blob would only mean that the one person
// debugging a paging bug with curl cannot see what they are asking for.
//
// All three parts are load-bearing. `since` pins the window so a walk cannot
// have the floor rise under it; `resolved` and `id` together are the sort key,
// and the id cannot be dropped because resolution times have second
// granularity — see ResolvedIncidentCursor.
func formatResolvedCursor(c store.ResolvedIncidentCursor) string {
	return strconv.FormatInt(c.Since.Unix(), 10) + "." +
		strconv.FormatInt(c.ResolvedAt.Unix(), 10) + "." +
		strconv.FormatInt(c.ID, 10)
}

func parseResolvedCursor(v string) (store.ResolvedIncidentCursor, error) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return store.ResolvedIncidentCursor{}, errors.New("cursor must be <since>.<resolved>.<id>")
	}
	nums := make([]int64, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n <= 0 {
			return store.ResolvedIncidentCursor{}, errors.New("every cursor component must be a positive integer")
		}
		nums[i] = n
	}
	return store.ResolvedIncidentCursor{
		Since:      time.Unix(nums[0], 0).UTC(),
		ResolvedAt: time.Unix(nums[1], 0).UTC(),
		ID:         nums[2],
	}, nil
}

// handleListMonitorIncidents returns the incident history for one monitor.
func (s *Server) handleListMonitorIncidents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.requireMonitor(w, r, id) {
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}

	incidents, err := s.db.ListIncidents(r.Context(), id, limit)
	if err != nil {
		s.log.Error("list incidents", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, toIncidentResponse(inc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

// handleAckIncident marks an incident as acknowledged.
//
// Acknowledging says "seen, working on it". It stops repeat notifications
// without claiming the problem is solved, which is the honest middle state
// between alerting and resolving.
func (s *Server) handleAckIncident(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	err = s.db.AckIncident(r.Context(), id, time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		s.log.Error("ack incident", "incident_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not acknowledge incident")
		return
	}

	s.log.Info("incident acknowledged", "incident_id", id)
	w.WriteHeader(http.StatusNoContent)
}
