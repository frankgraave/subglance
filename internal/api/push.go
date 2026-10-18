package api

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

// PushRecorder accepts a report from a push monitor's job.
//
// The API depends on this interface rather than on the monitor package for the
// same reason it depends on Prober: so the handler can be tested without a
// scheduler behind it, and so a build with no checker pipeline degrades to a
// clear 503 instead of a nil dereference.
type PushRecorder interface {
	RecordPush(ctx context.Context, m store.Monitor, rep monitor.PushReport) error
}

// pushCooldown is the minimum spacing between accepted pings for one monitor.
//
// This is the only unauthenticated write endpoint in the product, so the
// question is not "is this convenient" but "what does an attacker who guesses
// nothing still get". With a valid token they get to write heartbeats, and
// without a limit they get to write them as fast as a loop can post — filling
// the heartbeat table and, through it, the disk.
//
// Two seconds. The shortest expected interval a push monitor may have is sixty
// seconds, so no honest job can ever hit this; a script with a retry loop that
// pings twice in a second is not reporting twice, it is reporting once badly.
const pushCooldown = 2 * time.Second

// maxPushMessageLen caps the note a job may attach to a failure.
//
// Long enough for a real error line, short enough that this endpoint cannot be
// used as free storage by anyone holding one token. The text is truncated
// rather than rejected: a job reporting its own failure should never have that
// report refused because the stack trace it pasted was too long.
const maxPushMessageLen = 500

// maxPushMessageLen is enforced in bytes, and the documented limit says bytes
// for that reason: a 500-character message of three-byte runes would otherwise
// be silently cut at a third of its length by a limit claiming to allow it.

// pushFloodRate and pushFloodBurst bound the whole public push route before any
// database work happens.
//
// The per-monitor cooldown above cannot do this job: it is keyed on a monitor
// that is only known after the token has been looked up, so a flood of made-up
// tokens reaches the reader pool at whatever rate the network allows and pushes
// real API queries out of it. This limit is therefore deliberately placed in
// front of the lookup.
//
// Fifty reports a second, bursting to a hundred. The shortest interval a push
// monitor may have is sixty seconds, so a five-thousand-monitor install with
// every job on the minimum interval still averages under ninety reports a
// minute — two orders of magnitude below this ceiling. It bounds an attacker
// without being reachable by an honest deployment.
const (
	pushFloodRate  = 50.0
	pushFloodBurst = 100.0
)

// pushIPFloodRate and pushIPFloodBurst bound one source, checked first.
//
// The global ceiling above turned out to be the wrong shape on its own: it is
// a single budget shared by everyone, so a host posting nonsense tokens could
// empty it and the next report from a real job — a different host, a valid
// token — was refused. A refused report is a push monitor going overdue, which
// is this product raising an outage that is not happening. A dead man's switch
// that can be made to lie by an outsider is worse than no switch.
//
// Five a second bursting to twenty, per source. An honest job pings on an
// interval measured in minutes, and even a host running hundreds of cron lines
// does not approach this; a flood does so in its first second and then spends
// only its own allowance.
const (
	pushIPFloodRate  = 5.0
	pushIPFloodBurst = 20.0
)

// handlePush records a report from a monitored job.
//
// This is the one route in the API that is public by necessity rather than by
// choice. A cron line, a backup script or a CI step cannot hold a session
// cookie and should not hold an API token — a token would let a compromised
// backup script rewrite every monitor in the instance. The push token is the
// narrowest possible credential instead: it identifies one monitor and can do
// exactly one thing to it.
//
// Three decisions deserve stating:
//
//  1. GET is accepted as well as POST. `curl` in a crontab is the entire use
//     case, and half of the shells and wrappers people already have will only
//     do a GET. Refusing it on the grounds that a report is a write would be
//     REST pedantry paid for by the user.
//
//  2. An unknown token is a 404 with no detail. Saying "no such monitor"
//     versus "wrong token for this monitor" would turn the endpoint into an
//     oracle for testing guesses, and there is nothing a legitimate caller can
//     do with the difference anyway.
//
//  3. A ping for a paused monitor is accepted and not recorded, exactly as a
//     manual check is. A paused monitor is one that was promised not to be
//     watched, and a heartbeat written into that gap would present an
//     unmonitored period as a monitored one.
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if s.pusher == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this instance runs without a checker, so push reports are unavailable")
		return
	}

	// Both limits run before the lookup, deliberately: see pushFloodRate.
	// Per source first, so that one host exhausting its own allowance cannot
	// spend the global budget that every other host still needs.
	now := time.Now()
	ip := s.clientIP(r)
	if !s.pushFloodByIP.allow(ip, now, pushIPFloodRate, pushIPFloodBurst) {
		s.log.Warn("push reports from one source are arriving faster than the route accepts them",
			"ip", ip)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests,
			"too many push reports are arriving from this address; try again in a moment")
		return
	}
	if !s.pushFlood.allow(now, pushFloodRate, pushFloodBurst) {
		s.log.Warn("push reports are arriving faster than the route accepts them",
			"remote", r.RemoteAddr)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests,
			"too many push reports are arriving at once; try again in a moment")
		return
	}

	token := r.PathValue("token")

	m, err := s.db.MonitorByPushToken(r.Context(), token)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, store.ErrNotPushMonitor):
		// Deliberately the same answer for both, and deliberately vague.
		writeError(w, http.StatusNotFound, "unknown push URL")
		return
	default:
		s.log.Error("push: resolve token", "error", err)
		writeError(w, http.StatusInternalServerError, "could not record the report")
		return
	}

	if wait, ok := s.pushReports.reserve(m.ID, time.Now(), pushCooldown); !ok {
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests,
			"this monitor was reported to moments ago; try again in a few seconds")
		return
	}

	rep, p := parsePushReport(r)
	if !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}

	if !m.Enabled {
		s.log.Info("push report for a paused monitor, not recorded",
			"monitor_id", m.ID, "monitor", m.Name)
		writeJSON(w, http.StatusOK, pushResponse{
			MonitorID:  m.ID,
			OK:         rep.OK,
			Recorded:   false,
			ReceivedAt: rep.At,
		})
		return
	}

	if err := s.pusher.RecordPush(r.Context(), m, rep); err != nil {
		s.log.Error("push: record", "monitor_id", m.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not record the report")
		return
	}

	s.log.Info("push report", "monitor_id", m.ID, "monitor", m.Name, "ok", rep.OK)
	writeJSON(w, http.StatusOK, pushResponse{
		MonitorID:  m.ID,
		OK:         rep.OK,
		Recorded:   true,
		ReceivedAt: rep.At,
	})
}

// pushResponse tells the script what was made of its ping.
//
// It is small on purpose. The caller is a shell one-liner whose output goes to
// a cron mail nobody reads; the useful thing is a 200, and `recorded` for the
// one case where a 200 does not mean what it looks like.
type pushResponse struct {
	MonitorID  int64     `json:"monitor_id"`
	OK         bool      `json:"ok"`
	Recorded   bool      `json:"recorded"`
	ReceivedAt time.Time `json:"received_at"`
}

// parsePushReport reads the optional status and message from the query string.
//
// Query parameters rather than a JSON body, because the caller is `curl` in a
// crontab and appending `?status=down` to a URL is something a shell can do
// without quoting gymnastics. A body is not read at all: accepting both would
// mean deciding which wins when they disagree.
func parsePushReport(r *http.Request) (monitor.PushReport, problem) {
	q := r.URL.Query()

	rep := monitor.PushReport{OK: true, At: time.Now()}

	switch status := q.Get("status"); status {
	case "", "up", "0":
		// "0" because a shell reports success as exit code 0, and
		// `?status=$?` is the obvious thing to write at the end of a
		// script. Refusing it would make the obvious thing wrong.
	case "down", "fail":
		rep.OK = false
	default:
		// A non-zero exit code is a failure, which makes `?status=$?` work
		// for the failing case too rather than only the happy one.
		if n, err := strconv.Atoi(status); err == nil && n != 0 {
			rep.OK = false
			break
		}
		return monitor.PushReport{}, fieldProblem("status",
			"status must be up, down, or a shell exit code")
	}

	rep.Message = pushMessage(q.Get("msg"))

	return rep, problem{}
}

// pushMessage makes a job's failure note safe to store: valid UTF-8 and at
// most maxPushMessageLen bytes.
//
// The query string is whatever bytes the job sent, and a job under a Latin-1
// locale, or one piping a tool's raw output into the URL, sends bytes that
// are not UTF-8. Each run of invalid bytes becomes one U+FFFD rather than
// being dropped: a dropped byte turns "/home/m\xfcller" into "/home/mller", a
// different path that reads as real, where the replacement character says a
// character stood there and could not be read.
//
// The replacement comes first and the cut second, so the cut only ever
// removes the tail. Searching for a valid prefix of the raw text instead cuts
// the note back to just before its first invalid byte, wherever that is.
//
// The cut is at maxPushMessageLen bytes, backing off to a rune boundary.
// Cutting mid-sequence would store invalid UTF-8 in a STRICT TEXT column and
// render as a replacement character in the one place the text exists to be
// read. Bytes rather than runes because the limit exists to bound what one
// token can write to disk, and a byte is what disk is measured in.
func pushMessage(raw string) string {
	msg := strings.ToValidUTF8(raw, string(utf8.RuneError))
	if len(msg) <= maxPushMessageLen {
		return msg
	}
	cut := maxPushMessageLen
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
}

// validatePushWindow checks the push fields of a create request.
//
// The interval is required for a push monitor and forbidden for anything else.
// Defaulting it would mean guessing how often someone's backup runs, and the
// consequence of guessing wrong is either a monitor that never alerts or one
// that alerts every night — both of which read as "the tool is broken" rather
// than "the setting was wrong".
func validatePushWindow(typ string, interval, grace *int) problem {
	if typ != store.TypePush {
		if interval != nil || grace != nil {
			return fieldProblem("push_interval_s",
				"push_interval_s and push_grace_s apply only to push monitors")
		}
		return problem{}
	}
	if interval == nil {
		return fieldProblem("push_interval_s",
			"a push monitor needs push_interval_s: how often the job is expected to report")
	}

	g := store.DefaultPushGraceS
	if grace != nil {
		g = *grace
	}
	return validatePushRange(*interval, g)
}

// validatePushRange applies the bounds the schema declares, so a bad window is
// a named field error rather than a CHECK constraint surfacing as a 500.
func validatePushRange(interval, grace int) problem {
	if interval < store.MinPushIntervalS || interval > store.MaxPushIntervalS {
		return fieldProblem("push_interval_s",
			"push_interval_s must be between "+strconv.Itoa(store.MinPushIntervalS)+
				" and "+strconv.Itoa(store.MaxPushIntervalS))
	}
	if grace < 0 || grace > store.MaxPushGraceS {
		return fieldProblem("push_grace_s",
			"push_grace_s must be between 0 and "+strconv.Itoa(store.MaxPushGraceS))
	}
	return problem{}
}

// validatePushTimezone checks a push monitor's zone against its interval.
//
// The zone moves a deadline to the same wall-clock time a whole number of days
// later, so it means nothing for an interval of hours: an hourly job does not
// keep a time of day. Refusing the pair, rather than ignoring the zone, keeps
// a setting from being saved that the watchdog would never read.
//
// Callers check it when push_timezone was sent, and a PATCH also when the
// interval moved. Sending the field at all for another type is refused, as
// push_grace_s is, even empty: it is a setting that type does not have.
func validatePushTimezone(typ string, interval int, zone string) problem {
	if typ != store.TypePush {
		return fieldProblem("push_timezone", "push_timezone applies only to push monitors")
	}
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return problem{}
	}
	if _, err := store.LoadPushTimezone(zone); err != nil {
		return fieldProblem("push_timezone", "push_timezone: "+err.Error())
	}
	if !store.PushIntervalIsWholeDays(interval) {
		return fieldProblem("push_timezone",
			"push_timezone needs push_interval_s in whole days (86400 for a daily job); "+
				"an interval of hours or minutes is counted in elapsed time")
	}
	return problem{}
}

// handleReplacePushToken issues a push monitor a new URL and stops the old one
// working, without touching the monitor's history.
//
// Deleting and recreating the monitor was the only way to get a new URL, and
// it cost every heartbeat, incident and uptime figure the monitor had. That
// price is what kept a leaked URL in service: someone who can reach it can
// keep a dead job green.
//
// The response is the create response's shape, push_url included, and like
// that one it is the only time the new URL is ever shown. The old token gets
// the same 404 as one that never existed (see handlePush): a separate answer
// would confirm to whoever holds it that it was once valid.
func (s *Server) handleReplacePushToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "monitor")
	if !ok {
		return
	}

	m, err := s.db.ReplacePushToken(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	case errors.Is(err, store.ErrNotPushMonitor):
		writeProblem(w, http.StatusBadRequest,
			fieldProblem("type", "only a push monitor has a push URL to replace"))
		return
	default:
		s.log.Error("replace push token", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not replace the push URL")
		return
	}

	var userID int64
	if user, ok := UserFromContext(r.Context()); ok {
		userID = user.ID
	}
	// The token stays out of the log, like everywhere else: the prefix is
	// what tells two push monitors apart.
	s.log.Info("push URL replaced", "monitor_id", m.ID, "monitor", m.Name,
		"prefix", m.PushTokenPrefix, "user_id", userID)

	resp := s.describeMonitor(r, m)
	resp.PushURL = s.pushURL(r, m.PushToken)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, resp)
}

// pushPath is where a push URL points under the address SubGlance is
// reached at. The route table registers the same path with {token}.
const pushPath = "/api/v1/push/"

// pushURL builds the absolute URL a job should ping.
//
// With --base-url set, the URL is built on it, like every link in an alert.
// The operator declared that address as the one SubGlance is reached at, and
// behind a reverse proxy it is the only source that is right: the request
// that creates the monitor arrives with the proxy's upstream Host (nginx
// sends 127.0.0.1:8080 unless told otherwise), often without
// X-Forwarded-Proto, and never with the path prefix the proxy serves
// SubGlance under. A URL built from that request names an address the job
// cannot reach, and it is shown once: the job's pings never arrive, and the
// push monitor alerts on a job that ran.
//
// Without --base-url the request is all there is, so the host comes from it
// and the scheme from the rule that decides the session cookie (isHTTPS):
// TLS, X-Forwarded-Proto, or an https Origin from the dashboard.
func (s *Server) pushURL(r *http.Request, token string) string {
	if s.baseURL != "" {
		return s.baseURL + pushPath + token
	}
	// Behind a TLS-terminating proxy the request arrives as plain HTTP, so
	// the headers are the only evidence of the original scheme. Only the
	// scheme is taken from them: the host is not, because trusting a
	// client-supplied X-Forwarded-Host for a secret URL would let anyone who
	// can reach the API mint a link pointing at their own server.
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + pushPath + token
}

// tokenBucket is a rate limit with no background goroutine: it refills lazily
// from the timestamp of the last call.
//
// A bucket rather than a fixed window, because a window lets a burst of twice
// the limit through across its boundary — and a burst is precisely the shape of
// the flood this guards against.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// allow takes one token if the bucket has one, refilling at rate per second up
// to burst.
func (b *tokenBucket) allow(now time.Time, rate, burst float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.last.IsZero() {
		b.tokens = burst
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(burst, b.tokens+elapsed*rate)
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ipBuckets is one token bucket per source address.
//
// Unbounded growth is the obvious worry, so the map is swept rather than kept:
// a bucket that has refilled to full carries no information — it is
// indistinguishable from an address never seen — and is dropped on the next
// sweep. That bounds the map by the number of addresses active within a sweep
// interval rather than by everything the process has ever been sent.
type ipBuckets struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	lastSweep time.Time
}

// ipBucketSweepInterval is how often idle buckets are dropped. Frequent enough
// that a scan from many forged sources cannot accumulate, rare enough that the
// sweep itself is not the hot path.
const ipBucketSweepInterval = time.Minute

func (m *ipBuckets) allow(ip string, now time.Time, rate, burst float64) bool {
	m.mu.Lock()
	if m.buckets == nil {
		m.buckets = make(map[string]*tokenBucket)
		m.lastSweep = now
	}
	if now.Sub(m.lastSweep) >= ipBucketSweepInterval {
		for k, b := range m.buckets {
			if full(b, now, rate, burst) {
				delete(m.buckets, k)
			}
		}
		m.lastSweep = now
	}
	b, ok := m.buckets[ip]
	if !ok {
		b = &tokenBucket{}
		m.buckets[ip] = b
	}
	m.mu.Unlock()

	return b.allow(now, rate, burst)
}

// full reports whether a bucket has refilled completely, and so remembers
// nothing worth keeping.
func full(b *tokenBucket, now time.Time, rate, burst float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.IsZero() {
		return true
	}
	return b.tokens+now.Sub(b.last).Seconds()*rate >= burst
}
