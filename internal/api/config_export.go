package api

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
func (s *Server) exportConfig(ctx context.Context, now time.Time) (configfile.Document, error) {
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
			Config: exportChannelConfig(c.Config),
		}
		if q, ok := quiet[c.ID]; ok {
			out.QuietHours = &configfile.QuietHours{
				Start: q.Start, End: q.End, Timezone: q.Timezone, During: q.During,
			}
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
	return doc, nil
}

// exportChannelConfig withholds every value whose key is not on the public
// allowlist. An empty value is not a secret and stays empty, so the file does
// not claim a credential exists where none was set.
func exportChannelConfig(cfg map[string]string) map[string]string {
	if len(cfg) == 0 {
		return nil
	}
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if publicKeys[k] || v == "" {
			out[k] = v
			continue
		}
		out[k] = configfile.Placeholder
	}
	return out
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
		if len(m.Headers) > 0 {
			// Header names are kept so the file still says which headers
			// the check sends; the values are what authenticate it.
			out.Headers = make(map[string]string, len(m.Headers))
			for k := range m.Headers {
				out.Headers[k] = configfile.Placeholder
			}
		}
		if m.Body != "" {
			out.Body = ptr(configfile.Placeholder)
		}
		out.JSONAssertion = exportAssertion(m.JSONAssertion)
		out.SSLWarnDays = ptr(m.SSLWarnDays)
		out.MinTLSVersion = ptr(checker.TLSVersionLabel(m.MinTLSVersion))
	case "ssl":
		out.SSLWarnDays = ptr(m.SSLWarnDays)
		out.MinTLSVersion = ptr(checker.TLSVersionLabel(m.MinTLSVersion))
	case store.TypePush:
		out.PushIntervalS = ptr(m.PushIntervalS)
		out.PushGraceS = ptr(m.PushGraceS)
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
