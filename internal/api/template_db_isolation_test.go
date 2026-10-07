package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The template only saves building the database; it must not make tests share
// one. If two servers ever pointed at the same file, a test would see another
// test's monitors and accounts, and its result would depend on run order.
func TestServersFromTheTemplateDoNotShareADatabase(t *testing.T) {
	firstSrv, first := testServerWithDB(t)
	secondSrv, second := testServerWithDB(t)
	ctx := context.Background()

	if _, err := first.CreateUser(ctx, "only-in-first@example.com", templatePassword, store.RoleViewer); err != nil {
		t.Fatalf("create user in the first database: %v", err)
	}

	n, err := second.CountUsers(ctx)
	if err != nil {
		t.Fatalf("count users in the second database: %v", err)
	}
	if n != 1 {
		t.Errorf("second database holds %d accounts, want only its own admin: the databases are shared", n)
	}
	if _, err := second.GetUserByEmail(ctx, "only-in-first@example.com"); err == nil {
		t.Error("an account created in the first database is visible in the second")
	}

	// Each copy carries a seeded admin whose token the server accepts, as
	// the per-test seeding did.
	for name, srv := range map[string]*Server{"first": firstSrv, "second": secondSrv} {
		rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors", "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s server: GET /api/v1/monitors with the seeded token = %d, want 200", name, rec.Code)
		}
	}
}

// openEmptyDB is the setup flow's starting point: a current schema and no
// account at all. Copying the seeded template by mistake would let the setup
// tests pass against a database where setup is already complete.
func TestEmptyTemplateHasNoAccounts(t *testing.T) {
	db := openEmptyDB(t)
	n, err := db.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("empty database holds %d accounts, want 0", n)
	}
}
