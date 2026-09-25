// Command seed fills a SubGlance database with a demo estate.
//
// It exists because the product cannot be seen without history. A fresh
// instance is a correct empty screen: no beat bars, no uptime, no incidents,
// no channel health, nothing to acknowledge. Everything SubGlance is for
// becomes visible only after days of real checks, so demonstrating it, judging
// a design change against realistic density, or reproducing a bug that needs a
// year of data all used to mean waiting or hand-writing SQL.
//
// The seeded database is an ordinary one. Point the server at it and every
// screen works, because the data goes in through the store and obeys the same
// schema, the same constraints and the same relationships the running product
// writes.
//
//	go run ./cmd/seed --data-dir ./tmp
//	go run ./cmd/seed --data-dir ./tmp --reset --seed 7
//	go run ./cmd/seed --data-dir ./tmp --secret-key "$(openssl rand -hex 32)"
//
// Then:
//
//	go run ./cmd/subglance --data-dir ./tmp
//
// It refuses to touch a database that already holds monitors unless --reset is
// given, so pointing it at a production data directory by mistake costs
// nothing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // the user asked for usage; not a failure
		}
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

// options are the knobs a seed run has.
type options struct {
	dataDir   string
	dbPath    string
	reset     bool
	seed      uint64
	history   time.Duration
	rawWindow time.Duration
	password  string
	secretKey string
}

func parseFlags(args []string, out io.Writer) (options, error) {
	var o options

	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.dataDir, "data-dir", "./tmp",
		"directory holding the database, the same one the server is given")
	fs.StringVar(&o.dbPath, "db", "",
		"path to the database file, overriding --data-dir")
	fs.BoolVar(&o.reset, "reset", false,
		"delete the existing database first; without this a populated database is refused")
	fs.Uint64Var(&o.seed, "seed", 1,
		"random seed; the same seed produces the same estate, so screenshots stay comparable")
	fs.DurationVar(&o.history, "history", 120*24*time.Hour,
		"how far back the seeded history reaches")
	// A week rather than the server's retention default: it keeps the demo
	// database small enough to reset in seconds, and a week of raw beats is
	// already more than any chart on the dashboard draws at full resolution.
	fs.DurationVar(&o.rawWindow, "raw-window", 7*24*time.Hour,
		"how much of that history is individual heartbeats rather than hourly rollups")
	fs.StringVar(&o.password, "password", "demo-password-123",
		"password for the seeded accounts; must meet the ordinary policy")
	fs.StringVar(&o.secretKey, "secret-key", "",
		"encrypt notification channel configuration, exactly as the server's --secret-key does")

	fs.Usage = func() {
		_, _ = io.WriteString(out, "Usage: seed [flags]\n\n"+
			"Fill a SubGlance database with a demo estate: monitors of every type,\n"+
			"months of heartbeats, incidents in every state, channels, users and tokens.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if o.dbPath == "" {
		o.dbPath = filepath.Join(o.dataDir, "subglance.db")
	}
	if o.history <= 0 {
		return options{}, errors.New("--history must be positive")
	}
	if o.rawWindow <= 0 {
		return options{}, errors.New("--raw-window must be positive")
	}
	if o.rawWindow > o.history {
		// Not an error worth failing on: it only means every beat is raw,
		// which is a legitimate thing to ask for on a short history.
		o.rawWindow = o.history
	}
	return o, nil
}

func run(args []string, out io.Writer) error {
	opts, err := parseFlags(args, out)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opts.reset {
		if err := removeDatabase(opts.dbPath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(opts.dbPath), 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// The key is parsed through the same function the server uses, so a key
	// that works here works there and a bad one is refused with the same
	// message rather than a second opinion.
	secretKey, err := store.ParseSecretKey(opts.secretKey)
	if err != nil {
		return fmt.Errorf("secret-key: %w", err)
	}

	db, err := store.Open(ctx, store.Options{Path: opts.dbPath, SecretKey: secretKey})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := refuseIfPopulated(ctx, db, opts); err != nil {
		return err
	}

	summary, err := seed(ctx, db, opts)
	if err != nil {
		return err
	}
	return summary.report(out, opts)
}

// removeDatabase deletes the database and the two files SQLite keeps beside
// it. Leaving a stale -wal behind would hand the next open a journal for a
// database that no longer exists.
func removeDatabase(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}

// refuseIfPopulated stops a seed run that would add a fictional estate to real
// data.
//
// The check is on monitors and users rather than on the file existing, because
// --data-dir pointed at a directory the server has merely started in once is
// harmless and refusing it would be noise.
func refuseIfPopulated(ctx context.Context, db *store.DB, opts options) error {
	monitors, err := db.ListMonitors(ctx)
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	users, err := db.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	if len(monitors) == 0 && users == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s already holds %d monitor(s) and %d user(s); re-run with --reset to replace it",
		opts.dbPath, len(monitors), users)
}

// summary is what the run did, for the report at the end.
type summary struct {
	monitors   int
	channels   int
	users      int
	tokens     int
	beats      int
	buckets    int
	incidents  int
	open       int
	deliveries int

	// pushURLs are the push tokens, which exist in plaintext exactly once
	// and are printed because a demo of push monitoring needs a URL to curl.
	pushURLs map[string]string
}

// report renders the run into one string and writes it once.
//
// One write rather than a dozen: a summary half-printed because the pipe it
// was going to closed is a worse outcome than no summary, and there is exactly
// one error to consider rather than fifteen to ignore.
func (s summary) report(out io.Writer, opts options) error {
	var b strings.Builder

	fmt.Fprintf(&b, "seeded %s\n\n", opts.dbPath)
	fmt.Fprintf(&b, "  %4d monitors, %d of them down right now\n", s.monitors, s.open)
	fmt.Fprintf(&b, "  %4d notification channels\n", s.channels)
	fmt.Fprintf(&b, "  %4d users, %d API tokens\n", s.users, s.tokens)
	fmt.Fprintf(&b, "  %4d heartbeats over the last %s\n", s.beats, roundDays(opts.rawWindow))
	fmt.Fprintf(&b, "  %4d hourly buckets reaching back %s\n", s.buckets, roundDays(opts.history))
	fmt.Fprintf(&b, "  %4d incidents\n", s.incidents)
	fmt.Fprintf(&b, "  %4d queued, delivered and failed notifications\n", s.deliveries)

	fmt.Fprintf(&b, "\nsign in as %s with the password %q\n", adminEmail, opts.password)

	if len(s.pushURLs) > 0 {
		b.WriteString("\npush URLs (shown once; only their hashes are stored):\n")
		names := make([]string, 0, len(s.pushURLs))
		for name := range s.pushURLs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "  %-22s /api/v1/push/%s\n", name, s.pushURLs[name])
		}
	}

	fmt.Fprintf(&b, "\nnow run: go run ./cmd/subglance --data-dir %s\n", opts.dataDir)

	_, err := io.WriteString(out, b.String())
	return err
}

func roundDays(d time.Duration) string {
	if d < 48*time.Hour {
		return d.Round(time.Hour).String()
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// seed writes the whole estate.
func seed(ctx context.Context, db *store.DB, opts options) (summary, error) {
	now := time.Now().Truncate(time.Second)
	pl := plan{
		now:         now,
		rawSince:    now.Add(-opts.rawWindow),
		bucketSince: now.Add(-opts.history),
	}

	// One generator for the whole run, seeded from the flag. Deterministic
	// on purpose: a demo that reshuffles every time it is rebuilt cannot be
	// compared to the screenshot someone took of it last week — which is
	// also why it must not be crypto/rand.
	//
	// #nosec G404 -- this scatters dots on a chart; nothing here is a secret.
	rnd := rand.New(rand.NewPCG(opts.seed, 0x5e4d))

	out := summary{pushURLs: map[string]string{}}

	users, err := seedUsers(ctx, db, opts)
	if err != nil {
		return summary{}, err
	}
	out.users = len(users)

	tokens, err := seedTokens(ctx, db, users)
	if err != nil {
		return summary{}, err
	}
	out.tokens = tokens

	chans, err := seedChannels(ctx, db)
	if err != nil {
		return summary{}, err
	}
	out.channels = len(chans)

	for _, spec := range monitors() {
		m := spec.monitor
		m.CreatedAt = now.Add(-spec.createdAgo)

		created, err := db.CreateMonitor(ctx, m)
		if err != nil {
			return summary{}, fmt.Errorf("create monitor %q: %w", m.Name, err)
		}
		out.monitors++

		if created.PushToken != "" {
			out.pushURLs[created.Name] = created.PushToken
		}

		// CreateMonitor stamps now, which would leave a monitor carrying a
		// year of history and claiming to be minutes old.
		if err := db.SeedMonitorTimestamps(ctx, created.ID, m.CreatedAt, m.CreatedAt); err != nil {
			return summary{}, err
		}
		created.CreatedAt = m.CreatedAt

		if err := assignChannels(ctx, db, created.ID, spec.channels, chans); err != nil {
			return summary{}, fmt.Errorf("assign channels to %q: %w", m.Name, err)
		}

		h := buildHistory(created, spec.profile, created.ID, pl, rnd)

		if err := db.SeedHourlyBuckets(ctx, h.buckets); err != nil {
			return summary{}, fmt.Errorf("seed buckets for %q: %w", m.Name, err)
		}
		if err := db.SeedHeartbeats(ctx, h.beats); err != nil {
			return summary{}, fmt.Errorf("seed heartbeats for %q: %w", m.Name, err)
		}
		out.buckets += len(h.buckets)
		out.beats += len(h.beats)

		for _, inc := range h.incidents {
			stored, err := db.SeedIncident(ctx, inc)
			if err != nil {
				return summary{}, fmt.Errorf("seed incident for %q: %w", m.Name, err)
			}
			out.incidents++
			if !stored.Resolved() {
				out.open++
			}

			n, err := seedDeliveries(ctx, db, created, stored, spec.channels, chans, now)
			if err != nil {
				return summary{}, fmt.Errorf("seed deliveries for %q: %w", m.Name, err)
			}
			out.deliveries += n
		}
	}

	return out, nil
}

// The demo accounts. Three, because the role a person has changes what the
// interface offers them, and a demo that only ever signs in as an
// administrator never shows that.
const (
	adminEmail  = "admin@example.com"
	editorEmail = "editor@example.com"
	viewerEmail = "viewer@example.com"
)

func seedUsers(ctx context.Context, db *store.DB, opts options) (map[string]store.User, error) {
	out := map[string]store.User{}

	admin, err := db.CreateFirstUser(ctx, adminEmail, opts.password, store.RoleAdmin)
	if err != nil {
		return nil, fmt.Errorf("create administrator: %w", err)
	}
	out[adminEmail] = admin

	for email, role := range map[string]store.Role{
		editorEmail: store.RoleEditor,
		viewerEmail: store.RoleViewer,
	} {
		u, err := db.CreateUser(ctx, email, opts.password, role)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", email, err)
		}
		out[email] = u
	}
	return out, nil
}

// seedTokens issues API tokens in the three states the token list renders:
// live, expiring, and revoked.
func seedTokens(ctx context.Context, db *store.DB, users map[string]store.User) (int, error) {
	admin := users[adminEmail]
	now := time.Now()

	if _, _, err := db.CreateAPIToken(ctx, admin.ID, "CI status badge", nil); err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}

	expires := now.Add(30 * 24 * time.Hour)
	if _, _, err := db.CreateAPIToken(ctx, admin.ID, "Grafana datasource", &expires); err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}

	_, revoked, err := db.CreateAPIToken(ctx, admin.ID, "Laptop (revoked)", nil)
	if err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}
	if err := db.RevokeAPIToken(ctx, revoked.ID, admin.ID); err != nil {
		return 0, fmt.Errorf("revoke token: %w", err)
	}

	if _, _, err := db.CreateAPIToken(ctx, users[editorEmail].ID, "Deploy pipeline", nil); err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}
	return 4, nil
}

func seedChannels(ctx context.Context, db *store.DB) (map[string]store.Channel, error) {
	out := map[string]store.Channel{}
	for _, spec := range channels() {
		c, err := db.CreateChannel(ctx, spec.channel)
		if err != nil {
			return nil, fmt.Errorf("create channel %q: %w", spec.channel.Name, err)
		}
		out[c.Name] = c
	}
	return out, nil
}

func assignChannels(ctx context.Context, db *store.DB, monitorID int64, names []string, chans map[string]store.Channel) error {
	if len(names) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		c, ok := chans[name]
		if !ok {
			return fmt.Errorf("no seeded channel named %q", name)
		}
		ids = append(ids, c.ID)
	}
	return db.SetMonitorChannels(ctx, monitorID, ids)
}

// seedDeliveries writes the outbox rows an incident would have produced.
//
// One row per channel per alert, which is how the notifier works: an alert for
// a monitor with three channels is three independent deliveries, so a broken
// Slack webhook cannot hold up the e-mail going to whoever is actually on
// call. Reproducing that here is what gives the notifications screen its
// per-channel health.
func seedDeliveries(
	ctx context.Context, db *store.DB,
	m store.Monitor, inc store.Incident,
	names []string, chans map[string]store.Channel,
	now time.Time,
) (int, error) {
	if !inc.Confirmed() {
		// An incident that never passed the failure threshold never
		// alerted anyone, which is the whole point of the threshold.
		return 0, nil
	}

	type alertMoment struct {
		event state.Event
		at    time.Time
	}
	moments := []alertMoment{{state.EventIncidentConfirmed, inc.ConfirmedAt}}
	for i := 1; i <= inc.ReminderCount; i++ {
		gap := time.Duration(m.RepeatAfterS) * time.Second
		if gap <= 0 {
			gap = time.Hour
		}
		moments = append(moments, alertMoment{state.EventIncidentReminder, inc.ConfirmedAt.Add(time.Duration(i) * gap)})
	}
	if inc.Resolved() {
		moments = append(moments, alertMoment{state.EventIncidentResolved, inc.ResolvedAt})
	}

	written := 0
	for _, name := range names {
		ch, ok := chans[name]
		if !ok {
			return 0, fmt.Errorf("no seeded channel named %q", name)
		}
		if !ch.Enabled {
			// A disabled channel is not attempted, so it has no outbox
			// rows and no health to show. Writing them anyway would
			// make the interface report deliveries that never happened.
			continue
		}

		for _, moment := range moments {
			alert := notifier.AlertFromStore(m, inc, moment.event, moment.at)
			payload, err := json.Marshal(alert)
			if err != nil {
				return 0, fmt.Errorf("render alert: %w", err)
			}

			d := store.Delivery{
				ChannelID:  ch.ID,
				MonitorID:  m.ID,
				IncidentID: inc.ID,
				Event:      string(moment.event),
				Payload:    string(payload),
				CreatedAt:  moment.at,
			}
			applyDeliveryHealth(&d, deliveryHealthOf(name), moment.at, now)

			if _, err := db.SeedDelivery(ctx, d); err != nil {
				return 0, err
			}
			written++
		}
	}
	return written, nil
}

// deliveryHealthOf looks up how a named channel has been behaving.
func deliveryHealthOf(name string) health {
	for _, spec := range channels() {
		if spec.channel.Name == name {
			return spec.deliveryHealth
		}
	}
	return healthy
}

// applyDeliveryHealth puts a delivery into the state its channel is in.
//
// A queued delivery is dated in the future rather than in the past it belongs
// to, because the seeded database is meant to be handed to a running server:
// a backlog whose next attempt is already overdue is drained within seconds of
// startup, and the screen that was supposed to show a queue shows an empty one
// before anybody has looked at it.
func applyDeliveryHealth(d *store.Delivery, h health, at, now time.Time) {
	switch h {
	case broken:
		d.Status = store.OutboxFailed
		d.Attempts = 5
		d.LastError = "Post \"https://pager.example.invalid/hook\": lookup failed: no such host"
		d.UpdatedAt = at.Add(11 * time.Minute)
		d.NextAttemptAt = d.UpdatedAt
	case backlogged:
		d.Status = store.OutboxPending
		d.Attempts = 2
		d.LastError = "unexpected status 503"
		d.UpdatedAt = at.Add(3 * time.Minute)
		d.NextAttemptAt = now.Add(30 * time.Minute)
	default:
		d.Status = store.OutboxDelivered
		d.Attempts = 1
		d.UpdatedAt = at.Add(2 * time.Second)
		d.NextAttemptAt = at
	}
}
