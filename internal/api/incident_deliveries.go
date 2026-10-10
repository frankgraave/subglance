package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// What became of one alert on one channel, as an incident's delivery list
// names it.
const (
	// incidentDeliveryDelivered: the channel accepted it.
	incidentDeliveryDelivered = "delivered"
	// incidentDeliveryFailed: it gave up after its retries.
	incidentDeliveryFailed = "failed"
	// incidentDeliveryRetrying: an attempt failed and another is due.
	incidentDeliveryRetrying = "retrying"
	// incidentDeliveryQueued: waiting for its first attempt.
	incidentDeliveryQueued = "queued"
	// incidentDeliveryHeld: waiting for the channel's quiet hours to end.
	incidentDeliveryHeld = "held"
	// incidentDeliveryMerged: not sent on its own, because another delivery
	// carried its news: the morning's quiet-hours digest, or the recovery
	// that went out in its place. CarriedBy names that delivery.
	incidentDeliveryMerged = "merged"
	// incidentDeliveryNotSent: closed without being sent, for the reason in
	// Reason: maintenance, quiet hours set to drop, or the channel declining
	// it.
	incidentDeliveryNotSent = "not_sent"
)

// incidentDelivery is one alert about an incident, aimed at one channel.
type incidentDelivery struct {
	ID          int64  `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ChannelType string `json:"channel_type"`
	// Event is what the message said: incident_confirmed, incident_reminder,
	// incident_resolved, or quiet_hours_digest for a digest that carried it.
	Event string `json:"event"`
	State string `json:"state"`

	Attempts int        `json:"attempts"`
	QueuedAt time.Time  `json:"queued_at"`
	EndedAt  *time.Time `json:"ended_at"`

	// Error is the newest failure's message, with the channel's credentials
	// taken out, on a delivery that failed or is being retried; empty
	// otherwise.
	Error string `json:"error"`
	// Reason says why a not_sent delivery was not sent; empty otherwise.
	Reason string `json:"reason"`
	// MergedInto is "digest" or "recovery" on a merged delivery, and
	// CarriedBy the id of the delivery that carried it, when that is known.
	MergedInto string `json:"merged_into,omitempty"`
	CarriedBy  *int64 `json:"carried_by,omitempty"`
}

// incidentDeliveriesResponse is the list, plus what it can and cannot show.
type incidentDeliveriesResponse struct {
	IncidentID int64              `json:"incident_id"`
	Deliveries []incidentDelivery `json:"deliveries"`
	// WindowDays is how long the outbox keeps a delivered or skipped
	// alert. Complete is false when the incident started longer ago than
	// that, so some of its deliveries may be gone: a short list then means
	// less than it says. Failed deliveries are kept whatever their age.
	WindowDays int  `json:"window_days"`
	Complete   bool `json:"complete"`
}

// incidentDeliveryState decides which of the states a delivery is in.
//
// The outcome comes first: a row that failed is failed, even if it was also
// closed for maintenance on the way, because the failure is what a reader
// needs to know.
func incidentDeliveryState(d store.Delivery) string {
	switch {
	case d.Status == store.OutboxDelivered:
		return incidentDeliveryDelivered
	case d.Status == store.OutboxFailed:
		return incidentDeliveryFailed
	case d.MergedInto() != "":
		return incidentDeliveryMerged
	case d.Suppressed:
		return incidentDeliveryNotSent
	case d.QuietHeld:
		return incidentDeliveryHeld
	case d.Attempts > 0:
		return incidentDeliveryRetrying
	default:
		return incidentDeliveryQueued
	}
}

// handleListIncidentDeliveries lists where an incident's alerts went.
//
// The list is read from the outbox, which already holds every alert sent per
// channel with its outcome. Send test deliveries never enter the outbox, so
// none is listed here, and the notice about a failing channel goes out
// directly for the same reason: neither is an alert about this incident.
func (s *Server) handleListIncidentDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	ctx := r.Context()

	inc, err := s.db.GetIncident(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		s.log.Error("get incident", "incident_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list deliveries")
		return
	}

	rows, err := s.db.IncidentDeliveries(ctx, inc)
	if err != nil {
		s.log.Error("list incident deliveries", "incident_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list deliveries")
		return
	}
	rows, err = s.withCarriers(r, rows)
	if err != nil {
		s.log.Error("read carrying deliveries", "incident_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list deliveries")
		return
	}

	channels, err := s.db.ListChannels(ctx)
	if err != nil {
		s.log.Error("list channels for incident deliveries", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list deliveries")
		return
	}
	byID := make(map[int64]store.Channel, len(channels))
	for _, ch := range channels {
		byID[ch.ID] = ch
	}

	out := incidentDeliveriesResponse{
		IncidentID: id,
		Deliveries: make([]incidentDelivery, 0, len(rows)),
		WindowDays: int(store.DeliveryLogRetention / (24 * time.Hour)),
		Complete:   !inc.StartedAt.Before(time.Now().Add(-store.DeliveryLogRetention)),
	}
	for _, d := range rows {
		out.Deliveries = append(out.Deliveries, toIncidentDelivery(d, byID[d.ChannelID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// withCarriers adds the delivery that carried each merged one, when the
// list does not hold it already.
//
// A quiet-hours digest is carried by the first alert of the night to come
// due, which can be an alert about another incident queued before this one
// began. The list is read from the incident's start onwards, so that row
// would be missing, and the line that says "sent in the digest" would point
// at nothing.
func (s *Server) withCarriers(r *http.Request, rows []store.Delivery) ([]store.Delivery, error) {
	have := make(map[int64]bool, len(rows))
	for _, d := range rows {
		have[d.ID] = true
	}
	var extra []store.Delivery
	for _, d := range rows {
		carrier := d.Carrier()
		if carrier == 0 || have[carrier] {
			continue
		}
		c, err := s.db.GetDelivery(r.Context(), carrier)
		if errors.Is(err, store.ErrNotFound) {
			continue // pruned, or gone with its channel
		}
		if err != nil {
			return nil, err
		}
		have[carrier] = true
		extra = append(extra, c)
	}
	if len(extra) == 0 {
		return rows, nil
	}
	// The carriers were queued before every row in the list.
	return append(extra, rows...), nil
}

func toIncidentDelivery(d store.Delivery, ch store.Channel) incidentDelivery {
	out := incidentDelivery{
		ID:          d.ID,
		ChannelID:   d.ChannelID,
		ChannelName: ch.Name,
		ChannelType: ch.Type,
		Event:       d.Event,
		State:       incidentDeliveryState(d),
		Attempts:    d.Attempts,
		QueuedAt:    d.CreatedAt,
	}
	switch out.State {
	case incidentDeliveryDelivered, incidentDeliveryFailed, incidentDeliveryMerged, incidentDeliveryNotSent:
		ended := d.UpdatedAt
		out.EndedAt = &ended
	}
	switch out.State {
	case incidentDeliveryFailed, incidentDeliveryRetrying:
		out.Error = redactDeliveryError(d.LastError, ch)
	case incidentDeliveryNotSent:
		out.Reason = redactDeliveryError(d.LastError, ch)
	case incidentDeliveryMerged:
		out.MergedInto = d.MergedInto()
		if c := d.Carrier(); c != 0 {
			out.CarriedBy = &c
		}
	}
	return out
}
