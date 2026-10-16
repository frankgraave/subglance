package api

import (
	"errors"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
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

	// Delivery is how the channel's recent alerts went, nil only when the
	// outbox could not be read.
	Delivery *channelDelivery `json:"delivery"`
}

func toChannelResponse(c store.Channel, admin bool) channelResponse {
	return channelResponse{
		ID: c.ID, Name: c.Name, Type: c.Type,
		Config:    maskConfig(c.Type, c.Config, admin),
		Enabled:   c.Enabled,
		IsDefault: c.IsDefault,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// publicKeys are the config fields that may be read back in full. The list
// and its reasoning live in the notifier, which masks a failing channel's
// error with the same rules before it reports it through another channel.
var publicKeys = notifier.PublicConfigKeys

func maskConfig(typ string, cfg map[string]string, admin bool) map[string]string {
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		switch {
		case typ == store.ChannelSMS && k == smsNumbersKey && v != "":
			out[k] = smsNumbersView(cfg, admin)
		case !publicKeys[k] && v != "":
			out[k] = maskValue(v)
		default:
			out[k] = v
		}
	}
	return out
}

// smsNumbersKey is the SMS channel setting that holds its phone numbers.
const smsNumbersKey = "numbers"

// smsNumbersView is an SMS channel's numbers as the reader may see them.
//
// A phone number is personal data rather than a credential, so it gets a mask
// of its own instead of "****5678": an administrator, who manages the people
// on call, reads the numbers in full; an editor or viewer reads each one as
// "+31 6 •••• 5678", which is enough to tell whose phone a channel rings and
// no more.
func smsNumbersView(cfg map[string]string, admin bool) string {
	if admin {
		return cfg[smsNumbersKey]
	}
	return notifier.MaskSMSNumbers(cfg[smsNumbersKey], cfg["country_code"])
}

// readerIsAdmin reports whether the caller may read personal data in channel
// settings, today only an SMS channel's phone numbers.
func readerIsAdmin(r *http.Request) bool {
	u, ok := UserFromContext(r.Context())
	return ok && u.Role.CanAdmin()
}

// maskValue keeps enough of a value to recognise it without revealing it; see
// notifier.MaskValue.
func maskValue(v string) string { return notifier.MaskValue(v) }

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
	// ntfy's server is optional (it defaults to the public ntfy server), so
	// the field it cannot work without is the topic.
	store.ChannelNtfy:   "topic",
	store.ChannelGotify: "url",
	store.ChannelSMS:    smsNumbersKey,
}

// ntfyTopic mirrors the topic shape the ntfy server accepts, so a topic with a
// space or a slash in it fails on the form rather than as a 404 at 03:00.
var ntfyTopic = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// channelNameProblem refuses a channel name that is blank, too long or holds
// a control character once trimmed, which is the form it is stored in. A
// name equal to stored, the one the channel already has, is only refused
// when blank: a channel saved before a rule existed keeps its name through
// every other edit and through an import of its own export, as a monitor
// does. stored is "" for a channel that does not exist yet.
func channelNameProblem(name, stored string) problem {
	name = strings.TrimSpace(name)
	if name == "" {
		return fieldProblem("name", "name is required")
	}
	if name == stored {
		return problem{}
	}
	return nameProblem(name)
}

// validateChannel refuses a channel the notifier could not deliver through,
// naming the request field the refusal is about. storedName is the name the
// channel has now, "" on create; see channelNameProblem.
//
// Each sentence already starts with the key it is about; the field carries
// the same name on the wire, so a form can put the sentence under the input
// that has to change rather than below the whole form, and never has to
// read the key back out of the wording.
func validateChannel(req channelRequest, storedName string) problem {
	if p := channelNameProblem(req.Name, storedName); !p.ok() {
		return p
	}

	key, ok := requiredConfigKey[req.Type]
	if !ok {
		return fieldProblem("type", "type must be one of webhook, discord, slack, telegram, email, ntfy, gotify, sms")
	}
	if strings.TrimSpace(req.Config[key]) == "" {
		return fieldProblem("config."+key, "config."+key+" is required for a "+req.Type+" channel")
	}

	// A webhook target is a URL we will fetch on the user's behalf, so it has
	// to be checked here as well as at delivery time. Rejecting a bad scheme
	// now turns a silent, permanently failing channel into an error at the
	// moment the mistake is made.
	// ntfy's server URL is optional but, when given, is fetched exactly like
	// any other channel URL.
	if key == "url" || (req.Type == store.ChannelNtfy && strings.TrimSpace(req.Config["url"]) != "") {
		u, err := url.Parse(req.Config["url"])
		if err != nil {
			return fieldProblem("config.url", "config.url is not a valid URL")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fieldProblem("config.url", "config.url must use http or https")
		}
		if u.Host == "" {
			return fieldProblem("config.url", "config.url must include a host")
		}
	}

	if p := validatePushChannel(req); !p.ok() {
		return p
	}

	// Sorted, so a request with two over-long values is always blamed on
	// the same one.
	for _, k := range slices.Sorted(maps.Keys(req.Config)) {
		// A webhook's body template is the one value that is a document
		// rather than a setting, and has a ceiling of its own, checked
		// with the template.
		if req.Type == store.ChannelWebhook && k == "body" {
			continue
		}
		const msg = "config keys must be 64 characters or fewer and values 2048 or fewer"
		switch {
		case len(k) > 64:
			// A key that long is no input on any form, so nothing is
			// blamed for it but the request.
			return bodyProblem(msg)
		case len(req.Config[k]) > 2048:
			return fieldProblem("config."+k, msg)
		}
	}
	return problem{}
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

	health := s.channelDeliveries(r.Context())

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c, readerIsAdmin(r))
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		withDelivery(&resp, c, health)
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channel")
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
	resp := toChannelResponse(c, readerIsAdmin(r))
	q, ok, err := s.db.GetQuietHours(r.Context(), id)
	if err != nil {
		s.log.Error("get quiet hours", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the channel")
		return
	}
	if ok {
		resp.QuietHours = &q
	}
	withDelivery(&resp, c, s.channelDeliveries(r.Context()))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if !readJSON(w, r, &req) {
		return
	}
	if p := validateChannel(req, ""); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	if p := s.checkChannelTarget(r.Context(), req); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
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
	resp := toChannelResponse(created, readerIsAdmin(r))
	// A channel created a moment ago has sent nothing, which is a fact and
	// not a read that could fail: no outbox query for it.
	resp.Delivery = toChannelDelivery(store.ChannelHealth{}, nil, false, created)
	writeJSON(w, http.StatusCreated, resp)
}

// handleUpdateChannel replaces a channel definition.
//
// This is a PUT, not a PATCH: a channel is a small, self-contained document,
// and a partial config merge is ambiguous in a way that matters — there would
// be no way to remove a key. Sending the whole object each time keeps deletion
// expressible.
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channel")
	if !ok {
		return
	}

	var req channelRequest
	if !readJSON(w, r, &req) {
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
	//
	// Restored only while the channel still sends where it did: see
	// channel_destination.go. The keys are collected first and checked
	// after, because the destination can itself be a restored mask.
	var held []string
	if req.Config != nil {
		for k, v := range req.Config {
			if !publicKeys[k] && v == maskValue(existing.Config[k]) && existing.Config[k] != "" {
				req.Config[k] = existing.Config[k]
				held = append(held, k)
			}
		}
		// The same for phone numbers, which have a mask of their own: an
		// editor who changes an SMS channel's limit sends back the masked
		// numbers it was shown, and that means "unchanged".
		// Not under a new country code, though: a stored national number
		// is another phone under another code, one the editor never saw.
		// Then the numbers have to be typed again.
		if existing.Type == store.ChannelSMS && req.Type == store.ChannelSMS &&
			existing.Config[smsNumbersKey] != "" &&
			sameCountryCode(existing.Config, req.Config) &&
			req.Config[smsNumbersKey] == smsNumbersView(existing.Config, false) {
			req.Config[smsNumbersKey] = existing.Config[smsNumbersKey]
			held = append(held, smsNumbersKey)
		}
	}
	if moved := destinationChange(existing.Type, existing.Config, req.Type, req.Config); moved != "" && len(held) > 0 {
		slices.Sort(held)
		writeProblem(w, http.StatusBadRequest, heldCredentialProblem(held[0], moved))
		return
	}

	if p := validateChannel(req, existing.Name); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	// Edit is guarded as well as create. A channel that was saved before
	// this check existed, or before the operator dropped
	// --allow-private-targets, must not be able to keep a blocked target
	// alive simply because it is being updated rather than created.
	if p := s.checkChannelTarget(r.Context(), req); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
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
	resp := toChannelResponse(saved, readerIsAdmin(r))
	// A replace does not touch quiet hours, and the response must not
	// suggest it removed them. Nor does it touch the delivery record: an edit
	// does not make past failures disappear, and a response without the
	// record would draw the row as if they had.
	if hasQuietHours {
		resp.QuietHours = &q
	}
	withDelivery(&resp, saved, s.channelDeliveries(r.Context()))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channel")
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
	id, ok := pathID(w, r, "channel")
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
	id, ok := pathID(w, r, "channel")
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
	id, ok := pathID(w, r, "monitor")
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

	health := s.channelDeliveries(r.Context())

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c, readerIsAdmin(r))
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		withDelivery(&resp, c, health)
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

type setMonitorChannelsRequest struct {
	ChannelIDs []int64 `json:"channel_ids"`
}

// handleSetMonitorChannels replaces a monitor's channel assignments.
func (s *Server) handleSetMonitorChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "monitor")
	if !ok {
		return
	}

	var req setMonitorChannelsRequest
	if !readJSON(w, r, &req) {
		return
	}

	// Quiet hours are read before the assignments change: a failed read after
	// the replace would report a 500 for a write that was in fact stored.
	quiet, err := s.db.ListQuietHours(r.Context())
	if err != nil {
		s.log.Error("list quiet hours", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list channels")
		return
	}

	err = s.db.SetMonitorChannels(r.Context(), id, req.ChannelIDs)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if errors.Is(err, store.ErrUnknownChannel) {
		writeProblem(w, http.StatusBadRequest, unknownChannelProblem("channel_ids", err))
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

	health := s.channelDeliveries(r.Context())

	out := make([]channelResponse, 0, len(channels))
	for _, c := range channels {
		resp := toChannelResponse(c, readerIsAdmin(r))
		if q, ok := quiet[c.ID]; ok {
			resp.QuietHours = &q
		}
		withDelivery(&resp, c, health)
		out = append(out, resp)
	}
	s.log.Info("monitor channels updated", "monitor_id", id, "count", len(out))
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// validatePushChannel checks the settings each type needs beyond its required
// key: first the API's own wording for ntfy and Gotify, then every rule the
// type's sender holds the settings to (notifier.ValidateConfig). The sender
// rules are the ones that decide whether an alert can go out, so a channel
// that passes here is one its sender accepts; checking them on save is what
// puts the message on the form instead of in a failed delivery.
func validatePushChannel(req channelRequest) problem {
	cfg := req.Config
	switch req.Type {
	case store.ChannelNtfy:
		if !ntfyTopic.MatchString(strings.TrimSpace(cfg["topic"])) {
			return fieldProblem("config.topic", "config.topic may contain only letters, digits, - and _ (at most 64)")
		}
		if raw := strings.TrimSpace(cfg["url"]); raw != "" {
			if u, err := url.Parse(raw); err == nil && (u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "") {
				return fieldProblem("config.url", "config.url must name the ntfy server, not a topic or other path")
			}
		}
		if cfg["token"] != "" && strings.TrimSpace(cfg["token"]) == "" {
			return fieldProblem("config.token", "config.token must not be only whitespace")
		}
		if cfg["token"] != "" && (cfg["username"] != "" || cfg["password"] != "") {
			// The sentence leads with the token, so that is the input it
			// stands under: one of the two ways in has to go.
			return fieldProblem("config.token", "config.token cannot be combined with config.username and config.password")
		}
		if (cfg["username"] == "") != (cfg["password"] == "") {
			// Blamed on the half that is missing, which is the one to type.
			missing := "config.password"
			if cfg["username"] == "" {
				missing = "config.username"
			}
			return fieldProblem(missing, "config.username and config.password must be set together")
		}
	case store.ChannelGotify:
		if strings.TrimSpace(cfg["token"]) == "" {
			return fieldProblem("config.token", "config.token is required for a gotify channel")
		}
		for _, key := range []string{"priority_down", "priority_up"} {
			raw := strings.TrimSpace(cfg[key])
			if raw == "" {
				continue
			}
			if n, err := strconv.Atoi(raw); err != nil || n < 0 || n > 10 {
				return fieldProblem("config."+key, "config."+key+" must be a whole number from 0 to 10")
			}
		}
	}
	// One rule set, shared with the sender, so the form and the delivery
	// cannot disagree: a webhook's method, headers and body template, an
	// SMS channel's numbers, an email channel's host, sender and port, a
	// Telegram chat, a Discord URL. The type is known here (validateChannel
	// refused any other before this point).
	if err := notifier.ValidateConfig(req.Type, cfg); err != nil {
		return notifierProblem(err)
	}
	return problem{}
}

// notifierProblem is a notifier validation error as an API refusal: the
// sentence prefixed with "config.", as the key it starts with is a config
// key, and the field the notifier names (notifier.ConfigKey). A rule that is
// not about one key stays a refusal of the request.
func notifierProblem(err error) problem {
	msg := "config." + err.Error()
	if key := notifier.ConfigKey(err); key != "" {
		return fieldProblem("config."+key, msg)
	}
	return bodyProblem(msg)
}
