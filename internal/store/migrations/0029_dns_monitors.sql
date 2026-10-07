-- 0029_dns_monitors.sql — accept dns as a monitor type.
--
-- +subglance defer-foreign-keys
--
-- A dns monitor asks a resolver for one record type of one name and compares
-- the answer with the values it expects. Three columns carry that:
--
--   dns_record_type    A, AAAA, CNAME, MX or TXT. NULL for every other type,
--                      and the CHECK at the bottom makes the two facts one:
--                      a dns monitor without a record type cannot ask
--                      anything, and a record type on an http monitor would
--                      be a setting that does nothing.
--   dns_expected_json  the expected values as a JSON array of strings. NULL
--                      or empty means any record of the type passes. A list
--                      rather than one value because an A record set or an
--                      MX set is several records, and a check that compared
--                      only one of them would pass a zone with an extra,
--                      unexpected address in it.
--   dns_resolver       host or host:port of the resolver to ask, NULL for
--                      the host's own.
--
-- SQLite cannot widen a CHECK constraint in place, so the table is rebuilt
-- exactly as 0005 did. The directive above defers foreign keys while the old
-- table is removed, so heartbeats, incidents, tags, channel assignments,
-- maintenance windows, status page entries and config keys that point at a
-- monitor stay where they are. Every existing column is copied as is.

CREATE TABLE monitors_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    type            TEXT    NOT NULL
                            CHECK (type IN ('http', 'tcp', 'ping', 'ssl', 'push', 'dns')),
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

    push_token_hash   TEXT UNIQUE,
    push_token_prefix TEXT,
    push_interval_s   INTEGER CHECK (push_interval_s IS NULL
                                     OR push_interval_s BETWEEN 60 AND 2592000),
    push_grace_s      INTEGER CHECK (push_grace_s IS NULL
                                     OR push_grace_s BETWEEN 0 AND 2592000),

    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,

    capture_response INTEGER NOT NULL DEFAULT 1
                             CHECK (capture_response IN (0, 1)),
    repeat_after_s   INTEGER NOT NULL DEFAULT 0
                             CHECK (repeat_after_s = 0 OR repeat_after_s BETWEEN 60 AND 86400),
    min_tls_version  INTEGER CHECK (min_tls_version IS NULL
                                    OR min_tls_version IN (0, 769, 770, 771, 772)),
    recovery_threshold INTEGER NOT NULL DEFAULT 2
                               CHECK (recovery_threshold BETWEEN 1 AND 10),
    json_path        TEXT,
    json_operator    TEXT CHECK (json_operator IS NULL
                                 OR json_operator IN ('equals', 'not_equals', 'exists', 'less_than', 'greater_than')),
    json_expected    TEXT,
    resumed_at       INTEGER,

    dns_record_type   TEXT CHECK (dns_record_type IS NULL
                                  OR dns_record_type IN ('A', 'AAAA', 'CNAME', 'MX', 'TXT')),
    dns_expected_json TEXT,
    dns_resolver      TEXT,

    CHECK ((type = 'push') = (push_token_hash IS NOT NULL)),
    CHECK ((type = 'push') = (push_interval_s IS NOT NULL)),
    CHECK ((type = 'dns') = (dns_record_type IS NOT NULL))
) STRICT;

INSERT INTO monitors_new (
    id, name, type, target, interval_s, timeout_s, retries,
    method, expected_status, keyword, keyword_mode, follow_redirects,
    headers_json, body, ssl_warn_days, enabled,
    push_token_hash, push_token_prefix, push_interval_s, push_grace_s,
    created_at, updated_at, capture_response, repeat_after_s, min_tls_version,
    recovery_threshold, json_path, json_operator, json_expected, resumed_at
)
SELECT
    id, name, type, target, interval_s, timeout_s, retries,
    method, expected_status, keyword, keyword_mode, follow_redirects,
    headers_json, body, ssl_warn_days, enabled,
    push_token_hash, push_token_prefix, push_interval_s, push_grace_s,
    created_at, updated_at, capture_response, repeat_after_s, min_tls_version,
    recovery_threshold, json_path, json_operator, json_expected, resumed_at
FROM monitors;

-- AUTOINCREMENT promises that an id is never handed out twice, including the
-- id of a monitor that has since been removed. Carry the old high-water mark
-- across before the old table (and its sqlite_sequence row) goes away, as
-- 0024 does for channels.
UPDATE sqlite_sequence
   SET seq = max(seq, coalesce((SELECT seq FROM sqlite_sequence
                                 WHERE name = 'monitors'), 0))
 WHERE name = 'monitors_new';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'monitors_new', seq FROM sqlite_sequence
 WHERE name = 'monitors'
   AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'monitors_new');

DROP TABLE monitors;
ALTER TABLE monitors_new RENAME TO monitors;

CREATE INDEX idx_monitors_enabled ON monitors (enabled);
CREATE INDEX idx_monitors_push ON monitors (type) WHERE type = 'push';
