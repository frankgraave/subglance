package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/events"
)

// SSE tuning.
//
// heartbeatInterval is the comment ping that keeps the connection alive. It
// exists for two reasons, and the second is the one that bites: proxies and
// load balancers close idle connections (nginx defaults to 60s), and a browser
// cannot distinguish "nothing has happened" from "this socket is dead". The
// ping makes silence meaningful — if the client stops receiving them, the
// connection really is gone.
//
// 20s is comfortably inside common proxy timeouts without being chatty.
const (
	sseHeartbeatInterval = 20 * time.Second

	// sseRetryHint tells the browser how long to wait before reconnecting.
	// EventSource defaults to 3s; being explicit keeps behaviour predictable
	// across browsers.
	sseRetryHint = 3 * time.Second
)

// handleStream serves the live event stream over Server-Sent Events.
//
// SSE rather than WebSockets because the traffic is one-directional: the
// server tells the browser what happened, and the browser never needs to talk
// back over the same channel. SSE also reconnects on its own and survives
// proxies that mangle WebSocket upgrades.
//
// GET /api/v1/stream
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.bus == nil {
		writeError(w, http.StatusServiceUnavailable, "live updates are not available")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing, events would sit in a buffer until the response
		// ended — which for a stream is never.
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Defeats nginx's response buffering, which otherwise holds events until
	// its buffer fills and makes a live stream look broken.
	h.Set("X-Accel-Buffering", "no")

	sub := s.bus.Subscribe()
	defer sub.Close()

	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", sseRetryHint.Milliseconds()); err != nil {
		return
	}

	// Tell the client where the stream starts. A client reconnecting with a
	// Last-Event-ID older than this knows it has a hole and should re-fetch
	// rather than assume its view is current.
	lastSeen := parseLastEventID(r)
	current := s.bus.LastSeq()
	if err := writeSSE(w, "hello", helloPayload{
		Seq:     current,
		Gap:     lastSeen > 0 && lastSeen < current,
		Missed:  gapSize(lastSeen, current),
		ServerT: time.Now(),
	}); err != nil {
		return
	}
	flusher.Flush()

	ping := time.NewTicker(sseHeartbeatInterval)
	defer ping.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			// Client went away. Nothing to clean up beyond the deferred
			// unsubscribe; this is the normal exit path for a closed tab.
			return

		case <-ping.C:
			// A comment line: valid SSE, ignored by EventSource, keeps
			// intermediaries from reaping the connection.
			//
			// A failed write here is how a dead connection is usually
			// discovered: the request context is not always cancelled
			// promptly, so without this check the handler would keep
			// writing into a closed socket.
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case e, open := <-sub.C():
			if !open {
				return
			}

			// Report loss the moment it happens rather than letting the
			// client believe its picture is complete.
			if dropped := sub.Dropped(); dropped > 0 {
				if err := writeSSE(w, "lagged", laggedPayload{Dropped: dropped}); err != nil {
					return
				}
			}

			if err := writeSSE(w, string(e.Kind), e); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type helloPayload struct {
	Seq     uint64    `json:"seq"`
	Gap     bool      `json:"gap"`
	Missed  uint64    `json:"missed,omitempty"`
	ServerT time.Time `json:"server_time"`
}

type laggedPayload struct {
	Dropped uint64 `json:"dropped"`
}

// writeSSE emits one event frame.
//
// The id line lets the browser resume with Last-Event-ID after a drop.
//
// It returns the write error so the caller can stop streaming to a client that
// has gone away, rather than looping on a socket nobody is reading. A payload
// that cannot be marshalled is a programming error rather than a dead
// connection, so it is skipped without ending the stream — the frame is built
// completely before anything is written, which keeps a half-written frame from
// corrupting the stream.
func writeSSE(w http.ResponseWriter, event string, payload any) error {
	frame, ok := buildSSEFrame(event, payload)
	if !ok {
		return nil
	}
	_, err := io.WriteString(w, frame)
	return err
}

// buildSSEFrame renders one frame, reporting false when the payload cannot be
// serialised.
func buildSSEFrame(event string, payload any) (string, bool) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}

	var sb strings.Builder
	if e, ok := payload.(events.Event); ok && e.Seq > 0 {
		fmt.Fprintf(&sb, "id: %d\n", e.Seq)
	}
	fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", event, body)
	return sb.String(), true
}

// parseLastEventID reads the resume point a reconnecting client sends.
//
// The header is the standard mechanism; the query parameter is a fallback for
// clients that cannot set headers on an EventSource (browsers cannot).
func parseLastEventID(r *http.Request) uint64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func gapSize(from, to uint64) uint64 {
	if from == 0 || to <= from {
		return 0
	}
	return to - from
}
