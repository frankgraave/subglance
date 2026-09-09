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

	// AllowPrivateTargets permits monitoring of private/loopback/link-local
	// addresses. Off by default: without it, a user-supplied URL turns
	// SubGlance into an SSRF proxy into the host network (see SUB-18).
	AllowPrivateTargets bool
}

// DBPath returns the full path to the SQLite database file.
func (c Config) DBPath() string {
	return strings.TrimRight(c.DataDir, "/") + "/subglance.db"
}

func defaults() Config {
	return Config{
		Addr:                ":8080",
		DataDir:             "/data",
		LogLevel:            "info",
		LogFormat:           "text",
		ShutdownTimeout:     15 * time.Second,
		CheckWorkers:        0,
		AllowPrivateTargets: false,
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
	c.AllowPrivateTargets = envBool("SUBGLANCE_ALLOW_PRIVATE_TARGETS", c.AllowPrivateTargets)

	fs := flag.NewFlagSet("subglance", flag.ContinueOnError)
	fs.StringVar(&c.Addr, "addr", c.Addr, "HTTP listen address")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "directory for the database and persistent state")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "log level: debug, info, warn, error")
	fs.StringVar(&c.LogFormat, "log-format", c.LogFormat, "log format: text or json")
	fs.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "how long to let in-flight requests finish")
	fs.IntVar(&c.CheckWorkers, "check-workers", c.CheckWorkers, "max concurrent checks (0 = auto)")
	fs.BoolVar(&c.AllowPrivateTargets, "allow-private-targets", c.AllowPrivateTargets,
		"allow monitoring private/loopback addresses (SSRF risk, off by default)")

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
