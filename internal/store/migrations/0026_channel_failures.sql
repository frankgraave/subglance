-- 0026_channel_failures.sql — remember which channels have stopped delivering.
--
-- A delivery that gives up is recorded on its outbox row, and the
-- notifications screen reads those rows. Nothing else did: a revoked Slack
-- webhook or an expired SMTP password went unnoticed until someone happened
-- to open that screen, while the next outage was confirmed and told to nobody.
--
-- One row per channel that is failing now. The first delivery that gives up
-- opens it; the next delivery that arrives deletes it. Between the two, the
-- notifier reports the failure once through another channel and stamps
-- noticed_at, which is what keeps a second, third or eleventh dead letter on
-- the same channel from sending a second notice.
--
-- A table of its own rather than a query over notif_outbox: the retention pass
-- prunes outbox rows, and a failure whose evidence was pruned is still a
-- channel that has delivered nothing since. It also has to hold what the
-- outbox has no place for, which channel carried the notice and when.
--
-- notice_channel_id is SET NULL rather than CASCADE: deleting the channel that
-- carried the notice does not undo having sent it.
CREATE TABLE channel_failures (
    channel_id        INTEGER PRIMARY KEY REFERENCES notif_channels (id) ON DELETE CASCADE,
    failed_at         INTEGER NOT NULL,
    last_error        TEXT    NOT NULL DEFAULT '',
    noticed_at        INTEGER,
    notice_channel_id INTEGER REFERENCES notif_channels (id) ON DELETE SET NULL
) STRICT;
