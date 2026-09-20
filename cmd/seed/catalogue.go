package main

import (
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// The catalogue is the demo itself: a fictional company's infrastructure,
// chosen so that every screen in SubGlance has something real to show and
// every branch a screen can take is taken by at least one row.
//
// Hostnames are all under example.com, example.net and example.invalid, which
// are reserved for exactly this by RFC 2606 and can never resolve to anything
// belonging to anyone. A seeder that filled a database with real hostnames
// would turn a demo into a small, unannounced load test against strangers.

// channelSpec is one notification channel plus how its deliveries have been
// going lately, which is what the channel health badge reads.
type channelSpec struct {
	channel store.Channel

	// deliveryHealth decides what the seeded outbox rows look like for this
	// channel: healthy channels have delivered rows, a broken one has
	// failures with an error to explain itself, and a slow one has a
	// backlog still pending.
	deliveryHealth health
}

type health int

const (
	healthy health = iota
	broken
	backlogged
)

func channels() []channelSpec {
	return []channelSpec{
		{
			channel: store.Channel{
				Name: "Ops Slack", Type: store.ChannelSlack, Enabled: true,
				Config: map[string]string{
					"url": "https://hooks.slack.example.com/services/T0DEMO/B0DEMO/seeded",
				},
			},
		},
		{
			channel: store.Channel{
				Name: "Incidents Discord", Type: store.ChannelDiscord, Enabled: true,
				Config: map[string]string{
					"url": "https://discord.example.com/api/webhooks/1000/seeded",
				},
			},
		},
		{
			channel: store.Channel{
				Name: "On-call Telegram", Type: store.ChannelTelegram, Enabled: true,
				Config: map[string]string{
					"bot_token": "000000:example-not-a-real-token",
					"chat_id":   "-1000000000",
				},
			},
		},
		{
			channel: store.Channel{
				Name: "Ops mailbox", Type: store.ChannelEmail, Enabled: true,
				Config: map[string]string{
					// No credentials: an internal relay that accepts mail
					// from its own network is the common case, and a seeded
					// username would invite someone to reuse it.
					"host": "smtp.example.com",
					"port": "587",
					"from": "subglance@example.com",
					"to":   "ops@example.com",
				},
			},
		},
		{
			channel: store.Channel{
				Name: "Status page webhook", Type: store.ChannelWebhook, Enabled: true,
				Config: map[string]string{
					"url":     "https://status.example.com/hooks/subglance",
					"headers": "X-Demo: seeded",
				},
			},
		},
		{
			// The one that is failing, so the notifications screen has a
			// channel wearing its error rather than five identical green rows.
			channel: store.Channel{
				Name: "Pager relay", Type: store.ChannelWebhook, Enabled: true,
				Config: map[string]string{"url": "https://pager.example.invalid/hook"},
			},
			deliveryHealth: broken,
		},
		{
			// Enabled, reachable, but slow enough to keep a queue.
			channel: store.Channel{
				Name: "Warehouse webhook", Type: store.ChannelWebhook, Enabled: true,
				Config: map[string]string{"url": "https://warehouse.example.com/events"},
			},
			deliveryHealth: backlogged,
		},
		{
			// Disabled rather than deleted: an off channel is a state the
			// list has to render, and it is how people park an integration.
			channel: store.Channel{
				Name: "Old pager (retired)", Type: store.ChannelWebhook, Enabled: false,
				Config: map[string]string{"url": "https://retired.example.invalid/hook"},
			},
		},
	}
}

// profile says how a monitor has behaved over the seeded history.
type profile struct {
	// baseLatency is the typical response time in milliseconds. Zero means
	// the monitor records no latency at all, which is what a push monitor
	// looks like: nothing was timed, a job simply reported.
	baseLatency int
	// spread is the random variation around the base, peak to peak.
	spread int
	// dailyTrend is milliseconds added per day as history approaches now,
	// for a service that is getting slower rather than failing.
	dailyTrend float64

	// outages are the scripted failures: the ones a demo has to be able to
	// point at. An outage with a zero duration is still running.
	outages []outageSpec

	// randomOutages is how many additional short failures to scatter
	// through the history, so uptime is a believable number rather than a
	// row of hundreds.
	randomOutages int

	// neverChecked leaves the monitor without a single heartbeat, which is
	// the state of a monitor added a moment ago and the only way to see the
	// "no data yet" rendering.
	neverChecked bool

	// silentFor stops the history early. A paused monitor is not being
	// checked, so its beats end when it was paused rather than at now.
	silentFor time.Duration
}

// outageSpec is one failure, described relative to the moment the seeder runs.
type outageSpec struct {
	// ago is when the failure began, counted back from now.
	ago time.Duration
	// dur is how long it lasted. Zero means it is still going, which is the
	// only kind of outage the dashboard shows as red right now.
	dur time.Duration

	// cause is a checker.FailureKind, and message the error a human reads.
	cause   string
	message string

	// unconfirmed marks an outage that never reached the failure threshold:
	// the incident opened and closed without anyone being told. It is the
	// difference between "a check failed" and "you were woken up", and the
	// interface draws them differently.
	unconfirmed bool

	// acked marks an outage someone has acknowledged, which stops reminders
	// without claiming the problem is fixed.
	acked bool

	// reminders is how many repeat alerts have gone out for an open,
	// unacknowledged incident.
	reminders int

	// status is the HTTP status code the failing checks returned, zero when
	// the failure was below HTTP — a refused connection has no status.
	status int
}

// monitorSpec is a monitor, the channels it alerts on, and its history.
type monitorSpec struct {
	monitor store.Monitor
	// channels names entries from channels(); an empty list is a monitor
	// nobody is told about, which is a real and slightly alarming state the
	// interface should be able to show.
	channels []string
	profile  profile
	// createdAgo backdates the monitor. A row with a year of history that
	// claims to have been created this morning reads as a bug in the
	// product rather than an artefact of the seeder.
	createdAgo time.Duration
}

// A failing check's error text, by what went wrong. These are the strings the
// checker produces, kept close enough that a demo screenshot matches what a
// real failure looks like.
const (
	errRefused  = "dial tcp: connect: connection refused"
	errTimeout  = "context deadline exceeded (Client.Timeout exceeded while awaiting headers)"
	errDNS      = "lookup failed: no such host"
	errStatus5  = "unexpected status 503, want 200-299"
	errStatus4  = "unexpected status 502, want 200-299"
	errKeyword  = `expected keyword "ok" not found in response body`
	errCert     = "certificate expires in 6 days, threshold is 21"
	errOverdue  = "no report received within the expected window"
	errReported = "the nightly run exited non-zero"
)

// monitors is the fictional estate. Types, tags, options and states are spread
// across it deliberately: every monitor type, every keyword mode, both
// redirect settings, a custom method with a body and headers, response capture
// on and off, reminders on and off, a paused monitor, a monitor nobody is
// alerted about, and one that has never been checked at all.
func monitors() []monitorSpec {
	const (
		day  = 24 * time.Hour
		hour = time.Hour
		min  = time.Minute
	)

	return []monitorSpec{
		{
			monitor: store.Monitor{
				Name: "Marketing site", Type: string(checker.TypeHTTP),
				Target: "https://www.example.com", IntervalS: 60, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				Keyword: "Get started", KeywordMode: string(checker.KeywordMustContain),
				FollowRedirects: true, SSLWarnDays: 21, Enabled: true, CaptureResponse: true,
				RepeatAfterS: 3600,
				Tags:         map[string]string{"env": "prod", "team": "web", "region": "eu-west"},
			},
			channels:   []string{"Ops Slack", "Status page webhook"},
			createdAgo: 400 * day,
			profile: profile{
				baseLatency: 180, spread: 70, randomOutages: 2,
				outages: []outageSpec{
					{ago: 9 * day, dur: 22 * min, cause: string(checker.FailStatus),
						message: errStatus5, status: 503},
				},
			},
		},
		{
			monitor: store.Monitor{
				Name: "API — health", Type: string(checker.TypeHTTP),
				Target: "https://api.example.com/health", IntervalS: 30, TimeoutS: 5, Retries: 3,
				Method: "GET", ExpectedStatus: "200",
				Keyword: "ok", KeywordMode: string(checker.KeywordMustContain),
				FollowRedirects: true, SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				RepeatAfterS: 1800,
				Tags:         map[string]string{"env": "prod", "team": "platform", "tier": "critical"},
			},
			channels:   []string{"Ops Slack", "On-call Telegram", "Pager relay"},
			createdAgo: 400 * day,
			profile: profile{
				baseLatency: 95, spread: 40, randomOutages: 3,
				outages: []outageSpec{
					{ago: 17 * day, dur: 3 * hour, cause: string(checker.FailKeyword),
						message: errKeyword, status: 200},
					{ago: 2 * day, dur: 6 * min, cause: string(checker.FailTimeout),
						message: errTimeout, unconfirmed: true},
				},
			},
		},
		{
			// The headline outage: down now, confirmed, nobody has answered,
			// and the reminder schedule has escalated three times.
			monitor: store.Monitor{
				Name: "API — checkout", Type: string(checker.TypeHTTP),
				Target: "https://api.example.com/v2/checkout", IntervalS: 60, TimeoutS: 15, Retries: 2,
				Method: "POST", ExpectedStatus: "200,201",
				Body:    `{"probe":true}`,
				Headers: map[string]string{"Content-Type": "application/json", "X-Probe": "subglance"},
				Keyword: "order_id", KeywordMode: string(checker.KeywordMustContain),
				FollowRedirects: true, SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				RepeatAfterS: 900,
				Tags:         map[string]string{"env": "prod", "team": "payments", "tier": "critical"},
			},
			channels:   []string{"Ops Slack", "On-call Telegram", "Incidents Discord", "Pager relay"},
			createdAgo: 300 * day,
			profile: profile{
				baseLatency: 240, spread: 120, randomOutages: 1,
				outages: []outageSpec{
					{ago: 41 * min, cause: string(checker.FailStatus),
						message: errStatus4, status: 502, reminders: 3},
				},
			},
		},
		{
			monitor: store.Monitor{
				Name: "Docs", Type: string(checker.TypeHTTP),
				Target: "https://docs.example.com", IntervalS: 300, TimeoutS: 10, Retries: 1,
				Method: "HEAD", ExpectedStatus: "200-308",
				// Redirects deliberately not followed: this monitor exists to
				// assert that the canonical host answers, not that something
				// eventually does.
				FollowRedirects: false, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 30, Enabled: true, CaptureResponse: false,
				Tags: map[string]string{"env": "prod", "team": "web"},
			},
			channels:   []string{"Ops mailbox"},
			createdAgo: 200 * day,
			profile:    profile{baseLatency: 120, spread: 45, randomOutages: 1},
		},
		{
			monitor: store.Monitor{
				Name: "Admin portal", Type: string(checker.TypeHTTP),
				Target: "https://admin.example.com/sign-in", IntervalS: 120, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				// The inverse keyword mode: the page returning 200 is not the
				// question, the page returning 200 with a stack trace on it is.
				Keyword: "Internal Server Error", KeywordMode: string(checker.KeywordMustNotContain),
				FollowRedirects: true, SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				RepeatAfterS: 7200,
				Tags:         map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 250 * day,
			profile: profile{
				baseLatency: 310, spread: 90, randomOutages: 2,
				outages: []outageSpec{
					{ago: 5 * day, dur: 48 * min, cause: string(checker.FailKeyword),
						message: `forbidden keyword "Internal Server Error" found in response body`, status: 200},
				},
			},
		},
		{
			// Acknowledged and still down: the state that proves acknowledging
			// silences reminders without pretending the problem is solved.
			monitor: store.Monitor{
				Name: "Postgres primary", Type: string(checker.TypeTCP),
				Target: "db.example.com:5432", IntervalS: 60, TimeoutS: 5, Retries: 2,
				Enabled: true, RepeatAfterS: 600,
				Tags: map[string]string{"env": "prod", "team": "platform", "tier": "critical"},
			},
			channels:   []string{"Ops Slack", "On-call Telegram"},
			createdAgo: 400 * day,
			profile: profile{
				baseLatency: 12, spread: 8,
				outages: []outageSpec{
					{ago: 3*hour + 20*min, cause: string(checker.FailConnection),
						message: errRefused, acked: true, reminders: 2},
				},
			},
		},
		{
			monitor: store.Monitor{
				Name: "Redis cache", Type: string(checker.TypeTCP),
				Target: "cache.example.com:6379", IntervalS: 60, TimeoutS: 5, Retries: 2,
				Enabled: true,
				Tags:    map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 400 * day,
			profile:    profile{baseLatency: 4, spread: 3, randomOutages: 1},
		},
		{
			monitor: store.Monitor{
				Name: "SMTP relay", Type: string(checker.TypeTCP),
				Target: "smtp.example.com:587", IntervalS: 300, TimeoutS: 10, Retries: 2,
				Enabled: true,
				Tags:    map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops mailbox"},
			createdAgo: 180 * day,
			profile: profile{
				baseLatency: 28, spread: 12,
				outages: []outageSpec{
					{ago: 21 * day, dur: 95 * min, cause: string(checker.FailTimeout), message: errTimeout},
				},
			},
		},
		{
			// The flapper: short failures every few hours, which is what the
			// flap suppression exists for and what it looks like on a beat bar.
			monitor: store.Monitor{
				Name: "Office uplink", Type: string(checker.TypePing),
				Target: "gateway.example.net", IntervalS: 60, TimeoutS: 5, Retries: 1,
				Enabled: true,
				Tags:    map[string]string{"env": "office", "team": "it"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 120 * day,
			profile: profile{
				baseLatency: 22, spread: 18, randomOutages: 26,
			},
		},
		{
			monitor: store.Monitor{
				Name: "Edge router", Type: string(checker.TypePing),
				Target: "edge.example.net", IntervalS: 60, TimeoutS: 5, Retries: 2,
				Enabled: true,
				Tags:    map[string]string{"env": "prod", "team": "it"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 300 * day,
			profile:    profile{baseLatency: 9, spread: 5},
		},
		{
			monitor: store.Monitor{
				Name: "TLS — www", Type: string(checker.TypeSSL),
				Target: "www.example.com:443", IntervalS: 3600, TimeoutS: 10, Retries: 1,
				SSLWarnDays: 21, Enabled: true,
				Tags: map[string]string{"env": "prod", "team": "web"},
			},
			channels:   []string{"Ops mailbox"},
			createdAgo: 400 * day,
			profile:    profile{baseLatency: 65, spread: 20},
		},
		{
			// A certificate running out is a failure that is nobody's outage:
			// the service is up, the clock is the problem.
			monitor: store.Monitor{
				Name: "TLS — api", Type: string(checker.TypeSSL),
				Target: "api.example.com:443", IntervalS: 3600, TimeoutS: 10, Retries: 1,
				SSLWarnDays: 21, Enabled: true, RepeatAfterS: 21600,
				Tags: map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops Slack", "Ops mailbox"},
			createdAgo: 400 * day,
			profile: profile{
				baseLatency: 70, spread: 20,
				outages: []outageSpec{
					{ago: 30 * hour, cause: string(checker.FailCertExpiry), message: errCert},
				},
			},
		},
		{
			// Push, healthy: a nightly job that has reported on time.
			monitor: store.Monitor{
				Name: "Nightly backup", Type: store.TypePush,
				Target: "", IntervalS: 3600, TimeoutS: 10, Retries: 0,
				Enabled: true, PushIntervalS: 86400, PushGraceS: 3600,
				RepeatAfterS: 21600,
				Tags:         map[string]string{"env": "prod", "team": "platform", "job": "backup"},
			},
			channels:   []string{"Ops Slack", "Ops mailbox"},
			createdAgo: 200 * day,
			profile:    profile{},
		},
		{
			// Push, failing because the job itself said so rather than
			// because it went quiet. The two failure modes read differently
			// and the demo should contain both.
			monitor: store.Monitor{
				Name: "Hourly invoice sync", Type: store.TypePush,
				Target: "", IntervalS: 3600, TimeoutS: 10, Retries: 0,
				Enabled: true, PushIntervalS: 3600, PushGraceS: 300,
				RepeatAfterS: 3600,
				Tags:         map[string]string{"env": "prod", "team": "payments", "job": "sync"},
			},
			channels:   []string{"Ops Slack", "Incidents Discord"},
			createdAgo: 150 * day,
			profile: profile{
				outages: []outageSpec{
					{ago: 34 * day, dur: 4 * hour, cause: string(checker.FailPushReported),
						message: errReported},
				},
			},
		},
		{
			// Push, overdue: the dead man's switch tripping.
			monitor: store.Monitor{
				Name: "Weekly report mailer", Type: store.TypePush,
				Target: "", IntervalS: 3600, TimeoutS: 10, Retries: 0,
				Enabled: true, PushIntervalS: 604800, PushGraceS: 7200,
				Tags: map[string]string{"env": "prod", "team": "finance", "job": "report"},
			},
			channels:   []string{"Ops mailbox"},
			createdAgo: 300 * day,
			profile: profile{
				outages: []outageSpec{
					{ago: 40 * hour, cause: string(checker.FailPushOverdue), message: errOverdue},
				},
			},
		},
		{
			// Getting slower every day without ever failing — the case a beat
			// bar alone cannot show and the latency chart exists for.
			monitor: store.Monitor{
				Name: "Search cluster", Type: string(checker.TypeHTTP),
				Target: "https://search.example.com/_cluster/health", IntervalS: 120, TimeoutS: 20, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				Keyword: "green", KeywordMode: string(checker.KeywordMustContain),
				FollowRedirects: true, SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "team": "search"},
			},
			channels:   []string{"Ops Slack", "Warehouse webhook"},
			createdAgo: 220 * day,
			profile:    profile{baseLatency: 210, spread: 60, dailyTrend: 24},
		},
		{
			monitor: store.Monitor{
				Name: "Object storage", Type: string(checker.TypeHTTP),
				Target: "https://files.example.com/healthz", IntervalS: 120, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 260 * day,
			profile: profile{
				baseLatency: 140, spread: 55,
				outages: []outageSpec{
					{ago: 12 * day, dur: 2*hour + 10*min, cause: string(checker.FailDNS), message: errDNS},
				},
			},
		},
		{
			// Failing right now but not yet confirmed: the pending state, the
			// one that proves the tool waits before it wakes anyone.
			monitor: store.Monitor{
				Name: "Payment callbacks", Type: string(checker.TypeHTTP),
				Target: "https://hooks.example.com/payments", IntervalS: 60, TimeoutS: 10, Retries: 4,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				RepeatAfterS: 1800,
				Tags:         map[string]string{"env": "prod", "team": "payments"},
			},
			channels:   []string{"Ops Slack", "On-call Telegram"},
			createdAgo: 90 * day,
			profile: profile{
				baseLatency: 160, spread: 50,
				outages: []outageSpec{
					{ago: 2 * min, cause: string(checker.FailConnection), message: errRefused,
						unconfirmed: true},
				},
			},
		},
		{
			monitor: store.Monitor{
				Name: "Queue workers", Type: string(checker.TypeHTTP),
				Target: "https://queue.example.com/healthz", IntervalS: 60, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				Keyword: "draining", KeywordMode: string(checker.KeywordMustNotContain),
				FollowRedirects: true, SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "team": "platform"},
			},
			channels:   []string{"Ops Slack", "Warehouse webhook"},
			createdAgo: 140 * day,
			profile:    profile{baseLatency: 75, spread: 30, randomOutages: 2},
		},
		{
			monitor: store.Monitor{
				Name: "CDN edge", Type: string(checker.TypeHTTP),
				Target: "https://cdn.example.com/ping.txt", IntervalS: 60, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: false,
				Tags: map[string]string{"env": "prod", "team": "web", "region": "global"},
			},
			channels:   []string{"Status page webhook"},
			createdAgo: 320 * day,
			profile:    profile{baseLatency: 35, spread: 15, randomOutages: 1},
		},
		{
			monitor: store.Monitor{
				Name: "Identity provider", Type: string(checker.TypeHTTP),
				Target:    "https://id.example.com/.well-known/openid-configuration",
				IntervalS: 300, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				Keyword: "issuer", KeywordMode: string(checker.KeywordMustContain),
				FollowRedirects: true, SSLWarnDays: 30, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "team": "security"},
			},
			channels:   []string{"Ops Slack", "Ops mailbox"},
			createdAgo: 210 * day,
			profile:    profile{baseLatency: 190, spread: 60},
		},
		{
			// Two customers, so the tag grouping has something to group by
			// other than environment.
			monitor: store.Monitor{
				Name: "Acme portal", Type: string(checker.TypeHTTP),
				Target: "https://acme.example.com", IntervalS: 120, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "customer": "Acme", "team": "delivery"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 95 * day,
			profile: profile{
				baseLatency: 260, spread: 80, randomOutages: 2,
				outages: []outageSpec{
					{ago: 26 * day, dur: 55 * min, cause: string(checker.FailStatus),
						message: errStatus5, status: 503},
				},
			},
		},
		{
			monitor: store.Monitor{
				Name: "Globex portal", Type: string(checker.TypeHTTP),
				Target: "https://globex.example.com", IntervalS: 120, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "customer": "Globex", "team": "delivery"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 70 * day,
			profile:    profile{baseLatency: 230, spread: 75, randomOutages: 1},
		},
		{
			// Staging: no channels at all. Nobody is woken for it, and the
			// interface should say so rather than implying an alert path that
			// does not exist.
			monitor: store.Monitor{
				Name: "Staging API", Type: string(checker.TypeHTTP),
				Target: "https://api.staging.example.com/health", IntervalS: 300, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 7, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "staging", "team": "platform"},
			},
			createdAgo: 60 * day,
			profile: profile{
				baseLatency: 145, spread: 90, randomOutages: 6,
				outages: []outageSpec{
					{ago: 4 * day, dur: 9 * hour, cause: string(checker.FailConnection),
						message: errRefused},
				},
			},
		},
		{
			// Paused, with history that stops when it was paused.
			monitor: store.Monitor{
				Name: "Legacy VPN appliance", Type: string(checker.TypeHTTP),
				Target: "https://vpn.example.net/status", IntervalS: 300, TimeoutS: 20, Retries: 3,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 0, Enabled: false, CaptureResponse: false,
				Tags: map[string]string{"env": "office", "team": "it"},
			},
			channels:   []string{"Old pager (retired)"},
			createdAgo: 500 * day,
			profile: profile{
				baseLatency: 420, spread: 160, randomOutages: 3,
				silentFor: 6 * day,
			},
		},
		{
			// Never checked: added a moment ago, no data yet.
			monitor: store.Monitor{
				Name: "New: analytics API", Type: string(checker.TypeHTTP),
				Target: "https://analytics.example.com/health", IntervalS: 60, TimeoutS: 10, Retries: 2,
				Method: "GET", ExpectedStatus: "200-299",
				FollowRedirects: true, KeywordMode: string(checker.KeywordIgnore),
				SSLWarnDays: 14, Enabled: true, CaptureResponse: true,
				Tags: map[string]string{"env": "prod", "team": "data"},
			},
			channels:   []string{"Ops Slack"},
			createdAgo: 4 * min,
			profile:    profile{neverChecked: true},
		},
	}
}
