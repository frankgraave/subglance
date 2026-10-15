package notifier

import (
	"slices"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// senderRefusals is, per channel type, settings its sender refuses and the
// config key the refusal has to name. The API puts a refusal under the input
// it names, and one that names none lands below the form, away from the
// field to correct.
var senderRefusals = map[string][]struct {
	cfg map[string]string
	key string
}{
	store.ChannelEmail: {
		{map[string]string{"from": "sg@example.com", "to": "ops@example.com"}, "host"},
		{map[string]string{"host": "smtp.example.com", "to": "ops@example.com"}, "from"},
		{map[string]string{"host": "smtp.example.com", "from": "sg@example.com"}, "to"},
		{map[string]string{"host": "smtp.example.com", "from": "SubGlance", "to": "ops@example.com"}, "from"},
		{map[string]string{"host": "smtp.example.com", "from": "sg@example.com", "to": "ops"}, "to"},
		{map[string]string{"host": "smtp.example.com", "from": "sg@example.com", "to": "ops@example.com", "port": "abc"}, "port"},
		{map[string]string{"host": "smtp.example.com", "from": "sg@example.com", "to": "ops@example.com", "port": "99999"}, "port"},
		{map[string]string{"host": "smtp.example.com", "from": "sg@example.com", "to": "ops@example.com", "port": "0"}, "port"},
		{map[string]string{"host": "smtp.example.com:2525", "from": "sg@example.com", "to": "ops@example.com"}, "host"},
		{map[string]string{"host": "smtp..example.com", "from": "sg@example.com", "to": "ops@example.com"}, "host"},
	},
	store.ChannelTelegram: {
		{map[string]string{"chat_id": "42"}, "bot_token"},
		{map[string]string{"bot_token": "123:abc"}, "chat_id"},
		{map[string]string{"bot_token": "123:abc", "chat_id": "  "}, "chat_id"},
		{map[string]string{"bot_token": "@my_bot", "chat_id": "42"}, "bot_token"},
	},
	store.ChannelDiscord: {
		{map[string]string{}, "url"},
		{map[string]string{"url": "ftp://discord.com/api/webhooks/1/x"}, "url"},
		{map[string]string{"url": "https://hooks.example.com/services/x"}, "url"},
		{map[string]string{"url": "https://discord.com:99999/api/webhooks/1/x"}, "url"},
	},
	store.ChannelSlack: {
		{map[string]string{}, "url"},
		{map[string]string{"url": "https:///services/x"}, "url"},
		{map[string]string{"url": "https://hooks.slack.com:0/services/x"}, "url"},
		{map[string]string{"url": "https://hooks..slack.com/services/x"}, "url"},
	},
	store.ChannelGotify: {
		{map[string]string{"token": "t"}, "url"},
		{map[string]string{"url": "https://gotify.example.com"}, "token"},
		{map[string]string{"url": "https://got!fy.example.com", "token": "t"}, "url"},
		{map[string]string{"url": "https://gotify.example.com", "token": "t", "priority_down": "11"}, "priority_down"},
		{map[string]string{"url": "https://gotify.example.com", "token": "t", "priority_up": "x"}, "priority_up"},
	},
	store.ChannelNtfy: {
		{map[string]string{}, "topic"},
		{map[string]string{"topic": "a b"}, "topic"},
		{map[string]string{"topic": "t", "url": "ftp://ntfy.example.com"}, "url"},
		{map[string]string{"topic": "t", "url": "https://ntfy.example.com/t"}, "url"},
		{map[string]string{"topic": "t", "url": "https://ntfy.example.com:70000"}, "url"},
		{map[string]string{"topic": "t", "token": "  "}, "token"},
		{map[string]string{"topic": "t", "token": "k", "username": "u", "password": "p"}, "token"},
		{map[string]string{"topic": "t", "username": "u"}, "password"},
		{map[string]string{"topic": "t", "password": "p"}, "username"},
	},
	store.ChannelWebhook: {
		{map[string]string{}, "url"},
		{map[string]string{"url": "https://hooks.example.com/{{monitor_name}}"}, "url"},
		{map[string]string{"url": "https://hooks.example.com/in", "method": "GET"}, "method"},
		{map[string]string{"url": "https://-hooks.example.com/in"}, "url"},
		{map[string]string{"url": "https://hooks.example.com:99999/in"}, "url"},
		{map[string]string{"url": "https://hooks.example.com/in", "headers": "not-a-header"}, "headers"},
		{map[string]string{"url": "https://hooks.example.com/in", "body": `{"x": {{summary}}}`}, "body"},
	},
	store.ChannelSMS: {
		{map[string]string{"numbers": "+31612345678"}, "provider"},
		{map[string]string{"numbers": "+31612345678", "provider": "twilio", "account_sid": "nope"}, "account_sid"},
		{map[string]string{"provider": "android-gateway", "url": "http://phone.example:8080", "username": "u", "password": "p"}, "numbers"},
		{map[string]string{"numbers": "+31612345678", "provider": "android-gateway", "url": "http://192.168.1.50:80800", "username": "u", "password": "p"}, "url"},
	},
}

func TestEverySenderRefusalNamesItsConfigKey(t *testing.T) {
	for _, typ := range deliverableTypes(t) {
		cases, ok := senderRefusals[typ]
		if !ok || len(cases) == 0 {
			t.Errorf("no refusals listed for channel type %q; add its sender's rules to senderRefusals", typ)
			continue
		}
		for _, tc := range cases {
			err := ValidateConfig(typ, tc.cfg)
			if err == nil {
				t.Errorf("%s %v: accepted, want a refusal about %q", typ, tc.cfg, tc.key)
				continue
			}
			if got := ConfigKey(err); got != tc.key {
				t.Errorf("%s %v: refusal %q names key %q, want %q", typ, tc.cfg, err, got, tc.key)
			}
		}
	}
}

// ValidateConfig asks the same senders a Notifier delivers through, so the
// API cannot accept a type the notifier has no sender for, or hold one to
// another type's rules.
func TestValidateConfigCoversEveryDeliverableType(t *testing.T) {
	var rules []string
	for typ := range configRules {
		rules = append(rules, typ)
	}
	slices.Sort(rules)
	if want := deliverableTypes(t); !slices.Equal(rules, want) {
		t.Errorf("ValidateConfig knows %v; the notifier delivers through %v", rules, want)
	}
	if err := ValidateConfig("pager", map[string]string{}); err == nil {
		t.Error("an unknown channel type was accepted")
	}
}
