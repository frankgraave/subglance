package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

type createdToken struct {
	Token   string           `json:"token"`
	Details apiTokenResponse `json:"details"`
}

func mintToken(t *testing.T, h http.Handler, bearer, body string) (*httptest.ResponseRecorder, createdToken) {
	t.Helper()
	req := jsonRequest(http.MethodPost, "/api/v1/tokens", body)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out createdToken
	if rec.Code == http.StatusCreated {
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, out
}

func statusWith(h http.Handler, token, method, path, body string) int {
	req := jsonRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// An administrator can hand a dashboard a key that can only read.
func TestViewerScopedTokenCannotWriteOrAdminister(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	rec, created := mintToken(t, h, "", `{"name":"grafana","role":"viewer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if created.Details.Role != "viewer" {
		t.Errorf("details.role = %q, want viewer", created.Details.Role)
	}

	raw := srv.Handler()
	if got := statusWith(raw, created.Token, http.MethodGet, "/api/v1/monitors", ""); got != http.StatusOK {
		t.Errorf("read with a viewer token: status = %d, want 200", got)
	}
	if got := statusWith(raw, created.Token, http.MethodPost, "/api/v1/monitors",
		`{"name":"x","type":"http","target":"https://example.com"}`); got != http.StatusForbidden {
		t.Errorf("write with a viewer token: status = %d, want 403", got)
	}
	if got := statusWith(raw, created.Token, http.MethodGet, "/api/v1/users", ""); got != http.StatusForbidden {
		t.Errorf("admin route with a viewer token: status = %d, want 403", got)
	}
}

// A token is never more than its creator: an editor cannot mint an admin key.
func TestTokenCannotOutrankItsCreator(t *testing.T) {
	srv, db := testServerWithDB(t)
	editor := seedUser(t, srv, db, "editor@example.com", store.RoleEditor)

	rec, _ := mintToken(t, srv.Handler(), editor, `{"name":"escalate","role":"admin"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Field != "role" {
		t.Errorf("field = %q, want role", body.Field)
	}
}

// A scoped token that mints a token without naming a role hands on its own
// scope, not its owner's full role.
func TestTokenMintedByScopedTokenKeepsTheScope(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	_, scoped := mintToken(t, h, "", `{"name":"deploy","role":"editor"}`)
	if scoped.Token == "" {
		t.Fatal("no editor-scoped token was created")
	}
	rec, child := mintToken(t, srv.Handler(), scoped.Token, `{"name":"child"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if child.Details.Role != "editor" {
		t.Errorf("child role = %q, want editor", child.Details.Role)
	}
	if got := statusWith(srv.Handler(), child.Token, http.MethodGet, "/api/v1/users", ""); got != http.StatusForbidden {
		t.Errorf("admin route with the child token: status = %d, want 403", got)
	}
}

func TestTokenRoleValidation(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec, _ := mintToken(t, authedHandler(srv), "", `{"name":"x","role":"root"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// The list states what each token may do, and a request made with a
// viewer-scoped token does not relabel the owner's other tokens.
func TestTokenListReportsEachRole(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)
	_, viewer := mintToken(t, h, "", `{"name":"read","role":"viewer"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+viewer.Token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Tokens []apiTokenResponse `json:"tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	roles := map[string]string{}
	for _, tok := range list.Tokens {
		roles[tok.Name] = tok.Role
	}
	if roles["read"] != "viewer" {
		t.Errorf("read token role = %q, want viewer", roles["read"])
	}
	for name, role := range roles {
		if name != "read" && role != "admin" {
			t.Errorf("token %q listed as %q, want admin (the owner's role)", name, role)
		}
	}
}
