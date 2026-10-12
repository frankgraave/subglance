package api

import (
	"strings"

	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

// A channel's credentials are never readable through the API: they are
// masked on read for every role, and withheld from an export. What a client
// cannot read it sends back as its mask (or as an export placeholder), and the
// server restores the stored value. That restore is only safe while the
// credential still goes where it was saved for. A client that changes where a
// channel sends to and echoes the mask of its password would otherwise be
// handing a credential it was never shown to a server of its choosing — an
// SMTP host that logs PLAIN auth, a webhook URL that records its headers.
//
// So a channel whose destination changes takes its credentials typed again,
// which proves the person changing it holds them.

// destinationKeys are the settings that name the server a channel's
// credentials are sent to, per type. Telegram has none: its bot token only
// ever goes to Telegram's own API, whichever chat it posts in.
var destinationKeys = map[string][]string{
	store.ChannelWebhook:  {"url"},
	store.ChannelDiscord:  {"url"},
	store.ChannelSlack:    {"url"},
	store.ChannelGotify:   {"url"},
	store.ChannelNtfy:     {"url"},
	store.ChannelEmail:    {"host", "port"},
	store.ChannelSMS:      {"provider", "url"},
	store.ChannelTelegram: nil,
}

// destinationDefaults are the values a sender uses for a destination setting
// that is left empty, so an empty value and its default are one destination.
var destinationDefaults = map[string]map[string]string{
	store.ChannelNtfy:  {"url": notifier.DefaultNtfyServer},
	store.ChannelEmail: {"port": "587"},
}

// destinationValue is one destination setting as the sender reads it.
func destinationValue(typ string, cfg map[string]string, key string) string {
	v := strings.TrimSpace(cfg[key])
	if v == "" {
		return destinationDefaults[typ][key]
	}
	return v
}

// destinationChange names what moved a channel to another destination: "the
// type", or the first destination setting that differs, as "config.<key>".
// It is empty when the channel still sends where it did.
func destinationChange(fromType string, from map[string]string, toType string, to map[string]string) string {
	if fromType != toType {
		return "the type"
	}
	for _, k := range destinationKeys[toType] {
		if destinationValue(fromType, from, k) != destinationValue(toType, to, k) {
			return "config." + k
		}
	}
	return ""
}

// heldCredentialProblem refuses a stored value kept for a channel that now
// sends somewhere else. key is the config key whose stored value was asked
// for; moved is what destinationChange reported.
func heldCredentialProblem(key, moved string) problem {
	return fieldProblem("config."+key, "config."+key+" must be entered again because "+moved+
		" changed: a stored value is only sent to the destination it was saved for")
}
