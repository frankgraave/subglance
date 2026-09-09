package store

import (
	"testing"
	"time"
)

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role     Role
		valid    bool
		canWrite bool
		canAdmin bool
	}{
		{RoleAdmin, true, true, true},
		{RoleEditor, true, true, false},
		{RoleViewer, true, false, false},
		{Role("superuser"), false, false, false},
		{Role(""), false, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.Valid(); got != tt.valid {
				t.Errorf("Valid() = %v, want %v", got, tt.valid)
			}
			if got := tt.role.CanWrite(); got != tt.canWrite {
				t.Errorf("CanWrite() = %v, want %v", got, tt.canWrite)
			}
			if got := tt.role.CanAdmin(); got != tt.canAdmin {
				t.Errorf("CanAdmin() = %v, want %v", got, tt.canAdmin)
			}
		})
	}
}

func TestCountUsers(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	// Zero is what the setup endpoint keys on, so it has to be right.
	n, err := db.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Fatalf("CountUsers = %d on a fresh database, want 0", n)
	}

	if _, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if n, _ = db.CountUsers(ctx); n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

func TestCreateAndGetUser(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	created, err := db.CreateUser(ctx, "frank@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == 0 {
		t.Error("no ID assigned")
	}
	if created.PasswordHash == "correct-horse-battery-staple" {
		t.Fatal("the password was stored in clear")
	}

	byEmail, err := db.GetUserByEmail(ctx, "frank@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != created.ID {
		t.Errorf("ID = %d, want %d", byEmail.ID, created.ID)
	}

	// Email matching must be case-insensitive, or a user who capitalises their
	// address on the login screen cannot get in.
	mixed, err := db.GetUserByEmail(ctx, "Frank@Example.COM")
	if err != nil {
		t.Fatalf("case-insensitive lookup failed: %v", err)
	}
	if mixed.ID != created.ID {
		t.Error("a differently-cased email resolved to another account")
	}

	byID, err := db.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if byID.Email != created.Email || byID.Role != RoleAdmin {
		t.Errorf("got %+v, want the created admin", byID)
	}
}

func TestGetUserNotFound(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.GetUser(t.Context(), 99999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetUserByEmail(t.Context(), "ghost@example.com"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateUserRejectsInvalidInput(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	if _, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", Role("god")); err == nil {
		t.Error("an unknown role was accepted")
	}
	if _, err := db.CreateUser(ctx, "a@example.com", "short", RoleAdmin); err == nil {
		t.Error("a password below the policy minimum was accepted")
	}

	if _, err := db.CreateUser(ctx, "dup@example.com", "correct-horse-battery-staple", RoleAdmin); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := db.CreateUser(ctx, "DUP@example.com", "correct-horse-battery-staple", RoleAdmin); err == nil {
		t.Error("the same email in different case was accepted twice")
	}
}

func TestListUsersOmitsPasswordHashes(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	for _, e := range []string{"a@example.com", "b@example.com"} {
		if _, err := db.CreateUser(ctx, e, "correct-horse-battery-staple", RoleViewer); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}

	users, err := db.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	for _, u := range users {
		if u.PasswordHash != "" {
			t.Errorf("ListUsers returned a password hash for %s", u.Email)
		}
	}
}

func TestUpdatePassword(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	old := user.PasswordHash

	if err := db.UpdatePassword(ctx, user.ID, "an-entirely-different-passphrase"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	updated, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if updated.PasswordHash == old {
		t.Error("the stored hash did not change")
	}

	if err := db.UpdatePassword(ctx, user.ID, "short"); err == nil {
		t.Error("UpdatePassword accepted a password below the policy minimum")
	}
}

func TestDeleteUserCascadesToSessionsAndTokens(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.CreateSession(ctx, user.ID, "curl", "127.0.0.1"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, _, err := db.CreateAPIToken(ctx, user.ID, "ci", nil); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if err := db.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// A deleted account whose credentials still worked would be a serious
	// problem: this is how an offboarded colleague keeps access.
	for _, table := range []string{"sessions", "api_tokens"} {
		var n int
		if err := db.Reader.QueryRowContext(ctx,
			"SELECT count(*) FROM "+table+" WHERE user_id = ?", user.ID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%d rows survived in %s after the user was deleted", n, table)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	token, err := db.CreateSession(ctx, user.ID, "Mozilla/5.0", "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// The plaintext must not be recoverable from the database.
	var stored string
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT token_hash FROM sessions WHERE user_id = ?", user.ID).Scan(&stored); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if stored == token {
		t.Fatal("the session token is stored in clear; a leaked backup would hand out live sessions")
	}

	got, err := db.LookupSession(ctx, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("resolved to user %d, want %d", got.ID, user.ID)
	}

	if err := db.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := db.LookupSession(ctx, token); err != ErrNotFound {
		t.Errorf("the session still resolves after deletion (err = %v)", err)
	}
}

func TestExpiredSessionIsRejectedAndPurged(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, err := db.CreateSession(ctx, user.ID, "curl", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Age it past its expiry.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE sessions SET expires_at = ? WHERE user_id = ?",
		time.Now().Add(-time.Hour).Unix(), user.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if _, err := db.LookupSession(ctx, token); err != ErrNotFound {
		t.Errorf("an expired session still authenticates (err = %v)", err)
	}

	var n int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM sessions WHERE user_id = ?", user.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the expired session was not cleaned up on lookup")
	}
}

func TestDeleteUserSessionsLogsOutEverywhere(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var tokens []string
	for range 3 {
		tok, err := db.CreateSession(ctx, user.ID, "curl", "127.0.0.1")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		tokens = append(tokens, tok)
	}

	if err := db.DeleteUserSessions(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	for i, tok := range tokens {
		if _, err := db.LookupSession(ctx, tok); err != ErrNotFound {
			t.Errorf("session %d still works after a global logout", i)
		}
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	live, err := db.CreateSession(ctx, user.ID, "curl", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.CreateSession(ctx, user.ID, "curl", "127.0.0.2"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE sessions SET expires_at = ? WHERE ip = ?",
		time.Now().Add(-time.Hour).Unix(), "127.0.0.2"); err != nil {
		t.Fatalf("expire: %v", err)
	}

	n, err := db.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d sessions, want 1", n)
	}
	if _, err := db.LookupSession(ctx, live); err != nil {
		t.Errorf("the live session was purged too: %v", err)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "bot@example.com", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	plaintext, meta, err := db.CreateAPIToken(ctx, user.ID, "ci", nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if meta.Prefix == "" || len(meta.Prefix) >= len(plaintext) {
		t.Errorf("prefix %q is not a short display prefix", meta.Prefix)
	}

	var stored string
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT token_hash FROM api_tokens WHERE id = ?", meta.ID).Scan(&stored); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if stored == plaintext {
		t.Fatal("the API token is stored in clear")
	}

	got, err := db.LookupAPIToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("LookupAPIToken: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("resolved to user %d, want %d", got.ID, user.ID)
	}

	// Using a token must record it, so an operator can spot ones nobody uses.
	tokens, err := db.ListAPITokens(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("got %d tokens, want 1", len(tokens))
	}
	if tokens[0].LastUsedAt == nil {
		t.Error("last_used_at was not recorded on use")
	}

	if err := db.RevokeAPIToken(ctx, meta.ID, user.ID); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
	if _, err := db.LookupAPIToken(ctx, plaintext); err != ErrNotFound {
		t.Errorf("a revoked token still resolves (err = %v)", err)
	}

	// Revoking is not deleting: the audit trail has to survive.
	tokens, _ = db.ListAPITokens(ctx, user.ID)
	if len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Error("the revoked token vanished from the list instead of being marked")
	}
}

func TestExpiredAPITokenIsRejected(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "bot@example.com", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	plaintext, _, err := db.CreateAPIToken(ctx, user.ID, "expired", &past)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if _, err := db.LookupAPIToken(ctx, plaintext); err != ErrNotFound {
		t.Errorf("an expired token still resolves (err = %v)", err)
	}
}

// A token id belonging to someone else must not be revocable by guessing the
// number.
func TestCannotRevokeAnotherUsersToken(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	owner, err := db.CreateUser(ctx, "owner@example.com", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	other, err := db.CreateUser(ctx, "other@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	plaintext, meta, err := db.CreateAPIToken(ctx, owner.ID, "mine", nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if err := db.RevokeAPIToken(ctx, meta.ID, other.ID); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound when revoking someone else's token", err)
	}
	if _, err := db.LookupAPIToken(ctx, plaintext); err != nil {
		t.Errorf("the owner's token stopped working: %v", err)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.LookupAPIToken(t.Context(), "sgp_this-token-never-existed"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := db.LookupSession(t.Context(), "no-such-session"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLoginAttemptTracking(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	const key = "email:target@example.com"

	n, err := db.CountRecentLoginAttempts(ctx, key, time.Hour)
	if err != nil {
		t.Fatalf("CountRecentLoginAttempts: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d on a fresh database, want 0", n)
	}

	for range 3 {
		if err := db.RecordLoginAttempt(ctx, key); err != nil {
			t.Fatalf("RecordLoginAttempt: %v", err)
		}
	}
	if n, _ = db.CountRecentLoginAttempts(ctx, key, time.Hour); n != 3 {
		t.Errorf("count = %d, want 3", n)
	}

	// Attempts outside the window must not count, or the limiter would lock an
	// account out permanently after a bad day months ago.
	if n, _ = db.CountRecentLoginAttempts(ctx, key, time.Nanosecond); n != 0 {
		t.Errorf("count = %d within a 1ns window, want 0", n)
	}

	// One identifier's failures must not affect another's.
	if n, _ = db.CountRecentLoginAttempts(ctx, "email:someone-else@example.com", time.Hour); n != 0 {
		t.Errorf("count = %d for an unrelated identifier, want 0", n)
	}

	if err := db.ClearLoginAttempts(ctx, key); err != nil {
		t.Fatalf("ClearLoginAttempts: %v", err)
	}
	if n, _ = db.CountRecentLoginAttempts(ctx, key, time.Hour); n != 0 {
		t.Errorf("count = %d after clearing, want 0", n)
	}
}

func TestPurgeOldLoginAttempts(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	if err := db.RecordLoginAttempt(ctx, "email:recent@example.com"); err != nil {
		t.Fatalf("RecordLoginAttempt: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO login_attempts (identifier, attempted_at) VALUES (?, ?)",
		"email:ancient@example.com", time.Now().Add(-48*time.Hour).Unix()); err != nil {
		t.Fatalf("insert old attempt: %v", err)
	}

	n, err := db.PurgeOldLoginAttempts(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PurgeOldLoginAttempts: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	if c, _ := db.CountRecentLoginAttempts(ctx, "email:recent@example.com", time.Hour); c != 1 {
		t.Error("the recent attempt was purged too")
	}
}
