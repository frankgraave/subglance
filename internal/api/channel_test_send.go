package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// ChannelTester sends a real message through a channel.
//
// An interface rather than a direct dependency on the notifier, for the same
// reason as Prober and PushRecorder: the API has to stay usable in a test and
// in any build that runs without a delivery pipeline behind it.
type ChannelTester interface {
	Test(ctx context.Context, ch store.Channel) error
}

// WithChannelTester attaches a tester, enabling POST /api/v1/channels/{id}/test.
func (s *Server) WithChannelTester(t ChannelTester) *Server {
	s.tester = t
	return s
}

// channelTestTimeout bounds one test send.
//
// Longer than a delivery attempt, because a person is watching this one and a
// slow mail server that eventually works is a more useful answer than a quick
// "timed out" they cannot act on.
const channelTestTimeout = 20 * time.Second

// handleTestChannel sends a real alert through one channel, so an operator can
// prove the configuration works before an outage does it for them.
//
// It reports the delivery error verbatim rather than a generic failure. The
// whole value of a test button is learning that the token is wrong or the host
// unreachable, and "could not send" teaches nothing.
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	if s.tester == nil {
		writeError(w, http.StatusServiceUnavailable, "this instance has no delivery pipeline")
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	ch, err := s.db.GetChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("get channel for test", "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), channelTestTimeout)
	defer cancel()

	if err := s.tester.Test(ctx, ch); err != nil {
		// 502, not 500: the failure is at the far end, and the
		// distinction tells an operator whether to look at their
		// configuration or at SubGlance.
		s.log.Warn("channel test failed", "channel", ch.Name, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	s.log.Info("channel test sent", "channel", ch.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
