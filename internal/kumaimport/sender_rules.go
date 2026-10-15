package kumaimport

import (
	"maps"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// holdToSender holds a converted channel to the rules its sender holds a
// channel to, as the importer will, so one Kuma notification the sender
// refuses cannot fail the import of the whole file.
//
// Most converters write the server address Kuma used as it is, and Kuma
// accepted addresses SubGlance refuses: a port of 0 or above 65535, a
// hostname the resolver refuses without asking, an ntfy server with a path.
// Such an address is withheld instead, which leaves the channel to be
// created switched off with the url to fill in, and the report says why. A
// channel its sender still refuses is skipped with the sender's reason.
//
// The withheld values are given the stand-ins the importer gives them
// (configfile.WithheldStandIn), so this check and the importer's cannot
// disagree about a value the file does not hold.
func holdToSender(out configfile.Channel, note func(string)) (string, bool) {
	err := notifier.ValidateConfig(out.Type, withStandIns(out))
	if err == nil {
		return "", true
	}
	if raw := out.Config["url"]; notifier.ConfigKey(err) == "url" && raw != configfile.Placeholder {
		out.Config["url"] = configfile.Placeholder
		// The note is written only for a channel that comes over: a
		// skipped one is reported once, with the reason that skipped it.
		if again := notifier.ValidateConfig(out.Type, withStandIns(out)); again != nil {
			err = again
		} else {
			note(refusedAddress(raw, err))
			return "", true
		}
	}
	return "SubGlance's " + out.Type + " channel cannot use its settings: " + err.Error(), false
}

// withStandIns is a channel's settings as the importer validates them: a
// withheld value is replaced by its stand-in, or left out when it has none.
func withStandIns(c configfile.Channel) map[string]string {
	cfg := maps.Clone(c.Config)
	for k, v := range cfg {
		if v != configfile.Placeholder {
			continue
		}
		if s, ok := configfile.WithheldStandIn(c.Type, k); ok {
			cfg[k] = s
		} else {
			delete(cfg, k)
		}
	}
	return cfg
}

// refusedAddress says why the url was withheld: Kuma's settings gave none,
// or gave one SubGlance cannot post to. An address with an @, a ? or a #
// can hold a credential, so it is not quoted, and neither is the sender's
// reason, which can quote the address when it does not parse.
func refusedAddress(raw string, err error) string {
	if raw == "" {
		return "Kuma's settings gave no address; fill in the url"
	}
	if strings.ContainsAny(raw, "@?#") {
		return "Kuma's address is not one SubGlance can post to, so it was left out; fill in the url"
	}
	return "Kuma's address " + strconv.Quote(raw) + " is not one SubGlance can post to (" + err.Error() +
		"), so it was left out; fill in the url"
}
