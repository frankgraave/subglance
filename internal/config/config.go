// Package config loads SubGlance configuration from flags and the environment.
//
// Precedence, highest first: command-line flags, environment variables,
// built-in defaults. Every option has a working default so that SubGlance
// starts with no configuration at all — that is a product principle, not a
// convenience (product principle §3.2).
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
	"github.com/frankgraave/subglance/internal/trustedproxy"
	"github.com/frankgraave/subglance/internal/watchdog"
)

// Config holds every runtime setting.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string

	// DataDir holds the SQLite database and any other persistent state.
	DataDir string

	// LogLevel is one of: debug, info, warn, error.
	LogLevel string

	// LogFormat is either "text" (human) or "json" (machine).
	LogFormat string

	// ShutdownTimeout bounds how long in-flight requests may finish
	// after a shutdown signal.
	ShutdownTimeout time.Duration

	// CheckWorkers caps concurrently running checks. Zero means auto
	// (derived from CPU count at scheduler start).
	CheckWorkers int

	// WatchdogURL is an external dead man's switch that SubGlance pings
	// while it is demonstrably still checking things. Empty disables it.
	//
	// Off by default because it is the one thing in SubGlance that talks
	// outbound to a third party; that has to be a deliberate act, never a
	// surprise found in a packet capture.
	WatchdogURL string

	// WatchdogInterval is the gap between watchdog pings.
	WatchdogInterval time.Duration

	// AllowPrivateTargets permits monitoring of private/loopback/link-local
	// addresses. Off by default: without it, a user-supplied URL turns
	// SubGlance into an SSRF proxy into the host network (see SUB-18).
	AllowPrivateTargets bool

	// RawRetention is how long individual heartbeats are kept before being
	// folded into hourly buckets. Short windows suit a small VPS; long ones
	// keep full resolution at the cost of disk.
	RawRetention time.Duration

	// RollupRetention is how long hourly buckets and resolved incidents are
	// kept. Zero means keep them forever, which is what SubGlance did before
	// this option existed.
	RollupRetention time.Duration

	// AlertGroupWindow is how long an alert waits for others before it is
	// sent, so that one outage across many monitors becomes one message
	// instead of one per monitor.
	//
	// Zero turns grouping off and delivers every alert the moment it
	// happens. That is the right setting for someone watching three
	// services, who has nothing to group and would only be paying the
	// delay; it is the wrong setting for a fleet, where the burst is the
	// reason people mute the channel.
	//
	// The default follows the check schedule rather than taste: monitors on
	// a 60-second interval do not fail in the same second, they fail across
	// the following minute as each one's turn comes round.
	AlertGroupWindow time.Duration

	// TrustedProxies lists the peers whose X-Forwarded-For and X-Real-Ip
	// headers may be believed, as a comma-separated list of addresses and
	// CIDR blocks. Empty means believe nobody.
	//
	// Empty by default because those headers are set by whoever sends them.
	// Trusting one unconditionally means a caller can name its own address,
	// and the login rate limiter keys on that address — so rotating the
	// header per request is credential spraying with the limiter off. An
	// operator running behind a reverse proxy names it here and gets real
	// client addresses back in the limiter and the session log.
	TrustedProxies string
}

// DBPath returns the full path to the SQLite database file.
func (c Config) DBPath() string {
	return strings.TrimRight(c.DataDir, "/") + "/subglance.db"
}

// NotifierGroupWindow translates the configured window into the value the
// notifier expects.
//
// The two disagree about zero on purpose. To an operator, "0" plainly means
// off; inside Options, zero has to mean "use the default", because that is how
// every other field there behaves and an Options literal that omits a field
// must keep working. Doing the translation here, once, is cheaper than making
// either side surprising.
func (c Config) NotifierGroupWindow() time.Duration {
	if c.AlertGroupWindow <= 0 {
		return notifier.GroupingDisabled
	}
	return c.AlertGroupWindow
}

func defaults() Config {
	return Config{
		Addr:                ":8080",
		DataDir:             "/data",
		LogLevel:            "info",
		LogFormat:           "text",
		ShutdownTimeout:     15 * time.Second,
		CheckWorkers:        0,
		WatchdogURL:         "",
		WatchdogInterval:    watchdog.DefaultInterval,
		AllowPrivateTargets: false,
		RawRetention:        store.DefaultRawRetention,
		RollupRetention:     store.DefaultRollupRetention,
		AlertGroupWindow:    notifier.DefaultGroupWindow,
		TrustedProxies:      "",
	}
}

// Load builds a Config from defaults, then environment, then the given
// command-line arguments. It returns flag.ErrHelp when the user asked for
// usage, which the caller should treat as a clean exit.
func Load(args []string) (Config, error) {
	c := defaults()

	// Environment first, so that flags can still override it.
	c.Addr = envStr("SUBGLANCE_ADDR", c.Addr)
	c.DataDir = envStr("SUBGLANCE_DATA_DIR", c.DataDir)
	c.LogLevel = envStr("SUBGLANCE_LOG_LEVEL", c.LogLevel)
	c.LogFormat = envStr("SUBGLANCE_LOG_FORMAT", c.LogFormat)
	c.ShutdownTimeout = envDur("SUBGLANCE_SHUTDOWN_TIMEOUT", c.ShutdownTimeout)
	c.CheckWorkers = envInt("SUBGLANCE_CHECK_WORKERS", c.CheckWorkers)
	c.WatchdogURL = envStr("SUBGLANCE_WATCHDOG_URL", c.WatchdogURL)
	c.WatchdogInterval = envDur("SUBGLANCE_WATCHDOG_INTERVAL", c.WatchdogInterval)
	c.AllowPrivateTargets = envBool("SUBGLANCE_ALLOW_PRIVATE_TARGETS", c.AllowPrivateTargets)
	c.RawRetention = envDur("SUBGLANCE_RAW_RETENTION", c.RawRetention)
	c.RollupRetention = envDur("SUBGLANCE_ROLLUP_RETENTION", c.RollupRetention)
	groupWindow, err := envDurStrict("SUBGLANCE_ALERT_GROUP_WINDOW", c.AlertGroupWindow)
	if err != nil {
		return Config{}, err
	}
	c.AlertGroupWindow = groupWindow
	c.TrustedProxies = envStr("SUBGLANCE_TRUSTED_PROXIES", c.TrustedProxies)

	fs := flag.NewFlagSet("subglance", flag.ContinueOnError)
	fs.StringVar(&c.Addr, "addr", c.Addr, "HTTP listen address")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "directory for the database and persistent state")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "log level: debug, info, warn, error")
	fs.StringVar(&c.LogFormat, "log-format", c.LogFormat, "log format: text or json")
	fs.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "how long to let in-flight requests finish")
	fs.IntVar(&c.CheckWorkers, "check-workers", c.CheckWorkers, "max concurrent checks (0 = auto)")
	fs.StringVar(&c.WatchdogURL, "watchdog-url", c.WatchdogURL,
		"external dead man's switch to ping while checks are running (empty = off)")
	fs.DurationVar(&c.WatchdogInterval, "watchdog-interval", c.WatchdogInterval,
		"how often to ping the watchdog URL")
	fs.BoolVar(&c.AllowPrivateTargets, "allow-private-targets", c.AllowPrivateTargets,
		"allow monitoring private/loopback addresses (SSRF risk, off by default)")
	fs.DurationVar(&c.RawRetention, "raw-retention", c.RawRetention,
		"how long raw heartbeats are kept before being rolled up into hourly buckets")
	fs.DurationVar(&c.RollupRetention, "rollup-retention", c.RollupRetention,
		"how long hourly buckets and resolved incidents are kept (0 = forever)")
	fs.DurationVar(&c.AlertGroupWindow, "alert-group-window", c.AlertGroupWindow,
		"how long an alert waits for others so one outage sends one message (0 = send immediately)")
	fs.StringVar(&c.TrustedProxies, "trusted-proxies", c.TrustedProxies,
		"comma-separated addresses or CIDR blocks whose X-Forwarded-For may be believed (empty = none)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.Addr == "" {
		return fmt.Errorf("addr must not be empty")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data-dir must not be empty")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log-level %q: want debug, info, warn or error", c.LogLevel)
	}
	switch c.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("invalid log-format %q: want text or json", c.LogFormat)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown-timeout must be positive, got %s", c.ShutdownTimeout)
	}
	if c.CheckWorkers < 0 {
		return fmt.Errorf("check-workers must not be negative, got %d", c.CheckWorkers)
	}
	if c.RawRetention <= 0 {
		return fmt.Errorf("raw-retention must be positive, got %s", c.RawRetention)
	}
	if c.RollupRetention < 0 {
		return fmt.Errorf("rollup-retention must not be negative, got %s", c.RollupRetention)
	}
	// Hourly buckets are only worth anything once the raw beats behind them
	// are gone. A rollup window inside the raw one would delete a bucket that
	// still has its own heartbeats sitting next to it, so the history would
	// jump back into existence on the next read and vanish again on the next
	// rollup.
	if c.RollupRetention > 0 && c.RollupRetention < c.RawRetention {
		return fmt.Errorf("rollup-retention (%s) must be at least raw-retention (%s)",
			c.RollupRetention, c.RawRetention)
	}
	if c.AlertGroupWindow < 0 {
		return fmt.Errorf("alert-group-window must not be negative, got %s (use 0 to send alerts immediately)", c.AlertGroupWindow)
	}
	if c.TrustedProxies != "" {
		if err := trustedproxy.Validate(c.TrustedProxies); err != nil {
			return err
		}
	}
	if c.WatchdogURL != "" {
		if err := watchdog.ValidateURL(c.WatchdogURL); err != nil {
			return err
		}
		if c.WatchdogInterval <= 0 {
			return fmt.Errorf("watchdog-interval must be positive, got %s", c.WatchdogInterval)
		}
	}
	return nil
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// envDurStrict reads a duration from the environment and reports a malformed
// value instead of falling back to the default. An operator who mistypes a
// duration wants to hear about it, not to run with a window they never asked
// for.
func envDurStrict(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return d, nil
}

func envDur(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
