package store

import (
	"errors"
	"testing"
)

func TestSetUserRoleChangesTheRole(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	if _, err := db.CreateUser(ctx, "admin@example.com", "correct-horse-battery-staple", RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	viewer, err := db.CreateUser(ctx, "viewer@example.com", "correct-horse-battery-staple", RoleViewer)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := db.SetUserRole(ctx, viewer.ID, RoleEditor)
	if err != nil {
		t.Fatalf("SetUserRole: %v", err)
	}
	if got.Role != RoleEditor {
		t.Errorf("returned role = %q, want editor", got.Role)
	}
	stored, err := db.GetUser(ctx, viewer.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.Role != RoleEditor {
		t.Errorf("stored role = %q, want editor", stored.Role)
	}
}

// Demoting or deleting the only administrator leaves an instance nobody can
// manage, so the store refuses both rather than trusting every caller to check.
func TestLastAdministratorIsKept(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	admin, err := db.CreateUser(ctx, "admin@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := db.SetUserRole(ctx, admin.ID, RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: err = %v, want ErrLastAdmin", err)
	}
	if err := db.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: err = %v, want ErrLastAdmin", err)
	}
	// Re-stating the role an administrator already has is not a demotion.
	if _, err := db.SetUserRole(ctx, admin.ID, RoleAdmin); err != nil {
		t.Errorf("keep admin as admin: %v", err)
	}

	stored, err := db.GetUser(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.Role != RoleAdmin {
		t.Errorf("role = %q after refused changes, want admin", stored.Role)
	}
}

// With two administrators, either may go, but not both: the second change is
// judged against the first one's result, not against a count read before it.
func TestOneOfTwoAdministratorsMayGo(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	first, err := db.CreateUser(ctx, "one@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	second, err := db.CreateUser(ctx, "two@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := db.SetUserRole(ctx, first.ID, RoleEditor); err != nil {
		t.Fatalf("demote one of two admins: %v", err)
	}
	if err := db.DeleteUser(ctx, second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete the remaining admin: err = %v, want ErrLastAdmin", err)
	}
}

func TestUnknownUserIsNotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	if _, err := db.SetUserRole(ctx, 4242, RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetUserRole: err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteUser(ctx, 4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteUser: err = %v, want ErrNotFound", err)
	}
	if _, err := db.SetUserRole(ctx, 4242, Role("owner")); err == nil {
		t.Error("SetUserRole accepted an unknown role")
	}
}
