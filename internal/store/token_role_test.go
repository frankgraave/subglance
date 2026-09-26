package store

import (
	"testing"
)

func TestRoleCapped(t *testing.T) {
	tests := []struct {
		token, owner, want Role
	}{
		{"", RoleAdmin, RoleAdmin},
		{"", RoleViewer, RoleViewer},
		{RoleViewer, RoleAdmin, RoleViewer},
		{RoleEditor, RoleAdmin, RoleEditor},
		{RoleAdmin, RoleEditor, RoleEditor},
		{RoleAdmin, RoleViewer, RoleViewer},
		{RoleEditor, RoleEditor, RoleEditor},
	}
	for _, tt := range tests {
		if got := tt.token.Capped(tt.owner); got != tt.want {
			t.Errorf("%q capped by %q = %q, want %q", tt.token, tt.owner, got, tt.want)
		}
	}
}

// A token acts with the lower of its own role and its owner's current one,
// and a token without a role of its own keeps acting as its owner.
func TestLookupAPITokenCapsTheRole(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	admin, err := db.CreateUser(ctx, "admin@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	inherited, _, err := db.CreateAPIToken(ctx, admin.ID, "legacy", nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	viewer, meta, err := db.CreateScopedAPIToken(ctx, admin.ID, "dashboard", RoleViewer, nil)
	if err != nil {
		t.Fatalf("CreateScopedAPIToken: %v", err)
	}
	if meta.Role != RoleViewer {
		t.Errorf("created token role = %q, want viewer", meta.Role)
	}
	full, _, err := db.CreateScopedAPIToken(ctx, admin.ID, "ops", RoleAdmin, nil)
	if err != nil {
		t.Fatalf("CreateScopedAPIToken: %v", err)
	}

	for token, want := range map[string]Role{inherited: RoleAdmin, viewer: RoleViewer, full: RoleAdmin} {
		got, err := db.LookupAPIToken(ctx, token)
		if err != nil {
			t.Fatalf("LookupAPIToken: %v", err)
		}
		if got.Role != want {
			t.Errorf("token acts as %q, want %q", got.Role, want)
		}
	}

	// Demoting the owner demotes every token, including one minted as admin.
	if _, err := db.Writer.ExecContext(ctx, "UPDATE users SET role = 'editor' WHERE id = ?", admin.ID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	for token, want := range map[string]Role{inherited: RoleEditor, viewer: RoleViewer, full: RoleEditor} {
		got, err := db.LookupAPIToken(ctx, token)
		if err != nil {
			t.Fatalf("LookupAPIToken: %v", err)
		}
		if got.Role != want {
			t.Errorf("after demotion the token acts as %q, want %q", got.Role, want)
		}
	}

	tokens, err := db.ListAPITokens(ctx, admin.ID)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	stored := map[string]Role{}
	for _, tok := range tokens {
		stored[tok.Name] = tok.Role
	}
	if stored["legacy"] != "" || stored["dashboard"] != RoleViewer || stored["ops"] != RoleAdmin {
		t.Errorf("stored roles = %v, want legacy empty, dashboard viewer, ops admin", stored)
	}
}

func TestCreateScopedAPITokenRejectsUnknownRole(t *testing.T) {
	db := openTestDB(t)
	user, err := db.CreateUser(t.Context(), "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, _, err := db.CreateScopedAPIToken(t.Context(), user.ID, "x", Role("root"), nil); err == nil {
		t.Error("a token with an unknown role was created")
	}
}
