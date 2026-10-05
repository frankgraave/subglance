package notifier

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/frankgraave/subglance/internal/state"
)

// smsSeptets is the size of one SMS part in the GSM 7-bit alphabet.
//
// An SMS is 140 bytes. In GSM-7 that is 160 characters; one character outside
// that alphabet (an emoji, a curly quote, a "ê") switches the whole message to
// UCS-2, which fits 70, and a longer message is split into parts that are each
// billed as a message. An alert text is therefore built to fit one GSM-7 part,
// always: the cost of an alert should not depend on a monitor's name.
const smsSeptets = 160

// gsm7Basic is the GSM 03.38 default alphabet, less the escape and the line
// feeds, which are handled apart. Each of these costs one septet.
const gsm7Basic = "@£$¥èéùìòÇØøÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
	"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

// gsm7Extension is the extension table: characters that exist in GSM-7 but
// are sent as an escape plus a code, so each costs two septets.
const gsm7Extension = "^{}\\[~]|€"

// smsFold maps characters outside GSM-7 to the nearest one inside it. It
// covers the Latin letters with diacritics used in European languages and the
// punctuation that word processors and phones substitute on their own; a
// monitor named "Café – Zürich" has to arrive as "Cafe - Zürich", not force the
// whole message into UCS-2.
var smsFold = map[rune]string{
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ā': "A", 'Ă': "A", 'Ą': "A",
	'á': "a", 'â': "a", 'ã': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'Ć': "C", 'Ĉ': "C", 'Ċ': "C", 'Č': "C", 'ç': "c", 'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c",
	'Ď': "D", 'Đ': "D", 'Ð': "D", 'ď': "d", 'đ': "d", 'ð': "d",
	'È': "E", 'Ê': "E", 'Ë': "E", 'Ē': "E", 'Ĕ': "E", 'Ė': "E", 'Ę': "E", 'Ě': "E",
	'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'Ĝ': "G", 'Ğ': "G", 'Ġ': "G", 'Ģ': "G", 'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	'Ĥ': "H", 'Ħ': "H", 'ĥ': "h", 'ħ': "h",
	'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I", 'Ĩ': "I", 'Ī': "I", 'Ĭ': "I", 'Į': "I", 'İ': "I",
	'í': "i", 'î': "i", 'ï': "i", 'ĩ': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i",
	'Ĳ': "IJ", 'ĳ': "ij", 'Ĵ': "J", 'ĵ': "j", 'Ķ': "K", 'ķ': "k",
	'Ĺ': "L", 'Ļ': "L", 'Ľ': "L", 'Ŀ': "L", 'Ł': "L", 'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ŀ': "l", 'ł': "l",
	'Ń': "N", 'Ņ': "N", 'Ň': "N", 'ń': "n", 'ņ': "n", 'ň': "n",
	'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ō': "O", 'Ŏ': "O", 'Ő': "O",
	'ó': "o", 'ô': "o", 'õ': "o", 'ō': "o", 'ŏ': "o", 'ő': "o",
	'Œ': "OE", 'œ': "oe",
	'Ŕ': "R", 'Ŗ': "R", 'Ř': "R", 'ŕ': "r", 'ŗ': "r", 'ř': "r",
	'Ś': "S", 'Ŝ': "S", 'Ş': "S", 'Š': "S", 'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s",
	'Ţ': "T", 'Ť': "T", 'Ŧ': "T", 'ţ': "t", 'ť': "t", 'ŧ': "t", 'Þ': "Th", 'þ': "th",
	'Ù': "U", 'Ú': "U", 'Û': "U", 'Ũ': "U", 'Ū': "U", 'Ŭ': "U", 'Ů': "U", 'Ű': "U", 'Ų': "U",
	'ú': "u", 'û': "u", 'ũ': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'Ŵ': "W", 'ŵ': "w", 'Ý': "Y", 'Ŷ': "Y", 'Ÿ': "Y", 'ý': "y", 'ÿ': "y", 'ŷ': "y",
	'Ź': "Z", 'Ż': "Z", 'Ž': "Z", 'ź': "z", 'ż': "z", 'ž': "z",
	'‘': "'", '’': "'", '‚': "'", '′': "'", '“': "\"", '”': "\"", '„': "\"", '″': "\"",
	'«': "\"", '»': "\"", '‹': "'", '›': "'",
	'–': "-", '—': "-", '‐': "-", '‑': "-", '−': "-", '•': "-", '·': "-",
	'…': "...", '×': "x", '÷': "/", '°': "o", '©': "(c)", '®': "(R)", '™': "TM",
	'\t': " ", '\u00a0': " ", '\u2009': " ", '\u202f': " ",
}

// septets returns what r costs in GSM-7, and whether it is in GSM-7 at all.
func septets(r rune) (int, bool) {
	switch {
	case r == '\n' || r == '\r':
		return 1, true
	case strings.ContainsRune(gsm7Basic, r):
		return 1, true
	case strings.ContainsRune(gsm7Extension, r):
		return 2, true
	}
	return 0, false
}

// smsSeptetLen is the length of s in septets. It assumes s is already GSM-7.
func smsSeptetLen(s string) int {
	n := 0
	for _, r := range s {
		c, _ := septets(r)
		n += c
	}
	return n
}

// toGSM7 rewrites s into the GSM-7 alphabet on one line.
//
// A character in GSM-7 is kept, including the accented letters the alphabet
// has (é, ü, ñ). A Latin letter with a diacritic it lacks becomes the plain
// letter. Emoji, joiners and combining marks are dropped: they are decoration,
// and one of them would triple the cost of the message. Any other character
// (a name in Cyrillic, say) becomes "?", so the reader sees that something was
// there rather than a name with holes in it.
//
// Line breaks become spaces: an alert SMS is one line, and a break would cost
// a septet for nothing.
func toGSM7(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			b.WriteByte(' ')
		case isGSM7(r):
			b.WriteRune(r)
		case smsFold[r] != "":
			b.WriteString(smsFold[r])
		case unicode.In(r, unicode.So, unicode.Sk, unicode.Mn, unicode.Me, unicode.Cf, unicode.Cc, unicode.Cs, unicode.Co) ||
			r == unicode.ReplacementChar:
			// Emoji, modifiers, variation selectors, zero-width joiners.
		default:
			b.WriteByte('?')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func isGSM7(r rune) bool {
	_, ok := septets(r)
	return ok
}

// smsEllipsis marks a shortened field. Three dots, because "…" is not GSM-7.
const smsEllipsis = "..."

// fitSeptets shortens s to at most limit septets, at a word boundary when
// there is one in the second half, and marks the cut with smsEllipsis. The
// input must already be GSM-7.
func fitSeptets(s string, limit int) string {
	if smsSeptetLen(s) <= limit {
		return s
	}
	room := limit - len(smsEllipsis)
	if room <= 0 {
		return ""
	}
	cut, used := 0, 0
	for i, r := range s {
		c, _ := septets(r)
		if used+c > room {
			break
		}
		used += c
		cut = i + len(string(r))
	}
	head := s[:cut]
	if sp := strings.LastIndexByte(head, ' '); sp > len(head)/2 {
		head = head[:sp]
	}
	return strings.TrimRight(head, " ,;:-") + smsEllipsis
}

// smsNameLimit bounds a monitor name in an alert SMS, so that the status
// after it and the start of the reason still fit. The status word is never
// shortened; the name is, and the reason gives up the rest.
const smsNameLimit = 60

// smsText renders an alert as one line of GSM-7 of at most limit septets
// (160 for a whole message).
//
// The status word comes first ("DOWN", "UP"), because a lock screen shows
// the start of a message and that is the word that decides whether to get up.
// Times are in the channel's time zone when it has one, which is the reader's
// own clock and needs no label. Without one they are in the server's zone,
// labelled ("14:03 UTC"), because a container's clock is often not the
// reader's.
func smsText(a Alert, zone string, limit int) string {
	loc, labelled := time.Local, true
	if zone != "" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc, labelled = l, false
		}
	}
	clock := func(t time.Time) string {
		t = t.In(loc)
		if labelled {
			return t.Format("15:04 MST")
		}
		return t.Format("15:04")
	}

	var status, name, reason, tail string
	switch {
	case a.Digest:
		status, name = "", DigestTitle(a)
	case a.Grouped():
		status = "DOWN"
		if !a.Down() {
			status = "UP"
		}
		name = fmt.Sprintf("%d monitors:", len(a.GroupedNames))
		reason = strings.Join(a.GroupedNames, ", ")
	case a.MonitorID == 0 && a.MonitorName == testAlertName:
		status, name = "TEST", "SubGlance:"
		reason = "this is a test message from the Send test button. No monitor is down."
	case a.Event == EventBackupFailed:
		status, name, reason = "FAILED", "SubGlance backup:", a.LastError
	case a.Event == EventChannelFailing:
		status, name, reason = "FAILING", "SubGlance channel "+a.Target+":", a.LastError
	case a.Event == EventLocalNetworkRestored:
		status, name = "ONLINE", "SubGlance"
		tail = fmt.Sprintf(" again after %s without a connection", smsDuration(a.At.Sub(a.StartedAt)))
	default:
		name = a.MonitorName
		reason = a.LastError
		if reason == "" {
			reason = a.Cause
		}
		switch state.Event(a.Event) {
		case state.EventIncidentResolved:
			status, reason = "UP", ""
			if !a.StartedAt.IsZero() {
				tail = " after " + smsDuration(a.At.Sub(a.StartedAt))
			}
		case state.EventIncidentReminder:
			status = "STILL DOWN"
		case state.EventIncidentOpened:
			status = "MAYBE DOWN"
		default:
			status = "DOWN"
		}
		if status != "UP" && !a.StartedAt.IsZero() {
			tail = " (since " + clock(a.StartedAt) + ")"
		}
	}

	status, name, reason, tail = toGSM7(status), toGSM7(name), toGSM7(reason), toGSM7Tail(tail)
	name = fitSeptets(name, smsNameLimit)
	head := strings.TrimSpace(status + " " + name)
	if reason == "" {
		return fitSeptets(head+tail, limit)
	}
	sep := ": "
	if strings.HasSuffix(head, ":") {
		sep = " "
	}
	room := limit - smsSeptetLen(head) - len(sep) - smsSeptetLen(tail)
	if room < len(smsEllipsis)+1 {
		return fitSeptets(head+tail, limit)
	}
	return head + sep + fitSeptets(reason, room) + tail
}

// toGSM7Tail is toGSM7 for a suffix, which keeps its leading space.
func toGSM7Tail(s string) string {
	if s == "" {
		return ""
	}
	return " " + toGSM7(s)
}

// smsDuration is a duration as short as an SMS wants it: "45 s", "12 min",
// "2 h 13 min", "3 d 4 h".
func smsDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		if m := int(d.Minutes()) % 60; m != 0 {
			return fmt.Sprintf("%d h %d min", int(d.Hours()), m)
		}
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		if h := int(d.Hours()) % 24; h != 0 {
			return fmt.Sprintf("%d d %d h", int(d.Hours())/24, h)
		}
		return fmt.Sprintf("%d d", int(d.Hours())/24)
	}
}
