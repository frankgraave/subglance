# Scheduled maintenance

Open **Monitors → Scheduled maintenance → Manage scheduled maintenance** to
list, schedule or cancel a window. Editors and administrators can make changes;
viewers can inspect schedules. The API offers the same operations:

- `GET /api/v1/maintenance`
- `POST /api/v1/maintenance`
- `DELETE /api/v1/maintenance/{id}`

Use a monitor ID for one monitor, or an exact tag key/value pair for a group.
Tag membership is evaluated when a check or notification is processed. Adding a
monitor to the tag group includes it in the next decision; removing it ends its
participation. Overlapping windows form a union: cancelling one does not cancel
another. Up to 200 schedules can be stored; cancel expired one-off schedules to
make room. To change a schedule, create its replacement and cancel the old one.

One-off example (timestamps require an explicit UTC offset):

```json
{"name":"Deploy","monitor_id":12,"starts_at":"2026-10-01T20:00:00Z","ends_at":"2026-10-01T21:00:00Z"}
```

Weekly example (Sunday is 0, Saturday is 6):

```json
{"name":"Weekly deploy","tag_key":"env","tag_value":"prod","timezone":"Europe/Amsterdam","weekdays":[0],"local_time":"02:30","duration_minutes":60}
```

The one-off form uses **UTC**, irrespective of the browser's timezone. Weekly
windows use the named IANA timezone. Timezone data is embedded in the binary,
so minimal containers do not need an external timezone package. There is no
external scheduler. Intent is stored in SQLite and evaluated against timestamps,
so a restart neither loses schedules nor moves their boundaries.

Every interval includes its start and excludes its end. One-off windows can last
up to 366 days; weekly occurrences last 1–1440 elapsed minutes. A local time
missing at a daylight-saving change is skipped. A repeated local time starts
once, at its earlier occurrence. An elapsed duration can therefore end at a
different wall-clock time on a daylight-saving change day. New schedules do not
apply to observations before their creation, even if their stated start is past.

## Measurements, uptime and pause

Checks continue during maintenance, including push reports and overdue-push
checks. Warning, confirmation, incidents, raw results and diagnostic snapshots
continue to describe what was observed. Maintenance is an independent flag, not
a replacement health status. The timestamp reported by the checker determines
whether the sample belongs to maintenance; a check crossing the boundary uses
that timestamp, not the time its database write finishes.

A maintenance sample contributes neither up nor down to the uptime denominator.
The exclusion is stored with the sample and carried into hourly rollups.
Warning and legacy counts exclude maintenance samples so the categories do not
double-count. Latency statistics still include measured maintenance checks.
With no eligible samples, uptime is unknown (`null`), never 0% or 100%.
Cancelling a schedule or changing a tag does not rewrite recorded exclusions.
Uptime remains a sample ratio, not a duration-weighted percentage.

Pause keeps its existing meaning: no scheduled measurements. A maintenance
window does not resume a paused monitor, and its clock keeps running while the
monitor is paused. Resuming during a window resumes measurements with maintenance
exclusions; resuming afterwards starts normal monitoring.

## Alert delivery

Maintenance suppresses initial alerts, recoveries and reminders. A confirmed
incident first observed during maintenance remains visible, but an eventual
recovery stays silent if no initial alert was released. If it is still failing
after maintenance, the next failed check releases its initial alert, subject to
flapping suppression. This pending intent survives restart. Its release writes all assigned, enabled
channel deliveries and clears the intent in one SQLite transaction. These
deferred initials bypass the in-memory grouping window; a failed enqueue leaves
the intent for the next failed check. Reminder schedules
are not advanced while maintenance suppresses them.

The notifier checks again immediately before each send attempt. A queued alert
that meets maintenance is removed from that delivery. A removed initial alert
keeps durable intent for that channel only. The next failed check after maintenance
queues it again without repeating the initial alert to channels already informed.
If the monitor recovers first, the uninformed channel stays silent; other channels
can receive recovery. Reminders and recoveries removed during maintenance are
discarded, not replayed when the window ends.
Groups retain their members, so only maintained monitors are removed; other
members can still be delivered. Removed members stay removed on retries.
Suppressed outbox rows retain a suppression flag and reason without being counted
as successful deliveries or channel failures. They follow the existing retention
policy for completed deliveries. Groups queued by older binaries
have no member IDs: an active window suppresses the entire legacy group rather
than guessing which names it covers. A send already in progress cannot be recalled.

The maintenance overview refreshes every 15 seconds while open. Monitor reads
report current maintenance separately from health; heartbeat history and SSE
report the immutable flag for each measurement. SSE also carries
`current_maintenance` at publish time, so a check finishing across a boundary
does not confuse historical exclusion with current suppression. Live monitor
views refresh read-time state every 15 seconds as well, including paused monitors
that do not emit heartbeats. A failed schedule read is not
presented as proof that no maintenance exists. If the runner cannot read schedules,
it reports the persistence error instead of recording an incorrectly classified
sample; the notifier leaves a queued delivery unsent until it can evaluate it.
Evaluation and decision-persistence failures defer delivery with backoff and a
recorded error; they do not consume the channel's send-attempt budget.
