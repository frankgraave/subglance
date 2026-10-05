package kumaimport

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/configfile"
)

// maxChannelName is the channel name limit the API enforces.
const maxChannelName = 100

// convertChannel converts one row of Kuma's notification table.
//
// Every value that proves the right to send is written as the placeholder,
// the same rule an export follows: the file is meant to be read, kept and
// passed around before it is imported, and Kuma's tokens have no business in
// it. A server address, a recipient or a chat id is written as it is,
// because it says where a message goes and the user would otherwise have to
// look it up again. The import creates a channel with a placeholder switched
// off and lists the field under needs_secrets.
func convertChannel(n row, res *Result) (configfile.Channel, bool) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(n.str("config")), &cfg)
	get := func(k string) string {
		switch v := cfg[k].(type) {
		case nil:
			return ""
		case string:
			return strings.TrimSpace(v)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			return ""
		}
	}

	typ := get("type")
	if typ == "" {
		typ = n.str("type")
	}
	name := strings.TrimSpace(n.str("name"))
	if name == "" {
		name = get("name")
	}
	if name == "" {
		name = "Kuma " + typ
	}
	note := func(reason string) { changed(res, "channel", name, typ, reason) }
	skip := func(reason string) (configfile.Channel, bool) {
		res.Skipped = append(res.Skipped, Note{Kind: "channel", Name: name, Type: typ, Reason: reason})
		return configfile.Channel{}, false
	}
	if utf8.RuneCountInString(name) > maxChannelName {
		name = string([]rune(name)[:maxChannelName])
		note("the name was shortened to " + strconv.Itoa(maxChannelName) + " characters")
	}

	out := configfile.Channel{Name: name, Enabled: ptr(n.bool("active")), Config: map[string]string{}}
	secret := func(key string) { out.Config[key] = configfile.Placeholder }
	plain := func(key, value string) {
		if value != "" {
			out.Config[key] = value
		}
	}

	switch typ {
	case "discord":
		out.Type = "discord"
		secret("url")
	case "slack":
		out.Type = "slack"
		secret("url")
	case "telegram":
		out.Type = "telegram"
		secret("bot_token")
		plain("chat_id", get("telegramChatID"))
		if get("telegramMessageThreadID") != "" {
			note("messages went to topic " + get("telegramMessageThreadID") + " of the chat; SubGlance posts to the chat itself")
		}
	case "smtp":
		out.Type = "email"
		to := get("smtpTo")
		if cc := get("smtpCC"); cc != "" {
			to = joinRecipients(to, cc)
			note("the Cc recipients are now ordinary recipients")
		}
		if get("smtpBCC") != "" {
			note("the Bcc recipients were left out: SubGlance has no Bcc, and adding them as recipients would show their addresses to everyone")
		}
		if to == "" {
			return skip("it has no recipients")
		}
		plain("to", to)
		plain("from", get("smtpFrom"))
		plain("host", get("smtpHost"))
		plain("port", get("smtpPort"))
		if get("smtpUsername") != "" {
			plain("username", get("smtpUsername"))
			secret("password")
		}
		if get("smtpSecure") == "true" {
			note("Kuma connected with TLS from the start; SubGlance uses STARTTLS, so a server that only offers TLS on port 465 needs its submission port, usually 587")
		}
	case "ntfy":
		out.Type = "ntfy"
		// The server and the user name say where messages go. The topic
		// does not: on a server without access control the topic name is
		// the whole credential, and SubGlance withholds it everywhere else.
		plain("url", strings.TrimRight(get("ntfyserverurl"), "/"))
		secret("topic")
		switch get("ntfyAuthenticationMethod") {
		case "usernamePassword":
			plain("username", get("ntfyusername"))
			secret("password")
		case "accessToken":
			secret("token")
		}
	case "gotify":
		out.Type = "gotify"
		plain("url", get("gotifyserverurl"))
		secret("token")
		if p := get("gotifyPriority"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n >= 0 && n <= 10 {
				plain("priority_down", p)
			}
		}
	case "webhook":
		out.Type = "webhook"
		secret("url")
		if raw := get("webhookAdditionalHeaders"); raw != "" && raw != "{}" {
			secret("headers")
		}
		if ct := get("webhookContentType"); ct == "custom" {
			note("Kuma posted a custom body; SubGlance posts its own JSON payload, so the receiver has to read that instead")
		} else {
			note("SubGlance posts its own JSON payload, not Kuma's; a receiver written for Kuma's fields has to be changed")
		}
	default:
		return skip("SubGlance has no " + typ + " channel")
	}
	return out, true
}

// joinRecipients merges two comma-separated lists, dropping duplicates.
func joinRecipients(lists ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, part := range strings.Split(l, ",") {
			p := strings.TrimSpace(part)
			if p == "" || seen[strings.ToLower(p)] {
				continue
			}
			seen[strings.ToLower(p)] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}
