package kumaimport

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// signalBody is the message from "Signal" in docs/channels.md: the request
// signal-cli-rest-api's /v2/send reads, and the one Kuma sent. The two
// numbers are left to fill in, so it is never written into a file; the
// report gives it as the body to paste.
const signalBody = `{ "message": "{{summary}}\n{{details}}", "number": "<Signal number>", "recipients": ["<recipient>"] }
`

// signalURLShape is the address to fill in when Kuma's could not be kept.
const signalURLShape = "http://<signal-cli-rest-api>:8080/v2/send"

// convertSignal turns a Kuma Signal notification into a webhook that posts
// to the same signal-cli-rest-api. Kuma posted {message, number, recipients}
// to its Post URL, which a webhook with a body of its own sends too, so no
// channel type of SubGlance's own is needed.
//
// The address says where a message goes and comes over as it is: the API
// has no credential of its own to put in it. The body does not come over.
// It names the sender's number and the recipients, which are phone numbers,
// and a phone number is personal data that the import keeps out of the file
// as it does an SMS channel's numbers. The report gives the body to fill in
// and the numbers Kuma used, masked, so the person filling it in can tell
// which they were.
func convertSignal(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["body"] = configfile.Placeholder

	// An address that comes out of signalURL is an http or https URL with
	// a host, written by url.String, which escapes braces in its path, so
	// it holds no placeholder and passes the webhook's rules; the test
	// holds every address it writes to them.
	if u, ok := signalURL(get("signalURL"), note); ok {
		out.Config["url"] = u.String()
		if n := localHostNote(u.Hostname()); n != "" {
			note(n)
		}
	}

	fill := "now a webhook channel that posts to signal-cli-rest-api; fill in the body as " +
		strings.TrimSpace(signalBody)
	var used []string
	if from := get("signalNumber"); from != "" {
		used = append(used, "the number Kuma sent from ("+notifier.MaskSMSNumbers(from, "")+")")
	}
	if to := maskSignalRecipients(get("signalRecipients")); to != "" {
		used = append(used, "the recipients it sent to ("+to+")")
	}
	if len(used) > 0 {
		fill += ", with " + strings.Join(used, " and ")
	}
	note(fill)
}

// signalURL checks Kuma's Post URL, or notes why it does not come over.
//
// A user name and password, or a query string, can hold a credential for a
// proxy in front of the API, so an address with either stays out of the
// file and is not quoted. A fragment is never sent, and is dropped.
func signalURL(raw string, note func(string)) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err == nil && (u.User != nil || u.RawQuery != "") {
		note("Kuma's Signal address carried a user name, a password or a query string, which can hold a credential; " +
			"fill in the url as " + signalURLShape)
		return nil, false
	}
	// "http://:8080" parses with a Host but no Hostname, and posts nowhere.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		// An address that did not parse can still hold a credential; one
		// with an @ or a ? is not quoted.
		shown := " "
		if !strings.ContainsAny(raw, "@?") {
			shown = " " + strconv.Quote(raw) + " "
		}
		note("Kuma's Signal address" + shown + "is not one SubGlance can post to; fill in the url as " + signalURLShape)
		return nil, false
	}
	u.Fragment, u.RawFragment = "", ""
	return u, true
}

// maskSignalRecipients writes Kuma's recipients as the report shows them: a
// phone number masked, a group id as "a group". Kuma split the list on
// commas after removing every space, and so does this.
func maskSignalRecipients(raw string) string {
	var out []string
	for _, r := range strings.Split(strings.Join(strings.Fields(raw), ""), ",") {
		switch {
		case r == "":
		case strings.HasPrefix(r, "group."):
			out = append(out, "a group")
		default:
			out = append(out, notifier.MaskSMSNumbers(r, ""))
		}
	}
	return strings.Join(out, ", ")
}
