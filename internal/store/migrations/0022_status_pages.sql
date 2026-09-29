-- 0022_status_pages.sql — public status pages (docs/design/status-page.md §4).
--
-- A status page is the first thing SubGlance shows to someone without an
-- account, so the tables keep the public side apart from the monitor itself:
-- an entry carries its own display name and its own random key, and the page
-- never reads a monitor's name or id into its output.

CREATE TABLE status_pages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Chosen by the operator and therefore guessable; secrecy is not its job.
    -- NOCASE so /status/Acme and /status/acme cannot name two pages.
    slug        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    timezone    TEXT    NOT NULL DEFAULT 'UTC',
    -- A page lists hand-picked monitors, or every monitor carrying one tag.
    selection   TEXT    NOT NULL CHECK (selection IN ('monitors', 'tag')),
    tag_key     TEXT,
    tag_value   TEXT,
    indexable   INTEGER NOT NULL DEFAULT 0 CHECK (indexable IN (0, 1)),
    -- Off by default: saving a draft publishes nothing.
    enabled     INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    CHECK (
        (selection = 'tag' AND tag_key IS NOT NULL AND tag_value IS NOT NULL) OR
        (selection = 'monitors' AND tag_key IS NULL AND tag_value IS NULL)
    )
) STRICT;

-- One row per monitor on a page. The primary key lets a monitor appear once
-- per page, and on several pages under different public names.
--
-- public_key is what the page and its JSON use instead of the monitor id:
-- sequential ids would tell a visitor how many monitors the instance has and
-- let two pages be correlated. It is random, and UNIQUE across all pages.
CREATE TABLE status_page_entries (
    page_id      INTEGER NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    monitor_id   INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    public_key   TEXT    NOT NULL UNIQUE,
    display_name TEXT    NOT NULL,
    position     INTEGER NOT NULL,
    PRIMARY KEY (page_id, monitor_id)
) STRICT, WITHOUT ROWID;

-- Deleting a monitor cascades through this column; without an index that
-- cascade scans every entry of every page.
CREATE INDEX idx_status_page_entries_monitor ON status_page_entries (monitor_id);
