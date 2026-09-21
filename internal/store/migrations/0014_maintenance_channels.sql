-- Delivery suppression belongs to a channel. Released rows provide idempotency
-- until the incident itself is pruned.
CREATE TABLE maintenance_channel_alerts (
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES notif_channels(id) ON DELETE CASCADE,
    pending INTEGER NOT NULL CHECK (pending IN (0,1)),
    PRIMARY KEY (incident_id, channel_id)
);
