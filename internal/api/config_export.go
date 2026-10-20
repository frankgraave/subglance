package api

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
	"gopkg.in/yaml.v3"
)

// exportConfig renders the instance's configuration as a configfile.Document.
//
// A monitor or channel that has no configuration key yet is given one here,
// derived from its name, and the key is stored. That write is what makes the
// file re-importable: the next import finds the object by the key this export
// handed out, instead of creating a copy of it. It happens once per object;
// every later export reads the stored key back.
//
// Credentials never leave through this function. Channel settings follow the
// same allowlist as the masked API read (publicKeys), and request headers and
// bodies are withheld whole. See configfile.Placeholder.
//
// Status pages are written only when withPages is set, which the handler
// does for an administrator: the status page API is theirs alone, and an
// export is a read of it like any other.
func (s *Server) exportConfig(ctx context.Context, now time.Time, withPages bool) (configfile.Document, error) {
	doc := configfile.Document{Version: configfile.Version}

	channels, err := s.db.ListChannels(ctx)
	if err != nil {
		return doc, fmt.Errorf("list channels: %w", err)
	}
	channelKeys, err := assignKeys(ctx, "channel", channelsNamed(channels),
		s.db.ChannelConfigKeys, s.db.SetChannelConfigKey)
	if err != nil {
		return doc, err
	}
	quiet, err := s.db.ListQuietHours(ctx)
	if err != nil {
		return doc, fmt.Errorf("list quiet hours: %w", err)
	}
	for _, c := range channels {
		out := configfile.Channel{
			Key: channelKeys[c.ID], Name: c.Name, Type: c.Type,
			Enabled: ptr(c.Enabled), Default: c.IsDefault,
			Config: exportChannelConfig(c),
		}
		// Written for every channel, null when it has none, so importing
		// the file removes quiet hours the target channel still has.
		var window *configfile.QuietHours
		if q, ok := quiet[c.ID]; ok {
			window = &configfile.QuietHours{
				Start: q.Start, End: q.End, Timezone: q.Timezone, During: q.During,
			}
		}
		if out.QuietHours, err = configfile.QuietHoursNode(window); err != nil {
			return doc, err
		}
		doc.Channels = append(doc.Channels, out)
	}

	monitors, err := s.db.ListMonitors(ctx)
	if err != nil {
		return doc, fmt.Errorf("list monitors: %w", err)
	}
	monitorKeys, err := assignKeys(ctx, "monitor", monitorsNamed(monitors),
		s.db.MonitorConfigKeys, s.db.SetMonitorConfigKey)
	if err != nil {
		return doc, err
	}
	attached, err := s.db.MonitorChannelSummaries(ctx)
	if err != nil {
		return doc, fmt.Errorf("list monitor channels: %w", err)
	}
	for _, m := range monitors {
		out := exportMonitor(m)
		out.Key = monitorKeys[m.ID]
		out.Channels = []string{}
		for _, c := range attached[m.ID] {
			out.Channels = append(out.Channels, channelKeys[c.ID])
		}
		doc.Monitors = append(doc.Monitors, out)
	}

	rules, err := s.db.ListRoutingRules(ctx)
	if err != nil {
		return doc, fmt.Errorf("list routing rules: %w", err)
	}
	for _, r := range rules {
		out := configfile.RoutingRule{TagKey: r.TagKey, TagValue: r.TagValue,
			Channels: []string{}, Exclude: []string{}}
		for _, id := range r.ChannelIDs {
			out.Channels = append(out.Channels, channelKeys[id])
		}
		for _, id := range r.ExcludedMonitorIDs {
			out.Exclude = append(out.Exclude, monitorKeys[id])
		}
		doc.RoutingRules = append(doc.RoutingRules, out)
	}

	windows, err := s.db.ListMaintenance(ctx)
	if err != nil {
		return doc, fmt.Errorf("list maintenance windows: %w", err)
	}
	for _, w := range windows {
		// A one-off window that has ended is history, not configuration.
		// Carrying it to another instance would only use up that
		// instance's window allowance with maintenance that already
		// happened.
		if w.Timezone == "" && !w.EndsAt.After(now) {
			continue
		}
		doc.Maintenance = append(doc.Maintenance, exportMaintenance(w, monitorKeys))
	}
	if withPages {
		if doc.StatusPages, err = s.exportStatusPages(ctx, monitorKeys); err != nil {
			return doc, err
		}
	}
	return doc, nil
}

// exportChannelConfig withholds every value whose key is not on the public
// allowlist. An empty value is not a secret and stays empty, so the file does
// not claim a credential exists where none was set. A value the channel is
// stored without but cannot be imported without is written as withheld too
// (stillWithheld).
func exportChannelConfig(c store.Channel) map[string]string {
	out := make(map[string]string, len(c.Config))
	for k, v := range c.Config {
		if publicKeys[k] || v == "" {
			out[k] = v
			continue
		}
		out[k] = configfile.Placeholder
	}
	for _, k := range stillWithheld(c, out) {
		out[k] = configfile.Placeholder
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stillWithheld returns the settings a channel is stored without that an
// import would refuse the written file for lacking. An import of a file that
// withheld a credential leaves it out and switches the channel off, so a
// Discord channel can be stored with no url at all; written without one, the
// file was refused for a missing required value, by another instance and by
// the one that wrote it, which promises that its own export imports as
// unchanged. Written as withheld, the file says what is still to be filled
// in and imports the way the first file did: switched off, the setting
// listed under needs_secrets.
//
// The check is the importer's own: the channel as the file has it, each
// withheld value replaced by its stand-in (configfile.WithheldStandIn), held
// to the rules a save is. A refusal of a setting the file leaves out, and
// that has a stand-in, adds it and checks again. So does a webhook body
// refused while the headers are left out, because the body is read by the
// Content-Type the headers name. A key is kept only when the refusal moves
// on: one that changes nothing would claim a setting the channel never had.
// Each round adds a key from the finite set that has a stand-in, so the loop
// ends.
func stillWithheld(c store.Channel, written map[string]string) []string {
	cfg := maps.Clone(written)
	var added []string
	pr := importRefusal(c, cfg)
	for !pr.ok() {
		key, isConfig := strings.CutPrefix(pr.field, "config.")
		if c.Type == store.ChannelWebhook && key == "body" {
			key = "headers"
		}
		if _, given := cfg[key]; given || !isConfig {
			break
		}
		if _, ok := configfile.WithheldStandIn(c.Type, key); !ok {
			break
		}
		cfg[key] = configfile.Placeholder
		next := importRefusal(c, cfg)
		if next == pr {
			break
		}
		added = append(added, key)
		pr = next
	}
	return added
}

// importRefusal is what the importer says of a channel written with cfg as
// its settings, on the instance the channel is stored in.
func importRefusal(c store.Channel, cfg map[string]string) problem {
	req := channelRequest{Name: c.Name, Type: c.Type, Config: map[string]string{}}
	for k, v := range cfg {
		if v != configfile.Placeholder {
			req.Config[k] = v
		} else if s, ok := configfile.WithheldStandIn(c.Type, k); ok {
			req.Config[k] = s
		}
	}
	return validateChannel(req, &c)
}

// exportMonitor writes every field that applies to the monitor's type, so the
// file does not depend on defaults that a later version might change.
func exportMonitor(m store.Monitor) configfile.Monitor {
	out := configfile.Monitor{
		Name: m.Name, Type: m.Type, Target: m.Target,
		Enabled:           ptr(m.Enabled),
		IntervalS:         ptr(m.IntervalS),
		TimeoutS:          ptr(m.TimeoutS),
		Retries:           ptr(m.Retries),
		RecoveryThreshold: ptr(m.RecoveryThreshold),
		RepeatAfterS:      ptr(m.RepeatAfterS),
		Tags:              m.Tags,
	}
	if out.Tags == nil {
		out.Tags = map[string]string{}
	}
	switch m.Type {
	case "http":
		out.Method = ptr(m.Method)
		out.ExpectedStatus = ptr(m.ExpectedStatus)
		out.Keyword = ptr(m.Keyword)
		out.KeywordMode = ptr(m.KeywordMode)
		out.FollowRedirects = ptr(m.FollowRedirects)
		out.CaptureResponse = ptr(m.CaptureResponse)
		// Both are written when empty too: `headers: {}` and `body: ""`
		// remove what the target monitor sends, where an absent key would
		// keep it. Header names are kept so the file still says which
		// headers the check sends; the values are what authenticate it.
		// An empty value authenticates nothing and is written as it is,
		// as an empty channel setting is: withheld, it read as a
		// credential no instance has, and the import switched the
		// monitor off for it, on the instance that wrote the file too.
		out.Headers = make(configfile.Headers, len(m.Headers))
		for k, v := range m.Headers {
			out.Headers[k] = configfile.Placeholder
			if v == "" {
				out.Headers[k] = ""
			}
		}
		out.Body = ptr("")
		if m.Body != "" {
			out.Body = ptr(configfile.Placeholder)
		}
		out.JSONAssertion = exportAssertion(m.JSONAssertion)
		out.SSLWarnDays = ptr(m.SSLWarnDays)
		out.MinTLSVersion = ptr(checker.TLSVersionLabel(m.MinTLSVersion))
		// Written "" too, on every monitor that can carry a pin, so an
		// import of the file removes a pin the file does not have.
		if pinCarrier(m.Type, m.Target) {
			out.TrustedCertSHA256 = ptr(m.TrustedCertSHA256)
		}
	case "ssl":
		out.SSLWarnDays = ptr(m.SSLWarnDays)
		out.MinTLSVersion = ptr(checker.TLSVersionLabel(m.MinTLSVersion))
		out.TrustedCertSHA256 = ptr(m.TrustedCertSHA256)
	case store.TypeDNS:
		if w := dnsCheckToWire(m.DNS); w != nil {
			out.DNS = &configfile.DNSCheck{RecordType: w.RecordType, Expected: w.Expected, Resolver: w.Resolver}
		}
	case store.TypeDomain:
		out.DomainWarnDays = ptr(m.DomainWarnDays)
	case store.TypePush:
		out.PushIntervalS = ptr(m.PushIntervalS)
		out.PushGraceS = ptr(m.PushGraceS)
		out.PushTimezone = ptr(m.PushTimezone)
	}
	return out
}

// exportAssertion writes a monitor's JSON assertion, or an explicit null.
//
// The stored expected value was validated as JSON when it was saved, so
// AssertionNode cannot fail on it. Were it to, the field is left out rather
// than guessed at: on import an absent field keeps what the monitor has.
func exportAssertion(a *store.JSONAssertion) yaml.Node {
	var in *configfile.JSONAssertion
	if a != nil {
		in = &configfile.JSONAssertion{Path: a.Path, Operator: a.Operator, Expected: a.Expected}
	}
	n, err := configfile.AssertionNode(in)
	if err != nil {
		return yaml.Node{}
	}
	return n
}

func exportMaintenance(w store.MaintenanceWindow, monitorKeys map[int64]string) configfile.Maintenance {
	out := configfile.Maintenance{
		Name: w.Name, TagKey: w.TagKey, TagValue: w.TagValue,
		Timezone: w.Timezone, Weekdays: w.Weekdays, LocalTime: w.LocalTime,
		DurationMinutes: w.DurationMinutes,
	}
	if w.MonitorID > 0 {
		out.Monitor = monitorKeys[w.MonitorID]
	}
	if !w.StartsAt.IsZero() {
		out.StartsAt = ptr(w.StartsAt.UTC())
	}
	if !w.EndsAt.IsZero() {
		out.EndsAt = ptr(w.EndsAt.UTC())
	}
	return out
}

// named is the part of a monitor or a channel that key assignment needs.
type named struct {
	id   int64
	name string
}

func monitorsNamed(ms []store.Monitor) []named {
	out := make([]named, 0, len(ms))
	for _, m := range ms {
		out = append(out, named{m.ID, m.Name})
	}
	return out
}

func channelsNamed(cs []store.Channel) []named {
	out := make([]named, 0, len(cs))
	for _, c := range cs {
		out = append(out, named{c.ID, c.Name})
	}
	return out
}

// assignKeys returns every object's configuration key, deriving and storing
// one for each object that has none. Objects are handled in id order, so the
// oldest of two monitors called "API" keeps the plain key "api".
func assignKeys(ctx context.Context, what string, objs []named,
	list func(context.Context) (map[int64]string, error),
	set func(context.Context, int64, string) error,
) (map[int64]string, error) {
	keys, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list %s keys: %w", what, err)
	}
	taken := make(map[string]bool, len(keys))
	for _, k := range keys {
		taken[k] = true
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].id < objs[j].id })
	for _, o := range objs {
		if _, ok := keys[o.id]; ok {
			continue
		}
		key := configfile.DeriveKey(o.name, what+"-"+strconv.FormatInt(o.id, 10),
			func(k string) bool { return taken[k] })
		if err := set(ctx, o.id, key); err != nil {
			return nil, fmt.Errorf("store %s key: %w", what, err)
		}
		keys[o.id] = key
		taken[key] = true
	}
	return keys, nil
}

func ptr[T any](v T) *T { return &v }
