package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func patchUser(t *testing.T, h http.Handler, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPatch, "/api/v1/users/"+itoa(id), body))
	return rec
}

func problemField(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Field string `json:"field"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return body.Field
}

func TestAdminChangesAnotherUsersRole(t *testing.T) {
	srv, db := testServerWithDB(t)
	viewer, err := db.CreateUser(t.Context(), "viewer@example.com", "correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := patchUser(t, authedHandler(srv), viewer.ID, `{"role":"editor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got userResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != "editor" || got.Email != "viewer@example.com" {
		t.Errorf("response = %+v, want the same account as editor", got)
	}
	stored, err := db.GetUser(t.Context(), viewer.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.Role != store.RoleEditor {
		t.Errorf("stored role = %q, want editor", stored.Role)
	}
}

// The role takes effect on the account's very next request, including through
// tokens it already holds: nothing about the old role is cached anywhere.
func TestNewRoleAppliesToExistingTokens(t *testing.T) {
	srv, db := testServerWithDB(t)
	editor, err := db.CreateUser(t.Context(), "editor@example.com", "correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), editor.ID, "ci", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	if rec := patchUser(t, authedHandler(srv), editor.ID, `{"role":"viewer"}`); rec.Code != http.StatusOK {
		t.Fatalf("demote: status = %d: %s", rec.Code, rec.Body.String())
	}

	req := jsonRequest(http.MethodPost, "/api/v1/monitors", `{"name":"x","type":"http","target":"https://example.com"}`)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("write with a demoted account's token: status = %d, want 403", rec.Code)
	}
}

func TestUpdateUserRefusals(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := authedHandler(srv)
	users, err := db.ListUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("seeded users = %v, %v", users, err)
	}
	self := users[0].ID
	other, err := db.CreateUser(t.Context(), "other@example.com", "correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// A second administrator, so refusing "own role" is proven to be about
	// the caller and not merely the last-administrator guard.
	if _, err := db.CreateUser(t.Context(), "backup-admin@example.com", "correct-horse-battery-staple", store.RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tests := []struct {
		name   string
		id     int64
		body   string
		status int
		field  string
	}{
		{"own role", self, `{"role":"viewer"}`, http.StatusBadRequest, "role"},
		{"unknown role", other.ID, `{"role":"owner"}`, http.StatusBadRequest, "role"},
		{"missing role", other.ID, `{}`, http.StatusBadRequest, "role"},
		{"unknown field", other.ID, `{"role":"viewer","email":"x@example.com"}`, http.StatusBadRequest, ""},
		{"unknown account", 4242, `{"role":"viewer"}`, http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := patchUser(t, h, tt.id, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.field != "" {
				if got := problemField(t, rec); got != tt.field {
					t.Errorf("field = %q, want %q", got, tt.field)
				}
			}
		})
	}

	for _, u := range []int64{self, other.ID} {
		stored, err := db.GetUser(t.Context(), u)
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}
		if want := map[int64]store.Role{self: store.RoleAdmin, other.ID: store.RoleViewer}[u]; stored.Role != want {
			t.Errorf("user %d role = %q after refused changes, want %q", u, stored.Role, want)
		}
	}
}

// A second administrator may demote the first, but never the last one standing.
func TestLastAdministratorCannotBeDemoted(t *testing.T) {
	srv, db := testServerWithDB(t)
	users, err := db.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	first := users[0].ID
	second, err := db.CreateUser(t.Context(), "second@example.com", "correct-horse-battery-staple", store.RoleAdmin)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	secondToken, _, err := db.CreateAPIToken(t.Context(), second.ID, "ci", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	asSecond := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+secondToken)
		srv.Handler().ServeHTTP(w, r)
	})

	if rec := patchUser(t, asSecond, first, `{"role":"editor"}`); rec.Code != http.StatusOK {
		t.Fatalf("demote the first admin: status = %d: %s", rec.Code, rec.Body.String())
	}
	// The first account is an editor now, so its token cannot administer; the
	// only remaining path to the last admin is the store guard, tested there.
	if rec := patchUser(t, authedHandler(srv), second.ID, `{"role":"viewer"}`); rec.Code != http.StatusForbidden {
		t.Errorf("demoted admin changing roles: status = %d, want 403", rec.Code)
	}
}
