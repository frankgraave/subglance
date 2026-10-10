package kumaimport

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// wahaBody is the message from "WhatsApp (WAHA)" in docs/channels.md: the
// request WAHA's /api/sendText reads, and the one Kuma sent. The session and
// the chat are left to fill in, so it is never written into a file; the
// report gives it as the body to paste.
const wahaBody = `{ "session": "<session>", "chatId": "<chat id>", "text": "{{summary}}\n{{details}}" }
`

// wahaURLShape is the address to fill in when Kuma's could not be kept.
const wahaURLShape = "http://<waha>:3000/api/sendText"

// wahaHeader is the header to fill in when Kuma sent an API key.
const wahaHeader = "X-Api-Key: <api key>"

// convertWAHA turns a Kuma WhatsApp (WAHA) notification into a webhook that
// posts to the same WAHA server. Kuma posted {session, chatId, text} to
// <API URL>/api/sendText, with the API key in an X-Api-Key header, which a
// webhook with a body and headers of its own sends too, so no channel type
// of SubGlance's own is needed.
//
// The address says where a message goes and comes over. The key goes in the
// withheld headers. The body does not come over: it names the chat, which
// is a phone number or a group, and a phone number is personal data that the
// import keeps out of the file, as it does an SMS channel's numbers. The
// report gives the body to fill in, with the session and, masked, the chat
// Kuma used, so the person filling it in can tell which they were.
func convertWAHA(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = configfile.Placeholder

	// An address that comes out of wahaURL is an http or https URL with a
	// host and a path of escaped segments, so it holds no placeholder and
	// passes the webhook's rules; the test holds every address it writes
	// to them.
	if u, ok := wahaURL(get("wahaApiUrl"), note); ok {
		out.Config["url"] = u.String()
		if n := localHostNote(u.Hostname()); n != "" {
			note(n)
		}
	}
	if get("wahaApiKey") != "" {
		out.Config["headers"] = configfile.Placeholder
		note("fill in the headers as \"" + wahaHeader + "\", with the API key WAHA was started with")
	}

	fill := "now a webhook channel that posts to WAHA; fill in the body as " + strings.TrimSpace(wahaBody)
	var used []string
	if s := get("wahaSession"); s != "" {
		used = append(used, "the session Kuma used ("+strconv.Quote(s)+")")
	}
	if chat := maskWAHAChat(get("wahaChatId")); chat != "" {
		used = append(used, "the chat it sent to ("+chat+")")
	}
	if len(used) > 0 {
		fill += ", with " + strings.Join(used, " and ")
	}
	note(fill)
}

// wahaURL builds the address Kuma posted to, or notes why it does not come
// over.
//
// Kuma dropped trailing slashes and appended /api/sendText to the text of
// the address. A user name and password, or a query string, can hold a
// credential for a proxy in front of WAHA, so an address with either stays
// out of the file and is not quoted. A fragment would have swallowed the
// path Kuma appended, so Kuma's request never reached WAHA's endpoint, and
// an address with one is not carried over either.
func wahaURL(raw string, note func(string)) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err == nil && (u.User != nil || u.RawQuery != "") {
		note("Kuma's WAHA address carried a user name, a password or a query string, which can hold a credential; " +
			"fill in the url as " + wahaURLShape)
		return nil, false
	}
	// "http://:3000" parses with a Host but no Hostname, and posts nowhere.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.Opaque != "" || u.Fragment != "" {
		// An address that did not parse can still hold a credential; one
		// with an @ or a ? is not quoted.
		shown := " "
		if !strings.ContainsAny(raw, "@?") {
			shown = " " + strconv.Quote(raw) + " "
		}
		note("Kuma's WAHA address" + shown + "is not one SubGlance can post to; fill in the url as " + wahaURLShape)
		return nil, false
	}
	return u.JoinPath("api", "sendText"), true
}

// maskWAHAChat writes Kuma's chat as the report shows it. A chat id is a
// phone number, with or without @c.us after it, or a group, which ends in
// @g.us or, in the older form, is two numbers joined by a dash, the first
// of them the phone number of whoever made the group. A phone number is
// masked; a group is "a group", since its id can hold that number; anything
// else is masked whole.
func maskWAHAChat(raw string) string {
	id := strings.Join(strings.Fields(raw), "")
	if id == "" {
		return ""
	}
	local, domain, hasDomain := strings.Cut(id, "@")
	switch {
	case domain == "g.us" || (!hasDomain && strings.Contains(local, "-")):
		return "a group"
	case hasDomain && domain != "c.us" && domain != "s.whatsapp.net":
		return "••••"
	}
	// WAHA writes a number as the country code and the number, without
	// the plus; Kuma's form also showed it with 00 in front.
	if !strings.HasPrefix(local, "00") {
		local = "+" + local
	}
	return notifier.MaskSMSNumbers(local, "")
}
