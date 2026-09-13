-- 0006_failure_response.sql — keep the response that explains a failed check.
--
-- Numbered 0006 rather than 0005 because another migration is already in
-- flight under that number. Migrations are tracked by filename, not by number,
-- so a gap is harmless and a collision would not be.
--
-- Until now a failed HTTP check stored one line: `status 503, expected
-- 200-299`. The body that says *what* broke — an upstream timeout, a database
-- connection, a maintenance page — was read only to keep the connection
-- reusable and then discarded. The diagnosis was in hand and thrown away.
--
-- A SEPARATE TABLE, NOT COLUMNS ON heartbeats. heartbeats is the highest-write
-- table in the system and its hot read is the dashboard beat bar, which pulls
-- the newest N rows for every monitor at once. A 2 KiB text column on that row
-- would travel through that query for no reason. Keeping snapshots beside the
-- table means the beat bar never pays for them, and the detail view pays once.
--
-- ON DELETE CASCADE ties the snapshot to its heartbeat, which is what gives it
-- the retention rules for free: when the rollup folds raw beats into hourly
-- buckets and deletes them, their snapshots go with them. No second sweep to
-- write, and no way for the two policies to drift apart.
CREATE TABLE heartbeat_responses (
    heartbeat_id INTEGER PRIMARY KEY REFERENCES heartbeats (id) ON DELETE CASCADE,
    -- The first bytes of the body, truncated on a rune boundary and stored as
    -- valid UTF-8: the column is TEXT in a STRICT table and a health endpoint
    -- may answer with anything at all.
    body         TEXT    NOT NULL,
    -- A JSON object of the few response headers that help triage. An
    -- allowlist, never the whole set: Set-Cookie and Authorization have no
    -- business in a monitoring database.
    headers_json TEXT,
    -- 1 when the body was longer than the cap, so the UI can say so instead of
    -- letting someone read a sentence that stops mid-word and trust it.
    truncated    INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1))
) STRICT, WITHOUT ROWID;

-- Storing the body of someone else's service is a new class of data for this
-- product, so it is a per-monitor switch. The default is on, because a capture
-- that nobody enabled is a capture that is never there on the night it is
-- needed; a monitor whose responses carry anything sensitive turns it off.
ALTER TABLE monitors
    ADD COLUMN capture_response INTEGER NOT NULL DEFAULT 1
        CHECK (capture_response IN (0, 1));
