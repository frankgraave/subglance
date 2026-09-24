-- 0016_quiet_hours.sql — let a channel sleep without losing what happened.
--
-- Quiet hours belong to a channel, not to a monitor: they describe when the
-- person at the other end of a phone is asleep, and the same monitor may page
-- a phone and write to a log channel that never sleeps.
--
-- A separate table rather than columns on notif_channels, so a channel without
-- quiet hours carries nothing and removing them is a DELETE rather than four
-- columns reset to sentinels.
CREATE TABLE notif_quiet_hours (
    channel_id INTEGER PRIMARY KEY REFERENCES notif_channels(id) ON DELETE CASCADE,

    -- Wall-clock bounds, HH:MM, in the channel's own timezone. A window whose
    -- end is earlier than its start runs past midnight, which is the usual
    -- shape (23:00 to 07:00).
    starts_at TEXT NOT NULL,
    ends_at   TEXT NOT NULL,
    timezone  TEXT NOT NULL,

    -- during is what happens to an alert that arrives inside the window.
    -- 'hold' is the default because it is the only choice that cannot lose an
    -- alert: the delivery waits and is sent, in one digest with whatever else
    -- waited, when the window ends. 'drop' exists because some channels are
    -- worthless after the fact, but it has to be chosen.
    during    TEXT NOT NULL DEFAULT 'hold' CHECK (during IN ('hold', 'drop'))
);

-- quiet_held marks a delivery that is waiting for a window to end rather than
-- for a retry. It is what lets the worker collect a channel's overnight
-- alerts into one message instead of sending them one by one at 07:00.
ALTER TABLE notif_outbox ADD COLUMN quiet_held INTEGER NOT NULL DEFAULT 0 CHECK (quiet_held IN (0, 1));
