// Package api serves the SubGlance HTTP API and, in production builds, the
// embedded web UI.
//
// Product principle 4: the UI is a client of this API and
// gets no private endpoints. Anything the dashboard can do, a script can do.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/buildinfo"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/store"
	"github.com/frankgraave/subglance/internal/trustedproxy"
	"github.com/frankgraave/subglance/internal/watchdog"
)

// Server wires the HTTP routes together.
type Server struct {
	log              *slog.Logger
	db               *store.DB
	startedAt        time.Time
	watchdogSnapshot func() watchdog.Snapshot

	// bus carries live check results to streaming clients. Nil disables the
	// stream endpoint rather than crashing it, so the API stays usable in
	// tests and in any deployment that runs without a checker pipeline.
	bus *events.Bus

	// prober runs on-demand checks. Nil disables that endpoint, for the same
	// reason as bus: an API without a checker behind it should still serve
	// everything else.
	prober Prober

	// manualChecks rate-limits POST /monitors/{id}/check per monitor.
	manualChecks cooldown

	// pusher records reports arriving on a push URL. Nil disables the push
	// endpoint, matching how bus and prober are treated.
	pusher PushRecorder

	// tester sends a real message through a channel, for the test button.
	// Nil disables that endpoint, on the same principle as the others.
	tester ChannelTester

	// targetGuard vets a channel's destination when the channel is saved,
	// so a target that can never be delivered to is refused while the
	// operator is still looking at the form instead of during an outage.
	// Nil skips the check; see WithTargetGuard and checkChannelTarget, and
	// note that it is a usability layer, never the security boundary.
	targetGuard TargetGuard

	// pushReports rate-limits the public push endpoint per monitor. It is
	// separate from manualChecks because the two protect against different
	// things: one bounds what an authenticated human can ask the server to
	// dial, the other bounds what anyone holding one token can write.
	pushReports cooldown

	// pushFlood bounds the public push route as a whole, before a token is
	// resolved. pushReports cannot: it is keyed on a monitor id that only
	// exists after the database has already been asked.
	pushFlood tokenBucket

	// pushFloodByIP bounds each source separately, in front of pushFlood.
	// Without it the two share one budget, so a flood of made-up tokens from
	// one host spends the allowance that real jobs need — and a push monitor
	// whose report was refused goes overdue and alerts. That inverts a dead
	// man's switch into a generator of outages that are not happening.
	pushFloodByIP ipBuckets

	// trustedProxies lists the peers whose forwarding headers may be
	// believed. Empty by default: see clientIP.
	trustedProxies trustedproxy.Set

	// loginFlood bounds the public login route before any password is
	// hashed. The keyed limits in handleLogin cannot: an attacker who varies
	// the email and the address fills neither bucket, and every attempt past
	// them allocates 64 MiB inside argon2.
	loginFlood tokenBucket

	// previewChecks rate-limits POST /monitors/preview per user. A preview
	// has no monitor id to key on, so it cannot share the map above.
	//
	// In memory, and therefore per process and lost on restart. That is a
	// deliberate limit rather than an oversight: SubGlance is one binary with
	// an embedded SQLite database (ARCHITECTURE.md), and SQLite permits one
	// writer, so running two API processes against one database is not a
	// supported deployment. Moving the reservation into a table would buy
	// nothing today and would put a write on the path of every preview —
	// a write that exists only to refuse work.
	//
	// If SubGlance ever grows a multi-process Postgres deployment, this map
	// and manualChecks both have to move into shared storage at the same
	// time; neither survives horizontal scaling on its own.
	previewChecks cooldown

	// metrics supplies the operational counters for /metrics. Nil disables
	// that endpoint rather than crashing it, matching bus, prober and the
	// rest: an API assembled without a checker pipeline must still serve
	// everything else.
	metrics MetricsSource

	// streamPing overrides the SSE keepalive interval. Zero means the
	// default. It exists so a test can assert the ping behaviour in
	// milliseconds instead of sitting out twenty real seconds.
	streamPing time.Duration
}

// pingInterval is how often the live stream emits a `ping` event.
func (s *Server) pingInterval() time.Duration {
	if s.streamPing > 0 {
		return s.streamPing
	}
	return sseHeartbeatInterval
}

// New returns a Server ready to be mounted.
func New(log *slog.Logger, db *store.DB) *Server {
	return &Server{log: log, db: db, startedAt: time.Now()}
}

// WithBus attaches an event bus, enabling GET /api/v1/stream.
func (s *Server) WithBus(b *events.Bus) *Server {
	s.bus = b
	return s
}

// WithProber attaches a prober, enabling POST /api/v1/monitors/{id}/check.
func (s *Server) WithProber(p Prober) *Server {
	s.prober = p
	return s
}

// WithPushRecorder attaches a push recorder, enabling the push URL endpoint.
func (s *Server) WithPushRecorder(p PushRecorder) *Server {
	s.pusher = p
	return s
}

// WithTrustedProxies names the peers whose X-Forwarded-For and X-Real-Ip may
// be believed, as a comma-separated list of addresses and CIDR blocks.
//
// A malformed entry is an error rather than a silent skip: an operator who
// mistypes their proxy's subnet should be told at startup, not discover months
// later that every client behind it shared one rate-limit bucket.
func (s *Server) WithTrustedProxies(spec string) (*Server, error) {
	tp, err := trustedproxy.Parse(spec)
	if err != nil {
		return nil, err
	}
	s.trustedProxies = tp
	return s, nil
}

// access says what a route requires from its caller.
//
// The zero value is deliberately not "public": a route added without an
// explicit access level must fail closed, not silently expose itself.
type access int

const (
	// accessPublic is reachable without credentials — health, readiness,
	// setup and login.
	accessPublic access = iota + 1

	// accessRead is any authenticated user, including viewers.
	accessRead

	// accessWrite is editors and admins.
	accessWrite

	// accessAdmin is administrators only.
	accessAdmin
)

// String renders the access level for error messages and for the OpenAPI
// drift test.
func (a access) String() string {
	switch a {
	case accessPublic:
		return "public"
	case accessRead:
		return "read"
	case accessWrite:
		return "write"
	case accessAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// anyMethod is the Method of a route that answers every HTTP verb.
//
// Only the catch-all uses it. Registering the catch-all per method would leave
// the verbs nobody listed — a POST to a mistyped /api path, say — to net/http's
// own plain-text 404, which is exactly the HTML-to-a-JSON-client trap this
// route exists to avoid.
const anyMethod = ""

// route is one entry in the API surface.
type route struct {
	Method  string
	Pattern string // path only, e.g. "/api/v1/monitors/{id}"
	Access  access
}

// documented reports whether this route belongs in docs/openapi.yaml.
//
// Everything does, with one exception: the catch-all that serves the embedded
// dashboard. It belongs in the route table — the table is what the mux is
// built from, and a route registered outside it would escape the fail-closed
// access check — but docs/openapi.yaml describes an API for programs, and an
// entry saying "GET / returns an HTML page" would only add noise to every
// generated client. TestOpenAPIMatchesRoutes skips undocumented routes.
func (rt route) documented() bool { return rt.Pattern != webUIPattern }

// webUIPattern is the catch-all path that serves the dashboard. It is also
// where every request that matched no other pattern lands.
const webUIPattern = "/"

// routes is the single source of truth for the API surface.
//
// It is a table rather than a series of registration calls so the surface can
// be inspected programmatically: docs/openapi.yaml is checked against this
// list by TestOpenAPIMatchesRoutes, which makes it impossible for a route and
// its documentation to drift apart unnoticed.
//
// Groups are ordered by what they require:
//
//	public   — health, readiness, setup and login
//	read     — any authenticated user, including viewers
//	write    — editors and admins
//	admin    — administrators only
//
// Everything that is not explicitly public requires authentication. That
// default matters: forgetting to guard a new route should fail closed, not
// silently expose it.
func (s *Server) routes() []route {
	return []route{
		// Public.
		{http.MethodGet, "/health", accessPublic},
		{http.MethodGet, "/api/v1/health", accessPublic},
		{http.MethodGet, "/api/v1/ready", accessPublic},
		{http.MethodGet, "/api/v1/setup", accessPublic},
		{http.MethodPost, "/api/v1/setup", accessPublic},
		{http.MethodPost, "/api/v1/auth/login", accessPublic},

		// The push URL. Public by necessity, not by choice: a cron line
		// cannot hold a session, and handing a backup script an API token
		// would give it permission over every monitor in the instance. The
		// token in the path is the credential, and it authorises exactly
		// one thing on exactly one monitor.
		//
		// GET as well as POST because `curl URL` in a crontab is the whole
		// use case, and a great many wrappers people already have will only
		// issue a GET.
		{http.MethodGet, "/api/v1/push/{token}", accessPublic},
		{http.MethodPost, "/api/v1/push/{token}", accessPublic},

		// Authenticated: any role.
		//
		// /metrics is authenticated, unlike /health and /ready beside it,
		// and the difference is what each one discloses. A health probe
		// answers one bit about this process. A metrics endpoint publishes
		// how many monitors an instance watches, how much of its check
		// budget it is using and when its writes start failing — a fleet
		// inventory and a live map of when the operator is least able to
		// notice anything, to anyone who can reach the port.
		//
		// The counters carry no per-monitor labels, deliberately: names and
		// targets are the part an unauthenticated scrape would leak worst,
		// and per-monitor series would also make cardinality grow with the
		// monitor set. That is a reason to keep the surface small, not a
		// reason to leave it open.
		//
		// Authenticated costs a Prometheus operator one line
		// (`bearer_token`), which the API tokens already mint, and the
		// alternative — a public endpoint with an opt-in flag to close it —
		// is a default that fails open. Anything reachable without
		// credentials in this product is something that cannot work
		// otherwise: the login form, the setup screen, a push URL whose
		// token is the credential. A scrape is not in that set.
		{http.MethodGet, "/metrics", accessRead},

		{http.MethodGet, "/api/v1/auth/me", accessRead},
		{http.MethodGet, "/api/v1/watchdog", accessRead},
		{http.MethodPost, "/api/v1/auth/password", accessRead},

		// Logout is authenticated rather than public, which reads oddly for
		// an endpoint whose whole job is to discard a credential. It is the
		// CSRF check that makes the difference: that check lives inside
		// authenticate(), and a public route never calls it. Left public, any
		// page on the internet could end a visitor's session — an operator
		// thrown out of the dashboard mid-incident by a link they clicked.
		//
		// A logout with no session still answers 401 rather than 204. That is
		// a worse answer to a harmless request than the alternative is to a
		// hostile one.
		{http.MethodPost, "/api/v1/auth/logout", accessRead},

		{http.MethodGet, "/api/v1/maintenance", accessRead},
		{http.MethodGet, "/api/v1/monitors", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/heartbeats", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/uptime", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/latency", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/incidents", accessRead},
		{http.MethodGet, "/api/v1/incidents", accessRead},
		{http.MethodGet, "/api/v1/incidents/resolved", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/channels", accessRead},

		// Channel secrets are masked on read (see maskConfig), so a viewer may
		// see which targets exist without being handed the credentials to post
		// to them.
		{http.MethodGet, "/api/v1/channels", accessRead},
		{http.MethodGet, "/api/v1/channels/{id}", accessRead},

		// The live stream is a read: a viewer may watch, but watching is all it
		// does. It sits behind the same auth as everything else — an unguarded
		// stream would leak every monitor name and outage to anyone who can
		// reach the port.
		{http.MethodGet, "/api/v1/stream", accessRead},

		// Listing and revoking are scoped to the caller's own tokens, so a
		// viewer may always see and shred its own keys.
		{http.MethodGet, "/api/v1/tokens", accessRead},
		{http.MethodDelete, "/api/v1/tokens/{id}", accessRead},

		// Authenticated: editor or admin.
		//
		// Minting a token is a write even though the token itself may only
		// read. A token is a long-lived credential that outlives the session,
		// leaves the browser, and lands in CI logs and dotfiles; "may read in
		// the UI" is not the same permission as "may issue a permanent key
		// that works outside it". Viewers are the least-trusted role, so that
		// is exactly where the two should not be conflated.
		{http.MethodPost, "/api/v1/tokens", accessWrite},

		{http.MethodPost, "/api/v1/maintenance", accessWrite},
		{http.MethodDelete, "/api/v1/maintenance/{id}", accessWrite},
		{http.MethodPost, "/api/v1/monitors", accessWrite},
		{http.MethodPost, "/api/v1/monitors/preview", accessWrite},
		{http.MethodPost, "/api/v1/monitors/tags/preview", accessWrite},
		{http.MethodPost, "/api/v1/monitors/tags", accessWrite},
		{http.MethodPatch, "/api/v1/monitors/{id}", accessWrite},
		{http.MethodDelete, "/api/v1/monitors/{id}", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/check", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/pause", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/resume", accessWrite},
		{http.MethodPost, "/api/v1/incidents/{id}/ack", accessWrite},
		{http.MethodPut, "/api/v1/monitors/{id}/channels", accessWrite},

		{http.MethodPost, "/api/v1/channels", accessWrite},
		{http.MethodPut, "/api/v1/channels/{id}", accessWrite},
		{http.MethodDelete, "/api/v1/channels/{id}", accessWrite},

		// Quiet hours can only delay or, when chosen, drop what a channel
		// sends. That is a write: a viewer who could set them could silence
		// the phone everyone else relies on.
		{http.MethodPut, "/api/v1/channels/{id}/quiet-hours", accessWrite},
		{http.MethodDelete, "/api/v1/channels/{id}/quiet-hours", accessWrite},

		// Testing a channel sends a real message to a configured
		// destination, so it needs write access even though it changes
		// nothing here: a viewer who could trigger it could use the
		// instance to post into someone else's chat room.
		{http.MethodPost, "/api/v1/channels/{id}/test", accessWrite},
		{http.MethodPut, "/api/v1/channels/{id}/default", accessWrite},
		{http.MethodDelete, "/api/v1/channels/{id}/default", accessWrite},

		// Authenticated: admin only.
		{http.MethodGet, "/api/v1/users", accessAdmin},
		{http.MethodPost, "/api/v1/users", accessAdmin},
		{http.MethodDelete, "/api/v1/users/{id}", accessAdmin},

		// The embedded dashboard, and the catch-all for everything that
		// matched no pattern above.
		//
		// Public by necessity: this serves the login and first-run setup
		// screens, so requiring authentication would mean nobody could ever
		// reach the form that authenticates them. The shell itself carries no
		// data; every byte it displays comes from the guarded API above.
		{anyMethod, webUIPattern, accessPublic},
	}
}

// handlerFor maps a route to the handler that serves it.
//
// Keeping this separate from routes() is what lets the drift test walk the
// surface without constructing handlers, and it makes an unrouted entry a
// startup panic instead of a silent 404 in production.
func (s *Server) handlerFor(rt route) http.HandlerFunc {
	switch rt.Method + " " + rt.Pattern {
	case "GET /health", "GET /api/v1/health":
		return s.handleHealth
	case "GET /api/v1/ready":
		return s.handleReady
	case "GET /metrics":
		return s.handleMetrics
	case "GET /api/v1/setup":
		return s.handleSetupStatus
	case "POST /api/v1/setup":
		return s.handleSetup
	case "POST /api/v1/auth/login":
		return s.handleLogin
	case "POST /api/v1/auth/logout":
		return s.handleLogout
	case "GET /api/v1/push/{token}", "POST /api/v1/push/{token}":
		return s.handlePush

	case "GET /api/v1/auth/me":
		return s.handleMe
	case "GET /api/v1/watchdog":
		return s.handleWatchdog
	case "POST /api/v1/auth/password":
		return s.handleChangePassword

	case "GET /api/v1/maintenance":
		return s.handleListMaintenance
	case "POST /api/v1/maintenance":
		return s.handleCreateMaintenance
	case "DELETE /api/v1/maintenance/{id}":
		return s.handleDeleteMaintenance
	case "GET /api/v1/monitors":
		return s.handleListMonitors
	case "GET /api/v1/monitors/{id}":
		return s.handleGetMonitor
	case "GET /api/v1/monitors/{id}/heartbeats":
		return s.handleListHeartbeats
	case "GET /api/v1/monitors/{id}/uptime":
		return s.handleMonitorUptime
	case "GET /api/v1/monitors/{id}/latency":
		return s.handleMonitorLatency
	case "GET /api/v1/monitors/{id}/incidents":
		return s.handleListMonitorIncidents
	case "GET /api/v1/incidents":
		return s.handleListOpenIncidents
	case "GET /api/v1/incidents/resolved":
		return s.handleListResolvedIncidents
	case "GET /api/v1/monitors/{id}/channels":
		return s.handleListMonitorChannels

	case "GET /api/v1/channels":
		return s.handleListChannels
	case "GET /api/v1/channels/{id}":
		return s.handleGetChannel

	case "GET /api/v1/stream":
		return s.handleStream

	case "GET /api/v1/tokens":
		return s.handleListTokens
	case "POST /api/v1/tokens":
		return s.handleCreateToken
	case "DELETE /api/v1/tokens/{id}":
		return s.handleRevokeToken

	case "POST /api/v1/monitors":
		return s.handleCreateMonitor
	case "POST /api/v1/monitors/preview":
		return s.handlePreviewCheck
	case "POST /api/v1/monitors/tags/preview":
		return s.handlePreviewTags
	case "POST /api/v1/monitors/tags":
		return s.handleTransformTags
	case "PATCH /api/v1/monitors/{id}":
		return s.handlePatchMonitor
	case "DELETE /api/v1/monitors/{id}":
		return s.handleDeleteMonitor
	case "POST /api/v1/monitors/{id}/check":
		return s.handleCheckMonitor
	case "POST /api/v1/monitors/{id}/pause":
		return s.handlePauseMonitor
	case "POST /api/v1/monitors/{id}/resume":
		return s.handleResumeMonitor
	case "POST /api/v1/incidents/{id}/ack":
		return s.handleAckIncident
	case "PUT /api/v1/monitors/{id}/channels":
		return s.handleSetMonitorChannels

	case "POST /api/v1/channels":
		return s.handleCreateChannel
	case "PUT /api/v1/channels/{id}/quiet-hours":
		return s.handleSetQuietHours
	case "DELETE /api/v1/channels/{id}/quiet-hours":
		return s.handleClearQuietHours
	case "PUT /api/v1/channels/{id}":
		return s.handleUpdateChannel
	case "POST /api/v1/channels/{id}/test":
		return s.handleTestChannel
	case "DELETE /api/v1/channels/{id}":
		return s.handleDeleteChannel
	case "PUT /api/v1/channels/{id}/default":
		return s.handleSetDefaultChannel
	case "DELETE /api/v1/channels/{id}/default":
		return s.handleClearDefaultChannel

	case "GET /api/v1/users":
		return s.handleListUsers
	case "POST /api/v1/users":
		return s.handleCreateUser
	case "DELETE /api/v1/users/{id}":
		return s.handleDeleteUser

	case " " + webUIPattern:
		return s.handleWebUI
	}
	return nil
}

// Handler returns the root HTTP handler with all routes and middleware applied.
//
// It iterates routes() and wraps each handler in the guards its access level
// demands. An entry with no handler, or with an access level this function
// does not know, panics at construction time rather than being registered
// unguarded — the fail-closed default in executable form.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	requireWrite := s.requireRole(store.Role.CanWrite, "your role does not allow changes")
	requireAdmin := s.requireRole(store.Role.CanAdmin, "this action requires an administrator")

	for _, rt := range s.routes() {
		h := s.handlerFor(rt)
		if h == nil {
			panic("api: no handler for route " + rt.Method + " " + rt.Pattern)
		}

		// net/http wants "METHOD /path", or a bare path for any method.
		pattern := strings.TrimSpace(rt.Method + " " + rt.Pattern)
		switch rt.Access {
		case accessPublic:
			mux.Handle(pattern, h)
		case accessRead:
			mux.Handle(pattern, s.requireAuth(h))
		case accessWrite:
			mux.Handle(pattern, s.requireAuth(requireWrite(h)))
		case accessAdmin:
			mux.Handle(pattern, s.requireAuth(requireAdmin(h)))
		default:
			panic("api: unknown access level for route " + pattern)
		}
	}

	return s.withRecovery(s.withSecurityHeaders(s.withLogging(mux)))
}

// withSecurityHeaders sets defensive headers on every response.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		// The API returns only JSON, so the strictest possible policy is the
		// right default here. The dashboard needs a looser one and sets its
		// own in webui.Handler, which runs after this middleware and
		// therefore wins.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

// handleHealth reports whether SubGlance itself is alive.
//
// This is deliberately dependency-free: it must answer even when the database
// is unhappy, because an orchestrator uses it to decide whether to restart us.
// Dependency health belongs in a separate readiness endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: buildinfo.Short(),
		Uptime:  time.Since(s.startedAt).Truncate(time.Second).String(),
	})
}

type readyResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Error    string `json:"error,omitempty"`
}

// handleReady reports whether SubGlance can actually serve traffic, which
// unlike /health means checking its dependencies.
//
// The split matters operationally: /health answering 200 while /ready returns
// 503 tells an orchestrator "leave this process alone, but send it no traffic
// yet" — restarting it would not fix a database that is still coming up.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable", Database: "down", Error: "no database configured",
		})
		return
	}

	if err := s.db.Reader.PingContext(ctx); err != nil {
		s.log.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable", Database: "down", Error: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, readyResponse{Status: "ready", Database: "up"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written, so the response is beyond
		// saving; the connection will simply be cut short.
		return
	}
}

// statusRecorder captures the status code so the logging middleware can
// report it. http.ResponseWriter does not expose it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush forwards to the underlying writer so streaming responses still work.
//
// Wrapping a ResponseWriter silently drops any optional interface it
// implemented — here http.Flusher. Without this, the SSE endpoint cannot push
// anything to the client and correctly refuses to start, which looks like a
// bug in the stream but is really a bug in this wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.log.Debug("request",
			"method", r.Method,
			"path", redactPath(r.URL.Path),
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
		)
	})
}

// redactPath removes credentials that travel in the URL path.
//
// One route has that shape: a push URL, where the token IS the path. The rest
// of the code is careful never to store that token in the clear — only its
// hash — which made writing it to the log the single hole in otherwise
// deliberate handling. Debug logging is a documented, supported setting, so
// every push credential would land in stdout, journald and any log shipper.
//
// A monitor's target URL is not redacted and should not be: a target is
// something being watched, not something that proves the right to report.
func redactPath(path string) string {
	const pushPrefix = "/api/v1/push/"
	if strings.HasPrefix(path, pushPrefix) && len(path) > len(pushPrefix) {
		return pushPrefix + "{token}"
	}
	return path
}

// withRecovery keeps one panicking handler from taking down the whole process.
// A monitoring tool that dies because of a bad request is worse than useless.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in handler",
					"panic", v,
					"method", r.Method,
					"path", redactPath(r.URL.Path),
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
