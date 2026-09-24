package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrUnknownChannel is returned when an assignment references a channel that
// does not exist.
//
// It is distinct from ErrNotFound because the two mean different things to a
// caller assigning channels to a monitor: ErrNotFound says the monitor is
// gone (404), while this says the request body is wrong (400).
var ErrUnknownChannel = errors.New("store: unknown notification channel")

// Channel is a notification target: where alerts are delivered.
//
// Config holds the type-specific settings. It is a map of strings rather than
// a free-form JSON document on purpose: every setting any channel type needs
// (a URL, a bot token, a chat id, an address) is a scalar, and keeping it flat
// means the API can validate keys per type instead of storing whatever a
// client happened to send.
type Channel struct {
	ID   int64
	Name string
	Type string

	Config  map[string]string
	Enabled bool

	// IsDefault marks the instance-wide default: the channel a monitor with
	// no channels of its own alerts through. At most one channel carries it,
	// which a partial unique index enforces (migration 0015). Create and
	// Update leave it alone; SetDefaultChannel is the only writer.
	IsDefault bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Channel types, mirroring the CHECK constraint in migration 0001.
const (
	ChannelWebhook  = "webhook"
	ChannelDiscord  = "discord"
	ChannelSlack    = "slack"
	ChannelTelegram = "telegram"
	ChannelEmail    = "email"
)

const channelColumns = `id, name, type, config_json, enabled, is_default, created_at, updated_at`

// ListChannels returns every notification channel, oldest first.
func (db *DB) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := db.Reader.QueryContext(ctx,
		"SELECT "+channelColumns+" FROM notif_channels ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Channel
	for rows.Next() {
		c, err := db.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannel returns one channel. It reports ErrNotFound when absent.
func (db *DB) GetChannel(ctx context.Context, id int64) (Channel, error) {
	row := db.Reader.QueryRowContext(ctx,
		"SELECT "+channelColumns+" FROM notif_channels WHERE id = ?", id)
	c, err := db.scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, fmt.Errorf("%w: channel %d", ErrNotFound, id)
	}
	return c, err
}

// CreateChannel inserts a channel and returns it with its assigned ID.
func (db *DB) CreateChannel(ctx context.Context, c Channel) (Channel, error) {
	cfg, err := db.encodeChannelConfig(c.Config)
	if err != nil {
		return Channel{}, err
	}

	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO notif_channels (name, type, config_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.Name, c.Type, cfg, c.Enabled, now, now)
	if err != nil {
		return Channel{}, fmt.Errorf("insert channel: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Channel{}, fmt.Errorf("last insert id: %w", err)
	}

	c.ID = id
	c.CreatedAt = time.Unix(now, 0).UTC()
	c.UpdatedAt = c.CreatedAt
	return c, nil
}

// UpdateChannel overwrites the mutable fields of an existing channel.
//
// It takes a whole Channel rather than a patch: merging partial updates is the
// API's job, because only the API knows which fields the client actually sent.
func (db *DB) UpdateChannel(ctx context.Context, c Channel) (Channel, error) {
	cfg, err := db.encodeChannelConfig(c.Config)
	if err != nil {
		return Channel{}, err
	}

	now := time.Now().Unix()
	// is_default is read back rather than taken from c: Update never writes
	// the flag, so the caller's copy says nothing about the stored row.
	err = db.Writer.QueryRowContext(ctx, `
		UPDATE notif_channels
		   SET name = ?, type = ?, config_json = ?, enabled = ?, updated_at = ?
		 WHERE id = ?
		RETURNING is_default`,
		c.Name, c.Type, cfg, c.Enabled, now, c.ID).Scan(&c.IsDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, fmt.Errorf("%w: channel %d", ErrNotFound, c.ID)
	}
	if err != nil {
		return Channel{}, fmt.Errorf("update channel %d: %w", c.ID, err)
	}

	c.UpdatedAt = time.Unix(now, 0).UTC()
	return c, nil
}

// DeleteChannel removes a channel and, through ON DELETE CASCADE, every
// assignment of it to a monitor. It reports ErrNotFound when absent.
func (db *DB) DeleteChannel(ctx context.Context, id int64) error {
	res, err := db.Writer.ExecContext(ctx, "DELETE FROM notif_channels WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete channel %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete channel %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, id)
	}
	return nil
}

// ListMonitorChannels returns the channels assigned to a monitor.
func (db *DB) ListMonitorChannels(ctx context.Context, monitorID int64) ([]Channel, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+channelColumns+`
		  FROM notif_channels
		  JOIN monitor_channels ON monitor_channels.channel_id = notif_channels.id
		 WHERE monitor_channels.monitor_id = ?
		 ORDER BY notif_channels.id`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("query monitor channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Channel
	for rows.Next() {
		c, err := db.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AlertChannels returns the channels an alert for this monitor goes to.
//
// A monitor's own assignments win outright. Only a monitor with none falls
// back to the instance default, and usedDefault reports that it did, so a
// caller can say which rule applied instead of making a reader work it out.
// The default replaces an empty list rather than joining a populated one: a
// monitor someone deliberately routed to one channel keeps that routing when
// a default is added later.
//
// Disabled channels are returned like enabled ones. Skipping them is the
// sender's decision, and a disabled default still counts as "the default
// applied" — the monitor is routed, the route is switched off.
func (db *DB) AlertChannels(ctx context.Context, monitorID int64) (channels []Channel, usedDefault bool, err error) {
	own, err := db.ListMonitorChannels(ctx, monitorID)
	if err != nil || len(own) > 0 {
		return own, false, err
	}
	def, ok, err := db.DefaultChannel(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	return []Channel{def}, true, nil
}

// DefaultChannel returns the instance-wide default channel, if one is set.
func (db *DB) DefaultChannel(ctx context.Context) (Channel, bool, error) {
	row := db.Reader.QueryRowContext(ctx,
		"SELECT "+channelColumns+" FROM notif_channels WHERE is_default = 1")
	c, err := db.scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, fmt.Errorf("query default channel: %w", err)
	}
	return c, true, nil
}

// SetDefaultChannel makes id the instance-wide default, replacing any other.
// It reports ErrNotFound for an id that names no channel, and leaves the
// previous default in place when it does.
//
// Clearing the old default and setting the new one happen in one
// transaction, so there is no moment in which a concurrent alert finds no
// default at all.
func (db *DB) SetDefaultChannel(ctx context.Context, id int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"UPDATE notif_channels SET is_default = 0 WHERE is_default = 1 AND id != ?", id); err != nil {
		return fmt.Errorf("clear default channel: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		"UPDATE notif_channels SET is_default = 1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("set default channel %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set default channel %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, id)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ClearDefaultChannel stops id being the default. It is not an error for id
// to be a channel that was not the default: the state asked for already
// holds. It reports ErrNotFound only when id names no channel at all.
//
// It takes the channel rather than clearing whatever the default is, so a
// client acting on a stale screen cannot unset a default someone else chose
// after that screen was drawn.
func (db *DB) ClearDefaultChannel(ctx context.Context, id int64) error {
	res, err := db.Writer.ExecContext(ctx,
		"UPDATE notif_channels SET is_default = 0 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("clear default channel %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("clear default channel %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, id)
	}
	return nil
}

// ChannelSummary identifies an attachment without reading its credentials.
type ChannelSummary struct {
	ID   int64
	Name string
}

// MonitorChannelSummaries reads all attachments in one query. Missing monitor
// keys mean no attachments only when err is nil; callers must preserve errors
// as unknown. Disabled channels are still attached and remain in the result.
func (db *DB) MonitorChannelSummaries(ctx context.Context) (map[int64][]ChannelSummary, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT mc.monitor_id, c.id, c.name
		FROM monitor_channels mc JOIN notif_channels c ON c.id = mc.channel_id
		ORDER BY mc.monitor_id, c.id`)
	if err != nil {
		return nil, fmt.Errorf("query monitor channel summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]ChannelSummary)
	for rows.Next() {
		var monitorID int64
		var c ChannelSummary
		if err := rows.Scan(&monitorID, &c.ID, &c.Name); err != nil {
			return nil, err
		}
		out[monitorID] = append(out[monitorID], c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetMonitorChannels replaces a monitor's channel assignments with ids.
//
// Replace rather than add: the caller sends the set it wants, which makes the
// operation idempotent and lets a UI checkbox list submit its state directly
// without working out a diff first.
//
// The whole replacement runs in one transaction, so a request naming one bad
// channel leaves the existing assignments untouched instead of clearing them
// and then failing halfway.
func (db *DB) SetMonitorChannels(ctx context.Context, monitorID int64, ids []int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM monitors WHERE id = ?", monitorID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: monitor %d", ErrNotFound, monitorID)
	}
	if err != nil {
		return fmt.Errorf("look up monitor %d: %w", monitorID, err)
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM monitor_channels WHERE monitor_id = ?", monitorID); err != nil {
		return fmt.Errorf("clear monitor channels: %w", err)
	}

	for _, id := range ids {
		// A foreign-key violation would also catch an unknown channel, but
		// only as an opaque driver error. Checking here turns it into a
		// message that names the offending id.
		var found int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM notif_channels WHERE id = ?", id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrUnknownChannel, id)
		}
		if err != nil {
			return fmt.Errorf("look up channel %d: %w", id, err)
		}

		// Duplicates in the request are the client repeating itself, not an
		// error worth rejecting: the resulting set is the same either way.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_channels (monitor_id, channel_id) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, monitorID, id); err != nil {
			return fmt.Errorf("assign channel %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (db *DB) scanChannel(s scanner) (Channel, error) {
	var (
		c       Channel
		cfg     sql.NullString
		created int64
		updated int64
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Type, &cfg, &c.Enabled, &c.IsDefault, &created, &updated); err != nil {
		return Channel{}, err
	}

	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()
	c.Config = map[string]string{}

	raw := ""
	if cfg.Valid {
		raw = cfg.String
	}

	// Encryption is invisible to every caller above this line: the rest of the
	// code sees the same map it always did.
	//
	// A row that is ciphertext when no key is configured, or that refuses to
	// authenticate, cannot happen here — Open reconciles the whole table
	// against the configured keys and refuses to start otherwise, which is the
	// point of doing it there and not lazily on this path. It is still handled
	// rather than ignored, because a row written by a concurrent process with
	// a different key would otherwise become an empty config with no trace.
	if isEncryptedConfig(raw) {
		if db.cipher == nil {
			return Channel{}, fmt.Errorf("store: channel %d has encrypted configuration "+
				"but no secret key is configured", c.ID)
		}
		plain, err := db.cipher.open(raw)
		if err != nil {
			return Channel{}, fmt.Errorf("store: channel %d: %w", c.ID, err)
		}
		raw = plain
	}

	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Config); err != nil {
			// One unreadable row must not blank the whole list. An empty
			// config is visibly broken in the UI, which is the honest
			// outcome; a failed listing is not.
			//
			// This only covers malformed plaintext. Ciphertext that does not
			// authenticate is refused above instead of being swallowed here:
			// tampered config must never reach a notifier, and "the config
			// looks empty" is the wrong way to learn that it was altered.
			c.Config = map[string]string{}
		}
	}
	return c, nil
}

// encodeChannelConfig renders a config for storage, encrypting it when a
// secret key is configured. With no key the output is the same JSON this
// function has always produced, byte for byte.
func (db *DB) encodeChannelConfig(cfg map[string]string) (string, error) {
	if cfg == nil {
		cfg = map[string]string{}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encode channel config: %w", err)
	}
	if db.cipher == nil {
		return string(b), nil
	}
	return db.cipher.seal(string(b))
}
