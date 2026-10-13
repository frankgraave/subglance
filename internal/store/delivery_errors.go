package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// settingDeliveryErrorsRedacted records that the stored delivery errors have
// been rewritten without channel credentials, so the rewrite runs once per
// database rather than on every start.
const settingDeliveryErrorsRedacted = "notifier.delivery_errors_redacted"

// The rows a delivery error is stored in, with the key that names each one.
//
// Only outbox rows that were not suppressed: a suppressed row's last_error is
// a reason SubGlance wrote itself (maintenance, quiet hours, an SMS limit, the
// delivery that carried its news instead), never a sender's error, and
// MergedInto reads that text back by its prefix, which a rewrite must not
// touch.
const (
	selectOutboxErrors = `
		SELECT id, channel_id, last_error FROM notif_outbox
		 WHERE last_error <> '' AND suppressed = 0`
	updateOutboxError = `UPDATE notif_outbox SET last_error = ? WHERE id = ?`

	selectChannelFailureErrors = `
		SELECT channel_id, channel_id, last_error FROM channel_failures
		 WHERE last_error <> ''`
	updateChannelFailureError = `UPDATE channel_failures SET last_error = ? WHERE channel_id = ?`
)

// RedactDeliveryErrors rewrites every stored delivery error through redact,
// with the configuration of the channel it belongs to, and reports how many
// rows changed. It does the work once per database: a second call finds the
// mark it left and returns zero.
//
// It exists for the rows written before delivery errors were redacted at the
// source. Those carry a Slack, Discord or webhook URL in full, and waiting for
// retention would not remove them: a failed delivery is kept until someone
// deletes it, and a channel's failure record until the channel delivers
// again. With --secret-key set they are the one place the credential is still
// readable in the file and in every backup of it.
//
// redact is passed in rather than called because the rules live in the
// notifier, which imports this package. It must be idempotent; the notifier's
// RedactError is, and rows that come out unchanged are not written.
//
// updated_at is left alone, as the channel encryption rewrite leaves it: the
// retention pass dates a delivery by it, and a rewrite of how an error is
// stored is not something that happened to the delivery.
//
// The rows are rewritten in place. A copy of the old text can survive in the
// file's free pages until the database is compacted; a backup holds live rows
// only, so one taken after this has none.
func (db *DB) RedactDeliveryErrors(ctx context.Context, redact func(msg string, cfg map[string]string) string) (int, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("redact delivery errors: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	done, err := readSetting(ctx, tx, settingDeliveryErrorsRedacted)
	if err != nil {
		return 0, err
	}
	if done != nil {
		return 0, nil
	}

	configs, err := db.channelConfigs(ctx, tx)
	if err != nil {
		return 0, err
	}

	changed := 0
	for _, t := range []struct{ query, update string }{
		{selectOutboxErrors, updateOutboxError},
		{selectChannelFailureErrors, updateChannelFailureError},
	} {
		plan, err := planErrorRewrites(ctx, tx, t.query, configs, redact)
		if err != nil {
			return 0, err
		}
		for _, r := range plan {
			if _, err := tx.ExecContext(ctx, t.update, r.value, r.id); err != nil {
				return 0, fmt.Errorf("redact delivery error %d: %w", r.id, err)
			}
		}
		changed += len(plan)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingDeliveryErrorsRedacted, strconv.Itoa(changed), time.Now().Unix()); err != nil {
		return 0, fmt.Errorf("record delivery error redaction: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("redact delivery errors: %w", err)
	}
	return changed, nil
}

// channelConfigs returns every channel's configuration, decrypted, by id.
func (db *DB) channelConfigs(ctx context.Context, tx *sql.Tx) (map[int64]map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+channelColumns+" FROM notif_channels")
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]map[string]string{}
	for rows.Next() {
		c, err := db.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out[c.ID] = c.Config
	}
	return out, rows.Err()
}

// errorRewrite is one stored error's new text.
type errorRewrite struct {
	id    int64
	value string
}

// planErrorRewrites reads the errors query selects and works out the ones
// redact changes, without writing anything: the cursor is closed before the
// first UPDATE, as in planChannelRewrites.
func planErrorRewrites(ctx context.Context, tx *sql.Tx, query string, configs map[int64]map[string]string, redact func(string, map[string]string) string) ([]errorRewrite, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query delivery errors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []errorRewrite
	for rows.Next() {
		var (
			id, channelID int64
			msg           string
		)
		if err := rows.Scan(&id, &channelID, &msg); err != nil {
			return nil, err
		}
		// Every row names a channel that exists: both tables cascade on
		// delete. A missing one would get the URL pass alone, which
		// still takes the path off any address in the text.
		if clean := redact(msg, configs[channelID]); clean != msg {
			out = append(out, errorRewrite{id: id, value: clean})
		}
	}
	return out, rows.Err()
}
