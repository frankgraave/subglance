-- 0001_init.sql — the v0.1 schema (see docs/ARCHITECTURE.md §3).

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL DEFAULT 'admin'
                          CHECK (role IN ('admin', 'editor', 'viewer')),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE monitors (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    type            TEXT    NOT NULL
                            CHECK (type IN ('http', 'tcp', 'ping', 'ssl')),
    target          TEXT    NOT NULL,

    interval_s      INTEGER NOT NULL DEFAULT 60
                            CHECK (interval_s BETWEEN 20 AND 86400),
    timeout_s       INTEGER NOT NULL DEFAULT 10
                            CHECK (timeout_s BETWEEN 1 AND 120),
    -- Consecutive failures required before an incident is confirmed.
    -- This is product principle 6 in table form: no false alarms.
    retries         INTEGER NOT NULL DEFAULT 2
                            CHECK (retries BETWEEN 0 AND 10),

    -- HTTP-specific. Ignored by other check types.
    method          TEXT    NOT NULL DEFAULT 'GET',
    expected_status TEXT    NOT NULL DEFAULT '200-299',
    keyword         TEXT,
    keyword_mode    TEXT    NOT NULL DEFAULT 'absent_ok'
                            CHECK (keyword_mode IN ('absent_ok', 'must_contain', 'must_not_contain')),
    follow_redirects INTEGER NOT NULL DEFAULT 1 CHECK (follow_redirects IN (0, 1)),
    headers_json    TEXT,
    body            TEXT,

    -- SSL expiry warning threshold in days; applies to https and ssl checks.
    ssl_warn_days   INTEGER NOT NULL DEFAULT 14
                            CHECK (ssl_warn_days BETWEEN 0 AND 365),

    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),

    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_monitors_enabled ON monitors (enabled);

-- Heartbeats are the highest-write table in the system. Raw rows are kept for
-- a retention window and then rolled up into heartbeat_hourly (see SUB-14),
-- which gives unlimited history at negligible storage cost.
CREATE TABLE heartbeats (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id  INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    ts          INTEGER NOT NULL,
    ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
    latency_ms  INTEGER,
    status_code INTEGER,
    error       TEXT
) STRICT;

-- Serves the dashboard's "last N beats for this monitor" query, which is the
-- single most frequent read in the product.
CREATE INDEX idx_heartbeats_monitor_ts ON heartbeats (monitor_id, ts DESC);

CREATE TABLE heartbeat_hourly (
    monitor_id  INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    bucket      INTEGER NOT NULL,  -- unix ts truncated to the hour
    up_count    INTEGER NOT NULL,
    down_count  INTEGER NOT NULL,
    latency_min INTEGER,
    latency_max INTEGER,
    latency_avg INTEGER,
    PRIMARY KEY (monitor_id, bucket)
) STRICT, WITHOUT ROWID;

CREATE TABLE incidents (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id   INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    started_at   INTEGER NOT NULL,
    confirmed_at INTEGER,
    resolved_at  INTEGER,
    acked_at     INTEGER,
    cause        TEXT    NOT NULL DEFAULT '',
    last_error   TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_incidents_monitor ON incidents (monitor_id, started_at DESC);

-- Guarantees at most one unresolved incident per monitor. Without this the
-- state engine could double-open an incident on a race and alert twice.
CREATE UNIQUE INDEX idx_incidents_one_open
    ON incidents (monitor_id) WHERE resolved_at IS NULL;

CREATE TABLE notif_channels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    type        TEXT    NOT NULL
                        CHECK (type IN ('webhook', 'discord', 'slack', 'telegram', 'email')),
    config_json TEXT    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE monitor_channels (
    monitor_id INTEGER NOT NULL REFERENCES monitors (id)       ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES notif_channels (id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, channel_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
