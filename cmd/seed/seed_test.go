package main

import (
	"context"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The seeder's one substantive promise is that the data it writes agrees with
// itself: every failed heartbeat falls inside an incident, every incident
// covers failing heartbeats, and nothing claims a state the product cannot
// reach. These tests hold it to that, because a demo whose screens contradict
// each other teaches whoever is looking that the product is broken.

func testPlan(now time.Time) plan {
	return plan{
		now:         now,
		rawSince:    now.Add(-7 * 24 * time.Hour),
		bucketSince: now.Add(-60 * 24 * time.Hour),
	}
}

func TestHistoryAgreesWithIncidents(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pl := testPlan(now)

	for _, spec := range monitors() {
		if spec.profile.neverChecked {
			continue
		}
		m := spec.monitor
		m.CreatedAt = now.Add(-spec.createdAgo)
		store.ApplyMonitorDefaults(&m)

		rnd := rand.New(rand.NewPCG(3, 4))
		h := buildHistory(m, spec.profile, 1, pl, rnd)

		for _, hb := range h.beats {
			covered := false
			for _, inc := range h.incidents {
				if !hb.TS.Before(inc.StartedAt) &&
					(inc.ResolvedAt.IsZero() || hb.TS.Before(inc.ResolvedAt)) {
					covered = true
					break
				}
			}
			if !hb.OK && !covered {
				t.Fatalf("%s: failing heartbeat at %s falls outside every incident", m.Name, hb.TS)
			}
			if hb.OK && covered {
				t.Fatalf("%s: passing heartbeat at %s falls inside an incident", m.Name, hb.TS)
			}
		}
	}
}

func TestIncidentsAreReachableStates(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pl := testPlan(now)

	for _, spec := range monitors() {
		m := spec.monitor
		m.CreatedAt = now.Add(-spec.createdAgo)
		store.ApplyMonitorDefaults(&m)

		h := buildHistory(m, spec.profile, 1, pl, rand.New(rand.NewPCG(5, 6)))

		open := 0
		for _, inc := range h.incidents {
			if !inc.Resolved() {
				open++
			}
			if inc.Confirmed() && inc.ConfirmedAt.Before(inc.StartedAt) {
				t.Errorf("%s: incident confirmed before it started", m.Name)
			}
			if inc.Resolved() && inc.ResolvedAt.Before(inc.StartedAt) {
				t.Errorf("%s: incident resolved before it started", m.Name)
			}
			if inc.Acked() && !inc.Confirmed() {
				t.Errorf("%s: an unconfirmed incident cannot have been acknowledged", m.Name)
			}
			// The store refuses to remind an acknowledged incident, so a
			// seeded one carrying both would be a state the running
			// product can never produce.
			if inc.ReminderCount > 0 && inc.Acked() {
				t.Errorf("%s: acknowledged incident carries %d reminders", m.Name, inc.ReminderCount)
			}
			if inc.ReminderCount > 0 && !inc.Confirmed() {
				t.Errorf("%s: unconfirmed incident carries reminders", m.Name)
			}
		}
		// The schema allows one unresolved incident per monitor, enforced
		// by a partial unique index. Producing two would fail the insert.
		if open > 1 {
			t.Errorf("%s: %d incidents left open, at most one is storable", m.Name, open)
		}
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pl := testPlan(now)
	spec := monitors()[0]
	m := spec.monitor
	m.CreatedAt = now.Add(-spec.createdAgo)
	store.ApplyMonitorDefaults(&m)

	first := buildHistory(m, spec.profile, 1, pl, rand.New(rand.NewPCG(9, 9)))
	second := buildHistory(m, spec.profile, 1, pl, rand.New(rand.NewPCG(9, 9)))

	if len(first.beats) != len(second.beats) || len(first.incidents) != len(second.incidents) {
		t.Fatalf("same seed produced different history: %d/%d beats, %d/%d incidents",
			len(first.beats), len(second.beats), len(first.incidents), len(second.incidents))
	}
	for i := range first.beats {
		if first.beats[i] != second.beats[i] && first.beats[i].Response == nil {
			t.Fatalf("beat %d differs between runs with the same seed", i)
		}
	}
}

func TestPausedMonitorStopsReporting(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pl := testPlan(now)

	var found bool
	for _, spec := range monitors() {
		if spec.profile.silentFor == 0 {
			continue
		}
		found = true
		m := spec.monitor
		m.CreatedAt = now.Add(-spec.createdAgo)
		store.ApplyMonitorDefaults(&m)

		h := buildHistory(m, spec.profile, 1, pl, rand.New(rand.NewPCG(1, 1)))
		if len(h.beats) == 0 {
			t.Fatalf("%s: expected some history before it was paused", m.Name)
		}
		newest := h.beats[len(h.beats)-1].TS
		if newest.After(now.Add(-spec.profile.silentFor)) {
			t.Errorf("%s: newest beat %s is after it stopped being checked", m.Name, newest)
		}
	}
	if !found {
		t.Fatal("no paused monitor in the catalogue; this test guards a case that must exist")
	}
}

func TestCatalogueCoversEveryFeature(t *testing.T) {
	types := map[string]bool{}
	keywordModes := map[string]bool{}
	channelTypes := map[string]bool{}
	tagKeys := map[string]bool{}

	var (
		paused, unmonitored, neverChecked  bool
		withBody, withHeaders, noRedirects bool
		capture, noCapture, reminders      bool
	)

	for _, spec := range monitors() {
		m := spec.monitor
		types[m.Type] = true
		if m.KeywordMode != "" {
			keywordModes[m.KeywordMode] = true
		}
		for k := range m.Tags {
			tagKeys[k] = true
		}
		paused = paused || !m.Enabled
		unmonitored = unmonitored || len(spec.channels) == 0
		neverChecked = neverChecked || spec.profile.neverChecked
		withBody = withBody || m.Body != ""
		withHeaders = withHeaders || len(m.Headers) > 0
		noRedirects = noRedirects || (m.Type == "http" && !m.FollowRedirects)
		capture = capture || m.CaptureResponse
		noCapture = noCapture || (m.Type == "http" && !m.CaptureResponse)
		reminders = reminders || m.RepeatAfterS > 0
	}

	for _, want := range []string{"http", "tcp", "ping", "ssl", store.TypePush} {
		if !types[want] {
			t.Errorf("no %s monitor in the catalogue", want)
		}
	}
	for _, want := range []string{"absent_ok", "must_contain", "must_not_contain"} {
		if !keywordModes[want] {
			t.Errorf("no monitor uses keyword mode %s", want)
		}
	}
	for _, want := range []string{"env", "team", "customer"} {
		if !tagKeys[want] {
			t.Errorf("no monitor carries the %q tag", want)
		}
	}

	for _, spec := range channels() {
		channelTypes[spec.channel.Type] = true
	}
	for _, want := range []string{
		store.ChannelWebhook, store.ChannelDiscord, store.ChannelSlack,
		store.ChannelTelegram, store.ChannelEmail,
	} {
		if !channelTypes[want] {
			t.Errorf("no %s channel in the catalogue", want)
		}
	}

	for name, ok := range map[string]bool{
		"a paused monitor":                 paused,
		"a monitor with no channels":       unmonitored,
		"a monitor that was never checked": neverChecked,
		"a request body":                   withBody,
		"custom request headers":           withHeaders,
		"redirects switched off":           noRedirects,
		"response capture on":              capture,
		"response capture off":             noCapture,
		"repeat reminders":                 reminders,
	} {
		if !ok {
			t.Errorf("the catalogue has no example of %s", name)
		}
	}
}

// TestSeedTargetsAreReserved keeps the estate pointed at names that cannot
// belong to anyone. A seeder shipped with a real hostname in it turns every
// demo into an unannounced load test against a stranger.
func TestSeedTargetsAreReserved(t *testing.T) {
	reserved := []string{"example.com", "example.net", "example.org", "example.invalid"}

	check := func(what, value string) {
		if value == "" {
			return
		}
		for _, suffix := range reserved {
			if strings.Contains(value, suffix) {
				return
			}
		}
		t.Errorf("%s points at %q, which is not a reserved example domain", what, value)
	}

	for _, spec := range monitors() {
		if spec.monitor.Type == store.TypePush {
			continue // reported to, never dialled
		}
		check("monitor "+spec.monitor.Name, spec.monitor.Target)
	}
	for _, spec := range channels() {
		for key, value := range spec.channel.Config {
			if key == "url" || key == "host" {
				check("channel "+spec.channel.Name, value)
			}
		}
	}
}

// TestSeedEndToEnd runs the command against a real database and reads the
// result back through the ordinary store API, which is the only way to know
// the rows satisfy every CHECK constraint the schema carries.
func TestSeedEndToEnd(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder

	args := []string{
		"--data-dir", dir,
		// Short windows: the point is that it runs, not how much it writes.
		"--history", "72h",
		"--raw-window", "6h",
	}
	if err := run(args, &out); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "subglance.db")})
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ms, err := db.ListMonitors(ctx)
	if err != nil {
		t.Fatalf("list monitors: %v", err)
	}
	if len(ms) != len(monitors()) {
		t.Fatalf("stored %d monitors, catalogue has %d", len(ms), len(monitors()))
	}

	open, err := db.ListOpenIncidents(ctx)
	if err != nil {
		t.Fatalf("list open incidents: %v", err)
	}
	if len(open) == 0 {
		t.Error("nothing is down in the seeded estate; the dashboard's whole point is unshown")
	}

	cs, err := db.ListChannels(ctx)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(cs) != len(channels()) {
		t.Fatalf("stored %d channels, catalogue has %d", len(cs), len(channels()))
	}

	users, err := db.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("stored %d users, want 3", len(users))
	}

	// A monitor with history must report uptime, or every number on the
	// dashboard is a dash.
	var checked bool
	for _, m := range ms {
		st, err := db.Uptime(ctx, m.ID, 24*time.Hour)
		if err != nil {
			t.Fatalf("uptime for %s: %v", m.Name, err)
		}
		if st.Total > 0 {
			checked = true
			// Zero is legitimate: a monitor that has been failing all
			// day is exactly what the dashboard exists to show.
			if st.Percentage < 0 || st.Percentage > 100 {
				t.Errorf("%s: uptime %.2f%% is not a percentage", m.Name, st.Percentage)
			}
		}
	}
	if !checked {
		t.Error("no monitor has any heartbeats in the last day")
	}

	if !strings.Contains(out.String(), "sign in as "+adminEmail) {
		t.Errorf("the summary does not say how to sign in:\n%s", out.String())
	}
}

// TestSeedRefusesPopulatedDatabase is the guard that makes pointing this at a
// production data directory a mistake with no consequences.
func TestSeedRefusesPopulatedDatabase(t *testing.T) {
	dir := t.TempDir()
	args := []string{"--data-dir", dir, "--history", "24h", "--raw-window", "2h"}

	var first strings.Builder
	if err := run(args, &first); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	var second strings.Builder
	err := run(args, &second)
	if err == nil {
		t.Fatal("seeding an already-populated database was allowed")
	}
	if !strings.Contains(err.Error(), "--reset") {
		t.Errorf("the refusal does not name the way out: %v", err)
	}

	var third strings.Builder
	if err := run(append(args, "--reset"), &third); err != nil {
		t.Fatalf("seed with --reset: %v", err)
	}
}

// TestSeedWithSecretKey covers the half of SUB-118 a seeded database has to
// satisfy: channel config encrypted at rest must still read back as the map
// the notifier expects.
func TestSeedWithSecretKey(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("a1", store.SecretKeyLength)

	var out strings.Builder
	args := []string{
		"--data-dir", dir, "--history", "24h", "--raw-window", "2h",
		"--secret-key", key,
	}
	if err := run(args, &out); err != nil {
		t.Fatalf("seed with a secret key: %v", err)
	}

	ctx := context.Background()
	parsed, err := store.ParseSecretKey(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	db, err := store.Open(ctx, store.Options{
		Path: filepath.Join(dir, "subglance.db"), SecretKey: parsed,
	})
	if err != nil {
		t.Fatalf("open encrypted database: %v", err)
	}
	defer func() { _ = db.Close() }()

	cs, err := db.ListChannels(ctx)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	for _, c := range cs {
		if len(c.Config) == 0 {
			t.Errorf("channel %q came back with no configuration", c.Name)
		}
	}
}
