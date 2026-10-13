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
	// The names are not part of the settings API, so the switch alone
	// decides whether they are looked up: a canary turned on through the
	// API looks up the ones the flags and variables named.
	names := connectivity.ParseTargets(cfg.ConnectivityNames)
	if connectivity.ValidateNames(names) != nil {
		// Only reachable with the check turned off by a flag or variable,
		// as above: then nothing is looked up, and nothing can turn it on.
		names = nil
	}
	return connectivity.New(connectivity.Options{
		Targets:            targets,
		Names:              names,
		Disabled:           !resolved.Enabled.Value,
		OnRestored:         localNetworkNotice(notify, log, notifier.LocalNetworkNotice),
		OnResolverRestored: localNetworkNotice(notify, log, notifier.LocalDNSNotice),
	})
}

// localNetworkNotice returns the callback the canary calls once the host can
// reach its connectivity targets, or get an answer from its resolver, again
// after it could not. build makes the notice for that kind of episode.
func localNetworkNotice(notify *notifier.Notifier, log *slog.Logger, build func(from, to time.Time) notifier.Alert) func(from, to time.Time) {
	return func(from, to time.Time) {
		notice := build(from, to)
		log.Warn("connectivity restored", "event", notice.Event, "since", from, "duration", to.Sub(from).Round(time.Second))
		// The canary calls this from a check or its own sweep, neither of
		// which should be held up by a slow channel, and neither of whose
		// contexts should cut the notice short.
		go func() {
			err := notify.SendNotice(context.Background(), notice)
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
