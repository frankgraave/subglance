package kumaimport

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
)

// Teams, Matrix, Pushover, Mattermost, Rocket.Chat and Google Chat have no
// channel type of their own in SubGlance:
// docs/channels.md sends them to a webhook with a body of its own, and gives
// the URL, method, headers and body for each. The converter writes exactly
// those, so a Kuma notification of one of these types comes over as the
// channel the documentation would have had the user build by hand. A test
// holds each body below to the example in the documentation, so a fix to one
// reaches the other.

// teamsBody is the Adaptive Card from "Microsoft Teams" in docs/channels.md.
const teamsBody = `{
  "type": "message",
  "attachments": [
    {
      "contentType": "application/vnd.microsoft.card.adaptive",
      "content": {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "body": [
          { "type": "TextBlock", "text": "{{summary}}", "weight": "Bolder", "size": "Medium", "wrap": true },
          { "type": "FactSet", "facts": [
            { "title": "Target", "value": "{{target}}" },
            { "title": "Error", "value": "{{last_error}}" },
            { "title": "At", "value": "{{at}}" }
          ] }
        ]
      }
    }
  ]
}
`

// matrixBody is the event from "Matrix" in docs/channels.md.
const matrixBody = `{ "msgtype": "m.text", "body": "{{summary}}\n{{details}}" }
`

// pushoverBody is the message from "Pushover" in docs/channels.md.
const pushoverBody = `{ "title": "{{summary}}", "message": "{{details}}" }
`

// chatBody is the message from "Mattermost, Rocket.Chat and Google Chat" in
// docs/channels.md. Each of the three reads text as the message.
const chatBody = `{ "text": "{{summary}}\n{{details}}" }
`

// pushoverURL is where Pushover takes a message. The application token and
// the user key go in its query string, which is withheld as a whole.
const pushoverURL = "https://api.pushover.net/1/messages.json?token=<application token>&user=<user key>"

// pushoverName matches what Pushover accepts as a sound or a device name: a
// device list is comma-separated. Anything else is left out rather than
// written into the body, where it would be a value nobody chose.
var (
	pushoverSound  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	pushoverDevice = regexp.MustCompile(`^[A-Za-z0-9_-]{1,25}(,[A-Za-z0-9_-]{1,25})*$`)

	// A Mattermost channel is named by its lowercase handle, a person by
	// @ and a user name. A Rocket.Chat channel is #name, a person @name,
	// and a room may also be given by its id.
	mattermostChannel = regexp.MustCompile(`^@?[a-z0-9._-]{1,64}$`)
	rocketChannel     = regexp.MustCompile(`^[#@]?[A-Za-z0-9._-]{1,64}$`)
)

// convertTeams turns a Kuma Teams notification into a webhook. Its only
// setting is the Workflows URL, which is the credential.
func convertTeams(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = teamsBody
	note("now a webhook channel that posts a Teams card; fill in the url of a Teams Workflows webhook, " +
		"then press Send test and check that the card arrives in Teams, because the workflow accepts any request")
	if get("teamsEnableTags") == "true" {
		note("the monitor's tags were listed on Kuma's card; SubGlance's card does not list them")
	}
}

// convertMatrix turns a Kuma Matrix notification into a webhook that sends a
// PUT to the room. The homeserver and the room say where a message goes and
// come over as they are; the access token goes in the withheld headers.
func convertMatrix(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["method"] = "PUT"
	out.Config["body"] = matrixBody
	out.Config["headers"] = configfile.Placeholder

	server := strings.TrimRight(get("homeserverUrl"), "/")
	room := get("internalRoomId")
	if u, err := url.Parse(server); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && room != "" {
		out.Config["url"] = server + "/_matrix/client/v3/rooms/" + escapeRoom(room) +
			"/send/m.room.message/{{txn_id}}"
	} else {
		out.Config["url"] = configfile.Placeholder
		note("the room address could not be built from Kuma's homeserver and room id; fill in the url as " +
			"https://<homeserver>/_matrix/client/v3/rooms/<room id>/send/m.room.message/{{txn_id}}")
	}
	note("now a webhook channel that sends the message to the room; fill in the headers as \"Authorization: Bearer <access token>\"")
	if get("matrixUseTemplate") == "true" && get("matrixTemplate") != "" {
		note("Kuma's message template was not carried over, because Kuma's templates are written in another language")
	}
}

// escapeRoom writes a room id or alias as one path segment. url.PathEscape
// also escapes "!", which every room id starts with, although RFC 3986 allows
// it in a path; it is put back so the address reads like the one Matrix shows.
func escapeRoom(room string) string {
	return strings.ReplaceAll(url.PathEscape(room), "%21", "!")
}

// convertPushover turns a Kuma Pushover notification into a webhook. The
// token and the user key go in the withheld URL; the settings that only say
// how a message arrives (priority, sound, device, how long it is kept) are
// added to the body as Pushover's own fields.
func convertPushover(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	note("now a webhook channel; fill in the url as " + pushoverURL)

	var extra []jsonField
	if p := get("pushoverpriority"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= -2 && n <= 2 {
			extra = append(extra, jsonField{"priority", n})
			if n == 2 {
				// Pushover refuses an emergency message without both;
				// these are the values Kuma sent.
				extra = append(extra, jsonField{"retry", 30}, jsonField{"expire", 3600})
			}
		} else {
			note("the priority " + strconv.Quote(p) + " is not one Pushover accepts and was left out")
		}
	}
	sound := get("pushoversounds")
	if sound != "" {
		if pushoverSound.MatchString(sound) {
			extra = append(extra, jsonField{"sound", sound})
		} else {
			note("the sound " + strconv.Quote(sound) + " is not a Pushover sound name and was left out")
		}
	}
	if up := get("pushoversounds_up"); up != "" && up != sound {
		note("Kuma played another sound for a recovery; SubGlance sends one sound for every alert")
	}
	if d := get("pushoverdevice"); d != "" {
		if pushoverDevice.MatchString(d) {
			extra = append(extra, jsonField{"device", d})
		} else {
			note("the device " + strconv.Quote(d) + " is not a Pushover device name and was left out")
		}
	}
	if t := get("pushoverttl"); t != "" {
		switch n, err := strconv.Atoi(t); {
		case err == nil && n > 0:
			extra = append(extra, jsonField{"ttl", n})
		case err != nil || n < 0:
			note("the message lifetime " + strconv.Quote(t) + " is not a number of seconds and was left out")
		}
	}
	if get("pushovertitle") != "" {
		note("Kuma's message title was replaced by the alert's one-line summary")
	}
	out.Config["body"] = withFields(pushoverBody, extra)
}

// chatWebhook makes a webhook that posts chatBody to an incoming-webhook URL,
// which is the credential for all three chat services that take one.
func chatWebhook(out *configfile.Channel, note func(string), where string) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = chatBody
	note("now a webhook channel that posts a text message; fill in the url of " + where)
}

// senderNote is listed when Kuma posted under a name or an icon of its own.
// The webhook's own name and icon are the right ones for SubGlance's alerts,
// so these are not carried over.
const senderNote = "Kuma posted under its own name and icon; SubGlance's alerts arrive under the webhook's"

// convertMattermost turns a Kuma Mattermost notification into a webhook.
// Kuma's channel override says where a message goes, so it comes over in the
// body: a webhook that is not locked to its channel posts there.
func convertMattermost(get func(string) string, out *configfile.Channel, note func(string)) {
	chatWebhook(out, note, "a Mattermost incoming webhook")
	var extra []jsonField
	if ch := get("mattermostchannel"); ch != "" {
		// Kuma sent the channel in lowercase, which is how Mattermost
		// names one; the same is written here.
		if lower := strings.ToLower(ch); mattermostChannel.MatchString(lower) {
			extra = append(extra, jsonField{"channel", lower})
		} else {
			note("the channel " + strconv.Quote(ch) + " is not a Mattermost channel name and was left out; messages go to the webhook's own channel")
		}
	}
	if get("mattermostusername") != "" || get("mattermosticonurl") != "" || get("mattermosticonemo") != "" {
		note(senderNote)
	}
	out.Config["body"] = withFields(chatBody, extra)
}

// convertRocketChat turns a Kuma Rocket.Chat notification into a webhook,
// with Kuma's channel override in the body as for Mattermost. Kuma could
// only have sent it to an integration that allows overriding the channel.
func convertRocketChat(get func(string) string, out *configfile.Channel, note func(string)) {
	chatWebhook(out, note, "a Rocket.Chat incoming integration")
	var extra []jsonField
	if ch := get("rocketchannel"); ch != "" {
		if rocketChannel.MatchString(ch) {
			extra = append(extra, jsonField{"channel", ch})
		} else {
			note("the channel " + strconv.Quote(ch) + " is not a Rocket.Chat channel, user or room id and was left out; messages go to the integration's own channel")
		}
	}
	if get("rocketusername") != "" || get("rocketiconemo") != "" {
		note(senderNote)
	}
	out.Config["body"] = withFields(chatBody, extra)
}

// convertGoogleChat turns a Kuma Google Chat notification into a webhook.
// Its only setting is the space's webhook URL.
func convertGoogleChat(get func(string) string, out *configfile.Channel, note func(string)) {
	chatWebhook(out, note, "a Google Chat space webhook")
	if get("googleChatUseTemplate") == "true" && get("googleChatTemplate") != "" {
		note("Kuma's message template was not carried over, because Kuma's templates are written in another language")
	}
}

// jsonField is one field withFields adds to a body.
type jsonField struct {
	name  string
	value any
}

// withFields adds fields to the end of a one-line JSON object, each value
// written as JSON. No value can hold a placeholder: each one is a number or
// a name that passed a pattern without braces before it got here.
func withFields(body string, fields []jsonField) string {
	if len(fields) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(strings.TrimSuffix(strings.TrimSuffix(body, "\n"), " }"))
	for _, f := range fields {
		var v bytes.Buffer
		enc := json.NewEncoder(&v)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(f.value)
		b.WriteString(`, "` + f.name + `": ` + strings.TrimSuffix(v.String(), "\n"))
	}
	b.WriteString(" }\n")
	return b.String()
}
