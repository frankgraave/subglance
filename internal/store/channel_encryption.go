package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Startup handling of the notification-channel encryption state.
//
// Everything expensive or surprising about encryption at rest happens here,
// once, before the process serves a request — never lazily on the delivery
// path. The reason is the failure mode this feature exists to avoid: five
// channels that silently stop working, discovered during the outage they were
// meant to report. A key that cannot read the stored rows has to be a startup
// error, loudly, while the operator is still looking at the terminal.

// ChannelEncryptionReport records what Open did to the stored channel rows.
//
// Returned rather than logged because package store has no logger and should
// not acquire one for this; the command that owns the log decides how to say
// it. The counts are rows, never values.
type ChannelEncryptionReport struct {
	// Enabled is true when a current key is configured, so config is being
	// written encrypted from now on.
	Enabled bool
	// Encrypted counts plaintext rows converted on this start.
	Encrypted int
	// Rewrapped counts rows moved from the previous key to the current one.
	Rewrapped int
	// Decrypted counts rows written back as plaintext because a previous key
	// was given without a current one.
	Decrypted int
}

// ChannelEncryption returns what Open found and did. Safe to call on any DB.
func (db *DB) ChannelEncryption() ChannelEncryptionReport { return db.cryptoReport }

// prepareChannelEncryption reconciles the stored rows with the configured keys.
//
// The four states an operator can be in, and the answer to each:
//
//  1. No key, no encrypted rows — nothing happens. This is the default and it
//     means the config stays in plain text. See SECURITY.md; it is documented
//     rather than inferred.
//
//  2. A key, and plaintext rows — the rows are encrypted now, in one
//     transaction. The alternative, encrypting only on the next write, was
//     rejected: it leaves a database that is half protected with nothing
//     telling the operator which half, and the rows least likely to be
//     rewritten are the channels that have been working for a year.
//
//  3. A key that cannot open the encrypted rows — refusal to start. Starting
//     with an unusable key means every channel fails at delivery time, one by
//     one, in an incident. The database is still intact at the moment of
//     refusal, so putting the right key back is a complete recovery, which is
//     not true of anything that happens after a write.
//
//  4. Encrypted rows and no key at all — also refusal, for the same reason.
//     Removing the key must not be a way to quietly lose five channels.
//     Turning encryption off is possible, and deliberate: name the key as
//     --secret-key-previous with no --secret-key, and the rows are written
//     back as plaintext here.
//
// Rotation is the same mechanism read the other way: --secret-key-previous is
// the key the data is under, --secret-key is the key it should be under. Both
// given means rewrap every row once, at startup, in a transaction; a crash
// halfway leaves every row under the old key rather than a split database.
func (db *DB) prepareChannelEncryption(ctx context.Context, current, previous *configCipher) error {
	db.cryptoReport = ChannelEncryptionReport{Enabled: current != nil}

	// Nothing configured and nothing to check is the overwhelmingly common
	// case; do not open a transaction for it.
	if current == nil && previous == nil {
		n, err := db.countEncryptedChannels(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("store: %d notification channel%s ha%s encrypted configuration "+
				"but no --secret-key was given, so SubGlance cannot read them and refuses to start "+
				"rather than let every alert fail at delivery time. "+
				"Set --secret-key (or SUBGLANCE_SECRET_KEY) back to the key this database was encrypted with. "+
				"To turn encryption off on purpose, pass that key as --secret-key-previous with no --secret-key "+
				"and the configuration is written back as plain text",
				n, plural(n), has(n))
		}
		return nil
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin channel encryption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	pending, report, err := planChannelRewrites(ctx, tx, current, previous)
	if err != nil {
		return err
	}

	// updated_at is deliberately left alone: re-wrapping a row changes how it
	// is stored, not what the operator configured, and moving the timestamp
	// would make an upgrade look like someone edited every channel.
	for _, r := range pending {
		if _, err := tx.ExecContext(ctx,
			"UPDATE notif_channels SET config_json = ? WHERE id = ?", r.value, r.id); err != nil {
			return fmt.Errorf("store: rewrite channel %d configuration: %w", r.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit channel encryption: %w", err)
	}
	db.cryptoReport = report
	return nil
}

// channelRewrite is one row's new stored value.
type channelRewrite struct {
	id    int64
	value string
}

// planChannelRewrites reads every channel config and works out what each row
// should hold, without writing anything.
//
// It is a separate function so the rows can be closed by a defer rather than
// by a manual Close on every one of the six error paths inside the loop — one
// of which would eventually be forgotten, leaking a cursor inside the
// transaction that is about to do the writing. Collecting the plan first and
// executing it afterwards also avoids issuing UPDATEs on the same connection
// while a cursor from it is still open.
func planChannelRewrites(ctx context.Context, tx *sql.Tx, current, previous *configCipher) ([]channelRewrite, ChannelEncryptionReport, error) {
	report := ChannelEncryptionReport{Enabled: current != nil}

	rows, err := tx.QueryContext(ctx, "SELECT id, config_json FROM notif_channels ORDER BY id")
	if err != nil {
		return nil, report, fmt.Errorf("store: read channel configuration: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pending []channelRewrite
	for rows.Next() {
		var (
			id     int64
			stored sql.NullString
		)
		if err := rows.Scan(&id, &stored); err != nil {
			return nil, report, fmt.Errorf("store: read channel configuration: %w", err)
		}

		plain := stored.String
		if isEncryptedConfig(plain) {
			opened, err := openWithEitherKey(plain, current, previous)
			if err != nil {
				return nil, report, keyMismatchError(id, current != nil, previous != nil)
			}
			// A row the current key already opens is where it belongs;
			// re-sealing it would be work and a new nonce for nothing.
			if current != nil && opened.byCurrent {
				continue
			}
			plain = opened.plaintext
			if current == nil {
				pending = append(pending, channelRewrite{id: id, value: plain})
				report.Decrypted++
				continue
			}
			sealed, err := current.seal(plain)
			if err != nil {
				return nil, report, err
			}
			pending = append(pending, channelRewrite{id: id, value: sealed})
			report.Rewrapped++
			continue
		}

		// Plaintext row. With a current key it becomes ciphertext; without
		// one there is nothing to do, including in the disable case — it is
		// already the shape that was asked for.
		if current == nil {
			continue
		}
		sealed, err := current.seal(plain)
		if err != nil {
			return nil, report, err
		}
		pending = append(pending, channelRewrite{id: id, value: sealed})
		report.Encrypted++
	}
	if err := rows.Err(); err != nil {
		return nil, report, fmt.Errorf("store: read channel configuration: %w", err)
	}
	return pending, report, nil
}

type openedConfig struct {
	plaintext string
	byCurrent bool
}

// openWithEitherKey tries the current key and then the previous one.
//
// Current first so that a normal start — every row already under the current
// key — does no wasted work, and so that a rotation interrupted and retried is
// idempotent: rows already moved verify under the current key, rows not yet
// moved under the previous one.
func openWithEitherKey(stored string, current, previous *configCipher) (openedConfig, error) {
	if current != nil {
		if plain, err := current.open(stored); err == nil {
			return openedConfig{plaintext: plain, byCurrent: true}, nil
		}
	}
	if previous != nil && !previous.sameKeyAs(current) {
		if plain, err := previous.open(stored); err == nil {
			return openedConfig{plaintext: plain}, nil
		}
	}
	return openedConfig{}, errWrongKey
}

// keyMismatchError says which key failed and what to do, because a container
// log is the only thing the operator has at this point.
func keyMismatchError(id int64, haveCurrent, havePrevious bool) error {
	switch {
	case haveCurrent && havePrevious:
		return fmt.Errorf("store: the configuration of notification channel %d cannot be decrypted "+
			"with either --secret-key or --secret-key-previous, so SubGlance refuses to start. "+
			"No row has been changed. Check that --secret-key-previous is the key this database "+
			"was encrypted with", id)
	case haveCurrent:
		return fmt.Errorf("store: the configuration of notification channel %d cannot be decrypted "+
			"with the given --secret-key, so SubGlance refuses to start rather than run with "+
			"channels that would fail at delivery time. No row has been changed. "+
			"Either supply the key this database was encrypted with, or, to move the data to a new key, "+
			"pass the old one as --secret-key-previous alongside the new --secret-key", id)
	default:
		return fmt.Errorf("store: the configuration of notification channel %d cannot be decrypted "+
			"with the given --secret-key-previous, so SubGlance refuses to start. "+
			"No row has been changed", id)
	}
}

func (db *DB) countEncryptedChannels(ctx context.Context) (int, error) {
	var n int
	err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM notif_channels WHERE config_json LIKE ?",
		configCipherPrefix+"%").Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("store: count encrypted channels: %w", err)
	}
	return n, nil
}

func has(n int) string {
	if n == 1 {
		return "s"
	}
	return "ve"
}
