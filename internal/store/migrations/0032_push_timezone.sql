-- 0032_push_timezone.sql — let a push monitor keep a daily job's local clock.
--
-- A push deadline is the last report plus the interval plus grace, counted in
-- elapsed seconds. A job scheduled at a fixed local time does not keep that
-- count: the night the clocks go back, a daily job runs 25 hours after the
-- previous run, and a deadline of 24 hours and a minute calls it down for an
-- hour every year.
--
-- push_timezone is an IANA zone such as Europe/Amsterdam. When it is set, a
-- deadline is the same wall-clock time a whole number of days after the last
-- report, in that zone, plus grace. NULL keeps elapsed time, which is every
-- existing row: nothing is backfilled, because no row says which clock its job
-- runs on.
--
-- The CHECK keeps it NULL for every other type and refuses an empty string, so
-- "no zone" has one spelling. That the zone exists and that the interval is a
-- whole number of days is checked in Go, where the refusal can name the field.
ALTER TABLE monitors ADD COLUMN push_timezone TEXT
    CHECK (push_timezone IS NULL OR (type = 'push' AND push_timezone <> ''));
