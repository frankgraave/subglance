package checker

import "unicode/utf8"

// cutBytes returns the longest start of s that is at most n bytes long and
// does not end inside a character.
//
// A failure message quotes text it does not control (a record value, a JSON
// value from the response) and bounds it by bytes. Cut at exactly n, a
// two-byte "é" or a four-byte emoji loses its tail, and the message keeps a
// byte that is not UTF-8: it is stored as written and every reader of the
// heartbeat, the incident and the alert shows it as a replacement character,
// in the one sentence that says what went wrong.
//
// The cut backs off at most three bytes, the most a character continues
// past its first byte, so text that was not UTF-8 to begin with is still cut
// near n rather than walked back to its start.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := max(n, 0)
	for range utf8.UTFMax - 1 {
		if cut == 0 || utf8.RuneStart(s[cut]) {
			break
		}
		cut--
	}
	return s[:cut]
}
