package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/connectivity"
)

// The connectivity check's switch and targets, as chosen over the settings
// API. A flag or environment variable pins either one and wins over what is
// stored here; with neither, the built-in default applies (on, dialling
// connectivity.DefaultTargets).
//
// The targets are stored comma-separated, the same form the flag takes, so
// the row reads the same to anyone who opens the database. A host:port never
// contains a comma: IPv6 hosts are bracketed.
const (
	settingConnectivityEnabled = "connectivity.enabled"
	settingConnectivityTargets = "connectivity.targets"
	// settingConnectivityVersion counts saves, for the same optimistic
	// concurrency as the retention settings.
	settingConnectivityVersion = "connectivity.version"
)

// ErrConnectivityVersion reports that a conditional save was refused because
// someone else saved after the caller read the settings. Nothing was written.
var ErrConnectivityVersion = errors.New("connectivity settings were changed by someone else")

// ConnectivityEnabledPin is the switch fixed by a flag or variable.
type ConnectivityEnabledPin struct {
	Value bool
	// By names the flag or variable, as the operator would type it.
	By string
}

// ConnectivityTargetsPin is the target list fixed by a flag or variable.
type ConnectivityTargetsPin struct {
	Value []string
	By    string
}

// ConnectivityPins holds what was pinned at startup. A nil pin leaves that
// setting to the API.
type ConnectivityPins struct {
	Enabled *ConnectivityEnabledPin
	Targets *ConnectivityTargetsPin
}

// ConnectivityEnabled is the resolved switch and where it came from.
type ConnectivityEnabled struct {
	Value bool
	// Source is RetentionSourceDefault, RetentionSourceDatabase or
	// RetentionSourcePinned: the same three places a retention setting
	// comes from.
	Source   string
	PinnedBy string
}

// ConnectivityTargets is the resolved target list and where it came from.
type ConnectivityTargets struct {
	Value    []string
	Source   string
	PinnedBy string
}

// ConnectivitySettings is what the canary should run with.
type ConnectivitySettings struct {
	Enabled ConnectivityEnabled
	Targets ConnectivityTargets
}

// Canary returns the settings in the form connectivity.Canary.Configure takes.
func (s ConnectivitySettings) Canary() connectivity.Settings {
	return connectivity.Settings{Enabled: s.Enabled.Value, Targets: slices.Clone(s.Targets.Value)}
}

// ConnectivityVersion is the number of saves so far. Read it BEFORE the
// settings it is meant to describe, for the reason RetentionVersion gives.
func (db *DB) ConnectivityVersion(ctx context.Context) (int64, error) {
	return connectivityVersion(ctx, db.Reader)
}

func connectivityVersion(ctx context.Context, q rowQuerier) (int64, error) {
	v, err := readCount(ctx, q, settingConnectivityVersion)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// ResolveConnectivity works out what the canary runs with: a pinned value
// wins, then the stored one, then the default.
func (db *DB) ResolveConnectivity(ctx context.Context, pins ConnectivityPins) (ConnectivitySettings, error) {
	enabled, targets, err := storedConnectivity(ctx, db.Reader)
	if err != nil {
		return ConnectivitySettings{}, err
	}
	var out ConnectivitySettings
	switch {
	case pins.Enabled != nil:
		out.Enabled = ConnectivityEnabled{Value: pins.Enabled.Value, Source: RetentionSourcePinned, PinnedBy: pins.Enabled.By}
	case enabled != nil:
		out.Enabled = ConnectivityEnabled{Value: *enabled, Source: RetentionSourceDatabase}
	default:
		out.Enabled = ConnectivityEnabled{Value: true, Source: RetentionSourceDefault}
	}
	switch {
	case pins.Targets != nil:
		out.Targets = ConnectivityTargets{Value: slices.Clone(pins.Targets.Value), Source: RetentionSourcePinned, PinnedBy: pins.Targets.By}
	case targets != nil:
		out.Targets = ConnectivityTargets{Value: targets, Source: RetentionSourceDatabase}
	default:
		out.Targets = ConnectivityTargets{Value: slices.Clone(connectivity.DefaultTargets), Source: RetentionSourceDefault}
	}
	return out, nil
}

// storedConnectivity reads the saved switch and targets. Nil means never
// saved.
func storedConnectivity(ctx context.Context, q rowQuerier) (enabled *bool, targets []string, err error) {
	raw, err := readSetting(ctx, q, settingConnectivityEnabled)
	if err != nil {
		return nil, nil, err
	}
	if raw != nil {
		b, err := strconv.ParseBool(*raw)
		if err != nil {
			// A row this code did not write. Refused rather than
			// guessed: read as "on" it phones out against the
			// operator's choice, read as "off" it lets a dead uplink
			// page for every monitor.
			return nil, nil, fmt.Errorf("setting %s holds %q, want true or false", settingConnectivityEnabled, *raw)
		}
		enabled = &b
	}
	raw, err = readSetting(ctx, q, settingConnectivityTargets)
	if err != nil {
		return nil, nil, err
	}
	if raw != nil {
		targets = connectivity.ParseTargets(*raw)
		if err := connectivity.ValidateTargets(targets); err != nil {
			return nil, nil, fmt.Errorf("setting %s: %w", settingConnectivityTargets, err)
		}
	}
	return enabled, targets, nil
}

// readSetting reads one row of the settings table. Nil means it does not
// exist.
func readSetting(ctx context.Context, q rowQuerier, key string) (*string, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return &v, nil
}

// ConnectivityChange is one save. A nil field is left as it was.
type ConnectivityChange struct {
	Enabled *bool
	Targets []string
}

// SaveConnectivity stores a change, whatever was saved in the meantime, and
// returns the new version.
func (db *DB) SaveConnectivity(ctx context.Context, c ConnectivityChange) (int64, error) {
	return db.saveConnectivity(ctx, c, false, nil)
}

// SaveConnectivityIfVersion is SaveConnectivity on condition that the stored
// version is one of versions. It returns ErrConnectivityVersion, and writes
// nothing, when it is not. An empty list matches no version.
func (db *DB) SaveConnectivityIfVersion(ctx context.Context, c ConnectivityChange, versions []int64) (int64, error) {
	return db.saveConnectivity(ctx, c, true, versions)
}

func (db *DB) saveConnectivity(ctx context.Context, c ConnectivityChange, conditional bool, versions []int64) (int64, error) {
	// A list this code would refuse to read back must never be written:
	// the next start would fail on it.
	if c.Targets != nil {
		if err := connectivity.ValidateTargets(c.Targets); err != nil {
			return 0, err
		}
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin connectivity update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	version, err := connectivityVersion(ctx, tx)
	if err != nil {
		return 0, err
	}
	if conditional && !slices.Contains(versions, version) {
		return 0, ErrConnectivityVersion
	}

	values := map[string]string{settingConnectivityVersion: strconv.FormatInt(version+1, 10)}
	if c.Enabled != nil {
		values[settingConnectivityEnabled] = strconv.FormatBool(*c.Enabled)
	}
	if c.Targets != nil {
		values[settingConnectivityTargets] = strings.Join(c.Targets, ",")
	}
	now := time.Now().Unix()
	for key, v := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, v, now); err != nil {
			return 0, fmt.Errorf("save %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit connectivity update: %w", err)
	}
	return version + 1, nil
}
