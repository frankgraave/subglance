package kumaimport

import (
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
)

// homeAssistantBody is the message from "Home Assistant" in
// docs/channels.md: the two fields every notify action reads.
const homeAssistantBody = `{ "title": "{{summary}}", "message": "{{details}}" }
`

// haAction matches a notify action's name as it goes in the address: the
// part after "notify.", a lowercase slug.
var haAction = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

// haURLShape is the address to fill in when Kuma's could not be built.
const haURLShape = "https://<home assistant>/api/services/notify/<action>"

// localSuffixes are the name endings that only resolve on a local network.
// A name with one of these, or with no dot at all, almost certainly points
// at a private address.
var localSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain"}

// convertHomeAssistant turns a Kuma Home Assistant notification into a
// webhook that calls the notify action Kuma called. Kuma posted to
// <base>/api/services/notify/<action> with the long-lived access token as
// a bearer token, which is the request docs/channels.md describes. The base
// address and the action say where a message goes and come over as they
// are; the token goes in the withheld headers.
func convertHomeAssistant(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder
	out.Config["headers"] = configfile.Placeholder
	out.Config["body"] = homeAssistantBody

	// An address that comes out of homeAssistantURL is an http or https
	// URL with a host and a path of escaped segments, so it holds no
	// placeholder and passes the webhook's rules; the test holds every
	// address it writes to them.
	if u, ok := homeAssistantURL(get("homeAssistantUrl"), get("notificationService"), note); ok {
		out.Config["url"] = u.String()
		if n := localHostNote(u.Hostname()); n != "" {
			note(n)
		}
	}
	note("now a webhook channel that calls Home Assistant's notify action; fill in the headers as " +
		"\"Authorization: Bearer <long-lived access token>\"")
	note("Kuma's title \"Uptime Kuma\" and its data fields (monitor name, status 0 or 1) are not sent; " +
		"an automation in Home Assistant that matched on them has to be changed")
}

// homeAssistantURL builds the address Kuma posted to, or notes why it
// cannot.
func homeAssistantURL(base, action string, note func(string)) (*url.URL, bool) {
	// Kuma trims the address and drops trailing slashes before it appends
	// the path; get has trimmed it already, and JoinPath below drops the
	// slashes. A query or fragment would have broken Kuma's request, so an
	// address with one is not carried over.
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		note("Kuma's Home Assistant address " + strconv.Quote(base) + " is not one SubGlance can post to; " +
			"fill in the url as " + haURLShape)
		return nil, false
	}
	if u.User != nil {
		// A user name and password in the address are a credential: the
		// whole address stays out of the file rather than half of it.
		note("Kuma's Home Assistant address carried a user name and password; fill in the url as " + haURLShape)
		return nil, false
	}

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "notify" // Kuma's default: every device the notify action reaches
	}
	if rest, ok := strings.CutPrefix(action, "notify."); ok && haAction.MatchString(rest) {
		// Kuma's form asks for the part after "notify."; with the domain
		// in front, its request named an action Home Assistant does not
		// have.
		note("the notification action was given as " + strconv.Quote(action) + "; the address takes " +
			strconv.Quote(rest) + ", so the url names that, which Kuma's request did not")
		action = rest
	}
	if !haAction.MatchString(action) {
		note("the notification action " + strconv.Quote(action) + " is not a Home Assistant action name; " +
			"fill in the url as " + u.String() + "/api/services/notify/<action>")
		return nil, false
	}
	return u.JoinPath("api", "services", "notify", action), true
}

// localHostNote says when a channel's host is one SubGlance sends to only
// with --allow-private-targets. Home Assistant usually runs on the local
// network, at a private address or under a local name such as
// homeassistant.local. A public name is not noted: whether it resolves to a
// private address can only be known by asking, and Send test says so.
func localHostNote(host string) string {
	const flag = "SubGlance sends to it only when started with --allow-private-targets"
	if addr, err := netip.ParseAddr(host); err == nil {
		if checker.NewGuard(false).CheckAddr(addr) != nil {
			return "the address " + host + " is private or reserved; " + flag
		}
		return ""
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	local := !strings.Contains(name, ".")
	for _, s := range localSuffixes {
		local = local || strings.HasSuffix(name, s)
	}
	if local {
		return "the host " + host + " is a local name, which resolves to a private address; " + flag
	}
	return ""
}
