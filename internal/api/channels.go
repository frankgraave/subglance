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

	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toChannelResponse(c store.Channel) channelResponse {
	return channelResponse{
		ID: c.ID, Name: c.Name, Type: c.Type,
		Config:    maskConfig(c.Type, c.Config),
		Enabled:   c.Enabled,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// secretKeys are config fields that must never be echoed back in full.
//
// A webhook URL is itself the credential for Slack and Discord: anyone holding
// it can post to the channel. Returning it verbatim would mean every viewer
// with read access to the API could exfiltrate it, so reads get a masked form
// and only the writer who set it ever sees the whole value.
var secretKeys = map[string]bool{
	"url":       true,
	"token":     true,
	"password":  true,
	"bot_token": true,
}

func maskConfig(_ string, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if secretKeys[k] && v != "" {
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

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		out = append(out, toChannelResponse(c))
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
	writeJSON(w, http.StatusOK, toChannelResponse(c))
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
	if req.Config != nil {
		for k, v := range req.Config {
			if secretKeys[k] && v == maskValue(existing.Config[k]) && existing.Config[k] != "" {
				req.Config[k] = existing.Config[k]
			}
		}
	}

	if msg := validateChannel(req); msg != "" {
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
	writeJSON(w, http.StatusOK, toChannelResponse(saved))
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

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		out = append(out, toChannelResponse(c))
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

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		out = append(out, toChannelResponse(c))
	}
	s.log.Info("monitor channels updated", "monitor_id", id, "count", len(out))
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}
