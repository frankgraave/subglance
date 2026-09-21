-- Preserve raw results; old samples have no recorded confirmation assessment.
ALTER TABLE heartbeats ADD COLUMN assessment TEXT NOT NULL DEFAULT ''
    CHECK (assessment IN ('', 'up', 'warning', 'down'));
ALTER TABLE heartbeats ADD COLUMN failure_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE heartbeat_hourly ADD COLUMN assessed_up INTEGER NOT NULL DEFAULT 0;
ALTER TABLE heartbeat_hourly ADD COLUMN assessed_down INTEGER NOT NULL DEFAULT 0;
ALTER TABLE heartbeat_hourly ADD COLUMN warning_count INTEGER NOT NULL DEFAULT 0;
