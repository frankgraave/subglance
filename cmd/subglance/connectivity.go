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
	if !resolved.Enabled.Value && connectivity.ValidateSavedTargets(targets) != nil {
		// Only reachable with the check turned off by a flag or variable,
		// which is the one case Load does not judge the targets: they are
		// never dialled, and the pin keeps the API from turning it on.
		targets = connectivity.DefaultTargets
	} else if err := connectivity.ValidateTargets(targets); err != nil {
		// A list saved through the settings API before hosts were held
		// to the resolver's rule. It still runs, so an upgrade does not
		// stop the server, but that target fails every dial without
		// sending a packet, and the operator should hear which one.
		log.Warn("a connectivity target cannot be looked up and will never be reached; change it under Settings",
			"error", err, "source", resolved.Targets.Source)
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
		OnUnusable:         unusableTargetsWarning(log),
	})
}

// unusableTargetsWarning returns the callback the canary calls when its
// targets turn out unable to speak for this host. It only logs: the outages
// it would have held back are reported again, which is what matters, and a
// message about the instance's own settings belongs on its Settings page and
// dashboard rather than in a channel meant for outages.
func unusableTargetsWarning(log *slog.Logger) func(connectivity.Unusable) {
	return func(reason connectivity.Unusable) {
		switch reason {
		case connectivity.Contradicted:
			log.Warn("the connectivity targets failed while another check reached the internet; "+
				"they may be blocked from this host, so failures are not held back. Set targets this host can reach under Settings",
				"reason", reason)
		default:
			log.Warn("the connectivity targets have not answered since they were set, "+
				"so failures are not held back. Set targets this host can reach under Settings",
				"reason", reason)
		}
	}
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
