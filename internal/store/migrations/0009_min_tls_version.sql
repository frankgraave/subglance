-- 0009_min_tls_version.sql — let a monitor name the TLS floor it dials with.
--
-- The checkers have honoured a per-monitor floor since SUB-110: both the SSL
-- and the HTTP path read Monitor.MinTLSVersion and fall back to TLS 1.2. What
-- was missing was any way to set it. Nothing outside internal/checker ever
-- wrote the field, so the feature existed in Go and was invisible from the
-- product — which is the worse half of a gap, because the code reads as done.
--
-- The column stores the crypto/tls constant (769..772) rather than a label,
-- because that is the value handed straight to tls.Config.MinVersion and a
-- lookup table in SQL would be a second place for the set to drift. The API
-- speaks "1.0".."1.3"; the translation lives in internal/checker next to the
-- code that dials.
--
-- NULLABLE, and NULL means the default. There are two settings here and they
-- are not the same: "I have not expressed an opinion, follow SubGlance" and
-- "I have decided this endpoint must do TLS 1.2". Today both would dial the
-- same way, so collapsing them into NOT NULL DEFAULT 771 would be invisible —
-- until the product's default moves to TLS 1.3, at which point every monitor
-- that never had an opinion would silently be holding the old one. Existing
-- rows are therefore left NULL rather than backfilled.
--
-- Zero is accepted alongside NULL because Go's zero value for a uint16 is 0
-- and effectiveMinTLSVersion already reads it as "default". A row written by a
-- caller that never set the field must not be rejected for agreeing with the
-- schema.
--
-- The CHECK names the four constants explicitly instead of a range. A range
-- would accept 770 as well, which is TLS 1.1 and fine, but also every future
-- number that happens to land inside it; the set of versions this product
-- offers is a decision, not an interval.
ALTER TABLE monitors
    ADD COLUMN min_tls_version INTEGER
        CHECK (min_tls_version IS NULL
               OR min_tls_version IN (0, 769, 770, 771, 772));
