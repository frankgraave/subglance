package kumaimport

import (
	"regexp"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
)

// appriseBody is the request from "Apprise" in docs/channels.md: the title
// and body Apprise API's /notify/<key> reads from JSON, the two values
// Kuma handed the apprise command.
const appriseBody = `{ "title": "{{summary}}", "body": "{{details}}" }
`

// appriseURLShape is the address to fill in: Apprise API's port, and the key
// Kuma's Apprise URL is saved under.
const appriseURLShape = "http://<apprise>:8000/notify/<key>"

// appriseScheme finds the service at the start of each URL in an Apprise
// URL field, which holds one URL or several, separated by commas or white
// space, as Apprise reads it. Only the scheme is taken: what follows it is
// the service's credentials.
var appriseScheme = regexp.MustCompile(`(?:^|[\s,])([A-Za-z][A-Za-z0-9+.-]{0,31})://`)

// appriseServices names the Apprise schemes SubGlance reaches without
// Apprise: the channel types of its own, and the services docs/channels.md
// gives a webhook body for.
var appriseServices = map[string]struct {
	name string
	own  bool
}{
	"tgram": {"Telegram", true}, "discord": {"Discord", true}, "slack": {"Slack", true},
	"ntfy": {"ntfy", true}, "ntfys": {"ntfy", true}, "gotify": {"Gotify", true}, "gotifys": {"Gotify", true},
	"mailto": {"email", true}, "mailtos": {"email", true}, "twilio": {"Twilio", true},
	"workflow": {"Microsoft Teams", false}, "workflows": {"Microsoft Teams", false},
	"matrix": {"Matrix", false}, "matrixs": {"Matrix", false}, "pover": {"Pushover", false},
	"mmost": {"Mattermost", false}, "mmosts": {"Mattermost", false},
	"rocket": {"Rocket.Chat", false}, "rockets": {"Rocket.Chat", false}, "gchat": {"Google Chat", false},
	"hassio": {"Home Assistant", false}, "hassios": {"Home Assistant", false},
	"signal": {"Signal", false}, "signals": {"Signal", false}, "bark": {"Bark", false}, "barks": {"Bark", false},
}

// convertApprise turns a Kuma Apprise notification into a webhook that posts
// to Apprise API. Kuma ran the apprise command with the message and its
// Apprise URL; SubGlance runs no commands, but Apprise API, the same Apprise
// behind an HTTP endpoint, sends to every URL saved under a key when it is
// posted a title and a body, which a webhook with a body of its own sends,
// so no channel type of SubGlance's own is needed.
//
// Kuma had no server to carry over, only the Apprise URL, and that is the
// target service's credentials, so neither comes over: the url is left to
// fill in, and the report names the services the Apprise URL sent to by
// scheme alone, so the person filling it in knows what to save under the
// key, and which of them SubGlance reaches without Apprise.
func convertApprise(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = appriseBody
	note("now a webhook channel that posts to Apprise API, Apprise's HTTP server; save Kuma's Apprise URL there under a key " +
		"and fill in the url as " + appriseURLShape + ". An Apprise API on the local network is reached only when " +
		"SubGlance is started with --allow-private-targets")

	raw := get("appriseURL")
	if raw == "" {
		note("Kuma had no Apprise URL, so it sent nowhere; Apprise API needs one saved under the key")
	} else if schemes := appriseSchemes(raw); len(schemes) == 0 {
		// Not quoted: it can hold a credential even when it does not parse.
		note("Kuma's Apprise URL names no service SubGlance can read")
	} else {
		note(appriseServicesNote(schemes))
	}
	// Kuma sent its title, one for every alert, only when it was set.
	if get("title") != "" {
		note("Kuma sent every alert under one title of its own; the body sends each alert's summary as the title instead")
	}
}

// appriseSchemes lists the schemes in an Apprise URL field, lower case, each
// once, in the order they appear.
func appriseSchemes(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range appriseScheme.FindAllStringSubmatch(raw, -1) {
		s := strings.ToLower(m[1])
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// appriseServicesNote names the services an Apprise URL sent to, and the
// ones among them SubGlance sends to without Apprise.
func appriseServicesNote(schemes []string) string {
	shown := make([]string, len(schemes))
	var own, bodies []string
	seen := map[string]bool{}
	for i, s := range schemes {
		shown[i] = s + "://"
		svc, ok := appriseServices[s]
		if !ok || seen[svc.name] {
			continue
		}
		seen[svc.name] = true
		if svc.own {
			own = append(own, svc.name)
		} else {
			bodies = append(bodies, svc.name)
		}
	}
	text := "Kuma's Apprise URL sent to " + andList(shown)
	const asOwn, asBody = " as a channel type of its own", " as a webhook with the body the channel documentation gives"
	switch {
	case len(own) > 0 && len(bodies) > 0:
		text += "; SubGlance reaches " + andList(own) + " without Apprise," + asOwn + ", and " + andList(bodies) + asBody
	case len(own) > 0:
		text += "; SubGlance reaches " + andList(own) + " without Apprise," + asOwn
	case len(bodies) > 0:
		text += "; SubGlance reaches " + andList(bodies) + " without Apprise," + asBody
	}
	return text
}
