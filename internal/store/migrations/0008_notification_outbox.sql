-- 0008_notification_outbox.sql — deliver alerts, and survive the delivery failing.
--
-- Until now an alert reached the edge of the process and stopped there: the
-- state engine decided someone should be told, the runner called a callback,
-- and nothing was wired to the other end. Channels could be configured and
-- assigned, which made the gap worse than an empty screen — the interface
-- promised a delivery that was never attempted.
--
-- The table is an outbox rather than a direct send for one reason: a webhook
-- that hangs must never hold up a check. The runner writes a row and returns;
-- a separate worker drains the queue. That also means a delivery survives a
-- restart, which matters most in exactly the case notifications exist for —
-- the machine having a bad night.
CREATE TABLE notif_outbox (
    id          INTEGER PRIMARY KEY,

    channel_id  INTEGER NOT NULL REFERENCES notif_channels(id) ON DELETE CASCADE,
    monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,

    -- incident_id is nullable because not every alert has an incident behind
    -- it, and because an incident may be deleted while a delivery is still
    -- queued. Losing the link is better than losing the alert.
    incident_id INTEGER REFERENCES incidents(id) ON DELETE SET NULL,

    -- event mirrors state.Event. Kept as text rather than an enum table: the
    -- set changes with the code, not with the data, and a readable value in a
    -- manual query is worth more here than referential tidiness.
    event       TEXT    NOT NULL,

    -- payload_json is the rendered alert, frozen at enqueue time. A delivery
    -- retried an hour later must say what was true when it fired, not what is
    -- true now: "was down for 2 minutes" that silently becomes "was down for
    -- 62 minutes" on a retry is a lie told by a monitoring tool.
    payload_json TEXT   NOT NULL,

    -- status is one of: pending, delivered, failed.
    --
    -- failed is terminal — the dead letter. It means the attempt budget ran
    -- out, not that the channel is broken forever; a later alert on the same
    -- channel starts fresh, because a channel that was down for an hour is
    -- the normal case and permanently disabling it would turn one outage
    -- into silent monitoring.
    status      TEXT    NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'delivered', 'failed')),

    attempts    INTEGER NOT NULL DEFAULT 0,

    -- next_attempt_at carries the backoff. Storing the moment rather than a
    -- delay means the worker's query is a plain comparison and the schedule
    -- does not restart when the process does.
    next_attempt_at INTEGER NOT NULL,

    -- last_error keeps the most recent failure so the interface can say why a
    -- channel is not delivering. One outage produces one explanation, not a
    -- log the operator has to go and find.
    last_error  TEXT NOT NULL DEFAULT '',

    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- The worker's only hot query: the due pending rows, oldest first. Partial on
-- status so delivered rows — which become almost the whole table — cost
-- nothing to skip.
CREATE INDEX idx_notif_outbox_due
    ON notif_outbox (next_attempt_at, id)
    WHERE status = 'pending';

-- For the per-channel health view, and for pruning.
CREATE INDEX idx_notif_outbox_channel
    ON notif_outbox (channel_id, created_at);
