package configfile

// WithheldStandIn returns a value that satisfies a channel sender's rules
// in place of a required value written as Placeholder, or false when the
// key has no stand-in.
//
// The importer validates a channel whose credential is withheld as if the
// credential were there, so a file is refused for what it does say and not
// for what it leaves out; the stand-in is never stored. The Uptime Kuma
// converter holds a channel to the same rules with the same stand-ins
// before it writes it, so a file it writes is one the importer accepts.
// Keeping them here is what keeps the two from drifting apart.
func WithheldStandIn(channelType, key string) (string, bool) {
	if v, ok := withheldTypeStandIn[channelType][key]; ok {
		return v, true
	}
	v, ok := withheldStandIn[key]
	return v, ok
}

// withheldStandIn is the stand-in for each key, whatever the channel type.
// The .invalid top-level domain is reserved and can never resolve
// (RFC 2606).
var withheldStandIn = map[string]string{
	"url":       "https://withheld.invalid/",
	"bot_token": "0:withheld",
	// An SMS channel's numbers, password and auth token are all withheld on
	// export, and the SMS rules require each of them.
	"numbers":    "+10000000000",
	"password":   "withheld",
	"auth_token": "withheld",
	// ntfy's topic and Gotify's application token are withheld too, and each
	// is the field its channel type cannot work without.
	"topic": "withheld",
	"token": "withheld",
}

// withheldTypeStandIn overrides withheldStandIn where a type's sender holds
// the value to a shape of its own: a Discord channel's URL has to name
// Discord, and the generic stand-in does not. The keys are channel types as
// the store names them.
var withheldTypeStandIn = map[string]map[string]string{
	"discord": {"url": "https://discord.com/api/webhooks/0/withheld"},
}
