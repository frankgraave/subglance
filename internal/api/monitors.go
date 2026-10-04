package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// monitorResponse is the API shape of a monitor plus its current state.
//
// It deliberately differs from store.Monitor: the wire format is a contract we
// have to keep stable, while the storage shape must stay free to change.
type monitorResponse struct {
	Maintenance *bool  `json:"maintenance,omitempty"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Target      string `json:"target"`

	IntervalS int  `json:"interval_s"`
	TimeoutS  int  `json:"timeout_s"`
	Enabled   bool `json:"enabled"`

	// CaptureResponse says whether a failed check keeps the start of the
	// response body. Always present rather than omitempty: it governs what
	// this monitor stores about someone else's service, and a field that
	// disappears when false reads as "the server does not know about this".
	CaptureResponse bool `json:"capture_response"`

	// RepeatAfterS is the delay before a confirmed, unacknowledged incident
	// is alerted about again. 0 means reminders are off for this monitor.
	RepeatAfterS int `json:"repeat_after_s"`

	// MinTLSVersion is the floor this monitor negotiates with, written the
	// way a person writes it: "1.0" to "1.3". Omitted when the monitor has
	// no opinion, which is not the same as reporting the current default —
	// a monitor that says nothing follows SubGlance if the default moves,
	// and one that says "1.2" does not.
	MinTLSVersion string `json:"min_tls_version,omitempty"`

	Status     string     `json:"status"` // up, pending, warning, down, or recovering
	LastCheck  *time.Time `json:"last_check,omitempty"`
	LatencyMS  int        `json:"latency_ms,omitempty"`
	StatusCode int        `json:"status_code,omitempty"`
	Error      string     `json:"error,omitempty"`

	// FailureKind classifies Error: the checker's kind of the failing
	// heartbeat, or the open incident's cause when Error came from the
	// incident. It is what lets a list name the failure in a few words and
	// leave the full message to the monitor's page. Omitted whenever Error
	// has no classification, including on a passing check.
	FailureKind string `json:"failure_kind,omitempty"`

	// IncidentID and IncidentSince describe the open incident, if any. They
	// let the UI link straight from a red row to the incident without a
	// second round trip.
	IncidentID    int64      `json:"incident_id,omitempty"`
	IncidentSince *time.Time `json:"incident_since,omitempty"`

	// Recovery is the passing streak of a monitor whose status is
	// "recovering": the confirmed incident is still open, and closes after
	// Threshold passes in a row. Omitted for every other status.
	Recovery *recoveryResponse `json:"recovery,omitempty"`

	Uptime24h *float64 `json:"uptime_24h"`

	// PushURL is the full, secret URL a job pings. It is present exactly
	// once, in the response that created the monitor, and never again —
	// only its hash is stored, so it genuinely cannot be looked up later.
	PushURL string `json:"push_url,omitempty"`

	// PushTokenPrefix identifies the push URL without revealing it, so a
	// list can show which credential a monitor carries.
	PushTokenPrefix string `json:"push_token_prefix,omitempty"`

	// PushIntervalS is how often the job is expected to report and
	// PushGraceS how late it may be. Both omitted for non-push monitors.
	PushIntervalS int `json:"push_interval_s,omitempty"`
	PushGraceS    int `json:"push_grace_s,omitempty"`

	// PushWaiting says why a push monitor whose status is "pending" is
	// waiting although it has reported before: "resumed" when nothing has
	// been recorded since it was resumed, "restarted" when its window
	// closed while SubGlance was not running. Omitted otherwise. Without
	// it, a monitor waiting out its fresh window would read "up" on the
	// strength of a report from before the gap.
	PushWaiting string `json:"push_waiting,omitempty"`

	// Tags is a key/value map (`{"env":"prod"}`), omitted when empty so the
	// response stays byte for byte what it was for untagged monitors.
	//
	// An object rather than a list of `key:value` strings: the API then
	// never splits on a colon, so a value may contain one
	// (`url: https://example.com`). `key:value` is how a human writes a tag,
	// not how it travels.
	Tags map[string]string `json:"tags,omitempty"`

	// Present on list reads: [] means no attachments; omitted means unknown.
	Channels *[]monitorChannelResponse `json:"channels,omitempty"`

	// RuleChannels is present on list reads exactly when Channels is: the
	// routing rules that add channels for this monitor, each with the
	// channels it adds. [] means no rule routes it. Rules it is excluded from
	// are left out, because they send it nothing.
	RuleChannels *[]monitorRuleRouteResponse `json:"rule_channels,omitempty"`

	// DefaultChannel is set on list reads exactly when Channels is known, it
	// and RuleChannels are both empty, and an instance default exists: the
	// channel this monitor's alerts go to instead. Without it an empty
	// Channels would read as "nobody hears about this monitor" when somebody
	// does.
	DefaultChannel *monitorChannelResponse `json:"default_channel,omitempty"`

	// Heartbeats is filled in only when the caller asked for it with the
	// `heartbeats` query parameter, oldest first. Omitting the field
	// entirely when it was not requested keeps the default response byte
	// for byte what it has always been.
	Heartbeats []heartbeatResponse `json:"heartbeats,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type monitorChannelResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// monitorRuleRouteResponse is one routing rule's contribution to a monitor.
type monitorRuleRouteResponse struct {
	RuleID   int64                    `json:"rule_id"`
	TagKey   string                   `json:"tag_key"`
	TagValue string                   `json:"tag_value"`
	Channels []monitorChannelResponse `json:"channels"`
}

// monitorDetailResponse adds raw editable settings to the versioned detail
// read only. False, zero and empty are meaningful baselines. Only request
// secrets may be omitted, for callers who cannot edit the monitor.
// Keeping this separate prevents new consumers of monitorResponse (including
// live payloads) from inadvertently disclosing request credentials.
type monitorDetailResponse struct {
	monitorResponse
	Method          string             `json:"method"`
	ExpectedStatus  string             `json:"expected_status"`
	Keyword         string             `json:"keyword"`
	KeywordMode     string             `json:"keyword_mode"`
	FollowRedirects bool               `json:"follow_redirects"`
	Headers         *map[string]string `json:"headers,omitempty"`
	Body            *string            `json:"body,omitempty"`
	Retries         int                `json:"retries"`
	// RecoveryThreshold is how many passing checks in a row close a
	// confirmed incident. Push monitors store it but recover on one report.
	RecoveryThreshold int `json:"recovery_threshold"`
	SSLWarnDays       int `json:"ssl_warn_days"`
	// JSONAssertion is null when the monitor has none, rather than omitted,
	// so an editor can tell "no assertion" from "an older server".
	JSONAssertion *jsonAssertionWire `json:"json_assertion"`
}

// recoveryResponse is a recovering monitor's passing streak.
type recoveryResponse struct {
	Passes    int `json:"passes"`
	Threshold int `json:"threshold"`
}

// recoveryStateSource is the read-only facet of the checker pipeline that
// knows a recovering monitor's passing streak. It lives in memory only (see
// state.Engine.Restore), so the API asks the running engine rather than the
// database.
type recoveryStateSource interface {
	Recovery(monitorID int64) (passes, threshold int, ok bool)
}

// recovery describes monitorID's passing streak, or nil when it is not
// recovering or no checker pipeline is attached.
func (s *Server) recovery(monitorID int64) *recoveryResponse {
	source, ok := s.prober.(recoveryStateSource)
	if !ok {
		return nil
	}
	passes, threshold, recovering := source.Recovery(monitorID)
	if !recovering {
		return nil
	}
	return &recoveryResponse{Passes: passes, Threshold: threshold}
}

// heartbeatResponse is the wire shape of one recorded check result. It is
// shared by the per-monitor heartbeat listing and the optional beats embedded
// in a monitor listing, so both cannot drift apart.
type heartbeatResponse struct {
	Maintenance bool      `json:"maintenance"`
	Assessment  string    `json:"assessment"`
	FailureKind string    `json:"failure_kind,omitempty"`
	TS          time.Time `json:"ts"`
	OK          bool      `json:"ok"`
	LatencyMS   int       `json:"latency_ms"`
	StatusCode  int       `json:"status_code,omitempty"`
	Error       string    `json:"error,omitempty"`

	// Response is the captured failure response, present only on failures of
	// a monitor with capture enabled. Omitted otherwise, so every response
	// that had no snapshot stays byte for byte what it was.
	Response *responseSnapshotResponse `json:"response,omitempty"`
}

// responseSnapshotResponse is the wire shape of a captured failure response.
//
// This is diagnostic detail, not dashboard information: it is returned so a
// detail view can show it behind a disclosure, and it is never part of the
// bulk beat-bar payload.
type responseSnapshotResponse struct {
	Body      string            `json:"body"`
	Headers   map[string]string `json:"headers,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

// describeHeartbeat converts a stored heartbeat to its wire shape.
func describeHeartbeat(hb store.Heartbeat) heartbeatResponse {
	out := heartbeatResponse{
		TS:          hb.TS,
		OK:          hb.OK,
		Assessment:  hb.Assessment,
		Maintenance: hb.Maintenance,
		FailureKind: hb.FailureKind,
		LatencyMS:   hb.LatencyMS,
		StatusCode:  hb.StatusCode,
		Error:       hb.Error,
	}
	if hb.Response != nil {
		out.Response = &responseSnapshotResponse{
			Body:      hb.Response.Body,
			Headers:   hb.Response.Headers,
			Truncated: hb.Response.Truncated,
		}
	}
	return out
}

type createMonitorRequest struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	IntervalS int    `json:"interval_s"`
	TimeoutS  int    `json:"timeout_s"`
	Retries   *int   `json:"retries"`
	// RecoveryThreshold is optional; omitted means the schema default of 2.
	RecoveryThreshold *int              `json:"recovery_threshold"`
	Method            string            `json:"method"`
	ExpectedStatus    string            `json:"expected_status"`
	Keyword           string            `json:"keyword"`
	KeywordMode       string            `json:"keyword_mode"`
	FollowRedirects   *bool             `json:"follow_redirects"`
	Headers           map[string]string `json:"headers"`
	Body              string            `json:"body"`
	SSLWarnDays       *int              `json:"ssl_warn_days"`
	RepeatAfterS      *int              `json:"repeat_after_s"`
	Enabled           *bool             `json:"enabled"`
	CaptureResponse   *bool             `json:"capture_response"`
	MinTLSVersion     *string           `json:"min_tls_version"`
	Tags              map[string]string `json:"tags"`
	PushIntervalS     *int              `json:"push_interval_s"`
	PushGraceS        *int              `json:"push_grace_s"`
	// JSONAssertion is optional; omitted or null means none.
	JSONAssertion *jsonAssertionWire `json:"json_assertion"`
	// ChannelIDs attaches the monitor's own channels as it is created.
	// Omitted or empty means none, so its alerts go to the routing rules
	// its tags match, or to the default.
	ChannelIDs []int64 `json:"channel_ids"`
}

func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// `heartbeats` is opt-in so the existing response shape is untouched for
	// callers that do not want the extra payload.
	perMonitor := 0
	if v := r.URL.Query().Get("heartbeats"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "heartbeats must be between 1 and 1000")
			return
		}
		perMonitor = n
	}

	monitors, err := s.db.ListMonitors(ctx)
	if err != nil {
		s.log.Error("list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list monitors")
		return
	}

	// One bulk query rather than one per monitor: at a few hundred monitors
	// the per-monitor version is a few hundred round trips per page load.
	// A failure here only costs the beat bars, so the listing still goes out.
	var beats map[int64][]store.Heartbeat
	if perMonitor > 0 {
		beats, err = s.db.RecentHeartbeatsForAll(ctx, perMonitor)
		if err != nil {
			s.log.Error("recent heartbeats for all monitors", "error", err)
			beats = nil
		}
	}

	channels, channelErr := s.db.MonitorChannelSummaries(ctx)
	if channelErr != nil {
		s.log.Error("monitor channel summaries", "error", channelErr)
	}
	var ruleRoutes map[int64][]store.RuleRoute
	if channelErr == nil {
		// Unknown rule routes make an empty own list a claim nobody can
		// check, exactly like an unknown default below.
		ruleRoutes, channelErr = s.db.MonitorRuleRoutes(ctx)
		if channelErr != nil {
			s.log.Error("monitor rule routes", "error", channelErr)
		}
	}
	var fallback *monitorChannelResponse
	if channelErr == nil {
		def, ok, err := s.db.DefaultChannel(ctx)
		switch {
		case err != nil:
			// An unknown default makes every empty list a claim nobody can
			// check, so the channels go out as unknown rather than as "none".
			s.log.Error("default channel", "error", err)
			channelErr = err
		case ok:
			fallback = &monitorChannelResponse{ID: def.ID, Name: def.Name}
		}
	}
	out := make([]monitorResponse, 0, len(monitors))
	for _, m := range monitors {
		resp := s.describeMonitor(r, m)
		if channelErr == nil {
			withRoutes(&resp, channels[m.ID], ruleRoutes[m.ID], fallback)
		}
		if perMonitor > 0 {
			resp.Heartbeats = oldestFirstHeartbeats(beats[m.ID])
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": out})
}

// withRoutes fills in who an alert for this monitor reaches: its own
// channels, the routing rules that add more, and the default when both are
// empty. Shared by the list and the single-monitor read so the two can never
// describe the same monitor's routing differently.
func withRoutes(resp *monitorResponse, attached []store.ChannelSummary, rules []store.RuleRoute, fallback *monitorChannelResponse) {
	own := make([]monitorChannelResponse, 0, len(attached))
	for _, c := range attached {
		own = append(own, monitorChannelResponse{ID: c.ID, Name: c.Name})
	}
	resp.Channels = &own
	routes := make([]monitorRuleRouteResponse, 0, len(rules))
	for _, rt := range rules {
		via := make([]monitorChannelResponse, 0, len(rt.Channels))
		for _, c := range rt.Channels {
			via = append(via, monitorChannelResponse{ID: c.ID, Name: c.Name})
		}
		routes = append(routes, monitorRuleRouteResponse{
			RuleID: rt.RuleID, TagKey: rt.TagKey, TagValue: rt.TagValue, Channels: via,
		})
	}
	resp.RuleChannels = &routes
	if len(own) == 0 && len(routes) == 0 {
		resp.DefaultChannel = fallback
	}
}

// monitorRoutes reads one monitor's routing for the single-monitor read. It
// reports false, having logged why, when any part could not be read: the
// caller then leaves all three fields out, because a partial answer (own
// channels without the rules, say) would read as a complete one.
func (s *Server) monitorRoutes(ctx context.Context, id int64) ([]store.ChannelSummary, []store.RuleRoute, *monitorChannelResponse, bool) {
	attached, err := s.db.MonitorChannelRefs(ctx, id)
	if err != nil {
		s.log.Error("monitor channel refs", "monitor_id", id, "error", err)
		return nil, nil, nil, false
	}
	rules, err := s.db.RuleRoutesForMonitor(ctx, id)
	if err != nil {
		s.log.Error("monitor rule routes", "monitor_id", id, "error", err)
		return nil, nil, nil, false
	}
	def, ok, err := s.db.DefaultChannel(ctx)
	if err != nil {
		s.log.Error("default channel", "error", err)
		return nil, nil, nil, false
	}
	var fallback *monitorChannelResponse
	if ok {
		fallback = &monitorChannelResponse{ID: def.ID, Name: def.Name}
	}
	return attached, rules, fallback, true
}

// oldestFirstHeartbeats reverses a newest-first run of heartbeats.
//
// The store returns newest first, which is the natural order for "show me the
// last N". The dashboard's beat bar reads left to right through time, so it
// needs the opposite. Reversing once here keeps both contracts honest instead
// of making one of them lie about its order.
//
// A monitor with no heartbeats yields nil, so `omitempty` leaves the field out
// entirely rather than shipping an empty array.
func oldestFirstHeartbeats(hbs []store.Heartbeat) []heartbeatResponse {
	if len(hbs) == 0 {
		return nil
	}
	out := make([]heartbeatResponse, 0, len(hbs))
	for i := len(hbs) - 1; i >= 0; i-- {
		out = append(out, describeHeartbeat(hbs[i]))
	}
	return out
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var req createMonitorRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	// Reject fields the API does not know.
	//
	// Silently ignoring them is a trap: a client that sends interval_seconds
	// instead of interval_s gets a 201, believes it configured a 5-second
	// check, and receives the default instead. The failure surfaces much
	// later as "why is this monitor so slow" — a typo should be an error at
	// the moment it is made.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if p := validateCreateMonitor(req); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	method, _ := normaliseMethod(req.Method)

	m := store.Monitor{
		Name:      req.Name,
		Type:      req.Type,
		Target:    req.Target,
		IntervalS: req.IntervalS,
		TimeoutS:  req.TimeoutS,
		// Already validated above; the error is discarded because a second
		// failure here would be unreachable.
		Method:          method,
		ExpectedStatus:  req.ExpectedStatus,
		Keyword:         req.Keyword,
		KeywordMode:     req.KeywordMode,
		FollowRedirects: true,
		Headers:         req.Headers,
		Body:            req.Body,
		Enabled:         true,
		CaptureResponse: true,
	}
	if req.Retries != nil {
		m.Retries = *req.Retries
	} else {
		m.Retries = 2
	}
	if req.FollowRedirects != nil {
		m.FollowRedirects = *req.FollowRedirects
	}
	if req.SSLWarnDays != nil {
		m.SSLWarnDays = *req.SSLWarnDays
	}
	if req.RecoveryThreshold != nil {
		m.RecoveryThreshold = *req.RecoveryThreshold
	}
	// Unlike the other defaults this one is not in ApplyMonitorDefaults: the
	// column default is 0, which is a meaningful value ("no reminders"), so a
	// zero cannot be read as "unset" once the row exists. Only a create
	// request that omits the field entirely gets the opinionated default.
	m.RepeatAfterS = defaultRepeatAfterS
	if req.RepeatAfterS != nil {
		m.RepeatAfterS = *req.RepeatAfterS
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	if req.CaptureResponse != nil {
		m.CaptureResponse = *req.CaptureResponse
	}
	// Validated above, so the second return is discarded: a failure here
	// would be unreachable.
	if req.MinTLSVersion != nil {
		m.MinTLSVersion, _ = checker.ParseTLSVersion(*req.MinTLSVersion)
	}
	// Already validated above by validateCreateMonitor; normalising again
	// here rather than storing the raw map keeps the stored keys canonical
	// without the validator having to hand a value back.
	m.Tags, _ = store.NormaliseTags(req.Tags)
	// Validated above as well.
	m.JSONAssertion, _ = jsonAssertionFromWire(req.JSONAssertion)

	if req.Type == store.TypePush {
		m.PushIntervalS = *req.PushIntervalS
		m.PushGraceS = store.DefaultPushGraceS
		if req.PushGraceS != nil {
			m.PushGraceS = *req.PushGraceS
		}
	}

	m.ChannelIDs = req.ChannelIDs

	created, err := s.db.CreateMonitor(r.Context(), m)
	if errors.Is(err, store.ErrUnknownChannel) {
		writeProblem(w, http.StatusBadRequest, unknownChannelProblem(err))
		return
	}
	if err != nil {
		s.log.Error("create monitor", "error", err)
		writeError(w, http.StatusBadRequest, "could not create monitor: "+err.Error())
		return
	}

	s.log.Info("monitor created", "id", created.ID, "name", created.Name, "target", created.Target)

	// The plaintext push URL exists only in this response. The monitor is
	// useless without it and it can never be retrieved again, so it is
	// assembled here — from the request's own host, so a user behind a
	// reverse proxy is handed a URL that actually works from outside.
	resp := s.describeMonitor(r, created)
	if created.PushToken != "" {
		resp.PushURL = pushURL(r, created.PushToken)
	}
	writeJSON(w, http.StatusCreated, resp)
}

func validateCreateMonitor(req createMonitorRequest) problem {
	if req.Name == "" {
		return fieldProblem("name", "name is required")
	}
	// A push monitor has nothing to dial, so it has nothing to put in
	// target. Requiring one anyway would force every client to invent a
	// placeholder, and a column full of invented placeholders is worse than
	// an empty one: it looks like data.
	if req.Target == "" && req.Type != store.TypePush {
		return fieldProblem("target", "target is required")
	}
	if req.Target != "" && req.Type == store.TypePush {
		return fieldProblem("target", "a push monitor has no target; it is reported to, not probed")
	}
	switch req.Type {
	case "http", "tcp", "ping", "ssl", store.TypePush:
	case "":
		return fieldProblem("type", "type is required")
	default:
		return fieldProblem("type", "unknown type "+req.Type)
	}
	if p := validatePushWindow(req.Type, req.PushIntervalS, req.PushGraceS); !p.ok() {
		return p
	}
	if req.IntervalS != 0 && (req.IntervalS < 20 || req.IntervalS > 86400) {
		return fieldProblem("interval_s", "interval_s must be between 20 and 86400")
	}
	if req.TimeoutS != 0 && (req.TimeoutS < 1 || req.TimeoutS > 120) {
		return fieldProblem("timeout_s", "timeout_s must be between 1 and 120")
	}
	// Creation used to skip these three entirely, so a bad method or an
	// out-of-range ssl_warn_days only surfaced as a SQLite constraint error or,
	// worse, as a monitor that failed every check forever.
	if _, p := normaliseMethod(req.Method); !p.ok() {
		return p
	}
	if p := validateKeywordMode(req.KeywordMode); !p.ok() {
		return p
	}
	if p := validateSSLWarnDays(req.SSLWarnDays); !p.ok() {
		return p
	}
	if p := validateRecoveryThreshold(req.RecoveryThreshold); !p.ok() {
		return p
	}
	if p := validateRepeatAfterS(req.RepeatAfterS); !p.ok() {
		return p
	}
	if p := validateMinTLSVersion(req.MinTLSVersion); !p.ok() {
		return p
	}
	if p := validateTargetForType(req.Type, req.Target); !p.ok() {
		return p
	}
	if p := validateTags(req.Tags); !p.ok() {
		return p
	}
	if req.JSONAssertion != nil {
		if p := jsonAssertionTypeProblem(req.Type); !p.ok() {
			return p
		}
		if _, p := jsonAssertionFromWire(req.JSONAssertion); !p.ok() {
			return p
		}
	}
	return problem{}
}

// validateTags reports a bad tag map as a field problem.
//
// The rules themselves live in the store package, next to the table that has
// to hold the result: a key that this package accepted and the schema refused
// would surface as a 500 on a request that was wrong from the start.
func validateTags(tags map[string]string) problem {
	if _, err := store.NormaliseTags(tags); err != nil {
		return fieldProblem("tags", err.Error())
	}
	return problem{}
}

// monitorHTTPMethods is the set an http monitor may use, and the single place
// the list is written down. (Not `httpMethods`: openapi_test.go already owns
// that name for the operation keys in a spec path item.) Anything outside it reaches http.NewRequestWithContext
// and fails there, far from the field that caused it.
var monitorHTTPMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true,
	"PATCH": true, "DELETE": true, "OPTIONS": true,
}

// normaliseMethod upper-cases a method and rejects anything unsupported.
//
// An empty method is left empty rather than defaulted here: the store decides
// what an unset method becomes, and duplicating that choice in the API is how
// the two drift apart.
func normaliseMethod(method string) (string, problem) {
	if method == "" {
		return "", problem{}
	}
	upper := strings.ToUpper(method)
	if !monitorHTTPMethods[upper] {
		return "", fieldProblem("method", "unsupported HTTP method "+method)
	}
	return upper, problem{}
}

// validateSSLWarnDays applies the one range the column allows.
//
// 0 is rejected rather than accepted as "never warn", because
// store.ApplyMonitorDefaults would turn it back into 14 and the client would
// be told it had disabled a warning it still gets.
func validateSSLWarnDays(days *int) problem {
	if days == nil {
		return problem{}
	}
	if *days < 1 || *days > 365 {
		return fieldProblem("ssl_warn_days", "ssl_warn_days must be between 1 and 365")
	}
	return problem{}
}

// validateRecoveryThreshold applies the range the column allows.
//
// 0 is rejected rather than read as "unset": store.ApplyMonitorDefaults would
// turn it into 2, and the client would be told it had asked for something it
// did not get. 1 is the smallest meaningful value (close on the first pass);
// the upper bound matches retries.
func validateRecoveryThreshold(n *int) problem {
	if n == nil {
		return problem{}
	}
	if *n < 1 || *n > 10 {
		return fieldProblem("recovery_threshold", "recovery_threshold must be between 1 and 10")
	}
	return problem{}
}

// defaultRepeatAfterS is how long a new monitor waits before repeating an
// unacknowledged alert.
//
// Fifteen minutes, and on by default. Off by default would leave the
// acknowledge button decorative for everyone who never finds the setting,
// which is the state this ticket exists to fix. Fifteen minutes is long enough
// that a short outage resolves itself before anyone is told twice.
const defaultRepeatAfterS = 900

// validateRepeatAfterS applies the range the column allows.
//
// 0 is accepted here where ssl_warn_days rejects it, because 0 has a meaning:
// no reminders for this monitor. The lower bound of 60 on the non-zero side is
// not arbitrary — anything shorter reminds faster than most monitors check, so
// the second alert would carry exactly the information the first one did.
func validateRepeatAfterS(seconds *int) problem {
	if seconds == nil {
		return problem{}
	}
	if *seconds == 0 {
		return problem{}
	}
	if *seconds < 60 || *seconds > 86400 {
		return fieldProblem("repeat_after_s",
			"repeat_after_s must be 0 to disable reminders, or between 60 and 86400")
	}
	return problem{}
}

// validateMinTLSVersion accepts only the labels the checker can dial with.
//
// The empty string is rejected rather than read as "use the default". On
// create there is already a way to say that — leave the field out — so an
// empty string is a client that computed a value and got nothing, and telling
// it so at the moment it happens beats storing a floor it did not choose.
func validateMinTLSVersion(label *string) problem {
	if label == nil {
		return problem{}
	}
	if _, ok := checker.ParseTLSVersion(*label); !ok {
		return unknownTLSVersionProblem(*label)
	}
	return problem{}
}

// unknownTLSVersionProblem names the field and lists what would have worked.
// A rejection that only says "invalid" leaves the caller guessing whether the
// API wanted "TLSv1.2", "771" or "1.2".
func unknownTLSVersionProblem(label string) problem {
	return fieldProblem("min_tls_version",
		"min_tls_version must be one of "+strings.Join(checker.TLSVersions(), ", ")+
			"; got "+strconv.Quote(label))
}

// validateKeywordMode rejects modes the checker cannot act on.
func validateKeywordMode(mode string) problem {
	switch mode {
	case "", "absent_ok", "must_contain", "must_not_contain":
		return problem{}
	default:
		return fieldProblem("keyword_mode", "unknown keyword_mode "+mode)
	}
}

// validateTargetForType rejects target shapes that cannot work for a type.
//
// Catching this at creation beats letting the monitor fail forever with an
// internal error: a mistake made while typing should be corrected while the
// user is still looking at the form.
func validateTargetForType(typ, target string) problem {
	// Every rejection below is about the target the caller typed, even the
	// ones whose wording blames the type: "a ping monitor cannot use a port"
	// is a complaint about the port in the target, and the input a form has
	// to highlight is the target box.
	bad := func(msg string) problem { return fieldProblem("target", msg) }

	switch typ {
	case "http":
		u, err := url.Parse(target)
		if err != nil {
			return bad("target is not a valid URL: " + err.Error())
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return bad("an http monitor needs a target starting with http:// or https://")
		}
		if u.Host == "" {
			return bad("target has no host")
		}
		// net/url accepts Unicode whitespace in hosts, including escaped
		// forms. Inspect the decoded hostname, leaving path/query text alone.
		if strings.ContainsFunc(u.Hostname(), unicode.IsSpace) {
			return bad("a hostname cannot contain spaces")
		}

	case "tcp":
		host, port, err := checker.ParseHostPort(target, 0)
		if err != nil {
			return bad("invalid target: " + err.Error())
		}
		if host == "" {
			return bad("target has no host")
		}
		if port == 0 {
			return bad("a tcp monitor needs a port, for example db.example.com:5432")
		}

	case "ssl":
		host, _, err := checker.ParseHostPort(target, 443)
		if err != nil {
			return bad("invalid target: " + err.Error())
		}
		if host == "" {
			return bad("target has no host")
		}

	case "ping":
		// ICMP has no ports. A URL or a host:port here means the user picked
		// the wrong check type, so say which one they wanted rather than
		// complaining about a port they may not have realised they typed.
		if strings.Contains(target, "://") {
			return bad("a ping monitor takes a hostname or IP address, not a URL — " +
				"use " + hostOnly(target) + ", or an http monitor for the full URL")
		}
		// A ping target is a bare hostname or IP and nothing else. The other
		// three types hand the target to a parser that normalises it; ping
		// hands it to a resolver more or less as typed, so anything the
		// resolver cannot use has to be caught here. ParseHostPort's error was
		// previously discarded, which let a whitespace-only target through to
		// a monitor that failed its lookup forever — the exact fail-forever
		// case this function exists to prevent.
		host, port, err := checker.ParseHostPort(target, 0)
		if err != nil {
			return bad("a ping monitor takes a hostname or IP address: " + err.Error())
		}
		if port != 0 {
			return bad("a ping monitor cannot use a port; use a tcp monitor instead")
		}
		if host == "" {
			return bad("target has no host")
		}
		// ParseHostPort drops a path, query, fragment or credentials because
		// they are meaningless to a TCP dial. They are meaningless to ping
		// too, but dropping them silently would store a target that does not
		// say what it does, so name the part that cannot be used.
		if strings.ContainsAny(target, "/?#@") {
			return bad("a ping monitor takes a hostname or IP address, nothing after it — " +
				"use " + host)
		}
	}
	return problem{}
}

// hostOnly extracts the host from a URL-shaped target, for use in error
// messages that suggest a correction.
func hostOnly(target string) string {
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return target
}

func (s *Server) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	m, err := s.db.GetMonitor(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.log.Error("get monitor", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}
	setMonitorETag(w, m)
	// Only the authenticated detail read carries request configuration. Keep
	// credentials and request bodies out of the bulk/dashboard serializer.
	headers := m.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	// Routing is read after the row, never before: the edit form sends these
	// channels back under the ETag set above. Read in this order, a channel
	// change landing in between pairs new channels with an old version, and
	// the save is refused as stale. The other order would pair old channels
	// with the new version, and the save would quietly put them back.
	described := s.describeMonitor(r, m)
	if attached, rules, fallback, ok := s.monitorRoutes(r.Context(), id); ok {
		withRoutes(&described, attached, rules, fallback)
	}
	resp := monitorDetailResponse{
		monitorResponse: described,
		Method:          m.Method, ExpectedStatus: m.ExpectedStatus,
		Keyword: m.Keyword, KeywordMode: m.KeywordMode,
		FollowRedirects:   m.FollowRedirects,
		Retries:           m.Retries,
		RecoveryThreshold: m.RecoveryThreshold,
		SSLWarnDays:       m.SSLWarnDays,
		JSONAssertion:     jsonAssertionToWire(m.JSONAssertion),
	}
	// A viewer can inspect check rules but must not gain reusable credentials
	// merely because edit settings became readable. Never mask secrets into an
	// editable value: writers receive the exact stored value, viewers no field.
	if user, ok := UserFromContext(r.Context()); ok && user.Role.CanWrite() {
		resp.Headers = &headers
		resp.Body = &m.Body
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := s.db.DeleteMonitor(r.Context(), id); err != nil {
		s.log.Error("delete monitor", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete monitor")
		return
	}
	s.manualChecks.forget(id)
	s.log.Info("monitor deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePauseMonitor(w http.ResponseWriter, r *http.Request) {
	s.setEnabled(w, r, false)
}

func (s *Server) handleResumeMonitor(w http.ResponseWriter, r *http.Request) {
	s.setEnabled(w, r, true)
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := s.db.SetMonitorEnabled(r.Context(), id, enabled); err != nil {
		s.log.Error("set monitor enabled", "id", id, "enabled", enabled, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update monitor")
		return
	}

	m, err := s.db.GetMonitor(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}
	writeJSON(w, http.StatusOK, s.describeMonitor(r, m))
}

func (s *Server) handleListHeartbeats(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.requireMonitor(w, r, id) {
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return
		}
		limit = n
	}

	hbs, err := s.db.ListHeartbeats(r.Context(), id, limit)
	if err != nil {
		s.log.Error("list heartbeats", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load heartbeats")
		return
	}

	out, err := s.describeResponseHistory(r.Context(), hbs)
	if err != nil {
		s.log.Error("list heartbeat capture reasons", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load heartbeats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"heartbeats": out})
}

// describeMonitor enriches a stored monitor with its current state.
//
// Failures to load that state are logged but not fatal: a dashboard that shows
// monitors with unknown status beats a dashboard that shows nothing.
func (s *Server) describeMonitor(r *http.Request, m store.Monitor) monitorResponse {
	resp := monitorResponse{
		ID:        m.ID,
		Name:      m.Name,
		Type:      m.Type,
		Target:    m.Target,
		IntervalS: m.IntervalS,
		TimeoutS:  m.TimeoutS,
		Enabled:   m.Enabled,

		CaptureResponse: m.CaptureResponse,
		RepeatAfterS:    m.RepeatAfterS,
		MinTLSVersion:   checker.TLSVersionLabel(m.MinTLSVersion),

		Status:    "pending",
		Tags:      m.Tags,
		CreatedAt: m.CreatedAt,

		PushTokenPrefix: m.PushTokenPrefix,
		PushIntervalS:   m.PushIntervalS,
		PushGraceS:      m.PushGraceS,
	}

	ctx := r.Context()

	if active, err := s.db.InMaintenance(ctx, m.ID, time.Now()); err == nil {
		resp.Maintenance = &active
	}
	hb, err := s.db.LatestHeartbeat(ctx, m.ID)
	hasBeat := err == nil
	switch {
	case err == nil:
		// The last heartbeat says what the last probe saw; it does not say
		// whether the monitor is down. A single failed check is a blip until
		// the failure threshold is crossed, so the reported status comes from
		// the incident record — the same source the alerting uses. Deriving
		// it from the heartbeat alone would put the dashboard and the
		// notifications into disagreement, which is worse than either being
		// slightly stale.
		resp.Status = "up"
		if !hb.OK {
			resp.Status = "warning"
		}
		ts := hb.TS
		resp.LastCheck = &ts
		resp.LatencyMS = hb.LatencyMS
		resp.StatusCode = hb.StatusCode
		resp.Error = hb.Error
		if !hb.OK {
			resp.FailureKind = hb.FailureKind
		}
	case errors.Is(err, sql.ErrNoRows):
		// Never checked yet.
		resp.Status = "pending"
	default:
		s.log.Error("latest heartbeat", "monitor_id", m.ID, "error", err)
		resp.Status = "pending"
	}

	// A confirmed open incident is what "down" means.
	inc, err := s.db.OpenIncidentFor(ctx, m.ID)
	switch {
	case err == nil:
		resp.IncidentID = inc.ID
		resp.IncidentSince = &inc.StartedAt
		if inc.Confirmed() {
			resp.Status = "down"
			// Recovering is still a confirmed, open incident, so it is
			// only ever a refinement of down: the engine is asked only
			// once the incident record already says the outage is real.
			if rec := s.recovery(m.ID); rec != nil {
				resp.Status = "recovering"
				resp.Recovery = rec
			}
		}
		if resp.Error == "" {
			// The kind travels with the message it classifies, so a
			// fallback error never wears the heartbeat's kind or none.
			resp.Error = inc.LastError
			resp.FailureKind = inc.Cause
		}
	case errors.Is(err, store.ErrNoOpenIncident):
		// Nothing wrong.
	default:
		s.log.Error("open incident", "monitor_id", m.ID, "error", err)
	}

	if m.Type == store.TypePush && resp.Status != "down" && resp.Status != "recovering" {
		if reason := s.pushWaiting(m, hb, hasBeat); reason != "" {
			resp.Status = "pending"
			resp.PushWaiting = reason
		}
	}

	if stats, err := s.db.Uptime(ctx, m.ID, 24*time.Hour); err == nil {
		if stats.Total > 0 {
			resp.Uptime24h = &stats.Percentage
		}
	} else {
		s.log.Error("uptime", "monitor_id", m.ID, "error", err)
	}

	return resp
}

// pushListeningSource is implemented by the runner, which is the only thing
// that knows when this process started hearing push reports.
type pushListeningSource interface {
	PushListeningSince() time.Time
}

// pushWaiting reports why a push monitor that has reported before is waiting
// for its next report rather than standing on its last one: the window it is
// in started after that report, at a resume or at a start of SubGlance. Empty
// when the last report is still what the monitor stands on, and when it has
// never reported, which the UI already tells apart by the missing last_check.
//
// The rule is the watchdog's own (store.Monitor.PushWindow), so the API cannot
// call a monitor up that the watchdog is treating as unheard, or the reverse.
func (s *Server) pushWaiting(m store.Monitor, last store.Heartbeat, hasBeat bool) string {
	if !hasBeat {
		return ""
	}
	var since time.Time
	if src, ok := s.pusher.(pushListeningSource); ok {
		since = src.PushListeningSince()
	}
	return string(m.PushWindow(last.TS, since).Reason)
}

// requireMonitor confirms a monitor exists before a sub-resource is read.
//
// Without it, an unknown id returns a tidy empty list, which reads as "this
// monitor has no data yet" rather than "this monitor does not exist". A client
// polling a deleted monitor would keep getting cheerful 200s and never learn it
// is querying a ghost. Absence and emptiness are different facts, so they get
// different answers.
//
// A lookup failure that is not "no rows" is a 500, not a 404: claiming the
// monitor is gone because the database hiccuped would be the same confident lie
// in a different costume.
func (s *Server) requireMonitor(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, err := s.db.GetMonitor(r.Context(), id)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "monitor not found")
		return false
	default:
		s.log.Error("get monitor", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return false
	}
}

// pathID parses the {id} path segment, writing an error response when invalid.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return 0, false
	}
	return id, true
}

// writeError renders an error that is not about a particular request field.
// Validators that do know the field call writeProblem instead.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeProblem(w, status, bodyProblem(msg))
}

// patchMonitorRequest is a partial update: every field is a pointer so that
// "not sent" and "sent as the zero value" stay distinguishable.
//
// That distinction is the whole point of PATCH. With plain values, sending
// {"name":"x"} would also read as interval_s=0 and enabled=false, and a rename
// would silently pause the monitor and reset its schedule.
type patchMonitorRequest struct {
	Name              *string            `json:"name"`
	Type              *string            `json:"type"`
	Target            *string            `json:"target"`
	IntervalS         *int               `json:"interval_s"`
	TimeoutS          *int               `json:"timeout_s"`
	Retries           *int               `json:"retries"`
	RecoveryThreshold *int               `json:"recovery_threshold"`
	Method            *string            `json:"method"`
	ExpectedStatus    *string            `json:"expected_status"`
	Keyword           *string            `json:"keyword"`
	KeywordMode       *string            `json:"keyword_mode"`
	FollowRedirects   *bool              `json:"follow_redirects"`
	Headers           *map[string]string `json:"headers"`
	Body              *string            `json:"body"`
	SSLWarnDays       *int               `json:"ssl_warn_days"`
	RepeatAfterS      *int               `json:"repeat_after_s"`
	Enabled           *bool              `json:"enabled"`
	CaptureResponse   *bool              `json:"capture_response"`
	// MinTLSVersion is "1.0" to "1.3", or "" to go back to having no
	// opinion. The empty string is accepted here and rejected on create
	// because on an existing monitor it has a meaning — clear the floor I
	// set earlier — whereas on create it is indistinguishable from not
	// sending the field at all.
	MinTLSVersion *string `json:"min_tls_version"`
	// Tags replaces the whole set, like Headers. Sending `{}` clears them;
	// omitting the field leaves them alone.
	Tags *map[string]string `json:"tags"`

	// The push window may be retuned after the fact — a backup that moved
	// from hourly to nightly should not have to be recreated, which would
	// throw away its history and invalidate the URL already in the crontab.
	// The token itself is not patchable; see UpdateMonitor.
	PushIntervalS *int `json:"push_interval_s"`
	PushGraceS    *int `json:"push_grace_s"`

	// JSONAssertion replaces the assertion when present; `null` removes it.
	// Raw, because a pointer cannot tell an omitted key from an explicit
	// null, and here the two mean "leave it" and "remove it".
	JSONAssertion json.RawMessage `json:"json_assertion"`

	// ChannelIDs replaces the monitor's own channels when present, in the
	// same write as every other field, so the If-Match that guards the row
	// guards them too. `[]` removes them all; omitted or null leaves them.
	// The same set PUT /monitors/{id}/channels replaces.
	ChannelIDs *[]int64 `json:"channel_ids"`
}

// handlePatchMonitor applies a partial update to an existing monitor.
//
// Until now the only way to change a monitor was to delete and recreate it,
// which throws away its heartbeats, its uptime history and any open incident.
// Correcting a typo in a URL should not erase the record of last week's
// outage.
//
// An If-Match header makes the write conditional on the monitor not having
// changed since the client read it. Without one the write stays
// last-write-wins, because making the header mandatory would break every
// script written before it existed.
func (s *Server) handlePatchMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Parse the precondition before the body: a malformed If-Match is a
	// client error regardless of what it was trying to send.
	ifMatch := r.Header.Get("If-Match")
	var (
		wantTags []string
		wantAny  bool
	)
	if ifMatch != "" {
		var valid bool
		wantTags, wantAny, valid = parseIfMatch(ifMatch)
		if !valid {
			writeError(w, http.StatusBadRequest, "malformed If-Match header")
			return
		}
	}

	var req patchMonitorRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	ctx := r.Context()

	m, err := s.db.GetMonitor(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		// 404 rather than 412, even for a conditional request. RFC 9110 would
		// allow either, and a monitor that never existed is more usefully
		// described as missing than as a stale version: 412 tells the client to
		// re-fetch and retry, which would loop forever on an id that is simply
		// wrong. The genuine race — deleted between this read and the write
		// below — is answered as a conflict, where re-fetching is the right
		// advice.
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.log.Error("get monitor for patch", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}

	// A conditional request is decided by the UPDATE itself, so nothing is
	// compared here. `*` asks only that the monitor exist, which the
	// successful load above already established, so it needs no version
	// predicate and falls through to the unconditional write.
	conditional := ifMatch != "" && !wantAny

	if p := applyMonitorPatch(&m, req); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}

	var updated store.Monitor
	if conditional {
		updated, err = s.db.UpdateMonitorIfUnchanged(ctx, m, ifMatchVersions(wantTags))
	} else {
		updated, err = s.db.UpdateMonitor(ctx, m)
	}
	switch {
	case err == nil:
	case errors.Is(err, store.ErrVersionConflict):
		writeConflict(w, id)
		return
	case errors.Is(err, store.ErrUnknownChannel):
		writeProblem(w, http.StatusBadRequest, unknownChannelProblem(err))
		return
	case errors.Is(err, sql.ErrNoRows):
		// Deleted between the read and the write. For `If-Match: *` that is a
		// failed precondition, not a missing resource: the client asked us to
		// write only if the monitor still existed, and by the time we wrote it
		// did not. 404 would describe the row; 412 answers the question that
		// was actually asked.
		if wantAny {
			writeConflict(w, id)
			return
		}
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	default:
		s.log.Error("update monitor", "id", id, "error", err)
		writeError(w, http.StatusBadRequest, "could not update monitor: "+err.Error())
		return
	}

	// The response carries the new version so a client editing repeatedly can
	// keep going without re-fetching — without it, every conditional PATCH
	// would have to be followed by a GET just to learn the next validator.
	setMonitorETag(w, updated)

	s.log.Info("monitor updated", "id", updated.ID, "name", updated.Name, "target", updated.Target)
	writeJSON(w, http.StatusOK, s.describeMonitor(r, updated))
}

// unknownChannelProblem blames channel_ids for a channel that does not exist,
// which in a form usually means it was deleted after the form was opened.
func unknownChannelProblem(err error) problem {
	msg := "a chosen channel does not exist; it may have been deleted"
	if _, id, ok := strings.Cut(err.Error(), store.ErrUnknownChannel.Error()+": "); ok {
		msg = "channel " + id + " does not exist; it may have been deleted"
	}
	return fieldProblem("channel_ids", msg)
}

// writeConflict reports that the monitor moved on since the caller read it.
//
// No fresh ETag is returned with it: the client has to look at what the other
// editor wrote before deciding whether its own change still makes sense, and
// handing over a validator it could blindly retry with would invite exactly
// the overwrite this endpoint refused.
func writeConflict(w http.ResponseWriter, id int64) {
	writeError(w, http.StatusPreconditionFailed,
		"monitor "+strconv.FormatInt(id, 10)+" was modified by someone else; "+
			"fetch it again and reapply your change")
}

// applyMonitorPatch merges the request into m and validates the result. It
// returns a problem, or the zero problem when the merged monitor is valid.
//
// Validation runs on the merged monitor, not on the request. Changing only the
// type of an existing monitor can invalidate a target that was never touched,
// and that combination has to be rejected just as firmly as a bad create.
func applyMonitorPatch(m *store.Monitor, req patchMonitorRequest) problem {
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return fieldProblem("name", "name cannot be empty")
		}
		m.Name = *req.Name
	}
	if req.Type != nil {
		// A monitor cannot become a push monitor or stop being one. Both
		// directions would silently break something the user cannot see:
		// converting away leaves a live URL pointing at a monitor that no
		// longer listens, and converting to would need a token this
		// endpoint has no way to hand back. Delete and recreate is the
		// honest answer, and it is what the user means anyway.
		if (*req.Type == store.TypePush) != (m.Type == store.TypePush) {
			return fieldProblem("type",
				"a monitor cannot be converted to or from push; create a new one instead")
		}
		switch *req.Type {
		case "http", "tcp", "ping", "ssl", store.TypePush:
			m.Type = *req.Type
		default:
			return fieldProblem("type", "unknown type "+*req.Type)
		}
	}
	if req.Target != nil {
		if strings.TrimSpace(*req.Target) == "" {
			return fieldProblem("target", "target cannot be empty")
		}
		m.Target = *req.Target
	}

	// Unlike create, an explicit 0 here is a mistake rather than "use the
	// default": the client asked for a 0-second interval on a monitor that
	// already has a working one.
	if req.IntervalS != nil {
		if *req.IntervalS < 20 || *req.IntervalS > 86400 {
			return fieldProblem("interval_s", "interval_s must be between 20 and 86400")
		}
		m.IntervalS = *req.IntervalS
	}
	if req.TimeoutS != nil {
		if *req.TimeoutS < 1 || *req.TimeoutS > 120 {
			return fieldProblem("timeout_s", "timeout_s must be between 1 and 120")
		}
		m.TimeoutS = *req.TimeoutS
	}
	if req.Retries != nil {
		if *req.Retries < 0 || *req.Retries > 10 {
			return fieldProblem("retries", "retries must be between 0 and 10")
		}
		m.Retries = *req.Retries
	}
	if req.RecoveryThreshold != nil {
		if p := validateRecoveryThreshold(req.RecoveryThreshold); !p.ok() {
			return p
		}
		m.RecoveryThreshold = *req.RecoveryThreshold
	}
	if req.Method != nil {
		method, p := normaliseMethod(*req.Method)
		if !p.ok() {
			return p
		}
		m.Method = method
	}
	if req.ExpectedStatus != nil {
		m.ExpectedStatus = *req.ExpectedStatus
	}
	if req.Keyword != nil {
		m.Keyword = *req.Keyword
	}
	if req.KeywordMode != nil {
		if p := validateKeywordMode(*req.KeywordMode); !p.ok() {
			return p
		}
		m.KeywordMode = *req.KeywordMode
	}
	if req.FollowRedirects != nil {
		m.FollowRedirects = *req.FollowRedirects
	}
	if req.Headers != nil {
		m.Headers = *req.Headers
	}
	if req.Body != nil {
		m.Body = *req.Body
	}
	if req.SSLWarnDays != nil {
		if p := validateSSLWarnDays(req.SSLWarnDays); !p.ok() {
			return p
		}
		m.SSLWarnDays = *req.SSLWarnDays
	}
	if req.RepeatAfterS != nil {
		if p := validateRepeatAfterS(req.RepeatAfterS); !p.ok() {
			return p
		}
		m.RepeatAfterS = *req.RepeatAfterS
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	if req.CaptureResponse != nil {
		m.CaptureResponse = *req.CaptureResponse
	}
	if req.MinTLSVersion != nil {
		if *req.MinTLSVersion == "" {
			m.MinTLSVersion = 0
		} else {
			v, ok := checker.ParseTLSVersion(*req.MinTLSVersion)
			if !ok {
				return unknownTLSVersionProblem(*req.MinTLSVersion)
			}
			m.MinTLSVersion = v
		}
	}
	if req.ChannelIDs != nil {
		// Non-nil even for `[]`, because nil on the store side means
		// "leave the assignments alone" and `[]` means "remove them".
		m.ChannelIDs = append([]int64{}, (*req.ChannelIDs)...)
	}
	if req.Tags != nil {
		tags, err := store.NormaliseTags(*req.Tags)
		if err != nil {
			return fieldProblem("tags", err.Error())
		}
		m.Tags = tags
	}
	if len(req.JSONAssertion) > 0 {
		var wire *jsonAssertionWire
		dec := json.NewDecoder(bytes.NewReader(req.JSONAssertion))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&wire); err != nil {
			return fieldProblem("json_assertion", "json_assertion must be an object with path, operator and expected, or null: "+err.Error())
		}
		a, p := jsonAssertionFromWire(wire)
		if !p.ok() {
			return p
		}
		m.JSONAssertion = a
	}
	// Checked against the merged monitor: an assertion sent alone to a tcp
	// monitor, and a type change away from http that keeps one, are the
	// same mistake.
	if m.JSONAssertion != nil && (len(req.JSONAssertion) > 0 || req.Type != nil) {
		if p := jsonAssertionTypeProblem(m.Type); !p.ok() {
			return p
		}
	}
	if req.PushIntervalS != nil || req.PushGraceS != nil {
		if m.Type != store.TypePush {
			return fieldProblem("push_interval_s",
				"push_interval_s and push_grace_s apply only to push monitors")
		}
		if req.PushIntervalS != nil {
			m.PushIntervalS = *req.PushIntervalS
		}
		if req.PushGraceS != nil {
			m.PushGraceS = *req.PushGraceS
		}
		if p := validatePushRange(m.PushIntervalS, m.PushGraceS); !p.ok() {
			return p
		}
	}

	// Re-validate whenever either half of the pair moved. The target the
	// monitor ends up with is what the checker will dial, so that is what has
	// to be legal — the SSRF guard itself runs at dial time, in the checker.
	if req.Target != nil || req.Type != nil {
		if p := validateTargetForType(m.Type, m.Target); !p.ok() {
			return p
		}
	}
	return problem{}
}
