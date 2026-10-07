package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

func TestSessionIDSaysNothingAboutTheToken(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, err := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleViewer)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	seen := map[string]bool{}
	for range 20 {
		token, err := db.CreateSession(ctx, user.ID, "curl/8", "2001:db8::1")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		sessions, err := db.ListSessions(ctx, user.ID)
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		var id string
		for _, s := range sessions {
			if s.TokenHash == auth.HashToken(token) {
				id = s.ID
			}
		}
		if !ValidSessionID(id) {
			t.Fatalf("session id %q is not 32 lower-case hex characters", id)
		}
		if seen[id] {
			t.Fatalf("session id %q was handed out twice", id)
		}
		seen[id] = true

		// The id is shown and sent in URLs; the token and its hash never
		// are. None of them may be computed from the other: no shared
		// stretch, and not a hash of the token either.
		hash := auth.HashToken(token)
		for _, secret := range []string{token, hash} {
			if strings.Contains(secret, id) || strings.Contains(id, secret) {
				t.Fatalf("id %q is part of a credential", id)
			}
			if strings.Contains(secret, id[:8]) {
				t.Fatalf("id %q shares its first eight characters with a credential", id)
			}
		}
		if id == auth.HashToken(hash) || id == hash[:32] {
			t.Fatalf("id %q is derived from the token hash", id)
		}
	}
}

func TestListSessionsIsPerUserAndSkipsExpired(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	alice, err := db.CreateUser(ctx, "alice@example.com", "correct-horse-battery-staple", RoleEditor)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	bob, err := db.CreateUser(ctx, "bob@example.com", "correct-horse-battery-staple", RoleViewer)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.CreateSession(ctx, alice.ID, "Mozilla/5.0 Firefox/131.0", "2001:db8::a"); err != nil {
		t.Fatal(err)
	}
	stale, err := db.CreateSession(ctx, alice.ID, "curl/8", "2001:db8::b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSession(ctx, bob.ID, "curl/8", "2001:db8::c"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.ExecContext(ctx, "UPDATE sessions SET expires_at = ? WHERE token_hash = ?",
		time.Now().Add(-time.Minute).Unix(), auth.HashToken(stale)); err != nil {
		t.Fatal(err)
	}

	got, err := db.ListSessions(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want alice's one live session: %+v", len(got), got)
	}
	s := got[0]
	if s.UserID != alice.ID || s.IP != "2001:db8::a" || s.UserAgent != "Mozilla/5.0 Firefox/131.0" {
		t.Errorf("session = %+v, want alice's Firefox session", s)
	}
	if s.CreatedAt.IsZero() || s.LastSeenAt.IsZero() || !s.ExpiresAt.After(time.Now()) {
		t.Errorf("times not read back: %+v", s)
	}
}

func TestEndSessionOnlyEndsTheOwnersSession(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	alice, _ := db.CreateUser(ctx, "alice@example.com", "correct-horse-battery-staple", RoleEditor)
	bob, _ := db.CreateUser(ctx, "bob@example.com", "correct-horse-battery-staple", RoleViewer)
	bobToken, err := db.CreateSession(ctx, bob.ID, "curl/8", "2001:db8::c")
	if err != nil {
		t.Fatal(err)
	}
	bobSessions, err := db.ListSessions(ctx, bob.ID)
	if err != nil || len(bobSessions) != 1 {
		t.Fatalf("ListSessions(bob) = %v, %v", bobSessions, err)
	}
	id := bobSessions[0].ID

	// Alice knows the id, but it is not hers.
	if err := db.EndSession(ctx, alice.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("EndSession with another user's id: err = %v, want ErrNotFound", err)
	}
	if _, err := db.LookupSession(ctx, bobToken); err != nil {
		t.Fatalf("bob's session ended through alice: %v", err)
	}

	if err := db.EndSession(ctx, bob.ID, id); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, err := db.LookupSession(ctx, bobToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("an ended session still authenticates (err = %v)", err)
	}
	if err := db.EndSession(ctx, bob.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("ending it twice: err = %v, want ErrNotFound", err)
	}
}

func TestEndOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, _ := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleAdmin)
	other, _ := db.CreateUser(ctx, "b@example.com", "correct-horse-battery-staple", RoleAdmin)
	var tokens []string
	for range 3 {
		tok, err := db.CreateSession(ctx, user.ID, "curl/8", "2001:db8::1")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, tok)
	}
	otherTok, err := db.CreateSession(ctx, other.ID, "curl/8", "2001:db8::2")
	if err != nil {
		t.Fatal(err)
	}

	n, err := db.EndOtherSessions(ctx, user.ID, tokens[1])
	if err != nil {
		t.Fatalf("EndOtherSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("ended %d sessions, want 2", n)
	}
	if _, err := db.LookupSession(ctx, tokens[1]); err != nil {
		t.Errorf("the kept session was ended: %v", err)
	}
	for _, i := range []int{0, 2} {
		if _, err := db.LookupSession(ctx, tokens[i]); !errors.Is(err, ErrNotFound) {
			t.Errorf("session %d survived (err = %v)", i, err)
		}
	}
	if _, err := db.LookupSession(ctx, otherTok); err != nil {
		t.Errorf("another account's session was ended: %v", err)
	}

	// No session to keep: every one goes.
	if n, err := db.EndOtherSessions(ctx, user.ID, ""); err != nil || n != 1 {
		t.Errorf("EndOtherSessions with nothing to keep = %d, %v; want 1, nil", n, err)
	}
}

func TestEndUserSessions(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	user, _ := db.CreateUser(ctx, "a@example.com", "correct-horse-battery-staple", RoleViewer)
	for range 2 {
		if _, err := db.CreateSession(ctx, user.ID, "curl/8", "2001:db8::1"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.EndUserSessions(ctx, user.ID); err != nil || n != 2 {
		t.Errorf("EndUserSessions = %d, %v; want 2, nil", n, err)
	}
	if n, err := db.EndUserSessions(ctx, user.ID); err != nil || n != 0 {
		t.Errorf("EndUserSessions again = %d, %v; want 0, nil", n, err)
	}
	if _, err := db.EndUserSessions(ctx, user.ID+100); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}
}

func TestValidSessionID(t *testing.T) {
	for id, want := range map[string]bool{
		strings.Repeat("a1", 16):        true,
		strings.Repeat("A1", 16):        false,
		strings.Repeat("a", 31):         false,
		strings.Repeat("a", 33):         false,
		"":                              false,
		strings.Repeat("g", 32):         false,
		"../" + strings.Repeat("a", 29): false,
	} {
		if got := ValidSessionID(id); got != want {
			t.Errorf("ValidSessionID(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestSessionIDMigrationNamesExistingSessions covers 0028: a session signed
// in before the upgrade must get its own id, or it could not be listed or
// ended, and two of them must not share one, or ending one would end both.
func TestSessionIDMigrationNamesExistingSessions(t *testing.T) {
	ctx := context.Background()
	db := openUnmigratedDB(t)

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if err := db.prepareMigrationTable(ctx); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	var rest []migration
	for _, m := range all {
		if m.name >= "0028" {
			rest = append(rest, m)
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}
	if len(rest) == 0 {
		t.Fatal("no 0028 migration found")
	}

	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx, `INSERT INTO users (email, password_hash, role, created_at, updated_at)
		VALUES ('a@example.com', 'x', 'admin', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	for _, hash := range []string{"h1", "h2", "h3"} {
		if _, err := db.Writer.ExecContext(ctx, `INSERT INTO sessions
			(token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
			VALUES (?, ?, ?, ?, ?, 'curl/8', '2001:db8::1')`, hash, userID, now, now+3600, now); err != nil {
			t.Fatalf("insert session: %v", err)
		}
	}

	for _, m := range rest {
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}

	sessions, err := db.ListSessions(ctx, userID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions after the migration, want 3", len(sessions))
	}
	seen := map[string]bool{}
	for _, s := range sessions {
		if !ValidSessionID(s.ID) {
			t.Errorf("session %s got id %q, want 32 hex characters", s.TokenHash, s.ID)
		}
		if seen[s.ID] {
			t.Errorf("two existing sessions share id %q", s.ID)
		}
		seen[s.ID] = true
	}
}
