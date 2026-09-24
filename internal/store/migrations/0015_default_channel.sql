-- 0015_default_channel.sql — one channel may be the instance-wide default.
--
-- A monitor with no channels of its own alerts through the default. Without
-- one, "no channels" means nobody hears about the outage, and that is the one
-- failure an uptime monitor cannot afford: a misrouted alert is noticed, a
-- missing one is not.
--
-- A flag on the channel rather than a row in `settings`, so that deleting the
-- default channel clears the default with it. A setting holding a channel id
-- would survive the delete and point at nothing.
--
-- The partial unique index is what makes "the" default a fact of the schema
-- rather than a promise of the code that writes it: a second row with the flag
-- set is rejected by SQLite, however it got there.
ALTER TABLE notif_channels
    ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1));

CREATE UNIQUE INDEX notif_channels_one_default
    ON notif_channels (is_default) WHERE is_default = 1;
