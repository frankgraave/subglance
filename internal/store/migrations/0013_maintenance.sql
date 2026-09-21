CREATE TABLE maintenance_windows (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id INTEGER REFERENCES monitors(id) ON DELETE CASCADE,
    spec TEXT NOT NULL
);
ALTER TABLE heartbeats ADD COLUMN maintenance INTEGER NOT NULL DEFAULT 0 CHECK (maintenance IN (0,1));
ALTER TABLE heartbeat_hourly ADD COLUMN maintenance_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE incidents ADD COLUMN maintenance_pending INTEGER NOT NULL DEFAULT 0;
-- Suppression is neither a successful send nor a broken channel.
ALTER TABLE notif_outbox ADD COLUMN suppressed INTEGER NOT NULL DEFAULT 0 CHECK (suppressed IN (0,1));
