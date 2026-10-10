package kumaimport

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
)

// openWABody is the message from "WhatsApp (OpenWA)" in docs/channels.md:
// the request OpenWA's send-text endpoint reads, and the one Kuma sent. The
// chat is left to fill in, so it is never written into a file; the report
// gives it as the body to paste.
const openWABody = `{ "chatId": "<chat id>", "text": "{{summary}}\n{{details}}" }
`

// openWAURLShape is the address to fill in when Kuma's could not be kept.
const openWAURLShape = "http://<openwa>:2785/api/sessions/<session>/messages/send-text"

// openWAHeader is the header to fill in when Kuma sent an API key.
const openWAHeader = "X-API-Key: <api key>"

// convertOpenWA turns a Kuma WhatsApp (OpenWA) notification into a webhook
// that posts to the same OpenWA server. Kuma posted {chatId, text} to
// <API URL>/api/sessions/<session>/messages/send-text, with the API key in
// an X-Api-Key header, which a webhook with a body and headers of its own
// sends too, so no channel type of SubGlance's own is needed.
//
// The address says where a message goes and comes over. The key goes in
// the withheld headers. The body does not come over: it names the chat,
// which is a phone number or a group, kept out of the file as WAHA's chat
// is, and the report gives it to fill in with the chat Kuma used, masked.
//
// Kuma took a comma-separated list of chats and posted once to each; a
// webhook posts once. One Kuma notification still becomes one channel, as
// every other one does, and the report says how many chats there were, so
// the person filling it in adds a channel for each of the others.
func convertOpenWA(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = configfile.Placeholder

	// An address that comes out of openWAURL is an http or https URL with
	// a host and a path of escaped segments, so it holds no placeholder
	// and passes the webhook's rules; the test holds every address it
	// writes to them.
	if u, ok := openWAURL(get("openwaApiUrl"), get("openwaSession"), note); ok {
		out.Config["url"] = u.String()
		if n := localHostNote(u.Hostname()); n != "" {
			note(n)
		}
	}
	if get("openwaApiKey") != "" {
		out.Config["headers"] = configfile.Placeholder
		note("fill in the headers as \"" + openWAHeader + "\", with an OpenWA API key that may send messages")
	}
	// Kuma's custom message is a Liquid template, which SubGlance's
	// {{name}} placeholders do not read. Kuma used it only when it held
	// more than white space, which get has already trimmed.
	if get("openwaUseCustomMessage") == "true" && get("openwaCustomMessage") != "" {
		note("Kuma sent a custom message, a template SubGlance does not read; the body sends the summary and details instead")
	}

	var chats []string
	for _, id := range strings.Split(get("openwaChatId"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			chats = append(chats, maskWhatsAppChat(id))
		}
	}
	fill := "now a webhook channel that posts to OpenWA; fill in the body as " + strings.TrimSpace(openWABody)
	switch len(chats) {
	case 0:
	case 1:
		fill += ", with the chat Kuma sent to (" + chats[0] + ")"
	default:
		fill += ", with the first of the " + strconv.Itoa(len(chats)) + " chats Kuma sent to (" + chats[0] + ")"
	}
	note(fill)
	if len(chats) > 1 {
		note("Kuma sent each alert to " + strconv.Itoa(len(chats)) + " chats and a webhook posts to one; " +
			"add a webhook channel with the same url and headers for each of the others (" + strings.Join(chats[1:], ", ") + ")")
	}
}

// openWAURL builds the address Kuma posted to, or notes why it does not come
// over.
//
// Kuma dropped trailing slashes from the API URL and appended
// /api/sessions/, the session escaped as one path segment, and
// /messages/send-text. Without an API URL or a session Kuma's request
// failed, and the url is left to fill in.
func openWAURL(raw, session string, note func(string)) (*url.URL, bool) {
	if raw == "" {
		note("Kuma had no OpenWA address; fill in the url as " + openWAURLShape)
		return nil, false
	}
	base, ok := apiBaseURL("OpenWA", raw, openWAURLShape, note)
	if !ok {
		return nil, false
	}
	if session == "" {
		note("Kuma had no OpenWA session; fill in the url as " + openWAURLShape)
		return nil, false
	}
	u := base.JoinPath("api", "sessions")
	u.RawPath = u.EscapedPath() + "/" + url.PathEscape(session) + "/messages/send-text"
	u.Path += "/" + session + "/messages/send-text"
	return u, true
}
