package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/frankgraave/subglance/internal/backup"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

// newBackups builds the scheduled backup runner, or returns nil when no
// target is configured.
func newBackups(cfg config.Config, db *store.DB, log *slog.Logger) (*backup.Backups, error) {
	if cfg.BackupTarget == "" {
		return nil, nil
	}
	target, err := cfg.BackupTargetParsed()
	if err != nil {
		return nil, err
	}
	secret, err := cfg.BackupSecret()
	if err != nil {
		return nil, err
	}
	return backup.New(backup.Options{
		DB:              db,
		Target:          target,
		AccessKeyID:     cfg.BackupAccessKeyID,
		SecretAccessKey: secret,
		Interval:        cfg.BackupInterval,
		Keep:            cfg.BackupKeep,
		StagingDir:      cfg.DataDir,
		Log:             log,
	})
}

// backupFailureNotice returns the callback the backup runner calls after each
// run. It sends one notice to the default channel per failing streak, and
// again each day the streak lasts; see backup.AlertGate for why. A clean run
// (err == nil) ends the streak, so the next failure is news again even when it
// comes within the day.
func backupFailureNotice(ctx context.Context, notify *notifier.Notifier, log *slog.Logger) func(error) {
	var gate backup.AlertGate
	return func(err error) {
		if err == nil {
			gate.Forget()
			return
		}
		now := time.Now()
		if !gate.ShouldAlert(now) {
			return
		}
		sendErr := notify.SendNotice(ctx, notifier.Alert{
			MonitorName: "SubGlance",
			Target:      "Scheduled database backup",
			Event:       notifier.EventBackupFailed,
			LastError:   err.Error(),
			At:          now,
		})
		if sendErr == nil {
			return
		}
		// Not sent: let the next failure try again rather than wait a day.
		gate.Forget()
		if errors.Is(sendErr, notifier.ErrNoticeNotSent) {
			log.Warn("backup failure notice not sent", "reason", sendErr)
			return
		}
		log.Error("could not send backup failure notice", "error", sendErr)
	}
}
