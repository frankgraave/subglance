package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

// newCanary builds the connectivity canary from the flags, the environment
// and what was saved through the settings API, in that order of precedence.
// The notice it sends on reconnecting goes to the default channel, like every
// other notice about the instance itself.
//
// It is built even when the check is off, so the settings API can turn it on
// without a restart; switched off, it never dials and never holds a failure
// back.
func newCanary(ctx context.Context, cfg config.Config, db *store.DB, notify *notifier.Notifier, log *slog.Logger) (*connectivity.Canary, error) {
	resolved, err := db.ResolveConnectivity(ctx, cfg.ConnectivityPins())
	if err != nil {
		return nil, fmt.Errorf("connectivity settings: %w", err)
	}
	targets := resolved.Targets.Value
	if !resolved.Enabled.Value && connectivity.ValidateTargets(targets) != nil {
		// Only reachable with the check turned off by a flag or variable,
		// which is the one case Load does not judge the targets: they are
		// never dialled, and the pin keeps the API from turning it on.
		targets = connectivity.DefaultTargets
	}
	if !resolved.Enabled.Value {
		log.Info("connectivity check is off: a failure of this host's own network will count against every monitor",
			"source", resolved.Enabled.Source)
	}
	return connectivity.New(connectivity.Options{
		Targets:    targets,
		Disabled:   !resolved.Enabled.Value,
		OnRestored: localNetworkNotice(notify, log),
	})
}

// localNetworkNotice returns the callback the canary calls once the host can
// reach its connectivity targets again after it could not.
func localNetworkNotice(notify *notifier.Notifier, log *slog.Logger) func(from, to time.Time) {
	return func(from, to time.Time) {
		log.Warn("outbound connectivity restored", "offline_since", from, "duration", to.Sub(from).Round(time.Second))
		// The canary calls this from a check or its own sweep, neither of
		// which should be held up by a slow channel, and neither of whose
		// contexts should cut the notice short.
		go func() {
			err := notify.SendNotice(context.Background(), notifier.LocalNetworkNotice(from, to))
			switch {
			case err == nil:
			case errors.Is(err, notifier.ErrNoticeNotSent):
				log.Warn("connectivity notice not sent", "reason", err)
			default:
				log.Error("could not send connectivity notice", "error", err)
			}
		}()
	}
}
