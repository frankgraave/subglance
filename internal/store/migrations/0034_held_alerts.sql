-- 0034_held_alerts.sql — keep an alert that waits for company on disk.
--
-- An alert waits up to the grouping window (--alert-group-window) before it is
-- written to notif_outbox, so that twenty monitors failing on one dead uplink
-- become one message. A clean stop writes whatever is waiting. A process that
-- was killed, ran out of memory or lost power did not: the alert existed only
-- in memory, while the incident behind it was already stored as confirmed, so
-- the next start restored an outage nobody had been told about as one that
-- had been announced.
--
-- One row per alert per channel while it waits. It is written when the alert
-- joins its batch and removed in the same transaction that writes the batch to
-- notif_outbox, so a waiting alert is always in one of the two. On start the
-- notifier reads what is left back into its batches.
--
--   payload_json  the alert as the notifier renders it, as in notif_outbox;
--                 the batch it waits in follows from its event
--   due_at        when its batch's window closes, in Unix seconds
--
-- Both references cascade: a deleted monitor or channel takes its waiting
-- messages with it, as it does its queued ones in notif_outbox.
CREATE TABLE notif_held (
    id           INTEGER PRIMARY KEY,
    channel_id   INTEGER NOT NULL REFERENCES notif_channels (id) ON DELETE CASCADE,
    monitor_id   INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    payload_json TEXT    NOT NULL,
    due_at       INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_notif_held_monitor ON notif_held (monitor_id);
CREATE INDEX idx_notif_held_channel ON notif_held (channel_id);
