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
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func toTokenResponse(t store.APIToken) apiTokenResponse {
	return apiTokenResponse{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
		LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

type createTokenRequest struct {
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in"` // Go duration, e.g. "720h"; empty means never
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

	out := make([]apiTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toTokenResponse(t))
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

	plaintext, token, err := s.db.CreateAPIToken(r.Context(), user.ID, req.Name, expiresAt)
	if err != nil {
		s.log.Error("create api token", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not create the token")
		return
	}

	s.log.Info("api token created", "user_id", user.ID, "token_id", token.ID, "name", token.Name)

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":   plaintext,
		"warning": "This is the only time the token is shown. Store it now; it cannot be recovered.",
		"details": toTokenResponse(token),
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

	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not verify the account")
		return
	}

	admins := 0
	var target *store.User
	for i, u := range users {
		if u.Role == store.RoleAdmin {
			admins++
		}
		if u.ID == id {
			target = &users[i]
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role == store.RoleAdmin && admins <= 1 {
		writeError(w, http.StatusBadRequest, "cannot delete the last administrator")
		return
	}

	if err := s.db.DeleteUser(r.Context(), id); err != nil {
		s.log.Error("delete user", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the account")
		return
	}

	s.log.Info("user deleted", "user_id", id, "by", caller.ID)
	w.WriteHeader(http.StatusNoContent)
}

func containsAt(s string) bool {
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '@' {
			return true
		}
	}
	return false
}
