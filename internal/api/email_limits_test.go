package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A new account's address is held to store.MaxEmailLen characters and one
// line, and the sign-in route, which takes any address, stores and logs a
// bounded key for it. Before both rules, ten failed sign-ins with a
// 900,000-character address wrote 9 MB of identifiers to the database and
// each refused attempt after them logged the address in full.

var (
	// fullEmail is MaxEmailLen characters and twice as many bytes.
	fullEmail  = strings.Repeat("é", store.MaxEmailLen-len("@example.com")) + "@example.com"
	longEmail  = "a" + fullEmail
	emailLimit = "email must be 254 characters or fewer"
)

// credentialsBody is a JSON body of an address and a password. It is
// marshalled rather than formatted with %q, whose escapes are not JSON.
func credentialsBody(t *testing.T, email, password string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"email": email, "password": password})
	must(t, err)
	return string(b)
}

func TestFailedSignInsWithAHugeAddressStoreABoundedKey(t *testing.T) {
	srv, db := testServerWithDB(t)
	var logs bytes.Buffer
	srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	h := srv.Handler()
	huge := strings.Repeat("a", 900_000) + "@example.com"

	codes := make([]int, 0, maxLoginAttempts+2)
	for i := range maxLoginAttempts + 2 {
		// Each from its own address, so only the address's count can refuse.
		req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
			credentialsBody(t, huge, "wrong-password-entirely"))
		req.RemoteAddr = fmt.Sprintf("203.0.113.%d:40000", i+1)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if got := codes[len(codes)-1]; got != http.StatusTooManyRequests {
		t.Errorf("statuses %v: the address was not counted as one, last = %d, want 429", codes, got)
	}

	var longest int
	must(t, db.Reader.QueryRowContext(context.Background(),
		"SELECT coalesce(max(length(identifier)), 0) FROM login_attempts").Scan(&longest))
	if longest > 100 {
		t.Errorf("a failed sign-in stored an identifier of %d bytes, want at most 100", longest)
	}
	if strings.Contains(logs.String(), strings.Repeat("a", 1000)) {
		t.Errorf("the log holds the address it was given (%d bytes of log)", logs.Len())
	}
}

// An account saved before addresses were held to a length still signs in:
// the rule is for new accounts, and an address cannot be edited.
func TestAnAccountWithALongerAddressStillSignsIn(t *testing.T) {
	srv, db := testServerWithDB(t)
	address := strings.Repeat("x", 300) + "@example.com"
	_, err := db.CreateUser(context.Background(), address, strongPassword, store.RoleViewer)
	must(t, err)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		credentialsBody(t, address, strongPassword)))
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in with a %d-character address: status = %d, want 200: %s",
			len(address), rec.Code, rec.Body)
	}
}

func TestCreateUserHoldsTheAddressToOneDeliverableLine(t *testing.T) {
	cases := map[string]struct{ email, msg string }{
		"over the limit":   {longEmail, emailLimit},
		"line break":       {"ops@example.com\nX-Admin: yes", "email " + store.NoControlCharacters},
		"tab inside":       {"ops\t@example.com", "email " + store.NoControlCharacters},
		"null byte inside": {"ops\u0000@example.com", "email " + store.NoControlCharacters},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			before, err := db.ListUsers(context.Background())
			must(t, err)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/users",
				credentialsBody(t, tc.email, strongPassword))
			wantStatusProblem(t, rec.Code, rec.Body.String(), "email", tc.msg)
			after, err := db.ListUsers(context.Background())
			must(t, err)
			if len(after) != len(before) {
				t.Errorf("a refused create stored an account: %d accounts, want %d", len(after), len(before))
			}
		})
	}
}

func TestCreateUserTakesAnAddressOfExactlyTheLimit(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/users",
		credentialsBody(t, fullEmail, strongPassword))
	mustStatus(t, rec, http.StatusCreated, "create with a 254-character address")
	if _, err := db.GetUserByEmail(context.Background(), fullEmail); err != nil {
		t.Errorf("the account is not stored under its address: %v", err)
	}
}

func TestSetupHoldsTheAddressToOneDeliverableLine(t *testing.T) {
	cases := map[string]struct{ email, msg string }{
		"over the limit": {longEmail, emailLimit},
		"line break":     {"owner@example.com\nX", "email " + store.NoControlCharacters},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			db := openEmptyDB(t)
			srv := New(testLogger(), db)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup",
				credentialsBody(t, tc.email, strongPassword)))
			wantStatusProblem(t, rec.Code, rec.Body.String(), "email", tc.msg)
			if n, err := db.CountUsers(context.Background()); err != nil || n != 0 {
				t.Errorf("a refused setup left %d accounts (err %v)", n, err)
			}
		})
	}

	db := openEmptyDB(t)
	srv := New(testLogger(), db)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup",
		credentialsBody(t, fullEmail, strongPassword)))
	mustStatus(t, rec, http.StatusCreated, "setup with a 254-character address")
}

// TestOpenAPIEmailLimitMatchesTheServer keeps the limit on the two schemas
// that create an account and off the one sign-in takes, which looks up an
// address of any length.
func TestOpenAPIEmailLimitMatchesTheServer(t *testing.T) {
	spec := loadSpec(t)
	emailOf := func(name string) map[string]any {
		t.Helper()
		node, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no components.schemas.%s", name)
		}
		var s struct {
			Properties map[string]map[string]any `yaml:"properties"`
		}
		must(t, node.Decode(&s))
		return s.Properties["email"]
	}
	for _, name := range []string{"SetupRequest", "CreateUserRequest"} {
		if got := emailOf(name)["maxLength"]; got != store.MaxEmailLen {
			t.Errorf("%s.email maxLength = %v, want %d", name, got, store.MaxEmailLen)
		}
	}
	if got, ok := emailOf("Credentials")["maxLength"]; ok {
		t.Errorf("Credentials.email sets maxLength %v, which refuses an account saved before the limit", got)
	}
}
