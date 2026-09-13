package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
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

	// Truncate on a rune boundary. Cutting mid-sequence would store invalid
	// UTF-8 in a STRICT TEXT column and render as a replacement character in
	// the one place the text exists to be read.
	msg := q.Get("msg")
	if len(msg) > maxPushMessageLen {
		msg = msg[:maxPushMessageLen]
		for len(msg) > 0 && !utf8.ValidString(msg) {
			msg = msg[:len(msg)-1]
		}
	}
	rep.Message = msg

	return rep, problem{}
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

// pushURL builds the absolute URL a job should ping.
//
// The host comes from the request rather than from configuration, because the
// only host that is certainly correct is the one the user just reached the API
// on. A configured base URL would be one more thing to get wrong behind a
// reverse proxy, and getting it wrong here produces a URL that silently never
// reports — the exact failure a dead man's switch exists to catch.
func pushURL(r *http.Request, token string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Behind a TLS-terminating proxy the request arrives as plain HTTP, so
	// the header the proxy sets is the only evidence of the original scheme.
	// Only the scheme is taken from it: the host is not, because trusting a
	// client-supplied Host for a secret URL would let anyone who can reach
	// the API mint a link pointing at their own server.
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/v1/push/" + token
}
