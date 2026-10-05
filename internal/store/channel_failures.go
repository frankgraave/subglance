package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// ChannelFailure is a channel whose alerts have stopped arriving: a delivery
// through it gave up, and none has arrived since.
type ChannelFailure struct {
	ChannelID int64

	// FailedAt is when the first delivery of this spell gave up. Later
	// failures in the same spell leave it alone.
	FailedAt time.Time
	// LastError is the newest failure's message, as the transport wrote it.
	// It can carry the channel's credentials: redact it before showing it.
	LastError string

	// NoticedAt is when the notice about this spell went out through another
	// channel, zero while it has not. NoticeChannelID is that channel, zero
	// when it has since been deleted.
	NoticedAt       time.Time
	NoticeChannelID int64
}

// RecordChannelFailure notes that a delivery through a channel gave up at.
//
// The first failure opens the spell; a later one only refreshes the message,
// so the spell keeps its start and a notice that already went out stays sent.
func (db *DB) RecordChannelFailure(ctx context.Context, channelID int64, at time.Time, cause string) error {
	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO channel_failures (channel_id, failed_at, last_error)
		VALUES (?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET last_error = excluded.last_error`,
		channelID, at.Unix(), cause)
	if err != nil {
		return fmt.Errorf("record channel failure %d: %w", channelID, err)
	}
	return nil
}

// ClearChannelFailure ends a channel's spell of failures, because a delivery
// through it arrived. The next failure opens a new spell, with a notice of its
// own.
func (db *DB) ClearChannelFailure(ctx context.Context, channelID int64) error {
	_, err := db.Writer.ExecContext(ctx,
		`DELETE FROM channel_failures WHERE channel_id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("clear channel failure %d: %w", channelID, err)
	}
	return nil
}

// MarkChannelFailureNoticed records that the notice about a spell went out
// through via at.
//
// It names the spell by its start as well as its channel. A spell that ended
// and was followed by a new one while the notice was in flight is a different
// spell, and must not be marked as told.
func (db *DB) MarkChannelFailureNoticed(ctx context.Context, f ChannelFailure, via int64, at time.Time) error {
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE channel_failures
		   SET noticed_at = ?, notice_channel_id = ?
		 WHERE channel_id = ? AND failed_at = ? AND noticed_at IS NULL`,
		at.Unix(), via, f.ChannelID, f.FailedAt.Unix())
	if err != nil {
		return fmt.Errorf("mark channel failure noticed %d: %w", f.ChannelID, err)
	}
	return nil
}

// ChannelFailures returns every channel that is failing now, by channel id.
func (db *DB) ChannelFailures(ctx context.Context) (map[int64]ChannelFailure, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT channel_id, failed_at, last_error, noticed_at, notice_channel_id
		  FROM channel_failures`)
	if err != nil {
		return nil, fmt.Errorf("query channel failures: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]ChannelFailure{}
	for rows.Next() {
		var (
			f        ChannelFailure
			failedAt int64
			noticed  sql.NullInt64
			via      sql.NullInt64
		)
		if err := rows.Scan(&f.ChannelID, &failedAt, &f.LastError, &noticed, &via); err != nil {
			return nil, err
		}
		f.FailedAt = time.Unix(failedAt, 0).UTC()
		if noticed.Valid {
			f.NoticedAt = time.Unix(noticed.Int64, 0).UTC()
		}
		f.NoticeChannelID = via.Int64
		out[f.ChannelID] = f
	}
	return out, rows.Err()
}

// NoticeCandidates lists the channels that may carry the notice about the
// failing channel except, in the order they are tried.
//
// A candidate is enabled, is not except, and is not failing itself: a notice
// sent into a channel that is known to deliver nothing is the silence this
// notice exists to break. The default channel comes first, because it is the
// one the operator named as where things go when nothing more specific
// applies; the rest follow oldest first, so the choice is the same on every
// attempt.
//
// It is one rule shared by the notifier, which sends through the first
// candidate that is not in its quiet hours, and the API, which says there is
// no other channel exactly when this list is empty.
func NoticeCandidates(channels []Channel, failing map[int64]ChannelFailure, except int64) []Channel {
	var out []Channel
	for _, c := range channels {
		if c.ID == except || !c.Enabled {
			continue
		}
		if _, bad := failing[c.ID]; bad {
			continue
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b Channel) int {
		if a.IsDefault != b.IsDefault {
			if a.IsDefault {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}
