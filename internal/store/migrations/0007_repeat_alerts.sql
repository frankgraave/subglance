-- 0007_repeat_alerts.sql — repeat an unacknowledged alert until someone answers.
--
-- The state engine sends exactly one notification when a monitor goes down and
-- one when it recovers. Acknowledging an incident is documented as "stops
-- repeat notifications", but there were no repeats to stop: one alert at 02:40
-- that you sleep through leaves the outage unattended until morning, which is
-- the exact scenario a monitor is run for.
--
-- Three columns, all on rows that already exist, so no table rebuild is needed.
--
-- repeat_after_s is the delay before the FIRST reminder. The schedule that
-- follows it grows (see state/reminder.go): a flat interval is the version
-- Uptime Kuma ships and the complaint it collects, because a monitor that
-- nags every five minutes for six hours teaches people to mute it — and a
-- muted monitor misses the next outage too.
--
-- The column default is 0 (off) while the API's default for a create request
-- that omits the field is 15 minutes. That split is deliberate and matches how
-- `retries` already works: the schema default is the Go zero value, so a
-- Monitor built in Go and a bare INSERT agree, and the opinion about what a
-- new monitor should do lives in one place in the API instead of two.
--
-- 15 minutes is long enough that a brief outage resolves itself before anyone
-- is told twice, and 0 disables reminders entirely for anyone who disagrees.
ALTER TABLE monitors
    ADD COLUMN repeat_after_s INTEGER NOT NULL DEFAULT 0
                              CHECK (repeat_after_s = 0 OR repeat_after_s BETWEEN 60 AND 86400);

-- Existing monitors get reminders switched on, rather than inheriting the
-- column default. Someone already running SubGlance has monitors they care
-- about; leaving them on the old behaviour would mean the acknowledge button
-- they can already see stays decorative until they edit every monitor by hand.
UPDATE monitors SET repeat_after_s = 900;

-- reminded_at is when the last reminder went out, and reminder_count how many
-- have gone out for this incident.
--
-- Both are persisted rather than held in memory because the schedule has to
-- survive a restart. Without them, restarting SubGlance during a three-day
-- outage would start the escalation from the beginning and alert as if the
-- incident were new, which is the same re-announcement the state engine's
-- Restore already exists to prevent.
ALTER TABLE incidents
    ADD COLUMN reminded_at INTEGER;

ALTER TABLE incidents
    ADD COLUMN reminder_count INTEGER NOT NULL DEFAULT 0;
