package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

// sessionIDPattern is the shape newSessionID produces: 16 random bytes as
// lower-case hex.
var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidSessionID reports whether s could name a session. It lets a handler
// answer a malformed id without a query.
func ValidSessionID(s string) bool { return sessionIDPattern.MatchString(s) }

// newSessionID returns a fresh public id for a session.
//
// It is drawn from crypto/rand on its own rather than computed from the
// token or its hash. An id derived from either would put a function of the
// credential in every listing, every log line and every URL that ends a
// session; a random one is only a name.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ListSessions returns a user's sessions that have not expired, the most
// recently used first.
//
// last_seen_at moves at most once an hour (see LookupSession), so it says
// which hour a session was last used in, not which minute.
func (db *DB) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT token_hash, public_id, user_id, created_at, expires_at, last_seen_at, user_agent, ip
		FROM sessions
		WHERE user_id = ? AND expires_at >= ?
		ORDER BY last_seen_at DESC, created_at DESC, public_id`,
		userID, time.Now().Unix())
	if err != nil {
		return nil, fmt.Errorf("list sessions of user %d: %w", userID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Session
	for rows.Next() {
		var (
			s                          Session
			created, expires, lastSeen int64
		)
		if err := rows.Scan(&s.TokenHash, &s.ID, &s.UserID, &created, &expires, &lastSeen, &s.UserAgent, &s.IP); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.CreatedAt = time.Unix(created, 0).UTC()
		s.ExpiresAt = time.Unix(expires, 0).UTC()
		s.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions of user %d: %w", userID, err)
	}
	return out, nil
}

// EndSession ends one of a user's sessions by its public id.
//
// The user id is part of the statement, so an id that belongs to another
// account is ErrNotFound, exactly like one that does not exist: guessing ids
// neither ends someone else's session nor confirms that it is there.
func (db *DB) EndSession(ctx context.Context, userID int64, id string) error {
	res, err := db.Writer.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ? AND public_id = ?", userID, id)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// EndOtherSessions ends every session of a user except the one whose cookie
// is keepToken, and returns how many it ended. An empty keepToken keeps
// none, which is what a request made with an API token means by "the
// others": it holds no session of its own.
func (db *DB) EndOtherSessions(ctx context.Context, userID int64, keepToken string) (int64, error) {
	keep := ""
	if keepToken != "" {
		keep = auth.HashToken(keepToken)
	}
	res, err := db.Writer.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, keep)
	if err != nil {
		return 0, fmt.Errorf("end other sessions of user %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("end other sessions of user %d: %w", userID, err)
	}
	return n, nil
}

// EndUserSessions ends every session of an account and returns how many it
// ended. An unknown account is ErrNotFound, so an administrator is told the
// account is gone rather than that it had nothing to end.
func (db *DB) EndUserSessions(ctx context.Context, userID int64) (int64, error) {
	if _, err := db.GetUser(ctx, userID); err != nil {
		return 0, err
	}
	res, err := db.Writer.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	if err != nil {
		return 0, fmt.Errorf("end sessions of user %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("end sessions of user %d: %w", userID, err)
	}
	return n, nil
}
