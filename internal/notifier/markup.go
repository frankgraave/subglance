package notifier

import (
	"regexp"
	"strings"
	"unicode"
)

// Slack and Discord read an alert's text as markup, and much of that text is
// not SubGlance's own: a push monitor's failure message is whatever the
// caller of the push URL put in `msg`, a JSON assertion quotes the value the
// endpoint returned, and a keyword, a DNS record or a monitor name is typed
// by somebody. Passed through as markup, a message saying `<!channel>` pings
// everyone in a Slack channel, and `[the runbook](https://…)` becomes a link
// whose label hides where it goes — in a message from the tool people trust
// to page them. The functions here make such text read as typed. They are
// applied to the alert's text only, never to the links a sender adds itself,
// which are markup on purpose.

// slackReplacer encodes the three characters Slack's formatting rules name as
// its control characters, and only those three: Slack decodes the entities
// again, so the reader sees the text unchanged, and encoding more would show
// up as literal entities.
var slackReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// slackText makes text safe to place in a Slack message: no mention, no
// link and no channel reference can be written with it. Slack's emphasis
// characters are left alone, because mrkdwn has no escape for them; a stray
// asterisk can make a word bold, which is not worth inventing a workaround
// for.
func slackText(s string) string {
	return slackReplacer.Replace(s)
}

// discordURL matches an address the way Discord's markdown finds a bare link
// in text: a run up to whitespace or `<`, not ending in punctuation that
// usually closes a sentence. The match is kept as it is, because a backslash
// inside an address is part of the address once Discord has made it a link,
// and the reader would follow a broken URL.
var discordURL = regexp.MustCompile(`https?://[^\s<]+[^<.,:;"')\]\s]`)

// discordText makes text safe to place in a Discord embed description: a
// backslash before each markdown character makes Discord draw that character
// instead of acting on it, so `[label](url)` cannot become a masked link and
// `*`, `_`, `~`, `|` and backticks show as typed. A heading, quote or list
// marker only means something at the start of a line, so it is escaped only
// there, which keeps dates and ordinary dashes free of backslashes. Addresses
// are left untouched; see discordURL.
func discordText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lineStart := true
	last := 0
	for _, loc := range discordURL.FindAllStringIndex(s, -1) {
		// An address follows, so where the text before it leaves off
		// does not matter: the line has started once the address is in.
		escapeDiscord(&b, s[last:loc[0]], lineStart)
		b.WriteString(s[loc[0]:loc[1]])
		lineStart = false
		last = loc[1]
	}
	escapeDiscord(&b, s[last:], lineStart)
	return b.String()
}

// escapeDiscord writes s to b with Discord's markdown characters escaped, and
// reports whether the next character written would start a line. lineStart
// says whether s begins one; leading spaces keep a line's start open, as
// they do for Discord.
func escapeDiscord(b *strings.Builder, s string, lineStart bool) bool {
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '\n':
			b.WriteRune(r)
			lineStart = true
			continue
		case r == ' ' || r == '\t':
			b.WriteRune(r)
			continue
		case r == '_' && inWord(rs, i):
			// Discord italicises only from an underscore at a word's
			// edge, so one inside a word, as in a failure kind like
			// cert_expiry, is drawn as it is and needs no backslash.
		case strings.ContainsRune("\\*_~`|[]<", r):
			b.WriteByte('\\')
		case lineStart && strings.ContainsRune("#>-+", r):
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		lineStart = false
	}
	return lineStart
}

// inWord reports whether rs[i] stands between two letters or digits. A second
// underscore beside it does not count: two make an underline wherever they
// stand.
func inWord(rs []rune, i int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	return i > 0 && i < len(rs)-1 && word(rs[i-1]) && word(rs[i+1])
}
