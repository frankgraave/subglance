package checker

import (
	"net/url"
	"strings"
)

// ShownURL is a URL an operator configured as a log line or an error prints
// it: the scheme and the host, and "/…" where a path, query or fragment
// followed. A user name and password are left out. The watchdog URL keeps its
// credential in the path and the query, and a backup endpoint written with a
// key pair keeps it in the user part, so neither is quoted whole anywhere.
//
// An address that does not parse, or has no host, is cut from its text
// instead, so the error that refuses it does not quote it back in full.
// There everything up to the last "@" before any query or fragment is
// replaced by "***", as the
// --base-url refusal does: Go could not pick the user part out, and in
// https://user:pa/ss@host the password runs past the first slash. What
// follows a colon in the host is replaced by "***" as well: a port that
// does not parse may be a password whose "@" was left out, as in
// https://user:pa55word/path.
func ShownURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		shown := u.Scheme + "://" + u.Host
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return shown + "/…"
		}
		return shown + u.Path
	}
	scheme, rest, hasScheme := strings.Cut(raw, "://")
	if !hasScheme {
		scheme, rest = "", raw
	}
	// A query or a fragment is never the user part: an "@" there would
	// make what follows it the host.
	userPart, suffix := rest, ""
	if end := strings.IndexAny(rest, "?#"); end >= 0 {
		userPart, suffix = rest[:end], rest[end:]
	}
	if at := strings.LastIndex(userPart, "@"); at >= 0 {
		rest = "***" + userPart[at:] + suffix
	}
	host, tail := rest, ""
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		host, tail = rest[:end], rest[end:]
	}
	// The colons inside an IPv6 literal are not a port.
	if from := strings.LastIndex(host, "]") + 1; strings.Contains(host[from:], ":") {
		host = host[:from+strings.Index(host[from:], ":")] + ":***"
	}
	if hasScheme {
		host = scheme + "://" + host
	}
	if tail != "" && tail != "/" {
		return host + "/…"
	}
	return host + tail
}
