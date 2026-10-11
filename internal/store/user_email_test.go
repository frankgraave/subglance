package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateUserTrimsTheAddressAndRefusesOneInUse(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	u, err := db.CreateUser(ctx, "  ops@example.com ", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ops@example.com" {
		t.Errorf("returned email = %q, want it trimmed", u.Email)
	}
	if got, err := db.GetUserByEmail(ctx, "ops@example.com"); err != nil || got.ID != u.ID {
		t.Fatalf("GetUserByEmail(trimmed) = %+v, %v", got, err)
	}

	for _, again := range []string{"ops@example.com", "OPS@example.com", "\tops@example.com"} {
		if _, err := db.CreateUser(ctx, again, "correct-horse-battery-staple", RoleViewer); !errors.Is(err, ErrEmailInUse) {
			t.Errorf("CreateUser(%q) = %v, want ErrEmailInUse", again, err)
		}
	}
}

func TestCreateFirstUserTrimsTheAddress(t *testing.T) {
	db := openTestDB(t)
	u, err := db.CreateFirstUser(context.Background(), " owner@example.com\n", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "owner@example.com" {
		t.Errorf("returned email = %q, want it trimmed", u.Email)
	}
	if _, err := db.GetUserByEmail(context.Background(), "owner@example.com"); err != nil {
		t.Errorf("GetUserByEmail(trimmed): %v", err)
	}
}
