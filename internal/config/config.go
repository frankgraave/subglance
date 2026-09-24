// Package config loads SubGlance configuration from flags and the environment.
//
// Precedence, highest first: command-line flags, environment variables,
// built-in defaults. Every option has a working default so that SubGlance
// starts with no configuration at all — that is a product principle, not a
// convenience (product principle §3.2).
package config

import (
	"errors"
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

	// SecretKey encrypts notification channel configuration at rest. It is
	// either the key material itself or the path to a file holding it.
	//
	// **Empty by default, and empty means no encryption: webhook URLs, bot
	// tokens and SMTP passwords are stored in plain text.** That is stated
	// here as plainly as in the documentation, because it is the setting
	// almost every instance runs with.
	//
	// It is not defaulted to something automatic. The rejected alternative
	// was a key file created next to subglance.db on first start, which would
	// have made encryption the default — and would have travelled inside
	// every backup and every copy of the data volume, so it would have
	// protected nothing against the one attacker this feature is about while
	// looking like it did. An explicit setting that is off is more honest
	// than an automatic one that is hollow.
	//
	// Prefer the file form over the environment variable: an environment
	// variable is readable in /proc/<pid>/environ and in `docker inspect`,
	// where it outlives the process in the container's stored config.
	SecretKey string

	// PreviousSecretKey is the key the stored rows are currently encrypted
	// with, when that is not SecretKey. It exists because refusing to start
	// on a key mismatch is only defensible if there is a deliberate way to
	// change the key:
	//
	//   rotate: --secret-key-previous OLD --secret-key NEW
	//   disable: --secret-key-previous OLD and no --secret-key
	//
	// Both rewrite every row once, at startup, in one transaction. It is
	// meant to be passed for that one start and then removed, which is why
	// nothing warns about leaving it set — the reconciliation is idempotent,
	// so a stale value is inert rather than harmful.
	PreviousSecretKey string
}

// DBPath returns the full path to the SQLite database file.
func (c Config) DBPath() string {
	return strings.TrimRight(c.DataDir, "/") + "/subglance.db"
}

// ResolveSecretKey turns the configured --secret-key into key material.
//
// It is a method rather than a field filled in by Load because the value may
// be a file path, and a file read at validate time and again at Open time is
// a file the operator can fix without restarting twice. It returns nil when no
// key is configured, which is what store.Options treats as "no encryption".
func (c Config) ResolveSecretKey() ([]byte, error) {
	return store.ParseSecretKey(c.SecretKey)
}

// ResolvePreviousSecretKey does the same for --secret-key-previous.
func (c Config) ResolvePreviousSecretKey() ([]byte, error) {
	return store.ParseSecretKey(c.PreviousSecretKey)
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
		SecretKey:           "",
		PreviousSecretKey:   "",
	}
}

// Load builds a Config from defaults, then environment, then the given
// command-line arguments. It returns flag.ErrHelp when the user asked for
// usage, which the caller should treat as a clean exit.
func Load(args []string) (Config, error) {
	c := defaults()

	// Environment first, so that flags can still override it.
	//
	// The reads are collected rather than early-returned so that an operator
	// who mistyped two variables is told about both in one run, instead of
	// fixing one, restarting, and being told about the next.
	var env envErrors
	c.Addr = envStr("SUBGLANCE_ADDR", c.Addr)
	c.DataDir = envStr("SUBGLANCE_DATA_DIR", c.DataDir)
	c.LogLevel = envStr("SUBGLANCE_LOG_LEVEL", c.LogLevel)
	c.LogFormat = envStr("SUBGLANCE_LOG_FORMAT", c.LogFormat)
	c.ShutdownTimeout = env.dur("SUBGLANCE_SHUTDOWN_TIMEOUT", c.ShutdownTimeout)
	c.CheckWorkers = env.int("SUBGLANCE_CHECK_WORKERS", c.CheckWorkers)
	c.WatchdogURL = envStr("SUBGLANCE_WATCHDOG_URL", c.WatchdogURL)
	c.WatchdogInterval = env.dur("SUBGLANCE_WATCHDOG_INTERVAL", c.WatchdogInterval)
	c.AllowPrivateTargets = env.bool("SUBGLANCE_ALLOW_PRIVATE_TARGETS", c.AllowPrivateTargets)
	c.RawRetention = env.dur("SUBGLANCE_RAW_RETENTION", c.RawRetention)
	c.RollupRetention = env.dur("SUBGLANCE_ROLLUP_RETENTION", c.RollupRetention)
	c.AlertGroupWindow = env.dur("SUBGLANCE_ALERT_GROUP_WINDOW", c.AlertGroupWindow)
	c.TrustedProxies = envStr("SUBGLANCE_TRUSTED_PROXIES", c.TrustedProxies)
	c.SecretKey = envStr("SUBGLANCE_SECRET_KEY", c.SecretKey)
	c.PreviousSecretKey = envStr("SUBGLANCE_SECRET_KEY_PREVIOUS", c.PreviousSecretKey)
	if err := env.err(); err != nil {
		return Config{}, err
	}

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
	fs.StringVar(&c.SecretKey, "secret-key", c.SecretKey,
		"32 bytes of key material, or a path to a file holding it, to encrypt notification channel "+
			"configuration at rest (empty = no encryption, config stored in plain text)")
	fs.StringVar(&c.PreviousSecretKey, "secret-key-previous", c.PreviousSecretKey,
		"the key the stored configuration is currently under, for one start, to rotate to --secret-key "+
			"or to decrypt back to plain text when --secret-key is empty")

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
	// The shortest chart window is 24h. Raw retention below that lets the
	// hourly rollup fold away checks inside an ordinary 24h request, and the
	// bucket that straddles the window's start is not counted, so the chart
	// would silently drop checks it claims to cover.
	if c.RawRetention < 24*time.Hour {
		return fmt.Errorf("raw-retention must be at least 24h, got %s", c.RawRetention)
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
	// Resolving the key here rather than at store.Open means a mistyped path
	// or a short key is a config error named after the flag, next to every
	// other config error, instead of a database error further in.
	if _, err := c.ResolveSecretKey(); err != nil {
		return fmt.Errorf("invalid secret-key: %w", err)
	}
	if _, err := c.ResolvePreviousSecretKey(); err != nil {
		return fmt.Errorf("invalid secret-key-previous: %w", err)
	}
	if c.SecretKey != "" && c.SecretKey == c.PreviousSecretKey {
		return errors.New("secret-key and secret-key-previous are the same value; " +
			"secret-key-previous names the key the data is currently under, so pass it only " +
			"when it differs from the new one")
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

// envErrors collects malformed environment variables so that Load can report
// every one of them at once.
//
// Its methods return the default on failure. That is not a fallback in
// disguise: Load refuses to return the Config when err() is non-nil, so the
// value exists only to keep the read expressions assignable and readable.
type envErrors struct {
	errs []error
}

func (e *envErrors) int(key string, def int) int {
	v, err := envInt(key, def)
	if err != nil {
		e.errs = append(e.errs, err)
		return def
	}
	return v
}

func (e *envErrors) bool(key string, def bool) bool {
	v, err := envBool(key, def)
	if err != nil {
		e.errs = append(e.errs, err)
		return def
	}
	return v
}

func (e *envErrors) dur(key string, def time.Duration) time.Duration {
	v, err := envDur(key, def)
	if err != nil {
		e.errs = append(e.errs, err)
		return def
	}
	return v
}

func (e *envErrors) err() error { return errors.Join(e.errs...) }

// The env readers below all report a malformed value instead of falling back
// to the default, and every one of them names the variable and the text that
// could not be parsed.
//
// This used to be true of durations only, for the alert group window, while
// envInt, envBool and the other durations swallowed the parse error. That
// split was the bug: SUBGLANCE_ALLOW_PRIVATE_TARGETS=yes is not a Go bool and
// SUBGLANCE_WATCHDOG_INTERVAL=300 has no unit, so both started the process on
// the default with no warning at any log level — an operator who believes they
// enabled LAN monitoring, or that they have a dead man's switch on a five
// minute timer, and has neither. `--log-level garbage` refuses to start, so
// the inconsistency was inside the config layer rather than between layers.
//
// The cost is real and deliberate: an instance running with a malformed
// variable today stops booting after this change, with a message saying which
// variable and what it read.

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want a whole number", key, v)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: want true or false", key, v)
	}
	return b, nil
}

func envDur(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want a duration with a unit, such as 30s or 5m", key, v)
	}
	return d, nil
}
