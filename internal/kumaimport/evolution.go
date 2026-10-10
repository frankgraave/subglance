package kumaimport

import (
	"net/url"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
)

// evolutionBody is the message from "WhatsApp (Evolution API)" in
// docs/channels.md: the request Evolution API's /message/sendText reads,
// and the one Kuma sent. The number is left to fill in, so it is never
// written into a file; the report gives it as the body to paste.
const evolutionBody = `{ "number": "<number>", "text": "{{summary}}\n{{details}}" }
`

// evolutionURLShape is the address to fill in when Kuma's could not be kept.
const evolutionURLShape = "http://<evolution-api>:8080/message/sendText/<instance>"

// evolutionHeader is the header to fill in when Kuma sent a token.
const evolutionHeader = "apikey: <token>"

// convertEvolution turns a Kuma WhatsApp (Evolution) notification into a
// webhook that posts to the same Evolution API. Kuma posted {number, text}
// to <API URL>/message/sendText/<instance>, with the token in an apikey
// header, which a webhook with a body and headers of its own sends too, so
// no channel type of SubGlance's own is needed.
//
// The address says where a message goes and comes over. The token goes in
// the withheld headers. The body does not come over: it names the
// recipient, which is a phone number or a group, kept out of the file as
// WAHA's chat is, and the report gives it to fill in with the recipient
// Kuma used, masked.
func convertEvolution(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = configfile.Placeholder

	// An address that comes out of evolutionURL is an http or https URL
	// with a host and a path of escaped segments, so it holds no
	// placeholder and passes the webhook's rules; the test holds every
	// address it writes to them.
	if u, ok := evolutionURL(get("evolutionApiUrl"), get("evolutionInstanceName"), note); ok {
		out.Config["url"] = u.String()
		if n := localHostNote(u.Hostname()); n != "" {
			note(n)
		}
	}
	if get("evolutionAuthToken") != "" {
		out.Config["headers"] = configfile.Placeholder
		note("fill in the headers as \"" + evolutionHeader + "\", with the instance's token or the API key Evolution API was started with")
	}
	// Kuma's custom message is a Liquid template, which SubGlance's
	// {{name}} placeholders do not read.
	if get("evolutionUseCustomMessage") == "true" && get("evolutionCustomMessage") != "" {
		note("Kuma sent a custom message, a template SubGlance does not read; the body sends the summary and details instead")
	}

	fill := "now a webhook channel that posts to Evolution API; fill in the body as " + strings.TrimSpace(evolutionBody)
	if to := maskWhatsAppChat(get("evolutionRecipient")); to != "" {
		fill += ", with the recipient Kuma sent to (" + to + ")"
	}
	note(fill)
}

// evolutionURL builds the address Kuma posted to, or notes why it does not
// come over.
//
// Kuma dropped trailing slashes from the API URL and appended
// /message/sendText/ and the instance name, escaped as one path segment, so
// a slash in the name stays part of it. Without an API URL Kuma posted to
// a default, https://evolapicloud.com/, which is not the hosted service's
// evoapicloud.com and did not resolve when this was written; rather than
// guess which was meant, the url is left to fill in. Without an instance
// name Kuma posted to no instance, and the url is left to fill in too.
func evolutionURL(raw, instance string, note func(string)) (*url.URL, bool) {
	if raw == "" {
		note("Kuma had no Evolution API address; fill in the url as " + evolutionURLShape)
		return nil, false
	}
	base, ok := apiBaseURL("Evolution API", raw, evolutionURLShape, note)
	if !ok {
		return nil, false
	}
	if instance == "" {
		note("Kuma had no Evolution API instance name; fill in the url as " + evolutionURLShape)
		return nil, false
	}
	u := base.JoinPath("message", "sendText")
	u.RawPath = u.EscapedPath() + "/" + url.PathEscape(instance)
	u.Path += "/" + instance
	return u, true
}
