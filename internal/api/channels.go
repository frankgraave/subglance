package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// channelResponse is the wire shape of a notification channel.
//
// Config is returned with secret-looking values masked; see maskConfig.
type channelResponse struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`

	Enabled bool `json:"enabled"`
	// IsDefault marks the channel that monitors without channels of their
	// own alert through.
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// QuietHours is the channel's daily quiet window, nil when it has none.
	QuietHours *store.QuietHours `json:"quiet_hours"`
}

func toChannelResponse(c store.Channel) channelResponse {
	return channelResponse{
		ID: c.ID, Name: c.Name, Type: c.Type,
		Config:    maskConfig(c.Type, c.Config),
		Enabled:   c.Enabled,
		IsDefault: c.IsDefault,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// publicKeys are the config fields that may be read back in full.
//
// Deny by default, which is the opposite of how this started. The first
// version listed the secret keys instead, and that could not hold: a config
// accepts any key name, so the mask only ever covered the names someone had
// thought of. A channel carrying "authorization" or "secret" handed those
// straight to any viewer — the role that exists specifically to look without
// touching.
//
// Listing what is safe is a smaller and more checkable claim than listing what
// is dangerous. A new channel type that needs another public field has to say
// so here, and until it does its value is masked: the failure mode of
// forgetting is an over-masked field in the interface, not a leaked
// credential.
var publicKeys = map[string]bool{
	// Where a message goes, rather than what proves the right to send it.
	"to":       true,
	"from":     true,
	"chat_id":  true,
	"channel":  true,
	"username": true,
	"host":     true,
	"port":     true,
}

func maskConfig(_ string, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if !publicKeys[k] && v != "" {
			out[k] = maskValue(v)
			continue
		}
		out[k] = v
	}
	return out
}

// maskValue keeps enough of a value to recognise it without revealing it.
//
// The tail is shown rather than the head because that is the part that differs
// between two Slack webhooks; their prefixes are identical.
func maskValue(v string) string {
	const keep = 4
	if len(v) <= keep {
		return strings.Repeat("*", len(v))
	}
	return "****" + v[len(v)-keep:]
}

type channelRequest struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Config  map[string]string `json:"config"`
	Enabled *bool             `json:"enabled"`
}

// requiredConfigKey names the config field each channel type cannot work
// without. Types are validated against this map rather than a free-form list,
// so an unknown type is rejected in Go before SQLite's CHECK constraint turns
// it into a 500.
var requiredConfigKey = map[string]string{
	store.ChannelWebhook:  "url",
	store.ChannelDiscord:  "url",
	store.ChannelSlack:    "url",
	store.ChannelTelegram: "bot_token",
	store.ChannelEmail:    "to",
}

func validateChannel(req channelRequest) string {
	if strings.TrimSpace(req.Name) == "" {
		return "name is required"
	}
	if len(req.Name) > 100 {
		return "name must be 100 characters or fewer"
	}

	key, ok := requiredConfigKey[req.Type]
	if !ok {
		return "type must be one of webhook, discord, slack, telegram, email"
	}
	if strings.TrimSpace(req.Config[key]) == "" {
		return "config." + key + " is required for a " + req.Type + " channel"
	}

	// A webhook target is a URL we will fetch on the user's behalf, so it has
	// to be checked here as well as at delivery time. Rejecting a bad scheme
	// now turns a silent, permanently failing channel into an error at the
	// moment the mistake is made.
	if key == "url" {
		u, err := url.Parse(req.Config[key])
		if err != nil {
			return "config.url is not a valid URL"
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "config.url must use http or https"
		}
		if u.Host == "" {
			return "config.url must include a host"
		}
	}

	for k, v := range req.Config {
		if len(k) > 64 || len(v) > 2048 {
			return "config keys must be 64 characters or fewer and values 2048 or fewer"
		}
	}
	return ""
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.db.ListChannels(r.Context())
	if err != nil {
		s.log.Error("list channels", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list channels")
		return
	}

	quiet, err := s.db.ListQuietHours(r.Context())
	if err != nil {
		s.log.Error("list quiet hours", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list channels")
		return
	}

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c)
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	c, err := s.db.GetChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("get channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}
	resp := toChannelResponse(c)
	q, ok, err := s.db.GetQuietHours(r.Context(), id)
	if err != nil {
		s.log.Error("get quiet hours", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}
	if ok {
		resp.QuietHours = &q
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if msg := validateChannel(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := s.checkChannelTarget(r.Context(), req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	c := store.Channel{
		Name:    strings.TrimSpace(req.Name),
		Type:    req.Type,
		Config:  req.Config,
		Enabled: true,
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}

	created, err := s.db.CreateChannel(r.Context(), c)
	if err != nil {
		s.log.Error("create channel", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create the channel")
		return
	}

	s.log.Info("channel created", "channel_id", created.ID, "type", created.Type)
	writeJSON(w, http.StatusCreated, toChannelResponse(created))
}

// handleUpdateChannel replaces a channel definition.
//
// This is a PUT, not a PATCH: a channel is a small, self-contained document,
// and a partial config merge is ambiguous in a way that matters — there would
// be no way to remove a key. Sending the whole object each time keeps deletion
// expressible.
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var req channelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	existing, err := s.db.GetChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("get channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}

	// A client that reads a channel and writes it back sends the masked
	// secret, because that is all it ever saw. Treating the mask as a literal
	// value would silently destroy the credential on every round trip, so a
	// value that still equals its own mask means "unchanged".
	//
	// This follows the same deny-by-default rule as the mask itself: any
	// field that was masked on read can come back as its mask, which is
	// exactly the set of fields that are not public.
	if req.Config != nil {
		for k, v := range req.Config {
			if !publicKeys[k] && v == maskValue(existing.Config[k]) && existing.Config[k] != "" {
				req.Config[k] = existing.Config[k]
			}
		}
	}

	if msg := validateChannel(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// Edit is guarded as well as create. A channel that was saved before
	// this check existed, or before the operator dropped
	// --allow-private-targets, must not be able to keep a blocked target
	// alive simply because it is being updated rather than created.
	if msg := s.checkChannelTarget(r.Context(), req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	updated := store.Channel{
		ID:      id,
		Name:    strings.TrimSpace(req.Name),
		Type:    req.Type,
		Config:  req.Config,
		Enabled: existing.Enabled,
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}

	// Loaded before the update so a failed lookup cannot turn into a 200
	// that reports quiet_hours: null, which means "none configured".
	q, hasQuietHours, err := s.db.GetQuietHours(r.Context(), id)
	if err != nil {
		s.log.Error("get quiet hours before channel update", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}

	saved, err := s.db.UpdateChannel(r.Context(), updated)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("update channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update the channel")
		return
	}

	s.log.Info("channel updated", "channel_id", id)
	resp := toChannelResponse(saved)
	// A replace does not touch quiet hours, and the response must not
	// suggest it removed them.
	if hasQuietHours {
		resp.QuietHours = &q
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	err := s.db.DeleteChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("delete channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the channel")
		return
	}

	s.log.Info("channel deleted", "channel_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleSetDefaultChannel makes a channel the instance-wide default.
//
// The default is what keeps a monitor nobody routed from failing in silence:
// a monitor with no channels of its own alerts through it. Setting one moves
// the flag, so there is never more than one.
func (s *Server) handleSetDefaultChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.db.SetDefaultChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("set default channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not set the default channel")
		return
	}
	s.log.Info("default channel set", "channel_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleClearDefaultChannel stops a channel being the default.
//
// Scoped to the channel in the path rather than "whatever the default is", so
// a client acting on a stale screen cannot unset a default chosen after that
// screen was drawn.
func (s *Server) handleClearDefaultChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.db.ClearDefaultChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("clear default channel", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clear the default channel")
		return
	}
	s.log.Info("default channel cleared", "channel_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleListMonitorChannels returns the channels a monitor alerts through.
func (s *Server) handleListMonitorChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if !s.requireMonitor(w, r, id) {
		return
	}

	channels, err := s.db.ListMonitorChannels(r.Context(), id)
	if err != nil {
		s.log.Error("list monitor channels", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list the channels")
		return
	}

	quiet, err := s.db.ListQuietHours(r.Context())
	if err != nil {
		s.log.Error("list quiet hours", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list channels")
		return
	}

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c)
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

type setMonitorChannelsRequest struct {
	ChannelIDs []int64 `json:"channel_ids"`
}

// handleSetMonitorChannels replaces a monitor's channel assignments.
func (s *Server) handleSetMonitorChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var req setMonitorChannelsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	err := s.db.SetMonitorChannels(r.Context(), id, req.ChannelIDs)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if errors.Is(err, store.ErrUnknownChannel) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.log.Error("set monitor channels", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update the channels")
		return
	}

	channels, err := s.db.ListMonitorChannels(r.Context(), id)
	if err != nil {
		s.log.Error("list monitor channels", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list the channels")
		return
	}

	quiet, err := s.db.ListQuietHours(r.Context())
	if err != nil {
		s.log.Error("list quiet hours", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list channels")
		return
	}

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c)
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		out = append(out, resp)
	}
	s.log.Info("monitor channels updated", "monitor_id", id, "count", len(out))
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}
