package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

// A reset that cannot be carried out changes nothing: a password the policy
// refuses leaves the hash, the sessions and the sign-in count as they were,
// and an unknown account is ErrNotFound.
func TestResetPasswordIsAllOrNothing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx, "owner@example.com", "the-old-password-1", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(ctx, u.ID, "browser", "2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordLoginAttempt(ctx, LoginAttemptEmailKey("OWNER@example.com")); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ResetPassword(ctx, u.ID, "short"); !errors.Is(err, auth.ErrPasswordTooWeak) {
		t.Fatalf("weak password: err = %v, want ErrPasswordTooWeak", err)
	}
	if _, err := db.ResetPassword(ctx, u.ID+100, "a-new-password-of-mine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown account: err = %v, want ErrNotFound", err)
	}

	got, err := db.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != u.PasswordHash {
		t.Error("a refused reset changed the password")
	}
	if _, err := db.LookupSession(ctx, session); err != nil {
		t.Errorf("a refused reset ended the session: %v", err)
	}
	if n, _ := db.CountRecentLoginAttempts(ctx, LoginAttemptEmailKey(u.Email), time.Hour); n != 1 {
		t.Errorf("failed sign-ins = %d after a refused reset, want 1", n)
	}

	ended, err := db.ResetPassword(ctx, u.ID, "a-new-password-of-mine")
	if err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Errorf("ended %d sessions, want 1", ended)
	}
	if n, _ := db.CountRecentLoginAttempts(ctx, LoginAttemptEmailKey(u.Email), time.Hour); n != 0 {
		t.Errorf("failed sign-ins = %d after the reset, want 0", n)
	}
}

// The sign-in limit counts an address in one case, whatever case it was
// typed in, so a reset clears the count however the owner typed it.
func TestLoginAttemptEmailKeyIgnoresCase(t *testing.T) {
	if LoginAttemptEmailKey("Owner@Example.com") != LoginAttemptEmailKey("owner@example.com") {
		t.Error("two spellings of one address are counted apart")
	}
}

// An address of any length is counted under a key of bounded size, one per
// address and whatever its case, and the reset of an account with a long
// address still clears that account's count.
func TestLoginAttemptEmailKeyBoundsALongAddress(t *testing.T) {
	long := strings.Repeat("a", 900_000) + "@Example.com"
	key := LoginAttemptEmailKey(long)
	if len(key) > 100 {
		t.Errorf("key of a %d-byte address is %d bytes, want at most 100", len(long), len(key))
	}
	if key != LoginAttemptEmailKey(strings.ToUpper(long)) {
		t.Error("two spellings of one long address are counted apart")
	}
	if key == LoginAttemptEmailKey("b"+long[1:]) {
		t.Error("two long addresses share one count")
	}
	short := strings.Repeat("a", MaxEmailLen-len("@example.com")) + "@example.com"
	if got := LoginAttemptEmailKey(short); got != "email:"+short {
		t.Errorf("an address of %d bytes is keyed %q, want it kept as typed", len(short), got)
	}

	db := openTestDB(t)
	ctx := context.Background()
	address := strings.Repeat("x", 300) + "@example.com"
	u, err := db.CreateUser(ctx, address, "the-old-password-1", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordLoginAttempt(ctx, LoginAttemptEmailKey(strings.ToUpper(address))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResetPassword(ctx, u.ID, "a-new-password-of-mine"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountRecentLoginAttempts(ctx, LoginAttemptEmailKey(address), time.Hour); n != 0 {
		t.Errorf("failed sign-ins = %d after the reset of a long address, want 0", n)
	}
}
