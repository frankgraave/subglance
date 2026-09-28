package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/notifier"
)

// newCanary builds the connectivity canary, or returns nil when the check is
// turned off. The notice it sends on reconnecting goes to the default
// channel, like every other notice about the instance itself.
func newCanary(cfg config.Config, notify *notifier.Notifier, log *slog.Logger) (*connectivity.Canary, error) {
	if !cfg.ConnectivityCheck {
		log.Info("connectivity check is off: a failure of this host's own network will count against every monitor")
		return nil, nil
	}
	return connectivity.New(connectivity.Options{
		Targets:    cfg.ConnectivityTargetList(),
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
