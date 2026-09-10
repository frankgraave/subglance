-- 0003_heartbeat_rollup.sql — make hourly latency averages mergeable.
--
-- heartbeat_hourly (0001_init.sql) stores latency_avg but not how many samples
-- it averages over. That is enough to display a bucket, but not to combine
-- buckets: averaging averages over-weights quiet hours, and a rollup that runs
-- twice against the same hour cannot merge its old and new halves correctly.
--
-- latency_count is the number of heartbeats in the bucket that actually
-- carried a latency. It is deliberately not up_count + down_count: failed
-- checks usually record no latency at all, so weighting by the total sample
-- count would drag every average toward the successful checks' figure by an
-- amount that depends on the failure rate.
ALTER TABLE heartbeat_hourly
    ADD COLUMN latency_count INTEGER NOT NULL DEFAULT 0;
