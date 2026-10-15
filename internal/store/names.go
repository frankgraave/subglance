package store

import (
	"strings"
	"unicode"
)

// NoControlCharacters ends the sentence that refuses a name holding a
// control character, after the name of the field: "name must not ...".
const NoControlCharacters = "must not contain line breaks, tabs or other control characters"

// HasControlCharacter reports whether s holds a control character: the C0
// range (which includes tab, line feed and carriage return), DEL, or the C1
// range, the code points Unicode files under category Cc.
//
// A monitor's name is the title of every alert about it, and a channel's,
// a token's and a maintenance window's are each one row in a list. A line
// break splits an alert title across lines on every channel that prints it
// as text, and NUL, escape and the rest show as nothing at all, so two
// names that read the same can differ and a search for the visible one
// misses the other.
//
// Format characters (category Cf) are not control characters here: a
// zero-width joiner holds an emoji sequence together and a direction mark
// keeps a right-to-left name readable.
func HasControlCharacter(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
