-- 0010_resolved_incident_history.sql — make "what recovered recently, across
-- the whole instance" a query the database can answer cheaply.
--
-- Until now there was no instance-wide way to ask it. The screen assembled its
-- history one request per monitor and capped the fan-out at 24 monitors, so on
-- anything larger the card was genuinely incomplete and had to say so. The
-- replacement is a single endpoint that scans resolved incidents in resolution
-- order — and that order is the point of this index.
--
-- The existing idx_incidents_monitor is (monitor_id, started_at DESC), which
-- serves the per-monitor history and nothing else: an instance-wide sweep
-- ordered by resolved_at cannot use it at all, and would fall back to reading
-- every incident ever recorded and sorting it. On a box that has been running
-- for a year that is the whole table for one card.
--
-- Partial on resolved_at IS NOT NULL because open incidents are never in this
-- answer, and on a healthy instance they are a handful of rows against a
-- history of thousands. Keeping them out costs nothing and keeps the index the
-- size of the question.
--
-- The id column is in the key, not along for the ride: resolution timestamps
-- have second granularity, so a recovery sweep can resolve several incidents
-- in the same second. Pagination here is a keyset on (resolved_at, id), and a
-- cursor that cannot break a tie either repeats rows or skips them — which on
-- this screen means an outage silently missing from the history, the one thing
-- the endpoint exists to stop.
CREATE INDEX idx_incidents_resolved
    ON incidents (resolved_at DESC, id DESC)
    WHERE resolved_at IS NOT NULL;
