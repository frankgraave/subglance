package notifier

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// PublicConfigKeys are the channel config fields that may be read back in
// full: by the API, by a configuration export, and in the notice that reports
// a failing channel through another one.
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
var PublicConfigKeys = map[string]bool{
	// Where a message goes, rather than what proves the right to send it.
	"to":       true,
	"from":     true,
	"chat_id":  true,
	"channel":  true,
	"username": true,
	"host":     true,
	"port":     true,
	// Gotify priorities: how loud an alert is, not who may send one.
	//
	// ntfy's `topic` is deliberately NOT here. On a server without access
	// control the topic name is the whole credential: whoever knows it can
	// subscribe to the alerts and post fake ones.
	"priority_down": true,
	"priority_up":   true,
	// SMS: which provider, how numbers without a country code are read,
	// the hourly limit, whether recoveries are sent, and the zone the times
	// in a message are written in. A Twilio account SID names the account,
	// like a username; the auth token is what proves the right to use it,
	// and stays masked. The numbers are masked by role, see smsNumbersView.
	"provider":     true,
	"country_code": true,
	"hourly_limit": true,
	"recoveries":   true,
	"timezone":     true,
	"account_sid":  true,
}

// MaskValue keeps enough of a value to recognise it without revealing it.
//
// The tail is shown rather than the head because that is the part that differs
// between two Slack webhooks; their prefixes are identical.
func MaskValue(v string) string {
	const keep = 4
	if len(v) <= keep {
		return strings.Repeat("*", len(v))
	}
	return "****" + v[len(v)-keep:]
}

// urlInText finds the URLs in a delivery error.
var urlInText = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// minRedactLen is the length from which a credential is replaced wherever it
// appears in an error message. A shorter value is still a credential, and an
// ntfy server that echoes a three-letter topic in its 4xx body would read it
// out to every viewer, so it is replaced too, but only where it stands as a
// token of its own: replaced everywhere, a two-character password would
// shred every word that happens to contain it.
const minRedactLen = 4

// RedactError takes a channel's credentials out of a delivery error.
//
// The notifier stores errors as the transport wrote them, and Go's HTTP
// client writes the whole request URL into every one: Post
// "https://hooks.slack.com/services/…": dial tcp …. For Slack, Discord
// and a plain webhook that URL is the credential, which the config mask
// exists to keep from anybody reading the channel list, viewers included,
// and from whoever reads the channel_failing notice in another channel.
//
// Two passes, because either alone has a gap. Every masked config value is
// replaced by its own mask wherever it appears, which catches a token
// embedded in a longer string and hides a stored URL as a whole, exactly as
// the config mask does. Then every URL that is left keeps its scheme and host
// and loses its path and query, which catches a credential a sender put into
// a URL it built, such as Telegram's bot token in the API path. The host is
// what diagnoses "no such host" or "connection refused"; the path is what
// grants access.
func RedactError(msg string, cfg map[string]string) string {
	if msg == "" {
		return ""
	}
	// Longest first: a shorter value inside a longer one would otherwise be
	// replaced first, depending on map order, and leave the rest of the
	// longer value in the message unmasked.
	var vals, short []string
	for k, v := range cfg {
		t := strings.TrimSpace(v)
		if PublicConfigKeys[k] || t == "" {
			continue
		}
		if len(t) < minRedactLen {
			short = append(short, t)
			continue
		}
		vals = append(vals, v)
		if t != v {
			vals = append(vals, t)
		}
	}
	longestFirst := func(a, b string) int { return cmp.Compare(len(b), len(a)) }
	slices.SortFunc(vals, longestFirst)
	for _, v := range vals {
		msg = strings.ReplaceAll(msg, v, MaskValue(v))
	}
	slices.SortFunc(short, longestFirst)
	for _, v := range short {
		msg = replaceToken(msg, v, MaskValue(v))
	}
	return urlInText.ReplaceAllStringFunc(msg, trimURL)
}

// replaceToken replaces v in msg wherever it is not part of a longer word:
// the byte on either side of it, if there is one, is not a letter or digit.
func replaceToken(msg, v, mask string) string {
	var b strings.Builder
	from, last := 0, 0
	for {
		i := strings.Index(msg[from:], v)
		if i < 0 {
			break
		}
		i += from
		end := i + len(v)
		if (i == 0 || !isWordByte(msg[i-1])) && (end == len(msg) || !isWordByte(msg[end])) {
			b.WriteString(msg[last:i])
			b.WriteString(mask)
			last, from = end, end
			continue
		}
		from = i + 1
	}
	b.WriteString(msg[last:])
	return b.String()
}

// isWordByte reports whether c can be part of a word. Any byte of a
// multi-byte character counts, so a value never splits a non-ASCII word.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// trimURL keeps a URL's scheme and host, and marks that more was there.
func trimURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	host, tail := rest, ""
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		host, tail = rest[:end], rest[end:]
	}
	// Userinfo is a credential too: "https://user:pass@host/".
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if tail == "" || tail == "/" {
		return scheme + "://" + host + tail
	}
	return scheme + "://" + host + "/…"
}
