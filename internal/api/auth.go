package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
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

// loginFloodRate and loginFloodBurst bound the public login route as a whole,
// before any password is hashed.
//
// The per-email and per-address limits below cannot do this: both are keyed,
// and a caller that varies the email and the address never fills either bucket.
// Every attempt that gets past them pays for an argon2 verification, so the
// keyed limits bound guessing while this one bounds the work.
//
// Ten a second bursting to thirty. A person logging in does so once; thirty in
// a burst covers a browser retrying and an operator fumbling a password, and
// is still far below what it takes to keep the hashing semaphore saturated.
const (
	loginFloodRate  = 10.0
	loginFloodBurst = 30.0
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
		// Checked after the credential, and answered 403 rather than 401:
		// the session is valid, and a 401 tells the dashboard it is gone.
		// Answered 401, a write refused here signed the user out, and they
		// signed in again only to be signed out by their next save, with
		// nothing on screen saying why.
		if cookieAuthenticated(r) && isStateChanging(r.Method) {
			if msg := s.crossOriginRefusal(r); msg != "" {
				writeError(w, http.StatusForbidden, msg)
				return
			}
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
	return user, true
}

// cookieAuthenticated reports whether authenticate resolved the request by
// its session cookie rather than a bearer token.
//
// Only that request needs the cross-origin check: a browser attaches the
// cookie to a request another site's page makes, and never a bearer token.
func cookieAuthenticated(r *http.Request) bool {
	return r.Header.Get("Authorization") == ""
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// crossOriginRefusal says why a cookie-authenticated write is refused as
// coming from another site's page, or "" when it is not.
//
// This is the Fetch-metadata check rather than a token: browsers send
// Sec-Fetch-Site and will not let a page forge it, which covers the attack
// without threading a token through every form. They send it only to an
// origin they consider trustworthy, though: HTTPS, or a loopback address.
// A dashboard opened over plain HTTP at a LAN address or a host name gets
// none (measured in Chromium), and neither do older browsers, so those
// requests are judged by Origin instead, and pass when it names this server.
// A request with neither header is not from a browser, and a cross-site
// attack needs a browser to carry the cookie.
func (s *Server) crossOriginRefusal(r *http.Request) string {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return ""
	case "cross-site", "same-site":
		return "cross-origin request refused: a change made with a session cookie " +
			"must come from SubGlance's own pages"
	}

	origin := r.Header.Get("Origin")
	if origin == "" || s.sameOrigin(origin, r) {
		return ""
	}
	return "cross-origin request refused: the page is at a different address " +
		"than the one this server was reached at. Behind a reverse proxy, set " +
		"--base-url to the address in the browser, or have the proxy pass the " +
		"Host header on"
}

// sameOrigin reports whether a request's Origin names this server.
//
// Two addresses count. The Host the request arrived with is the browser's
// own address whenever nothing rewrites it. A reverse proxy can: nginx sends
// its upstream address unless told to pass Host on, and Apache does unless
// ProxyPreserveHost is on. Behind one of those, the address in the browser
// is the one --base-url declares, so its origin counts as well. Go's
// http.CrossOriginProtection calls this a trusted origin.
func (s *Server) sameOrigin(origin string, r *http.Request) bool {
	if s.baseOrigin != "" && origin == s.baseOrigin {
		return true
	}
	host := r.Host
	for _, scheme := range []string{"https://", "http://"} {
		if origin == scheme+host {
			return true
		}
	}
	return false
}

// originOf is the origin a browser sends for a page under base, a URL in
// the form notifier.ParseBaseURL returns: scheme and host, without the
// path, the host in lower case, an IPv6 address written as a browser
// writes it and the scheme's own port dropped, which is how the Origin
// header is serialised.
func originOf(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + browserIPv6(host) + "]"
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host
}

// browserIPv6 writes an IPv6 address the way a browser writes it in an
// Origin, by the WHATWG URL standard's IPv6 serializer: eight pieces in
// lower-case hex without leading zeroes, the first longest run of two or
// more zero pieces cut to "::", and an IPv4-mapped address in hex like the
// rest. A --base-url of http://[2001:0db8::1] is then compared as the
// http://[2001:db8::1] the browser sends. netip's String follows RFC 5952
// instead, which keeps the dotted IPv4 tail, so it is not used. A host that
// is not an IPv6 address is returned unchanged.
func browserIPv6(host string) string {
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Is6() {
		return host
	}
	b := addr.As16()
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	compress, longest := -1, 1
	for i := 0; i < len(pieces); {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < len(pieces) && pieces[j] == 0 {
			j++
		}
		if j-i > longest {
			compress, longest = i, j-i
		}
		i = j
	}
	var out strings.Builder
	for i := 0; i < len(pieces); i++ {
		if i == compress {
			if i == 0 {
				out.WriteString("::")
			} else {
				out.WriteString(":")
			}
			i += longest - 1
			continue
		}
		out.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i < len(pieces)-1 {
			out.WriteString(":")
		}
	}
	return out.String()
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
//
// The early count below is a courtesy, not the guard: it turns a late caller
// into a 409 without making it pay for an argon2 hash first. The guard that
// actually holds is inside CreateFirstUser, because only the insert can see
// whether another request beat this one to it.
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
	if !readJSON(w, r, &req) {
		return
	}
	email := store.NormaliseEmail(req.Email)
	if p := emailProblem(email); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	if p := passwordProblem("password", req.Password); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}

	user, err := s.db.CreateFirstUser(ctx, email, req.Password, store.RoleAdmin)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrSetupComplete):
		writeError(w, http.StatusConflict, "setup has already been completed")
		return
	default:
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

	// Ahead of the body read, deliberately: see loginFloodRate. Every attempt
	// past this point costs an argon2 verification whether the account exists
	// or not, which is the whole reason a global ceiling is needed on top of
	// the keyed limits further down.
	if !s.loginFlood.allow(time.Now(), loginFloodRate, loginFloodBurst) {
		s.log.Warn("login attempts are arriving faster than the route accepts them",
			"ip", s.clientIP(r))
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests,
			"too many sign-in attempts are arriving at once; try again in a moment")
		return
	}

	var req loginRequest
	if !readJSON(w, r, &req) {
		return
	}

	// Rate limit on both the email and the client IP. Email alone lets an
	// attacker spray many addresses from one host; IP alone lets a botnet
	// grind a single account.
	ip := s.clientIP(r)
	for _, key := range []string{store.LoginAttemptEmailKey(req.Email), "ip:" + ip} {
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

	for _, key := range []string{store.LoginAttemptEmailKey(req.Email), "ip:" + ip} {
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
	for _, key := range []string{store.LoginAttemptEmailKey(email), "ip:" + ip} {
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
	if !readJSON(w, r, &req) {
		return
	}

	// Re-check the current password even though the caller is authenticated:
	// it is what stops an unattended browser from becoming a permanent
	// account takeover.
	//
	// The refusal names its field so a client can tell it from an expired
	// session, which is the same status with no field, without matching the
	// sentence.
	valid, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !valid {
		writeProblem(w, http.StatusUnauthorized,
			fieldProblem("current_password", "current password is incorrect"))
		return
	}
	if p := passwordProblem("new_password", req.NewPassword); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
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
	token, err := s.db.CreateSession(r.Context(), user.ID, r.UserAgent(), s.clientIP(r))
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
//
// Forwarding headers are honoured only when the peer that sent them is a
// configured trusted proxy. They are set by the client otherwise, and a header
// the client controls is not an identity: rotating X-Forwarded-For per request
// gave one host a fresh rate-limit bucket every time, which is credential
// spraying with the limiter switched off. Falling back to the peer address
// costs a shared bucket for everyone behind an unconfigured proxy — the safe
// direction to be wrong in, and fixed by naming the proxy in --trusted-proxies.
//
// Behind a proxy, the chain is read from the right rather than the left: see
// trustedproxy.Set.ClientAddr. The leftmost entry is the one the client wrote
// whenever the proxy appends to the header instead of replacing it.
func (s *Server) clientIP(r *http.Request) string {
	return s.trustedProxies.ClientAddr(peerIP(r),
		r.Header.Values("X-Forwarded-For"), r.Header.Get("X-Real-Ip"))
}

// peerIP is the address of the machine that actually opened the connection.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
