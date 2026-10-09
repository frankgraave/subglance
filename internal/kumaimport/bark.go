package kumaimport

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/configfile"
)

// barkBody is the push from "Bark" in docs/channels.md: the two text fields
// bark-server reads from a JSON request.
const barkBody = `{ "title": "{{summary}}", "body": "{{details}}" }
`

// barkURLShape is the address to fill in when Kuma's server could not be
// named: the public Bark server, followed by the device key.
const barkURLShape = "https://api.day.app/<device key>"

// barkSound matches a sound name as the Bark app lists it, with or without
// the .caf of a custom sound. Kuma offered a fixed list; anything else is
// left out rather than written into the body as a value nobody chose.
var barkSound = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}(\.caf)?$`)

// barkDefaultSound is the sound Kuma sent when none was chosen.
const barkDefaultSound = "telegraph"

// barkDefaultGroup is the group Kuma sent when none was chosen, with both
// API versions, so pushes it imported keep arriving under that heading.
const barkDefaultGroup = "UptimeKuma"

// convertBark turns a Kuma Bark notification into a webhook that posts to
// the same Bark server. Kuma either sent a GET with the text in the path
// (API v1) or a JSON POST (v2) to its endpoint; bark-server takes a JSON
// POST at that endpoint for both, which a webhook with a body of its own
// sends, so no channel type of SubGlance's own is needed.
//
// The endpoint ends in the device key, which is the credential: whoever
// holds it can push to the phone. So the url is withheld, and the report
// names the server Kuma used, without the key, so that a self-hosted server
// is filled in and not the public one. The group and the sound only say
// how a push arrives, and come over in the body, with the defaults Kuma
// sent when either was left empty.
func convertBark(get func(string) string, out *configfile.Channel, note func(string)) {
	out.Type = "webhook"
	out.Config["url"] = configfile.Placeholder

	shape := barkURLShape
	if s, host, ok := barkServer(get("barkEndpoint")); ok {
		shape = s
		if n := localHostNote(host); n != "" {
			note(n)
		}
	} else {
		// The endpoint holds the key, so it is not quoted even when it
		// does not parse.
		note("Kuma's Bark endpoint is not an address SubGlance can post to")
	}
	note("now a webhook channel that pushes to Bark; fill in the url as " + shape +
		", with the device key the Bark app shows")

	var extra []jsonField
	group := get("barkGroup")
	if group == "" {
		group = barkDefaultGroup
	}
	if barkGroupOK(group) {
		extra = append(extra, jsonField{"group", group})
	} else {
		note("the group " + strconv.Quote(group) + " has braces, control characters or more than 64 characters and was left out")
	}
	sound := get("barkSound")
	if sound == "" {
		sound = barkDefaultSound
	}
	if barkSound.MatchString(sound) {
		extra = append(extra, jsonField{"sound", sound})
	} else {
		note("the sound " + strconv.Quote(sound) + " is not a Bark sound name and was left out")
	}
	out.Config["body"] = withFields(barkBody, extra)
}

// barkServer writes Kuma's endpoint as the address to fill in: the same
// server and path with the device key, its last segment, replaced. It
// returns the host as well, for the note on a local server. An endpoint
// with a user name, a password or a query string is not used, since either
// can hold a credential for a proxy in front of the server.
func barkServer(raw string) (shape, host string, ok bool) {
	// Kuma drops one trailing slash before it appends to the endpoint.
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.Opaque != "" || u.User != nil || u.RawQuery != "" {
		return "", "", false
	}
	// The key may hold characters that need escaping; the path before it
	// is written as url.URL would write it, so it holds no braces.
	dir := u.EscapedPath()
	dir = dir[:strings.LastIndex(dir, "/")+1]
	if dir == "" {
		dir = "/"
	}
	return u.Scheme + "://" + u.Host + dir + "<device key>", u.Hostname(), true
}

// barkGroupOK reports whether Kuma's group can go in the body as it is. A
// group is a label the Bark app sorts pushes under, so any text is fine,
// except braces, which the webhook would read as a placeholder, and
// control characters.
func barkGroupOK(g string) bool {
	if utf8.RuneCountInString(g) > 64 || strings.ContainsAny(g, "{}") {
		return false
	}
	for _, r := range g {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
