package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Optimistic concurrency for monitors.
//
// A monitor's `updated_at` doubles as its version: every write bumps it, so an
// editor that still holds the stamp it read is holding proof that nothing has
// changed underneath. Exposing that stamp as an ETag lets a client hand it back
// on PATCH and be told, rather than silently overwritten, when someone else got
// there first.
//
// The tag is weak. `updated_at` has second resolution and identifies the
// monitor's stored state, not the exact bytes of a GET response — those also
// carry live status, latency and uptime, which move without any edit. A strong
// validator would be a promise of byte equality we cannot keep.

// monitorTag is the opaque part of a monitor's entity tag: the version stamp,
// without quotes or the weakness prefix.
func monitorTag(m store.Monitor) string {
	return strconv.FormatInt(m.UpdatedAt.Unix(), 10)
}

// monitorETag renders a monitor's version as a weak entity tag, ready for an
// ETag header.
func monitorETag(m store.Monitor) string {
	return `W/"` + monitorTag(m) + `"`
}

// setMonitorETag stamps a response with the monitor's current version.
func setMonitorETag(w http.ResponseWriter, m store.Monitor) {
	w.Header().Set("ETag", monitorETag(m))
}

// parseIfMatch splits an If-Match field value into its opaque tags.
//
// star reports `If-Match: *`, which asks only that the resource exist. ok is
// false when the value is not a syntactically valid If-Match, so the caller can
// reject it instead of guessing what the client meant.
//
// The weakness prefix is stripped rather than recorded. RFC 9110 specifies
// strong comparison for If-Match, under which a weak validator never matches
// anything — including the weak tag we ourselves handed out, which would make
// the header useless here. Since the only tags this API issues are weak
// version stamps, the comparison is done on the opaque part alone. That is the
// documented deviation, and it is the safe direction: the stamp still changes
// on every write, so a stale tag is still refused.
//
// Splitting cannot be a plain strings.Split on commas: a comma is a legal
// character inside a quoted entity tag, so the scan has to respect the quotes.
func parseIfMatch(value string) (tags []string, star, ok bool) {
	rest := strings.Trim(value, " \t")
	if rest == "" {
		return nil, false, false
	}
	if rest == "*" {
		return nil, true, true
	}

	for {
		rest = strings.TrimLeft(rest, " \t")
		rest = strings.TrimPrefix(rest, "W/")
		if !strings.HasPrefix(rest, `"`) {
			return nil, false, false
		}
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return nil, false, false
		}
		tags = append(tags, rest[1:1+end])

		rest = strings.TrimLeft(rest[end+2:], " \t")
		if rest == "" {
			return tags, false, true
		}
		if rest[0] != ',' {
			return nil, false, false
		}
		rest = rest[1:]
	}
}

// ifMatchVersions converts the opaque tags a client offered into the monitor
// versions they denote, dropping any tag this API could never have issued.
//
// Returning versions rather than a yes/no verdict is what keeps the check
// honest: the caller hands these to the storage layer, which compares them
// against the row inside the UPDATE. Deciding here whether the condition holds
// would mean comparing against a monitor read moments earlier, and a second
// writer landing in that gap would slip straight through.
func ifMatchVersions(tags []string) []time.Time {
	versions := make([]time.Time, 0, len(tags))
	for _, tag := range tags {
		stamp, err := strconv.ParseInt(tag, 10, 64)
		if err != nil {
			// Not a stamp we ever handed out, so it cannot match any row.
			continue
		}
		// ParseInt normalises, so "+1757606400" and "01757606400" would both
		// arrive as a version we issued. An entity tag is opaque: the only
		// thing a client may do with it is hand back exactly what it got.
		// Accepting a re-spelling would quietly grant meaning to a value we
		// never minted, so require the canonical form.
		if strconv.FormatInt(stamp, 10) != tag {
			continue
		}
		versions = append(versions, time.Unix(stamp, 0).UTC())
	}
	return versions
}
