package notifier

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Several services refuse a message whose text is over a fixed length, and
// an alert's body quotes text of any length: a monitor's target, the error a
// check wrote, a keyword. A refusal is a 400, which httpSend treats as
// permanent, so one long signed URL in a target cost every alert for that
// monitor on that channel. The senders below fit the body to the service's
// limit before sending it.
const (
	// slackSectionMax is the most a section block's text may hold, in
	// Slack's Block Kit reference; a longer one is answered invalid_blocks.
	slackSectionMax = 3000
	// discordDescriptionMax is the most an embed description may hold.
	discordDescriptionMax = 4096
	// telegramTextMax is the most sendMessage takes, after entities are
	// parsed. The text is sent without a parse mode, so it is all text.
	telegramTextMax = 4096
	// ntfyMessageMax is ntfy's default message-size-limit, in bytes of the
	// decoded message. A longer message is published as an attachment file
	// instead of as the notification's text, or refused when the server
	// keeps no attachments. ntfy reads a JSON request up to twice this.
	ntfyMessageMax = 4096
)

// utf16Len is the length of s in UTF-16 code units, the count Telegram's
// limit is stated in. Slack and Discord count "characters" without saying
// which; UTF-16 is never fewer than code points, so a text that fits by this
// count fits by either.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// fitBody returns body, shortened when render(body) measures more than limit
// by size. render is what the sender makes of the body, its escaping and the
// text around it included, because that is what the service measures; the
// body is cut before render sees it, so an escape is never split.
//
// The longest lines are shortened first, each to the same length, so a
// target thousands of characters long gives way and the cause, the error and
// the time under it stay. A line loses its middle, marked with "…": the start
// of an address names the host and its end the page, and the end of an error
// often says what happened, as in `keyword "…" not found in response`. Only
// when every line cut short is still too long is the text cut from the end.
func fitBody(body string, limit int, size func(string) int, render func(string) string) string {
	if size(render(body)) <= limit {
		return body
	}
	lines := strings.Split(body, "\n")
	longest := 0
	for _, line := range lines {
		longest = max(longest, utf8.RuneCountInString(line))
	}
	fits := func(s string) bool { return size(render(s)) <= limit }

	// keep is how many characters a long line keeps, the "…" included. No
	// line longer than limit can fit, since every count used here is at
	// least one per character.
	best, found := "", false
	for lo, hi := 1, min(longest-1, limit); lo <= hi; {
		keep := lo + (hi-lo)/2
		if s := capLines(lines, keep); fits(s) {
			best, found, lo = s, true, keep+1
		} else {
			hi = keep - 1
		}
	}
	if found {
		return best
	}

	runes := []rune(body)
	for lo, hi := 0, min(len(runes), limit); lo <= hi; {
		n := lo + (hi-lo)/2
		if s := cutText(runes[:n]); fits(s) {
			best, found, lo = s, true, n+1
		} else {
			hi = n - 1
		}
	}
	if found {
		return best
	}
	return ""
}

// capLines writes lines back as one text with every line over keep
// characters cut to keep, the "…" that marks the cut included. Two thirds of
// what a line keeps is its start, which is where an address names its host
// and an error its kind.
func capLines(lines []string, keep int) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		runes := []rune(line)
		if len(runes) <= keep {
			out[i] = line
			continue
		}
		head := (keep - 1) * 2 / 3
		tail := keep - 1 - head
		out[i] = string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
	}
	return strings.Join(out, "\n")
}

// cutText marks kept as the start of a longer text. Spaces before the cut
// are dropped, so the mark follows the last word.
func cutText(kept []rune) string {
	return strings.TrimRightFunc(string(kept), unicode.IsSpace) + "…"
}
