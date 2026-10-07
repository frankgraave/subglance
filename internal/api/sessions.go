package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/store"
)

// sessionResponse is one browser session as its owner, or an administrator,
// sees it. The token and its hash are not in it: id is a separate random
// name, so a listing hands out nothing that authenticates.
type sessionResponse struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Browser    string    `json:"browser"`
	Platform   string    `json:"platform"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	// Current marks the session that made this request, so a client can
	// keep "sign out" away from the one the reader is using.
	Current bool `json:"current"`
}

// sessionToken returns the session cookie the request carries, or "" when
// it has none (a request made with an API token, for one).
func sessionToken(r *http.Request) string {
	if r.Header.Get("Authorization") != "" {
		// authenticate() resolves a bearer token before it looks at a
		// cookie, so a cookie sent beside one is not what authenticated
		// the request and must not be called the current session.
		return ""
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		cookie, err = r.Cookie(insecureSessionCookieName)
		if err != nil {
			return ""
		}
	}
	return cookie.Value
}

func toSessionResponses(sessions []store.Session, currentToken string) []sessionResponse {
	current := ""
	if currentToken != "" {
		current = auth.HashToken(currentToken)
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		browser, platform := describeUserAgent(s.UserAgent)
		out = append(out, sessionResponse{
			ID: s.ID, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
			Browser: browser, Platform: platform, UserAgent: s.UserAgent, IP: s.IP,
			Current: current != "" && s.TokenHash == current,
		})
	}
	return out
}

// handleListSessions returns the caller's own sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	sessions, err := s.db.ListSessions(r.Context(), user.ID)
	if err != nil {
		s.log.Error("list sessions", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"sessions": toSessionResponses(sessions, sessionToken(r))})
}

// handleEndSession signs one of the caller's own sessions out.
//
// Ending the session the request came from is allowed, and is a sign-out:
// the cookie is cleared as /auth/logout would clear it.
func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	id := r.PathValue("id")
	if !store.ValidSessionID(id) {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Read before deleting, so the answer can say whether this was the
	// caller's own cookie. The scope is the caller's sessions only: an id
	// from another account is "not found", which neither ends it nor
	// confirms that it exists.
	//
	// A failed read stops here rather than ending the session blind: the
	// session could be the caller's own, and ending it without clearing the
	// cookie would leave the browser holding a dead cookie after a 204.
	current := false
	if token := sessionToken(r); token != "" {
		sessions, err := s.db.ListSessions(r.Context(), user.ID)
		if err != nil {
			s.log.Error("list sessions", "user_id", user.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "could not end the session")
			return
		}
		hash := auth.HashToken(token)
		for _, sess := range sessions {
			if sess.ID == id && sess.TokenHash == hash {
				current = true
			}
		}
	}

	switch err := s.db.EndSession(r.Context(), user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "session not found")
		return
	case err != nil:
		s.log.Error("end session", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not end the session")
		return
	}

	if current {
		s.clearSessionCookie(w, r)
	}
	s.log.Info("session ended", "user_id", user.ID, "session_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleEndOtherSessions signs the caller out everywhere except here.
//
// "Here" is the session cookie the request carries. A request made with an
// API token has no session of its own, so for it every session is another.
func (s *Server) handleEndOtherSessions(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	ended, err := s.db.EndOtherSessions(r.Context(), user.ID, sessionToken(r))
	if err != nil {
		s.log.Error("end other sessions", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not end the other sessions")
		return
	}
	s.log.Info("other sessions ended", "user_id", user.ID, "ended", ended)
	writeJSON(w, http.StatusOK, map[string]any{"ended": ended})
}

// handleListUserSessions returns any account's sessions. Admin only.
func (s *Server) handleListUserSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if _, err := s.db.GetUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.log.Error("list sessions", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	sessions, err := s.db.ListSessions(r.Context(), id)
	if err != nil {
		s.log.Error("list sessions", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"sessions": toSessionResponses(sessions, sessionToken(r))})
}

// handleEndUserSessions signs an account out everywhere. Admin only.
//
// It leaves the account and its API tokens alone: this is for a lost laptop
// or a shared browser, where the person keeps their access and signs in
// again. Removing the account is what takes the access away.
func (s *Server) handleEndUserSessions(w http.ResponseWriter, r *http.Request) {
	caller, _ := UserFromContext(r.Context())
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	ended, err := s.db.EndUserSessions(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case err != nil:
		s.log.Error("end user sessions", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not end the sessions")
		return
	}
	s.log.Info("user sessions ended", "user_id", id, "ended", ended, "by", caller.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ended": ended})
}

// pathUserID reads {id} as an account id; pathID words its refusal for
// monitors.
func pathUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return 0, false
	}
	return id, true
}

// describeUserAgent names the browser and the platform a User-Agent header
// claims, for a person deciding which of their sessions is which.
//
// It reads the handful of tokens the common browsers send and nothing more.
// A header is whatever the client chose to send, so this is a label to
// recognise a session by, not evidence of anything; anything it does not
// recognise comes back empty and the raw header is shown instead.
func describeUserAgent(ua string) (browser, platform string) {
	has := func(s string) bool { return strings.Contains(ua, s) }
	switch {
	case has("Edg/"), has("EdgA/"), has("EdgiOS/"):
		browser = "Edge"
	case has("OPR/"), has("Opera"):
		browser = "Opera"
	case has("SamsungBrowser/"):
		browser = "Samsung Internet"
	case has("Firefox/"), has("FxiOS/"):
		browser = "Firefox"
	case has("CriOS/"), has("Chrome/"), has("Chromium/"):
		browser = "Chrome"
	case has("Safari/") && has("Version/"):
		browser = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		browser = "curl"
	}
	switch {
	case has("iPhone"):
		platform = "iPhone"
	case has("iPad"):
		platform = "iPad"
	case has("Android"):
		platform = "Android"
	case has("Windows"):
		platform = "Windows"
	case has("CrOS"):
		platform = "ChromeOS"
	case has("Macintosh"), has("Mac OS X"):
		platform = "macOS"
	case has("Linux"):
		platform = "Linux"
	}
	return browser, platform
}
