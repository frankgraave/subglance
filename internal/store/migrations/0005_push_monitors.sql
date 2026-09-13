-- 0005_push_monitors.sql — let a job report in instead of being probed.
--
-- +subglance defer-foreign-keys
--
-- A push monitor inverts the direction: nothing dials out, the job calls a
-- secret URL when it finishes, and silence past the expected window is what
-- counts as failure. That closes the one class of question the other four
-- types cannot answer at all — "did last night's backup actually run?" — for
-- work that lives behind NAT with no port to knock on.
--
-- Two columns rather than one. `push_interval_s` is how often the job is
-- expected to report and `push_grace_s` is how late it may be before that
-- silence becomes a failure. Folding them into a single number would force a
-- choice between alerting on every slow run and never alerting at all: a
-- nightly backup that usually takes ten minutes and occasionally forty is
-- healthy, and only the second number can express that.
--
-- `interval_s` is left alone and is meaningless for a push monitor: nothing is
-- dialled, so there is no dial rate. Reusing that column as the expected
-- period would have been cheaper by two columns and wrong in meaning, because
-- its range starts at 20 seconds and its purpose is "how often do I go out and
-- look", which for push is never. The new columns say what they mean, and the
-- range they allow (one minute to thirty days) is the range a job schedule
-- actually spans.
--
-- The token is stored hashed, exactly like an API token (auth.HashToken), and
-- never read back. The URL a user pastes into a script ends up in cron logs,
-- CI output and shell history; a database that could hand out working push
-- URLs after a backup leak would be a second, quieter credential store.
-- `push_token_prefix` is kept in clear so the UI can tell two push monitors
-- apart in a list without being able to reconstruct either.
--
-- SQLite cannot widen the `type` CHECK in place, so the table is rebuilt by
-- the documented twelve-step recipe. The directive at the top of this file
-- tells the migration runner to defer foreign keys for the duration: dropping
-- the old `monitors` table with them enforced would cascade away every
-- heartbeat, incident and tag in the database.

CREATE TABLE monitors_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    type            TEXT    NOT NULL
                            CHECK (type IN ('http', 'tcp', 'ping', 'ssl', 'push')),
    target          TEXT    NOT NULL,

    interval_s      INTEGER NOT NULL DEFAULT 60
                            CHECK (interval_s BETWEEN 20 AND 86400),
    timeout_s       INTEGER NOT NULL DEFAULT 10
                            CHECK (timeout_s BETWEEN 1 AND 120),
    retries         INTEGER NOT NULL DEFAULT 2
                            CHECK (retries BETWEEN 0 AND 10),

    method          TEXT    NOT NULL DEFAULT 'GET',
    expected_status TEXT    NOT NULL DEFAULT '200-299',
    keyword         TEXT,
    keyword_mode    TEXT    NOT NULL DEFAULT 'absent_ok'
                            CHECK (keyword_mode IN ('absent_ok', 'must_contain', 'must_not_contain')),
    follow_redirects INTEGER NOT NULL DEFAULT 1 CHECK (follow_redirects IN (0, 1)),
    headers_json    TEXT,
    body            TEXT,

    ssl_warn_days   INTEGER NOT NULL DEFAULT 14
                            CHECK (ssl_warn_days BETWEEN 0 AND 365),

    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),

    -- Push-specific. NULL for every other type, which is what makes
    -- "is this a push monitor" answerable from the row itself.
    push_token_hash   TEXT UNIQUE,
    push_token_prefix TEXT,
    push_interval_s   INTEGER CHECK (push_interval_s IS NULL
                                     OR push_interval_s BETWEEN 60 AND 2592000),
    push_grace_s      INTEGER CHECK (push_grace_s IS NULL
                                     OR push_grace_s BETWEEN 0 AND 2592000),

    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,

    -- A push monitor without a token can never be pinged, and a token on any
    -- other type would be a URL that does nothing. Both are unreachable
    -- through the API, which is exactly why the schema should say so: the
    -- next writer of a migration or a bulk import does not read handlers.
    CHECK ((type = 'push') = (push_token_hash IS NOT NULL)),

    -- The expected interval is what makes a push monitor monitorable at all:
    -- with it NULL the watchdog has no window to compare against and skips the
    -- row forever, so the monitor sits in the list looking watched while
    -- nothing would ever declare it down. Stating it here rather than only in
    -- the handler means a bulk import cannot create that monitor either.
    CHECK ((type = 'push') = (push_interval_s IS NOT NULL))
) STRICT;

INSERT INTO monitors_new (
    id, name, type, target, interval_s, timeout_s, retries,
    method, expected_status, keyword, keyword_mode, follow_redirects,
    headers_json, body, ssl_warn_days, enabled, created_at, updated_at
)
SELECT
    id, name, type, target, interval_s, timeout_s, retries,
    method, expected_status, keyword, keyword_mode, follow_redirects,
    headers_json, body, ssl_warn_days, enabled, created_at, updated_at
FROM monitors;

DROP TABLE monitors;
ALTER TABLE monitors_new RENAME TO monitors;

CREATE INDEX idx_monitors_enabled ON monitors (enabled);

-- The watchdog asks "which push monitors are overdue" on every pass. Without
-- this it is a scan of every monitor in the database on a timer.
CREATE INDEX idx_monitors_push ON monitors (type) WHERE type = 'push';
