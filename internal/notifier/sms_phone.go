package notifier

import (
	"fmt"
	"regexp"
	"strings"
)

// smsMaxRecipients bounds how many numbers one SMS channel sends to. Every
// number is one message per alert, and on a paid provider one bill line; a
// channel with fifty numbers is a broadcast list, not an alert route.
const smsMaxRecipients = 10

// e164 is the shape every stored number has: a plus, a country code that does
// not start with zero, and at most fifteen digits in all. The lower bound is
// seven because some small numbering plans are that short.
var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// normalizePhone turns a number as a person types it into E.164.
//
// It accepts the international form with a plus ("+31 6 1234 5678"), the
// international form with 00 ("0031 6 1234 5678"), the "(0)" some people
// write after the country code ("+31 (0)6 1234 5678"), and, when the channel
// has a country code, the national form ("06-12345678"): the leading trunk
// zero goes and the country code is put in front. Spaces, dashes, dots,
// slashes and brackets are layout and are dropped.
//
// The national rule does not hold everywhere (Italian landlines keep their
// zero), which is why the plus form is always accepted as typed. A number
// that cannot be read is refused with the number in the message, so the
// person typing it sees which of several entries is wrong.
func normalizePhone(raw, countryCode string) (string, error) {
	typed := strings.TrimSpace(raw)
	if strings.Contains(typed, smsMaskDots) {
		// What a non-administrator reads back. Saving it would store
		// the dots; saying so is kinder than "not a phone number".
		return "", &configError{fmt.Sprintf(
			"number %q is masked: type it in full (only administrators see numbers in full)", typed)}
	}
	s := strings.ReplaceAll(typed, "(0)", "")
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '/', '(', ')', '\u00a0':
			return -1
		}
		return r
	}, s)

	switch {
	case strings.HasPrefix(s, "+"):
	case strings.HasPrefix(s, "00"):
		s = "+" + s[2:]
	case countryCode != "":
		s = countryCode + strings.TrimPrefix(s, "0")
	default:
		return "", &configError{fmt.Sprintf(
			"number %q has no country code: write it as +<country code><number>, or set country_code", typed)}
	}
	if !e164.MatchString(s) {
		return "", &configError{fmt.Sprintf("number %q is not a phone number SubGlance can send to", typed)}
	}
	return s, nil
}

// countryCodePattern is a country code without its plus: one to three digits,
// never starting with zero.
var countryCodePattern = regexp.MustCompile(`^[1-9][0-9]{0,2}$`)

// normalizeCountryCode reads the channel's country code for numbers typed
// without one. "+31", "31" and "0031" all mean the same; empty means none.
func normalizeCountryCode(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "00"):
		s = s[2:]
	}
	if !countryCodePattern.MatchString(s) {
		return "", &configError{fmt.Sprintf("country_code %q is not a country code (expected something like +31)", raw)}
	}
	return "+" + s, nil
}

// smsRecipients reads the channel's numbers: separated by commas, semicolons
// or new lines, each normalised to E.164.
//
// A number listed twice is refused rather than quietly merged. Two spellings
// of one number usually mean a typo in one of them, and the person saving
// the form is the one who can tell.
func smsRecipients(cfg map[string]string) ([]string, error) {
	cc, err := normalizeCountryCode(cfg["country_code"])
	if err != nil {
		return nil, err
	}
	fields := strings.FieldsFunc(cfg["numbers"], func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	var out []string
	seen := map[string]string{}
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			continue
		}
		n, err := normalizePhone(f, cc)
		if err != nil {
			return nil, &configError{"numbers: " + err.Error()}
		}
		if first, dup := seen[n]; dup {
			return nil, &configError{fmt.Sprintf("numbers lists %s twice (%q and %q)",
				maskPhone(n), strings.TrimSpace(first), strings.TrimSpace(f))}
		}
		seen[n] = f
		out = append(out, n)
	}
	switch {
	case len(out) == 0:
		return nil, &configError{"numbers is required (one or more phone numbers)"}
	case len(out) > smsMaxRecipients:
		return nil, &configError{fmt.Sprintf("numbers lists %d numbers; at most %d are allowed", len(out), smsMaxRecipients)}
	}
	return out, nil
}

// twoDigitCountryCodes are the E.164 country codes of two digits. With +1 and
// +7, the only one-digit codes, they are all that is needed to split any
// number into country code and the rest; every other code has three digits.
// The list is fixed by the ITU and has not changed in decades.
var twoDigitCountryCodes = map[string]bool{
	"20": true, "27": true, "30": true, "31": true, "32": true, "33": true, "34": true,
	"36": true, "39": true, "40": true, "41": true, "43": true, "44": true, "45": true,
	"46": true, "47": true, "48": true, "49": true, "51": true, "52": true, "53": true,
	"54": true, "55": true, "56": true, "57": true, "58": true, "60": true, "61": true,
	"62": true, "63": true, "64": true, "65": true, "66": true, "81": true, "82": true,
	"84": true, "86": true, "90": true, "91": true, "92": true, "93": true, "94": true,
	"95": true, "98": true,
}

// maskPhone shows enough of an E.164 number to recognise it and no more:
// country code, the first digit after it and the last four, as in
// "+31 6 •••• 5678". A phone number is personal data, and this is the only
// form that reaches a log line, the delivery log or a non-admin reader.
//
// Anything that is not E.164 is masked completely rather than guessed at.
func maskPhone(n string) string {
	if !e164.MatchString(n) {
		return smsMaskDots
	}
	digits := n[1:]
	cc := 3
	switch {
	case digits[0] == '1' || digits[0] == '7':
		cc = 1
	case twoDigitCountryCodes[digits[:2]]:
		cc = 2
	}
	rest := digits[cc:]
	if len(rest) < 8 {
		// A short national number would be shown almost whole by the
		// usual form; two digits are enough to tell entries apart.
		return "+" + digits[:cc] + " " + smsMaskDots + " " + rest[len(rest)-2:]
	}
	return "+" + digits[:cc] + " " + rest[:1] + " " + smsMaskDots + " " + rest[len(rest)-4:]
}

// smsMaskDots stands for the hidden digits of a masked number.
const smsMaskDots = "••••"

// MaskSMSNumbers masks a channel's numbers setting for a reader who may not
// see them: each number as maskPhone shows it, comma-separated. A part that
// is not a number SubGlance could send to is masked completely.
func MaskSMSNumbers(raw, countryCode string) string {
	cc, err := normalizeCountryCode(countryCode)
	if err != nil {
		cc = ""
	}
	var out []string
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if strings.TrimSpace(f) == "" {
			continue
		}
		n, err := normalizePhone(f, cc)
		if err != nil {
			out = append(out, smsMaskDots)
			continue
		}
		out = append(out, maskPhone(n))
	}
	return strings.Join(out, ", ")
}

// scrubPhones replaces every listed number in s with its masked form. A
// provider's error text often quotes the number it refused, and that text is
// stored and shown; the number must not travel with it.
func scrubPhones(s string, numbers []string) string {
	for _, n := range numbers {
		m := maskPhone(n)
		s = strings.ReplaceAll(s, n, m)
		// Without the plus, as some providers echo it.
		s = strings.ReplaceAll(s, n[1:], m)
	}
	return s
}
