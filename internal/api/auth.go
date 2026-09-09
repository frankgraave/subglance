package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/store"
)

// sessionCookieName is the browser session cookie.
//
// The __Host- prefix is a browser-enforced guarantee: the cookie must be
// Secure, must have Path=/, and must have no Domain attribute. That last part
// is what matters — it stops a subdomain (or anything that manages to control
// one) from writing a cookie that our origin would then accept.
const sessionCookieName = "__Host-subglance_session"

// insecureSessionCookieName is used when TLS is absent.
//
// __Host- cookies require Secure, which browsers refuse over plain HTTP. A
// self-hoster running on a LAN without TLS must still be able to log in, so we
// fall back rather than break — but only then.
const insecureSessionCookieName = "subglance_session"

// Rate limiting for logins.
const (
	loginAttemptWindow = 15 * time.Minute
	maxLoginAttempts   = 10
)

type contextKey string

const userContextKey contextKey = "subglance.user"

// UserFromContext returns the authenticated user, if any.
func UserFromContext(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userContextKey).(store.User)
	return u, ok
}

// requireAuth rejects unauthenticated requests.
//
// Both credential types land on the same handlers with the same rights: the UI
// is a client of this API and gets no privileges a script cannot have
// (product principle 4).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

// requireRole rejects users whose role is insufficient.
func (s *Server) requireRole(check func(store.Role) bool, msg string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !check(user.Role) {
				writeError(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authenticate resolves a request's credentials to a user.
func (s *Server) authenticate(r *http.Request) (store.User, bool) {
	ctx := r.Context()

	// Bearer token first: machines are the more common caller on a busy
	// instance, and it avoids a cookie lookup for them.
	if header := r.Header.Get("Authorization"); header != "" {
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			return store.User{}, false
		}
		user, err := s.db.LookupAPIToken(ctx, strings.TrimSpace(token))
		if err != nil {
			return store.User{}, false
		}
		return user, true
	}

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		cookie, err = r.Cookie(insecureSessionCookieName)
		if err != nil {
			return store.User{}, false
		}
	}

	user, err := s.db.LookupSession(ctx, cookie.Value)
	if err != nil {
		return store.User{}, false
	}

	// Cookie-authenticated state changes need CSRF protection; a bearer token
	// does not, because a browser will not attach one on a cross-site request.
	if isStateChanging(r.Method) && !s.checkCSRF(r) {
		return store.User{}, false
	}
	return user, true
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// checkCSRF verifies a cookie-authenticated state-changing request.
//
// This is the Fetch-metadata check rather than a token: modern browsers send
// Sec-Fetch-Site on every request and will not let a page forge it, which
// covers the attack without threading a token through every form. Requests
// without the header (curl, older clients) must carry an Origin that matches,
// or be a bearer-token call that never reaches here.
func (s *Server) checkCSRF(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin and no Fetch metadata: not a browser. A cross-site attack
		// needs a browser to carry the cookie, so this is safe to allow.
		return true
	}
	return sameOrigin(origin, r)
}

func sameOrigin(origin string, r *http.Request) bool {
	host := r.Host
	for _, scheme := range []string{"https://", "http://"} {
		if origin == scheme+host {
			return true
		}
	}
	return false
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(u store.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, Role: string(u.Role), CreatedAt: u.CreatedAt}
}

// handleSetupStatus reports whether the instance still needs its first user.
//
// Unauthenticated by necessity: the UI has to know which screen to show before
// anyone can log in.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CountUsers(r.Context())
	if err != nil {
		s.log.Error("count users", "error", err)
		writeError(w, http.StatusInternalServerError, "could not determine setup state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setup_required": n == 0})
}

// handleSetup creates the first administrator.
//
// There is no default password to change and no seeded account: an instance
// exposed before setup has no credentials to guess. Once a user exists this
// endpoint is permanently closed.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	n, err := s.db.CountUsers(ctx)
	if err != nil {
		s.log.Error("count users", "error", err)
		writeError(w, http.StatusInternalServerError, "could not determine setup state")
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "setup has already been completed")
		return
	}

	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "a valid email address is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := s.db.CreateUser(ctx, req.Email, req.Password, store.RoleAdmin)
	if err != nil {
		s.log.Error("create first user", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create the account")
		return
	}

	s.log.Info("initial admin account created", "user_id", user.ID, "email", user.Email)

	if err := s.issueSession(w, r, user); err != nil {
		s.log.Error("create session", "error", err)
		writeError(w, http.StatusInternalServerError, "account created but sign-in failed")
		return
	}
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

// handleLogin exchanges credentials for a session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Rate limit on both the email and the client IP. Email alone lets an
	// attacker spray many addresses from one host; IP alone lets a botnet
	// grind a single account.
	ip := clientIP(r)
	for _, key := range []string{"email:" + strings.ToLower(req.Email), "ip:" + ip} {
		n, err := s.db.CountRecentLoginAttempts(ctx, key, loginAttemptWindow)
		if err != nil {
			s.log.Error("count login attempts", "error", err)
			continue // never let the limiter's own failure lock everyone out
		}
		if n >= maxLoginAttempts {
			s.log.Warn("login rate limit hit", "key", key, "attempts", n)
			w.Header().Set("Retry-After", "900")
			writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
			return
		}
	}

	user, err := s.db.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// Hash anyway so a missing account and a wrong password take the same
		// time. Otherwise the response time reveals which addresses exist.
		_, _ = auth.VerifyPassword(req.Password, dummyHash)
		s.recordFailedLogin(ctx, req.Email, ip)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	ok, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !ok {
		s.recordFailedLogin(ctx, req.Email, ip)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	for _, key := range []string{"email:" + strings.ToLower(req.Email), "ip:" + ip} {
		if err := s.db.ClearLoginAttempts(ctx, key); err != nil {
			s.log.Error("clear login attempts", "error", err)
		}
	}

	if err := s.issueSession(w, r, user); err != nil {
		s.log.Error("create session", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start a session")
		return
	}

	s.log.Info("user signed in", "user_id", user.ID, "email", user.Email, "ip", ip)
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// dummyHash is a valid argon2id hash of a random value, used to equalise the
// timing of a login for an address that does not exist.
const dummyHash = "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHR2YWx1ZTEyMw$Gk8fVzE2xJZ0qKQyR5vNhM3wL7pT9cX1bY4dA6eF8gI"

func (s *Server) recordFailedLogin(ctx context.Context, email, ip string) {
	for _, key := range []string{"email:" + strings.ToLower(email), "ip:" + ip} {
		if err := s.db.RecordLoginAttempt(ctx, key); err != nil {
			s.log.Error("record login attempt", "error", err)
		}
	}
}

// handleLogout ends the current session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		cookie, err = r.Cookie(insecureSessionCookieName)
	}
	if err == nil {
		if err := s.db.DeleteSession(r.Context(), cookie.Value); err != nil {
			s.log.Error("delete session", "error", err)
		}
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the authenticated user.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword updates the caller's own password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := UserFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Re-check the current password even though the caller is authenticated:
	// it is what stops an unattended browser from becoming a permanent
	// account takeover.
	valid, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !valid {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.db.UpdatePassword(ctx, user.ID, req.NewPassword); err != nil {
		s.log.Error("update password", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not change the password")
		return
	}

	// A password change must invalidate every other session — that is the
	// whole point of changing it after a suspected compromise.
	if err := s.db.DeleteUserSessions(ctx, user.ID); err != nil {
		s.log.Error("delete sessions after password change", "user_id", user.ID, "error", err)
	}
	if err := s.issueSession(w, r, user); err != nil {
		s.log.Error("re-issue session", "error", err)
	}

	s.log.Info("password changed", "user_id", user.ID)
	w.WriteHeader(http.StatusNoContent)
}

// issueSession creates a session and sets the cookie.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, user store.User) error {
	token, err := s.db.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		return err
	}

	secure := isHTTPS(r)
	name := sessionCookieName
	if !secure {
		name = insecureSessionCookieName
	}

	// gosec flags Secure being a variable rather than a literal true. It is
	// deliberate: browsers refuse Secure cookies over plain HTTP, and a
	// self-hoster running on a LAN without TLS must still be able to log in.
	// Over HTTPS this is true and the cookie also gains the __Host- prefix.
	//nolint:gosec // G124: Secure is conditional on TLS by design, see above
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		HttpOnly: true, // JavaScript must never see this; XSS should not be session theft
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(store.SessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	name := sessionCookieName
	if !secure {
		name = insecureSessionCookieName
	}
	//nolint:gosec // G124: mirrors issueSession; Secure is conditional on TLS by design
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// isHTTPS reports whether the original request used TLS.
//
// X-Forwarded-Proto is honoured because self-hosted instances almost always
// sit behind a reverse proxy that terminates TLS.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP extracts the caller's address for rate limiting and audit logs.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	// A bounded reader keeps a hostile client from making us allocate its way
	// to an out-of-memory kill.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}
