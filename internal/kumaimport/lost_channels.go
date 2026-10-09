package kumaimport

import (
	"strconv"
	"strings"
)

// noteLostChannels lists a converted monitor that Kuma alerted through a
// notification that did not come over.
//
// A notification of a type SubGlance has no channel for is skipped, and the
// report names it once, as a channel. Without this note it would say nothing
// on the monitors behind it, and a monitor whose every notification was
// skipped is imported with no channels at all. SubGlance sends such a
// monitor's alerts to the channels of the tag routing rules it matches, or to
// the default channel when that comes to none; an instance that has neither,
// as one set up for the move does, tells nobody when it goes down. So the
// monitor is listed with the notifications it lost, and with that
// consequence when it lost every one.
//
// lost holds the names of the notifications that did not come over, in the
// order Kuma linked them; kept counts the channels that did.
func noteLostChannels(lost []string, kept int, note func(string)) {
	if len(lost) == 0 {
		return
	}
	names := make([]string, len(lost))
	for i, n := range lost {
		names[i] = strconv.Quote(oneLine(n))
	}
	list := andList(names)
	if kept > 0 {
		note("Kuma also alerted through " + list + ", which did not come over; its alerts go to the channels that did")
		return
	}
	note("none of the notifications Kuma alerted through came over (" + list + "), so it has no channels: " +
		"until it gets one, it alerts only through a tag routing rule or the default channel, and tells nobody without either")
}

// andList joins items as a sentence does: "a", "a and b", "a, b and c".
func andList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
