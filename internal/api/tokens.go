package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

type apiTokenResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// toTokenResponse reports the role the token acts with today: its own,
// capped by the owner's current role, so a demoted owner's token does not
// claim a role it no longer gets.
func toTokenResponse(t store.APIToken, owner store.Role) apiTokenResponse {
	return apiTokenResponse{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix, Role: string(t.Role.Capped(owner)),
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
		LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

type createTokenRequest struct {
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in"` // Go duration, e.g. "720h"; empty means never
	// Role caps what the token may do. Empty means the creator's own role
	// (the one this request acts with), which is what every token did before
	// tokens had a role.
	Role string `json:"role"`
}

// handleListTokens returns the caller's API tokens.
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	tokens, err := s.db.ListAPITokens(r.Context(), user.ID)
	if err != nil {
		s.log.Error("list api tokens", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}

	// The owner's stored role, not the caller's: a request made with a
	// viewer-scoped token must not report the owner's other tokens as viewer.
	owner, err := s.db.GetUser(r.Context(), user.ID)
	if err != nil {
		s.log.Error("list api tokens", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}

	out := make([]apiTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toTokenResponse(t, owner.Role))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// handleCreateToken issues a new API token.
//
// The plaintext appears in this response and nowhere else, ever. Making that
// explicit in the payload is deliberate: a user who misses it and closes the
// tab must understand that the token is gone rather than assume it can be
// looked up later.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	var req createTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 100 {
		writeError(w, http.StatusBadRequest, "name must be 100 characters or fewer")
		return
	}

	// No role means the caller's own, stored explicitly. user.Role is already
	// capped when the caller is itself a token, so an editor-scoped token
	// cannot mint an unscoped one that inherits its owner's admin role.
	role := user.Role
	if req.Role != "" {
		role = store.Role(req.Role)
	}
	if !role.Valid() {
		writeProblem(w, http.StatusBadRequest, fieldProblem("role", "role must be admin, editor or viewer"))
		return
	}
	// A token is a copy of its creator's authority, never more of it: an
	// editor minting an admin token would be a way round requireRole.
	if !role.Within(user.Role) {
		writeProblem(w, http.StatusForbidden, fieldProblem("role", "a token cannot have a higher role than your own"))
		return
	}

	var expiresAt *time.Time
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "expires_in must be a duration such as 720h")
			return
		}
		if d <= 0 {
			writeError(w, http.StatusBadRequest, "expires_in must be positive")
			return
		}
		t := time.Now().Add(d)
		expiresAt = &t
	}

	plaintext, token, err := s.db.CreateScopedAPIToken(r.Context(), user.ID, req.Name, role, expiresAt)
	if err != nil {
		s.log.Error("create api token", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not create the token")
		return
	}

	s.log.Info("api token created", "user_id", user.ID, "token_id", token.ID, "name", token.Name,
		"role", string(token.Role.Capped(user.Role)))

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":   plaintext,
		"warning": "This is the only time the token is shown. Store it now; it cannot be recovered.",
		"details": toTokenResponse(token, user.Role),
	})
}

// handleRevokeToken disables a token.
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Scoped to the caller's own tokens, so an id from another account cannot
	// be revoked by guessing the number.
	err := s.db.RevokeAPIToken(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		s.log.Error("revoke api token", "token_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke the token")
		return
	}

	s.log.Info("api token revoked", "user_id", user.ID, "token_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleListUsers returns every account. Admin only.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		s.log.Error("list users", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list users")
		return
	}

	out := make([]userResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toUserResponse(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

type createUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// handleCreateUser adds an account. Admin only.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Email == "" || !containsAt(req.Email) {
		writeError(w, http.StatusBadRequest, "a valid email address is required")
		return
	}

	role := store.Role(req.Role)
	if req.Role == "" {
		role = store.RoleViewer // least privilege by default
	}
	if !role.Valid() {
		writeError(w, http.StatusBadRequest, "role must be admin, editor or viewer")
		return
	}

	user, err := s.db.CreateUser(r.Context(), req.Email, req.Password, role)
	if err != nil {
		// A duplicate email is the common case here and is the user's problem
		// to fix, not a server fault.
		writeError(w, http.StatusBadRequest, "could not create the account: "+err.Error())
		return
	}

	s.log.Info("user created", "user_id", user.ID, "email", user.Email, "role", user.Role)
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

// handleDeleteUser removes an account. Admin only.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	caller, _ := UserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Deleting yourself is how an instance ends up with no administrator and
	// no way back in.
	if id == caller.ID {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}

	// The last-administrator guard lives in the store statement itself, so
	// two administrators deleting each other at once cannot both succeed.
	switch err := s.db.DeleteUser(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusBadRequest, "cannot delete the last administrator")
		return
	case err != nil:
		s.log.Error("delete user", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the account")
		return
	}

	s.log.Info("user deleted", "user_id", id, "by", caller.ID)
	w.WriteHeader(http.StatusNoContent)
}

type updateUserRequest struct {
	Role *string `json:"role"`
}

// handleUpdateUser changes an account's role. Admin only.
//
// The role is the only field: an email address is how the person signs in,
// and a password is theirs to change, not an administrator's to know.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	caller, _ := UserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var req updateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Role == nil {
		writeProblem(w, http.StatusBadRequest, fieldProblem("role", "role is required"))
		return
	}
	role := store.Role(*req.Role)
	if !role.Valid() {
		writeProblem(w, http.StatusBadRequest, fieldProblem("role", "role must be admin, editor or viewer"))
		return
	}

	// Changing your own role can only ever lower it, and the administrator
	// doing that is the one person on the page who could undo it. Another
	// administrator can still do it for them.
	if id == caller.ID {
		writeProblem(w, http.StatusBadRequest, fieldProblem("role", "you cannot change your own role"))
		return
	}

	user, err := s.db.SetUserRole(r.Context(), id, role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeProblem(w, http.StatusBadRequest, fieldProblem("role", "cannot demote the last administrator"))
		return
	case err != nil:
		s.log.Error("set user role", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not change the role")
		return
	}

	s.log.Info("user role changed", "user_id", id, "role", user.Role, "by", caller.ID)
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

func containsAt(s string) bool {
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '@' {
			return true
		}
	}
	return false
}
