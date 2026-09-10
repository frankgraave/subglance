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

const channelColumns = `id, name, type, config_json, enabled, created_at, updated_at`

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
		c, err := scanChannel(rows)
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
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, fmt.Errorf("%w: channel %d", ErrNotFound, id)
	}
	return c, err
}

// CreateChannel inserts a channel and returns it with its assigned ID.
func (db *DB) CreateChannel(ctx context.Context, c Channel) (Channel, error) {
	cfg, err := encodeChannelConfig(c.Config)
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
	cfg, err := encodeChannelConfig(c.Config)
	if err != nil {
		return Channel{}, err
	}

	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx, `
		UPDATE notif_channels
		   SET name = ?, type = ?, config_json = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		c.Name, c.Type, cfg, c.Enabled, now, c.ID)
	if err != nil {
		return Channel{}, fmt.Errorf("update channel %d: %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Channel{}, fmt.Errorf("update channel %d: %w", c.ID, err)
	}
	if n == 0 {
		return Channel{}, fmt.Errorf("%w: channel %d", ErrNotFound, c.ID)
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
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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

func scanChannel(s scanner) (Channel, error) {
	var (
		c       Channel
		cfg     sql.NullString
		created int64
		updated int64
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Type, &cfg, &c.Enabled, &created, &updated); err != nil {
		return Channel{}, err
	}

	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()
	c.Config = map[string]string{}
	if cfg.Valid && cfg.String != "" {
		if err := json.Unmarshal([]byte(cfg.String), &c.Config); err != nil {
			// One unreadable row must not blank the whole list. An empty
			// config is visibly broken in the UI, which is the honest
			// outcome; a failed listing is not.
			c.Config = map[string]string{}
		}
	}
	return c, nil
}

func encodeChannelConfig(cfg map[string]string) (string, error) {
	if cfg == nil {
		cfg = map[string]string{}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encode channel config: %w", err)
	}
	return string(b), nil
}
