package kumaimport

import (
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// smsStandIn stands in for the withheld values when a converted Twilio
// channel is put through the SMS channel's own rules, so a channel the
// importer would refuse is caught here, with a reason, instead of failing
// the dry run of the whole file. The number is in the 555-01xx range North
// America reserves for fiction.
var smsStandIn = map[string]string{
	"numbers":    "+12025550100",
	"auth_token": "withheld",
}

// convertTwilio turns a Kuma Twilio notification into an SMS channel with
// the Twilio provider. It returns the reason when the channel cannot be
// converted, or "".
//
// The account SID names the account, like a user name, and comes over as it
// is, as does the sender. The auth token is the credential and is withheld.
// The recipient is withheld as well: a phone number is personal data, and a
// SubGlance export leaves an SMS channel's numbers out for the same reason.
// The report names it in the masked form an editor reads, so the person
// filling it in can tell which number it was.
func convertTwilio(get func(string) string, out *configfile.Channel, note func(string)) string {
	out.Type = "sms"
	out.Config["provider"] = notifier.SMSProviderTwilio
	out.Config["account_sid"] = get("twilioAccountSID")
	out.Config["from"] = twilioFrom(get("twilioFromNumber"))
	out.Config["auth_token"] = configfile.Placeholder
	out.Config["numbers"] = configfile.Placeholder

	check := map[string]string{}
	for k, v := range out.Config {
		check[k] = v
	}
	for k, v := range smsStandIn {
		check[k] = v
	}
	if err := notifier.ValidateSMSConfig(check); err != nil {
		return "its Twilio settings cannot be used by SubGlance's SMS channel: " + err.Error()
	}

	if to := get("twilioToNumber"); to != "" {
		note("fill in numbers with the number Kuma sent to (" + notifier.MaskSMSNumbers(to, "") + ")")
	} else {
		note("fill in numbers with the phone number to send to")
	}
	if get("twilioApiKey") != "" {
		// Kuma signs in with the API key and keeps the key's secret in the
		// auth token field. SubGlance signs in with the account SID, which
		// the secret of an API key does not open.
		note("Kuma signed in with an API key; SubGlance signs in with the account SID, " +
			"so fill in auth_token with the account's auth token from the Twilio console, not the API key secret")
	}
	if get("twilioMessagingServiceSID") != "" {
		note("Kuma also sent through a messaging service; SubGlance sends from the number or sender name alone")
	}
	note("SubGlance sends at most 10 alerts an hour on an SMS channel, each one text of at most 160 characters; " +
		"set hourly_limit to allow more")
	return ""
}

// twilioFrom writes Kuma's From Number as SubGlance reads it. Kuma passed
// the value to Twilio as it was typed; a phone number typed with spaces or
// dashes, or with 00 for the plus, is written in international form. A
// sender name is left alone.
func twilioFrom(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "00") {
		return s
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', '\u00a0':
			return -1
		}
		return r
	}, s)
	if strings.HasPrefix(s, "00") {
		// 00 stands for the plus only in front of a number: 00Ops is a
		// sender name, and comes over as it was typed.
		rest := s[2:]
		if rest == "" || strings.Trim(rest, "0123456789") != "" {
			return strings.TrimSpace(raw)
		}
		s = "+" + rest
	}
	return s
}
