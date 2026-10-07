-- 0028_session_ids.sql — a name for a session that is safe to show.
--
-- A session is keyed by the hash of its cookie, and that key never leaves the
-- server: it is what a stolen backup would be checked against. Listing and
-- ending sessions from the settings page needs something to point at, so each
-- session gets a second, public identifier. It is random, not computed from
-- the token or its hash, so knowing it says nothing about either.
--
-- Existing sessions get one here: randomblob() is evaluated once per row.
ALTER TABLE sessions ADD COLUMN public_id TEXT NOT NULL DEFAULT '';
UPDATE sessions SET public_id = lower(hex(randomblob(16)));
CREATE UNIQUE INDEX idx_sessions_public_id ON sessions (public_id);
