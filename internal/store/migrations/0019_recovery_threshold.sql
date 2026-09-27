-- 0019_recovery_threshold.sql — a confirmed incident closes on a streak of passes.
--
-- `retries` asks for several failures in a row before an incident is
-- confirmed. Recovery had no equivalent: one passing check closed a confirmed
-- incident and sent "resolved". A service that fails three times, passes once
-- and fails again produced an alert, a false all-clear and a second alert, and
-- a false "resolved" is worse than a late one because it tells people to stop
-- looking.
--
-- recovery_threshold is how many consecutive passing checks close a confirmed
-- incident. 1 is the old behaviour. The upper bound matches `retries`.
--
-- The default is 2, for new monitors and for every existing one. The cost is
-- one check interval before "resolved" goes out. Existing monitors change
-- behaviour on purpose: the false all-clear is a bug in what they already do,
-- not a feature someone opted into.
ALTER TABLE monitors
    ADD COLUMN recovery_threshold INTEGER NOT NULL DEFAULT 2
                                  CHECK (recovery_threshold BETWEEN 1 AND 10);
