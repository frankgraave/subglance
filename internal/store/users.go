package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

// ErrNotFound is returned when a lookup finds nothing.
var ErrNotFound = errors.New("store: not found")

// Role determines what a user may do.
type Role string

const (
	RoleAdmin  Role = "admin"  // full control, including users and tokens
	RoleEditor Role = "editor" // create and edit monitors, acknowledge incidents
	RoleViewer Role = "viewer" // read-only
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleEditor, RoleViewer:
		return true
	}
	return false
}

// CanWrite reports whether the role may change monitors or acknowledge
// incidents.
func (r Role) CanWrite() bool { return r == RoleAdmin || r == RoleEditor }

// CanAdmin reports whether the role may manage users and tokens.
func (r Role) CanAdmin() bool { return r == RoleAdmin }

// User is an account.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CountUsers reports how many accounts exist.
//
// Zero means the instance has not been set up yet, which is what the setup
// endpoint keys on.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateUser adds an account. The password is hashed here, never by the caller.
func (db *DB) CreateUser(ctx context.Context, email, password string, role Role) (User, error) {
	if !role.Valid() {
		return User{}, fmt.Errorf("store: invalid role %q", role)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return User{}, err
	}

	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx,
		"INSERT INTO users (email, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		email, hash, string(role), now, now)
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("last insert id: %w", err)
	}

	return User{
		ID: id, Email: email, PasswordHash: hash, Role: role,
		CreatedAt: time.Unix(now, 0).UTC(), UpdatedAt: time.Unix(now, 0).UTC(),
	}, nil
}

// GetUserByEmail looks up an account. Email matching is case-insensitive,
// enforced by the column's COLLATE NOCASE.
func (db *DB) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return db.scanUser(db.Reader.QueryRowContext(ctx,
		"SELECT id, email, password_hash, role, created_at, updated_at FROM users WHERE email = ?", email))
}

// GetUser looks up an account by ID.
func (db *DB) GetUser(ctx context.Context, id int64) (User, error) {
	return db.scanUser(db.Reader.QueryRowContext(ctx,
		"SELECT id, email, password_hash, role, created_at, updated_at FROM users WHERE id = ?", id))
}

func (db *DB) scanUser(row *sql.Row) (User, error) {
	var (
		u       User
		role    string
		created int64
		updated int64
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	u.Role = Role(role)
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return u, nil
}

// ListUsers returns every account, without password hashes.
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.Reader.QueryContext(ctx,
		"SELECT id, email, role, created_at, updated_at FROM users ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []User
	for rows.Next() {
		var (
			u       User
			role    string
			created int64
			updated int64
		)
		if err := rows.Scan(&u.ID, &u.Email, &role, &created, &updated); err != nil {
			return nil, err
		}
		u.Role = Role(role)
		u.CreatedAt = time.Unix(created, 0).UTC()
		u.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword replaces a user's password.
func (db *DB) UpdatePassword(ctx context.Context, userID int64, newPassword string) error {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = db.Writer.ExecContext(ctx,
		"UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
		hash, time.Now().Unix(), userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// DeleteUser removes an account and, by cascade, its sessions and tokens.
func (db *DB) DeleteUser(ctx context.Context, id int64) error {
	if _, err := db.Writer.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete user %d: %w", id, err)
	}
	return nil
}

// Session is a browser login.
type Session struct {
	TokenHash  string
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
	IP         string
}

// SessionTTL is how long a session stays valid without use.
const SessionTTL = 7 * 24 * time.Hour

// CreateSession stores a new session and returns the plaintext token, which is
// the only time it exists outside the caller's hand.
func (db *DB) CreateSession(ctx context.Context, userID int64, userAgent, ip string) (string, error) {
	token, err := auth.GenerateSessionToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	_, err = db.Writer.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		auth.HashToken(token), userID, now.Unix(), now.Add(SessionTTL).Unix(), now.Unix(),
		truncate(userAgent, 255), truncate(ip, 64))
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return token, nil
}

// LookupSession resolves a session token to its user.
//
// Expired sessions are treated as absent. The expiry is extended on use
// (sliding window), so an operator working through an incident is not logged
// out from under them.
func (db *DB) LookupSession(ctx context.Context, token string) (User, error) {
	hash := auth.HashToken(token)
	now := time.Now()

	var (
		userID    int64
		expiresAt int64
	)
	err := db.Reader.QueryRowContext(ctx,
		"SELECT user_id, expires_at FROM sessions WHERE token_hash = ?", hash,
	).Scan(&userID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup session: %w", err)
	}

	if now.Unix() > expiresAt {
		// Clean up on the way past; no separate reaper needed for this case.
		_, _ = db.Writer.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash)
		return User{}, ErrNotFound
	}

	// Only extend when it would move meaningfully, so a busy dashboard polling
	// every few seconds does not turn every request into a write.
	if time.Unix(expiresAt, 0).Sub(now) < SessionTTL-time.Hour {
		_, _ = db.Writer.ExecContext(ctx,
			"UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE token_hash = ?",
			now.Add(SessionTTL).Unix(), now.Unix(), hash)
	}

	return db.GetUser(ctx, userID)
}

// DeleteSession logs one session out.
func (db *DB) DeleteSession(ctx context.Context, token string) error {
	_, err := db.Writer.ExecContext(ctx,
		"DELETE FROM sessions WHERE token_hash = ?", auth.HashToken(token))
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions logs a user out everywhere, which is what a password
// change must do.
func (db *DB) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := db.Writer.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("delete sessions for user %d: %w", userID, err)
	}
	return nil
}

// PurgeExpiredSessions removes stale rows. Called periodically.
func (db *DB) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.Writer.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at < ?", time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// APIToken is a machine credential.
type APIToken struct {
	ID         int64
	Prefix     string
	Name       string
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateAPIToken issues a token. The plaintext is returned once and never
// stored; only its hash and display prefix are kept.
func (db *DB) CreateAPIToken(ctx context.Context, userID int64, name string, expiresAt *time.Time) (string, APIToken, error) {
	token, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		return "", APIToken{}, err
	}

	now := time.Now()
	var expiry any
	if expiresAt != nil {
		expiry = expiresAt.Unix()
	}

	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO api_tokens (token_hash, prefix, name, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		auth.HashToken(token), prefix, name, userID, now.Unix(), expiry)
	if err != nil {
		return "", APIToken{}, fmt.Errorf("insert api token: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return "", APIToken{}, fmt.Errorf("last insert id: %w", err)
	}

	return token, APIToken{
		ID: id, Prefix: prefix, Name: name, UserID: userID,
		CreatedAt: now.UTC(), ExpiresAt: expiresAt,
	}, nil
}

// LookupAPIToken resolves a token to its user, rejecting revoked and expired
// ones.
func (db *DB) LookupAPIToken(ctx context.Context, token string) (User, error) {
	hash := auth.HashToken(token)
	now := time.Now()

	var (
		id        int64
		userID    int64
		expiresAt sql.NullInt64
		revokedAt sql.NullInt64
	)
	err := db.Reader.QueryRowContext(ctx,
		"SELECT id, user_id, expires_at, revoked_at FROM api_tokens WHERE token_hash = ?", hash,
	).Scan(&id, &userID, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup api token: %w", err)
	}

	if revokedAt.Valid {
		return User{}, ErrNotFound
	}
	if expiresAt.Valid && now.Unix() > expiresAt.Int64 {
		return User{}, ErrNotFound
	}

	// Best-effort: knowing a token is unused for months is useful, but failing
	// the request because that write failed is not.
	_, _ = db.Writer.ExecContext(ctx,
		"UPDATE api_tokens SET last_used_at = ? WHERE id = ?", now.Unix(), id)

	return db.GetUser(ctx, userID)
}

// ListAPITokens returns a user's tokens, newest first. Hashes are never
// returned.
func (db *DB) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT id, prefix, name, user_id, created_at, expires_at, last_used_at, revoked_at
		FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []APIToken
	for rows.Next() {
		var (
			t                          APIToken
			created                    int64
			expires, lastUsed, revoked sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.Prefix, &t.Name, &t.UserID, &created, &expires, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		t.ExpiresAt = nullTime(expires)
		t.LastUsedAt = nullTime(lastUsed)
		t.RevokedAt = nullTime(revoked)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken disables a token without deleting it, so the audit trail
// survives.
func (db *DB) RevokeAPIToken(ctx context.Context, id, userID int64) error {
	res, err := db.Writer.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
		time.Now().Unix(), id, userID)
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordLoginAttempt logs a failed login for rate limiting.
func (db *DB) RecordLoginAttempt(ctx context.Context, identifier string) error {
	_, err := db.Writer.ExecContext(ctx,
		"INSERT INTO login_attempts (identifier, attempted_at) VALUES (?, ?)",
		identifier, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("record login attempt: %w", err)
	}
	return nil
}

// CountRecentLoginAttempts counts failures for an identifier within a window.
func (db *DB) CountRecentLoginAttempts(ctx context.Context, identifier string, window time.Duration) (int, error) {
	var n int
	err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM login_attempts WHERE identifier = ? AND attempted_at > ?",
		identifier, time.Now().Add(-window).Unix()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count login attempts: %w", err)
	}
	return n, nil
}

// ClearLoginAttempts wipes the failure record after a successful login.
func (db *DB) ClearLoginAttempts(ctx context.Context, identifier string) error {
	_, err := db.Writer.ExecContext(ctx,
		"DELETE FROM login_attempts WHERE identifier = ?", identifier)
	if err != nil {
		return fmt.Errorf("clear login attempts: %w", err)
	}
	return nil
}

// PurgeOldLoginAttempts removes rows past the rate-limit window.
func (db *DB) PurgeOldLoginAttempts(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := db.Writer.ExecContext(ctx,
		"DELETE FROM login_attempts WHERE attempted_at < ?", time.Now().Add(-olderThan).Unix())
	if err != nil {
		return 0, fmt.Errorf("purge login attempts: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
