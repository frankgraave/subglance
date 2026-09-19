package api

import (
	"context"

	"github.com/frankgraave/subglance/internal/store"
)

// responseHistoryHeartbeat extends the raw, detail-only heartbeat endpoint.
// The dashboard's bulk heartbeat payload deliberately does not carry reasons.
type responseHistoryHeartbeat struct {
	heartbeatResponse
	// Decimal string keeps SQLite's int64 identity exact in browser clients.
	ID                    int64               `json:"id,string"`
	ResponseCaptureReason store.CaptureReason `json:"response_capture_reason,omitempty"`
}

func (s *Server) describeResponseHistory(ctx context.Context, hbs []store.Heartbeat) ([]responseHistoryHeartbeat, error) {
	reasons, err := s.db.HeartbeatCaptureReasons(ctx, hbs)
	if err != nil {
		return nil, err
	}
	out := make([]responseHistoryHeartbeat, 0, len(hbs))
	for _, hb := range hbs {
		out = append(out, responseHistoryHeartbeat{
			heartbeatResponse:     describeHeartbeat(hb),
			ID:                    hb.ID,
			ResponseCaptureReason: reasons[hb.ID],
		})
	}
	return out, nil
}
