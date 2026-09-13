package state

import "time"

// ReminderCap is the longest gap the escalating schedule will ever wait.
//
// Once an outage has lasted long enough to reach this point, a reminder is no
// longer news; it is a daily note that the thing is still broken. Waiting
// longer than a day would let a forgotten incident fall out of sight
// completely, which is the failure the reminders exist to prevent.
const ReminderCap = 24 * time.Hour

// reminderGrowth is how much longer each gap is than the one before it.
//
// Four rather than two. A doubling schedule starting at 15 minutes spends its
// first six reminders inside the first eight hours, which is most of a night
// of sleep; quadrupling reaches the daily cap in four steps (15m, 1h, 4h, 16h)
// and so spends its reminders on the part of an outage where someone might
// actually still act on them.
const reminderGrowth = 4

// ReminderGap returns how long to wait before the reminder after `sent`
// reminders have already gone out.
//
// The gap grows with each reminder rather than staying flat. A flat interval
// is what Uptime Kuma ships ("resend if down X times") and it is also its
// most common complaint: a monitor that repeats every five minutes for six
// hours trains its owner to mute it, and a muted monitor misses the next
// outage too. Per product principle 5 a wrong alert costs more trust than ten
// missed ones, and the eleventh identical alert is a wrong alert.
//
// A base of zero or less means reminders are disabled and the gap is zero;
// callers must check ReminderEnabled rather than reading meaning into that.
func ReminderGap(base time.Duration, sent int) time.Duration {
	if base <= 0 {
		return 0
	}
	if sent < 0 {
		sent = 0
	}

	gap := base
	for i := 0; i < sent; i++ {
		if gap >= ReminderCap/reminderGrowth {
			return ReminderCap
		}
		gap *= reminderGrowth
	}
	if gap > ReminderCap {
		return ReminderCap
	}
	return gap
}

// ReminderEnabled reports whether a base interval asks for reminders at all.
func ReminderEnabled(base time.Duration) bool { return base > 0 }

// NextReminder returns the moment the next reminder is due for an incident.
//
// `since` is when the incident was confirmed, `last` when the previous
// reminder went out (zero if none has), and `sent` how many have. The first
// reminder is measured from confirmation rather than from the incident's start
// so that the confirmation delay a user chose with `retries` is not also spent
// on the reminder clock.
func NextReminder(since, last time.Time, sent int, base time.Duration) time.Time {
	if !ReminderEnabled(base) {
		return time.Time{}
	}

	from := since
	if sent > 0 && !last.IsZero() {
		from = last
	}
	return from.Add(ReminderGap(base, sent))
}

// ReminderDue reports whether a reminder should go out now.
//
// A zero `since` means the incident was never confirmed. Those never remind:
// nobody was told about them in the first place, and an unconfirmed incident
// is exactly the single failed probe the engine deliberately stays quiet about.
func ReminderDue(now, since, last time.Time, sent int, base time.Duration) bool {
	if since.IsZero() {
		return false
	}
	due := NextReminder(since, last, sent, base)
	if due.IsZero() {
		return false
	}
	return !now.Before(due)
}
