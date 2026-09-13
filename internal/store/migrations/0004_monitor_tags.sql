-- 0004_monitor_tags.sql — give a monitor key/value tags.
--
-- Tags are key/value pairs (`env:prod`, `customer:acme`), not free-form words.
-- Grouping in the Compact layout is keyed on a dimension, and notification
-- routing will want the same shape later; retrofitting structure onto strings
-- users have already typed is far more expensive than carrying it from day one.
--
-- ONE VALUE PER KEY PER MONITOR. That is what the primary key below enforces,
-- and it is a deliberate choice rather than an accident of the schema: a
-- monitor that is in two environments at once has no sensible place in a
-- grouped view, and every consumer would need a tie-break rule. A monitor that
-- genuinely needs several values uses several keys.
--
-- The key is stored already trimmed and lowercased; the value is stored
-- trimmed with its case as typed, because `customer:Acme` is a name and
-- flattening it would show up in the UI. Normalisation happens in Go
-- (tags.go) so that create, patch and preview cannot disagree about it.
--
-- A separate table rather than a JSON column: filtering and grouping by tag is
-- the entire reason tags exist, and that wants an index, not a scan over
-- serialised blobs.
CREATE TABLE monitor_tags (
    monitor_id INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    PRIMARY KEY (monitor_id, key)
) STRICT, WITHOUT ROWID;

-- Finding every monitor carrying a given tag is the read the dashboard makes;
-- the primary key above is no help for it because it leads with monitor_id.
CREATE INDEX idx_monitor_tags_key_value ON monitor_tags (key, value);
