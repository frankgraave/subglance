package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/datalock"
	"github.com/frankgraave/subglance/internal/store"
)

const (
	resetOldPassword = "the-old-password-1"
	resetNewPassword = "a-new-password-of-mine"
)

// fakeSecrets answers the password prompts from a list and records them.
type fakeSecrets struct {
	interactive bool
	answers     []string
	prompts     []string
}

func (f *fakeSecrets) Interactive() bool { return f.interactive }

func (f *fakeSecrets) ReadSecret(prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if len(f.answers) == 0 {
		return "", errors.New("no answer left")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func typed(answers ...string) *fakeSecrets {
	return &fakeSecrets{interactive: true, answers: answers}
}

// lockedOut is a data directory holding the database of an instance whose
// owner has forgotten the password: two accounts, sessions on both, an API
// token, and a sign-in limit already tripped for the owner's address.
type lockedOut struct {
	dir          string
	owner, other store.User
	ownerSession string
	otherSession string
	ownerToken   string
}

func newLockedOut(t *testing.T) lockedOut {
	t.Helper()
	clearBackupEnv(t)
	dir := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "subglance.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var l lockedOut
	l.dir = dir
	if l.owner, err = db.CreateUser(ctx, "Owner@example.com", resetOldPassword, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if l.other, err = db.CreateUser(ctx, "colleague@example.com", resetOldPassword, store.RoleEditor); err != nil {
		t.Fatal(err)
	}
	if l.ownerSession, err = db.CreateSession(ctx, l.owner.ID, "browser", "2001:db8::1"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateSession(ctx, l.owner.ID, "phone", "2001:db8::2"); err != nil {
		t.Fatal(err)
	}
	if l.otherSession, err = db.CreateSession(ctx, l.other.ID, "browser", "2001:db8::3"); err != nil {
		t.Fatal(err)
	}
	if l.ownerToken, _, err = db.CreateAPIToken(ctx, l.owner.ID, "deploys", nil); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if err := db.RecordLoginAttempt(ctx, store.LoginAttemptEmailKey("owner@example.com")); err != nil {
			t.Fatal(err)
		}
		if err := db.RecordLoginAttempt(ctx, store.LoginAttemptEmailKey(l.other.Email)); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// open reopens the data directory's database to look at what the command did.
func (l lockedOut) open(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(l.dir, "subglance.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// args points the command at the owner's account in this directory, with an
// address nothing answers on. The address is written in lower case on
// purpose: the account was created as "Owner@", and the lookup ignores case.
func (l lockedOut) args(t *testing.T, extra ...string) []string {
	t.Helper()
	return append([]string{"--data-dir", l.dir, "--addr", freeAddr(t), "--email", "owner@example.com"}, extra...)
}

func passwordIs(t *testing.T, db *store.DB, email, password string) bool {
	t.Helper()
	u, err := db.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// The whole of the way back in: the new password works, the old one does
// not, every session of the account ends and its sign-in limit is lifted,
// while the other account, its session and its count are left as they were.
func TestResetPasswordLetsTheOwnerBackIn(t *testing.T) {
	l := newLockedOut(t)
	secrets := typed(resetNewPassword, resetNewPassword)
	var out bytes.Buffer
	if err := resetPassword(l.args(t), &out, secrets); err != nil {
		t.Fatalf("reset-password: %v", err)
	}

	db := l.open(t)
	ctx := context.Background()
	if !passwordIs(t, db, l.owner.Email, resetNewPassword) {
		t.Error("the new password does not sign in")
	}
	if passwordIs(t, db, l.owner.Email, resetOldPassword) {
		t.Error("the old password still signs in")
	}
	if _, err := db.LookupSession(ctx, l.ownerSession); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the owner's session survived the reset: err = %v", err)
	}
	n, err := db.CountRecentLoginAttempts(ctx, store.LoginAttemptEmailKey(l.owner.Email), time.Hour)
	if err != nil || n != 0 {
		t.Errorf("failed sign-ins left for the owner = %d (%v), want 0", n, err)
	}
	if _, err := db.LookupAPIToken(ctx, l.ownerToken); err != nil {
		t.Errorf("the owner's API token stopped working: %v", err)
	}

	if !passwordIs(t, db, l.other.Email, resetOldPassword) {
		t.Error("the other account's password changed")
	}
	if _, err := db.LookupSession(ctx, l.otherSession); err != nil {
		t.Errorf("the other account was signed out: %v", err)
	}
	if n, _ := db.CountRecentLoginAttempts(ctx, store.LoginAttemptEmailKey(l.other.Email), time.Hour); n != 10 {
		t.Errorf("the other account's failed sign-ins = %d, want 10 untouched", n)
	}

	if got := out.String(); !strings.Contains(got, "signed it out of 2 sessions") || strings.Contains(got, resetNewPassword) {
		t.Errorf("output = %q, want the two ended sessions named and no password", got)
	}
	if len(secrets.prompts) != 2 || !strings.Contains(secrets.prompts[0], l.owner.Email) {
		t.Errorf("prompts = %q, want two, the first naming the account", secrets.prompts)
	}

	// Done with the directory: a server can start on it straight after.
	lock, err := datalock.Acquire(l.dir)
	if err != nil {
		t.Fatalf("data directory still locked after the reset: %v", err)
	}
	_ = lock.Release()
}

// A running server holds the data directory, and --force does not change
// that: the refusal names the lock file and the password stays as it was.
func TestResetPasswordRefusesWhileAServerHoldsTheDataDir(t *testing.T) {
	l := newLockedOut(t)
	server, err := datalock.Acquire(l.dir)
	if err != nil {
		t.Fatal(err)
	}
	secrets := typed(resetNewPassword, resetNewPassword)
	err = resetPassword(l.args(t, "--force"), &bytes.Buffer{}, secrets)
	_ = server.Release()
	if err == nil || !strings.Contains(err.Error(), "stop it first") || !strings.Contains(err.Error(), datalock.Path(l.dir)) {
		t.Fatalf("err = %v, want a refusal naming the lock file", err)
	}
	if len(secrets.prompts) != 0 {
		t.Errorf("asked for a password it could not set: %q", secrets.prompts)
	}
	if !passwordIs(t, l.open(t), l.owner.Email, resetOldPassword) {
		t.Error("the password changed under a running server")
	}
}

// Something answering on --addr is the second guard, as for restore; --force
// skips that one only.
func TestResetPasswordRefusesWhileTheServerAnswers(t *testing.T) {
	l := newLockedOut(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	args := []string{"--data-dir", l.dir, "--addr", ln.Addr().String(), "--email", l.owner.Email}

	err = resetPassword(args, &bytes.Buffer{}, typed(resetNewPassword, resetNewPassword))
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal that offers --force", err)
	}
	if err := resetPassword(append(args, "--force"), &bytes.Buffer{}, typed(resetNewPassword, resetNewPassword)); err != nil {
		t.Fatalf("with --force: %v", err)
	}
	if !passwordIs(t, l.open(t), l.owner.Email, resetNewPassword) {
		t.Error("--force did not reset the password")
	}
}

// An unknown address is refused by itself: the accounts that do exist are
// not listed, and no password is asked for.
func TestResetPasswordDoesNotListTheAccounts(t *testing.T) {
	l := newLockedOut(t)
	secrets := typed(resetNewPassword, resetNewPassword)
	args := []string{"--data-dir", l.dir, "--addr", freeAddr(t), "--email", "nobody@example.com"}
	err := resetPassword(args, &bytes.Buffer{}, secrets)
	if err == nil || !strings.Contains(err.Error(), "nobody@example.com") {
		t.Fatalf("err = %v, want one naming the address given", err)
	}
	for _, existing := range []string{"owner@", "colleague@"} {
		if strings.Contains(strings.ToLower(err.Error()), existing) {
			t.Errorf("the refusal gives away an existing account: %v", err)
		}
	}
	if len(secrets.prompts) != 0 {
		t.Errorf("asked for a password for an account that does not exist: %q", secrets.prompts)
	}
}

// The rule the sign-in page applies, and the two entries must agree. A
// refusal changes nothing.
func TestResetPasswordRefusesWhatTheUIWouldRefuse(t *testing.T) {
	cases := map[string]struct {
		answers []string
		want    string
		prompts int
	}{
		"too short": {[]string{"short"}, "at least", 1},
		"too long":  {[]string{strings.Repeat("x", 1025)}, "at most", 1},
		"differ":    {[]string{resetNewPassword, resetNewPassword + "!"}, "differ", 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l := newLockedOut(t)
			secrets := typed(tc.answers...)
			err := resetPassword(l.args(t), &bytes.Buffer{}, secrets)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(secrets.prompts) != tc.prompts {
				t.Errorf("prompts = %d, want %d", len(secrets.prompts), tc.prompts)
			}
			db := l.open(t)
			if !passwordIs(t, db, l.owner.Email, resetOldPassword) {
				t.Error("a refused password replaced the old one")
			}
			if _, err := db.LookupSession(context.Background(), l.ownerSession); err != nil {
				t.Errorf("a refused reset ended the session: %v", err)
			}
		})
	}
}

// Without a terminal there is nobody to ask, so the command says how to get
// one or to use --generate, rather than reading a password from a pipe.
func TestResetPasswordNeedsATerminalOrGenerate(t *testing.T) {
	l := newLockedOut(t)
	secrets := &fakeSecrets{interactive: false, answers: []string{resetNewPassword, resetNewPassword}}
	err := resetPassword(l.args(t), &bytes.Buffer{}, secrets)
	if err == nil || !strings.Contains(err.Error(), "--generate") {
		t.Fatalf("err = %v, want one pointing at --generate", err)
	}
	if len(secrets.prompts) != 0 {
		t.Errorf("read a password from a non-terminal: %q", secrets.prompts)
	}
	if !passwordIs(t, l.open(t), l.owner.Email, resetOldPassword) {
		t.Error("the password changed without being entered")
	}
}

// --generate makes one up, sets it and shows it exactly once.
func TestResetPasswordGeneratesOne(t *testing.T) {
	l := newLockedOut(t)
	secrets := &fakeSecrets{interactive: false}
	var out bytes.Buffer
	if err := resetPassword(l.args(t, "--generate"), &out, secrets); err != nil {
		t.Fatalf("reset-password --generate: %v", err)
	}
	var shown string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "    ") {
			shown = strings.TrimSpace(line)
		}
	}
	if err := auth.ValidatePassword(shown); err != nil {
		t.Fatalf("output %q shows no usable password: %v", out.String(), err)
	}
	if n := strings.Count(out.String(), shown); n != 1 {
		t.Errorf("the password appears %d times in the output, want once", n)
	}
	if !passwordIs(t, l.open(t), l.owner.Email, shown) {
		t.Error("the password shown is not the one set")
	}
	if len(secrets.prompts) != 0 {
		t.Errorf("--generate still asked: %q", secrets.prompts)
	}
}

// A data directory without a database is the wrong directory. Saying so is
// more use than "no such account", and the command must not leave an empty
// database behind in it.
func TestResetPasswordNeedsTheServersDatabase(t *testing.T) {
	clearBackupEnv(t)
	dir := t.TempDir()
	err := resetPassword([]string{"--data-dir", dir, "--addr", freeAddr(t), "--email", "owner@example.com"},
		&bytes.Buffer{}, typed(resetNewPassword, resetNewPassword))
	if err == nil || !strings.Contains(err.Error(), "--data-dir") {
		t.Fatalf("err = %v, want one pointing at --data-dir", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subglance.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a database was created in the wrong directory: %v", err)
	}
}

// There is no way to hand the password over on the command line, where the
// shell history and the process list would keep it.
func TestResetPasswordTakesNoPasswordArgument(t *testing.T) {
	l := newLockedOut(t)
	for _, flag := range []string{"--password", "--new-password"} {
		err := resetPassword(l.args(t, flag, resetNewPassword), &bytes.Buffer{}, typed())
		if err == nil {
			t.Errorf("%s was accepted", flag)
		}
	}
	err := resetPassword([]string{"--data-dir", l.dir}, &bytes.Buffer{}, typed())
	if err == nil || !strings.Contains(err.Error(), "--email") {
		t.Errorf("without --email: err = %v, want one asking for it", err)
	}
}

func TestSplitResetPasswordFlags(t *testing.T) {
	own, rest := splitResetPasswordFlags([]string{
		"--email", "owner@example.com", "--data-dir", "/data", "-generate", "--force", "--log-level=debug",
	})
	if got := strings.Join(own, " "); got != "--email owner@example.com -generate --force" {
		t.Errorf("own = %q", got)
	}
	if got := strings.Join(rest, " "); got != "--data-dir /data --log-level=debug" {
		t.Errorf("rest = %q", got)
	}
}
