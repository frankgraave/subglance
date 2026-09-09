-- 0002_auth.sql — sessions and API tokens.
--
-- The users table already exists (0001_init.sql); this adds what is needed to
-- authenticate as one.

-- Sessions back the browser UI. The primary key is a hash of the cookie value,
-- never the value itself: a stolen database backup must not hand out working
-- sessions.
CREATE TABLE sessions (
    token_hash  TEXT    PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    -- Sliding expiry: refreshed on use so an active operator is not logged out
    -- mid-incident.
    last_seen_at INTEGER NOT NULL,
    user_agent  TEXT    NOT NULL DEFAULT '',
    ip          TEXT    NOT NULL DEFAULT ''
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expiry ON sessions (expires_at);

-- API tokens are for scripts and CI. Same rule: only the hash is stored, so
-- the plaintext is shown exactly once at creation and is unrecoverable after.
CREATE TABLE api_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT    NOT NULL UNIQUE,
    -- The first characters of the token, kept in clear so the UI can show
    -- "sgp_a1b2…" and a human can tell two tokens apart without revealing
    -- enough to be useful to a thief.
    prefix      TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER,
    last_used_at INTEGER,
    revoked_at  INTEGER
) STRICT;

CREATE INDEX idx_api_tokens_user ON api_tokens (user_id);

-- Failed login attempts, for rate limiting. Keyed by identifier (email or IP)
-- rather than by user: an attacker guessing an address that does not exist
-- must be slowed down too, and the response must not reveal which case it was.
CREATE TABLE login_attempts (
    identifier  TEXT    NOT NULL,
    attempted_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_login_attempts ON login_attempts (identifier, attempted_at);
