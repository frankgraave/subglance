package main

import (
	"math/rand/v2"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// This file turns a profile into history: heartbeats, hourly buckets and the
// incidents that agree with them.
//
// The one rule everything here follows is that the three must tell the same
// story. An incident whose window contains passing heartbeats, or a beat bar
// with a gap no incident explains, is worse than no demo data at all — it
// teaches whoever is looking that the product's own screens disagree with each
// other. So the outage list is decided first, and the beats, the buckets and
// the incident rows are all derived from it.

// outage is a resolved outageSpec: absolute times rather than durations back
// from now, and with the random ones mixed in.
type outage struct {
	start time.Time
	// end is the zero time while the outage is still running.
	end time.Time

	cause   string
	message string
	status  int

	unconfirmed bool
	acked       bool
	reminders   int
}

func (o outage) covers(t time.Time) bool {
	if t.Before(o.start) {
		return false
	}
	return o.end.IsZero() || t.Before(o.end)
}

// history is everything one monitor contributes to the database.
type history struct {
	beats     []store.Heartbeat
	buckets   []store.HourlyBucket
	incidents []store.Incident
}

// plan is the timing a seed run works to, decided once so every monitor shares
// one "now" and one horizon.
type plan struct {
	now time.Time
	// rawSince is the oldest raw heartbeat written. Everything older is
	// only hourly buckets, which is exactly the shape a long-running
	// instance has after its own rollup pass.
	rawSince time.Time
	// bucketSince is the oldest hourly bucket written, and so the oldest
	// history the demo has at all.
	bucketSince time.Time
}

// buildHistory produces the beats, buckets and incidents for one monitor.
//
// rnd is the caller's generator: seeded deterministically, so two runs with
// the same seed produce the same demo and a screenshot can be compared to the
// one taken last week.
func buildHistory(m store.Monitor, p profile, id int64, pl plan, rnd *rand.Rand) history {
	if p.neverChecked {
		return history{}
	}

	end := pl.now.Add(-p.silentFor)
	start := pl.bucketSince
	if created := monitorStart(m, pl); created.After(start) {
		start = created
	}
	if !start.Before(end) {
		return history{}
	}

	interval := beatInterval(m)
	outages := resolveOutages(p, start, end, interval, m.Retries, rnd)

	h := history{}
	for _, o := range outages {
		inc := incidentFor(id, o, m, interval)
		// The exclusive history horizon cannot contain a future confirmation
		// or recovery, even for an outage that extends beyond the last check.
		if !inc.ConfirmedAt.Before(end) {
			inc.ConfirmedAt = time.Time{}
			inc.AckedAt = time.Time{}
			inc.RemindedAt = time.Time{}
			inc.ReminderCount = 0
		}
		if !inc.ResolvedAt.Before(end) {
			inc.ResolvedAt = time.Time{}
		}
		h.incidents = append(h.incidents, inc)
	}

	rawFrom := pl.rawSince
	if rawFrom.Before(start) {
		rawFrom = start
	}

	if rawFrom.After(end) {
		rawFrom = end
	}

	h.buckets = buildBuckets(id, m, p, start, rawFrom, interval, outages, pl)
	h.beats = buildBeats(id, m, p, rawFrom, end, interval, outages, pl, rnd)
	return h
}

// monitorStart is when this monitor began producing data.
func monitorStart(m store.Monitor, pl plan) time.Time {
	if m.CreatedAt.IsZero() {
		return pl.bucketSince
	}
	return m.CreatedAt
}

// beatInterval is how often this monitor produces a heartbeat.
//
// A push monitor has no check interval that matters: it produces one beat per
// report, and the report schedule is what the operator told it to expect.
func beatInterval(m store.Monitor) time.Duration {
	if m.Type == store.TypePush && m.PushIntervalS > 0 {
		return time.Duration(m.PushIntervalS) * time.Second
	}
	if m.IntervalS > 0 {
		return time.Duration(m.IntervalS) * time.Second
	}
	return time.Minute
}

// resolveOutages turns the scripted outages into absolute times and scatters
// the random ones through the remaining history.
//
// Overlaps are dropped rather than merged. Two open incidents for one monitor
// is impossible in the schema, and two that merely touch would produce a beat
// timeline that neither incident explains on its own.
func resolveOutages(p profile, start, end time.Time, interval time.Duration, retries int, rnd *rand.Rand) []outage {
	var out []outage

	add := func(o outage) {
		first := sampleAtOrAfter(o.start, interval)
		if o.unconfirmed {
			// A requested warning must recover before its Nth failure, not
			// override the assessment of a longer streak of failed samples.
			if retries <= 1 {
				return
			}
			limit := first.Add(time.Duration(retries-1) * interval)
			if (o.end.IsZero() && limit.Before(end)) || (!o.end.IsZero() && o.end.After(limit)) {
				o.end = limit
			}
		}
		if !first.Before(end) || (!o.end.IsZero() && !first.Before(o.end)) {
			return
		}
		if o.start.Before(start) || !o.start.Before(end) {
			return
		}
		for _, existing := range out {
			if overlaps(existing, o, interval) {
				return
			}
		}
		out = append(out, o)
	}

	for _, spec := range p.outages {
		o := outage{
			cause: spec.cause, message: spec.message, status: spec.status,
			unconfirmed: spec.unconfirmed, acked: spec.acked, reminders: spec.reminders,
			start: end.Add(-spec.ago),
		}
		if spec.dur > 0 {
			o.end = o.start.Add(spec.dur)
		}
		add(o)
	}

	// Random outages are short and shallow on purpose: they exist to make
	// uptime a believable 99.x rather than a flat 100, not to compete with
	// the scripted failures the demo is meant to talk about. Anything long
	// enough to be interesting is written down in the catalogue.
	span := end.Sub(start)
	for i := 0; i < p.randomOutages; i++ {
		at := start.Add(time.Duration(rnd.Int64N(int64(span))))
		length := interval * time.Duration(2+rnd.IntN(6))
		cause, message, status := randomFailure(rnd)
		add(outage{
			start: at, end: at.Add(length),
			cause: cause, message: message, status: status,
			// Short and self-healing: most of these never reach the
			// failure threshold, which is the honest outcome and also
			// the one that keeps a demo's incident list readable.
			unconfirmed: rnd.IntN(2) == 0,
		})
	}

	return out
}

// overlaps reports whether two outages are close enough that keeping both
// would produce an unreadable timeline. The gap requirement is a few intervals
// so that recovery is visible between them rather than a single passing beat.
func overlaps(a, b outage, interval time.Duration) bool {
	aEnd, bEnd := a.end, b.end
	if aEnd.IsZero() {
		aEnd = a.start.Add(365 * 24 * time.Hour)
	}
	if bEnd.IsZero() {
		bEnd = b.start.Add(365 * 24 * time.Hour)
	}
	pad := 4 * interval
	return a.start.Before(bEnd.Add(pad)) && b.start.Before(aEnd.Add(pad))
}

func randomFailure(rnd *rand.Rand) (cause, message string, status int) {
	switch rnd.IntN(4) {
	case 0:
		return "timeout", errTimeout, 0
	case 1:
		return "connection", errRefused, 0
	case 2:
		return "status", errStatus5, 503
	default:
		return "dns", errDNS, 0
	}
}

// incidentFor builds the incident record for one outage.
//
// The confirmation delay is the monitor's own: retries consecutive failures at
// its own interval is exactly what the state engine waits for, so an incident
// seeded any other way would contradict the setting shown on the monitor.
func incidentFor(monitorID int64, o outage, m store.Monitor, interval time.Duration) store.Incident {
	inc := store.Incident{
		MonitorID: monitorID,
		StartedAt: sampleAtOrAfter(o.start, interval),
		Cause:     o.cause,
		LastError: o.message,
	}
	if !o.end.IsZero() {
		inc.ResolvedAt = sampleAtOrAfter(o.end, interval)
	}

	confirmed := inc.StartedAt.Add(time.Duration(max(1, m.Retries)-1) * interval)
	// Confirmation belongs to the Nth observed failure, with the first
	// failure already counting as one. Recovery at that sample prevents it.
	if inc.ResolvedAt.IsZero() || confirmed.Before(inc.ResolvedAt) {
		inc.ConfirmedAt = confirmed
	}

	if o.acked && inc.Confirmed() {
		inc.AckedAt = inc.ConfirmedAt.Add(11 * time.Minute)
	}

	// Reminders only exist for a confirmed incident nobody has answered;
	// acknowledging is precisely the thing that stops them.
	if o.reminders > 0 && inc.Confirmed() && inc.AckedAt.IsZero() {
		inc.ReminderCount = o.reminders
		gap := time.Duration(m.RepeatAfterS) * time.Second
		if gap <= 0 {
			gap = time.Hour
		}
		inc.RemindedAt = inc.ConfirmedAt.Add(time.Duration(o.reminders) * gap)
	}
	return inc
}

// sampleAtOrAfter is the first check on the seed cadence at or after t.
func sampleAtOrAfter(t time.Time, interval time.Duration) time.Time {
	at := t.Truncate(interval)
	if at.Before(t) {
		at = at.Add(interval)
	}
	return at
}

// buildBeats writes one heartbeat per interval between from and to.
func buildBeats(
	id int64, m store.Monitor, p profile,
	from, to time.Time, interval time.Duration,
	outages []outage, pl plan, rnd *rand.Rand,
) []store.Heartbeat {
	if !from.Before(to) {
		return nil
	}

	// Align to the interval so a beat bar reads as a regular cadence rather
	// than as whatever second the seeder happened to start at.
	t := sampleAtOrAfter(from, interval)

	out := make([]store.Heartbeat, 0, int(to.Sub(t)/interval)+1)
	// Response snapshots are capped per outage, exactly as the runner caps
	// them: capturing every failing beat of a nine-hour outage would fill
	// the table with the same error page hundreds of times.
	captured := map[time.Time]int{}

	for ; t.Before(to); t = t.Add(interval) {
		hb := store.Heartbeat{MonitorID: id, TS: t, OK: true, Assessment: "up"}

		if o, down := outageAt(outages, t); down {
			hb.OK = false
			hb.Assessment = outageAssessment(id, m, o, interval, t)
			hb.FailureKind = o.cause
			hb.Error = o.message
			hb.StatusCode = o.status
			if o.status > 0 {
				hb.LatencyMS = latencyAt(p, t, pl, rnd)
			}
			if m.CaptureResponse && captured[o.start] < 3 {
				captured[o.start]++
				hb.Response = snapshotFor(o)
			}
		} else if p.baseLatency > 0 {
			hb.LatencyMS = latencyAt(p, t, pl, rnd)
			hb.StatusCode = successStatus(m)
		}

		out = append(out, hb)
	}
	return out
}

// outageAt reports the outage covering t, if any.
func outageAt(outages []outage, t time.Time) (outage, bool) {
	for _, o := range outages {
		if o.covers(t) {
			return o, true
		}
	}
	return outage{}, false
}

// successStatus is the status code a passing check of this type records. Only
// HTTP has one; a TCP dial or a ping has nothing to report and an invented 200
// would be a lie the detail view renders in a column of its own.
func successStatus(m store.Monitor) int {
	if m.Type != "http" {
		return 0
	}
	if m.ExpectedStatus == "200,201" {
		return 201
	}
	return 200
}

// latencyAt is the response time at a moment: a base, a daily trend, a slow
// diurnal swell so the chart has a shape, and noise.
func latencyAt(p profile, t time.Time, pl plan, rnd *rand.Rand) int {
	if p.baseLatency == 0 {
		return 0
	}

	v := float64(p.baseLatency)

	if p.dailyTrend != 0 {
		daysAgo := pl.now.Sub(t).Hours() / 24
		v += p.dailyTrend * max(0, 90-daysAgo)
	}

	// Busier by day than by night, which is what makes a latency chart look
	// like a measurement rather than a random walk.
	hour := float64(t.UTC().Hour())
	v *= 1 + 0.18*dayCurve(hour)

	// A nil generator asks for the trend without the noise, which is what
	// an hourly average wants: the mean of a symmetric jitter is the value
	// it was scattered around anyway, and drawing it would make two runs
	// with the same seed disagree about the past.
	if p.spread > 0 && rnd != nil {
		v += float64(rnd.IntN(p.spread)) - float64(p.spread)/2
	}

	if v < 1 {
		v = 1
	}
	return int(v)
}

// dayCurve is a cheap sine over the hour of the day, peaking mid-afternoon
// UTC, returned in [-1, 1]. Written out rather than pulled from math so the
// shape and its phase are readable at the call site.
func dayCurve(hour float64) float64 {
	// A triangle is indistinguishable from a sine at this amplitude and
	// costs no explanation of where the constants came from.
	x := (hour - 3) / 24
	if x < 0 {
		x++
	}
	if x < 0.5 {
		return 4*x - 1
	}
	return 3 - 4*x
}

// snapshotFor is the captured response of a failing check.
//
// Only failures that got as far as a response have one: a refused connection
// and a DNS failure never saw a server, and inventing a body for them would
// show the operator something that cannot happen.
func snapshotFor(o outage) *store.ResponseSnapshot {
	if o.status == 0 {
		return nil
	}
	body := `{"error":"service unavailable","request_id":"demo-` +
		o.start.UTC().Format("20060102T150405") + `"}`
	if o.status == 200 {
		body = "<!doctype html><title>Example</title><h1>Internal Server Error</h1>" +
			"<p>The page answered, and said the wrong thing.</p>"
	}
	return &store.ResponseSnapshot{
		Body: body,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Server":       "example-edge",
		},
		Truncated: o.status == 200,
	}
}

// buildBuckets summarises the history that is older than the raw window.
//
// This is what RollupHeartbeats would have left behind had the instance really
// been running for months, and it is the only way a demo can show a year of
// uptime without a year of rows: one hour of a 30-second monitor is 120
// heartbeats or one bucket.
func buildBuckets(
	id int64, m store.Monitor, p profile,
	from, to time.Time, interval time.Duration,
	outages []outage, pl plan,
) []store.HourlyBucket {
	if !from.Before(to) {
		return nil
	}

	var out []store.HourlyBucket
	for h := from.Truncate(time.Hour); h.Before(to); h = h.Add(time.Hour) {
		first := sampleAtOrAfter(h, interval)
		if first.Before(from) {
			first = sampleAtOrAfter(from, interval)
		}
		limit := h.Add(time.Hour)
		if to.Before(limit) {
			limit = to
		}
		beats, down, assessedDown, warnings := 0, 0, 0, 0
		var failing outage
		for at := first; at.Before(limit); at = at.Add(interval) {
			beats++
			if o, isDown := outageAt(outages, at); isDown {
				down++
				if outageAssessment(id, m, o, interval, at) == "down" {
					assessedDown++
				} else {
					warnings++
				}
				failing = o
			}
		}

		if beats == 0 {
			continue
		}

		b := store.HourlyBucket{
			MonitorID: id, Bucket: h,
			Up: beats - down, Down: down,
			AssessedUp: beats - down, AssessedDown: assessedDown, Warning: warnings,
		}

		// Latency is only recorded for beats that were timed, which is the
		// same rule buildBeats applies: an unreachable service has no
		// response time, and a push monitor never had one to begin with.
		timed := b.Up
		if failing.status > 0 {
			timed += down
		}
		if p.baseLatency > 0 && timed > 0 {
			mid := latencyAt(p, h.Add(30*time.Minute), pl, nil)
			b.LatencyAvg = mid
			b.LatencyMin = max(1, mid-p.spread/2)
			b.LatencyMax = mid + p.spread/2
			b.LatencyCount = timed
		}
		out = append(out, b)
	}
	return out
}

// Synthetic assessments follow the same confirmation timestamps as the seeded
// incidents. Earlier failures stay warnings; confirmation never rewrites them.
func outageAssessment(id int64, m store.Monitor, o outage, interval time.Duration, at time.Time) string {
	inc := incidentFor(id, o, m, interval)
	if inc.Confirmed() && !at.Before(inc.ConfirmedAt) {
		return "down"
	}
	return "warning"
}
