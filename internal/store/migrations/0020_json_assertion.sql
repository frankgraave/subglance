-- 0020_json_assertion.sql - an HTTP monitor may assert one field of a JSON body.
--
-- A health endpoint that answers 200 with {"status":"degraded"} passes a
-- status check, and a keyword check for "ok" can match the wrong field or
-- `"ok": false`. One assertion on one field closes that gap: a path in dot
-- notation with array indexes, an operator, and an expected value.
--
-- Three nullable columns rather than one JSON blob, so a CHECK can refuse an
-- operator the checker does not know. All three are NULL when the monitor has
-- no assertion, which is every existing row. json_expected holds the value as
-- compact JSON text, so the string "1" and the number 1 stay different values;
-- it is NULL for `exists`, which compares nothing.
--
-- The path grammar is validated in Go (internal/checker) where it is parsed.
-- Repeating it in SQL would be a second grammar to keep in step.
ALTER TABLE monitors ADD COLUMN json_path TEXT;
ALTER TABLE monitors ADD COLUMN json_operator TEXT
    CHECK (json_operator IS NULL
           OR json_operator IN ('equals', 'not_equals', 'exists', 'less_than', 'greater_than'));
ALTER TABLE monitors ADD COLUMN json_expected TEXT;
