package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// Import actions, as reported per object.
const (
	actionCreate    = "create"
	actionUpdate    = "update"
	actionUnchanged = "unchanged"
)

// importItem reports what an import does, or would do, to one object.
type importItem struct {
	Key    string `json:"key,omitempty"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action"`

	// Changes names the fields an update touches, in the file's spelling.
	Changes []string `json:"changes,omitempty"`

	// NeedsSecrets lists withheld values this instance has nothing to
	// replace with. The object is saved switched off, because a channel
	// without its token, or a check without its auth header, would fail
	// on every attempt and look like an outage.
	NeedsSecrets []string `json:"needs_secrets,omitempty"`

	// PushURL is the new push URL of a push monitor this import created.
	// Like the create response, it is the only time it is shown.
	PushURL string `json:"push_url,omitempty"`
}

// importReport is the response to an import, dry run or not.
type importReport struct {
	DryRun       bool         `json:"dry_run"`
	Channels     []importItem `json:"channels"`
	Monitors     []importItem `json:"monitors"`
	RoutingRules []importItem `json:"routing_rules"`
	Maintenance  []importItem `json:"maintenance"`
	Summary      importCounts `json:"summary"`
}

type importCounts struct {
	Create       int `json:"create"`
	Update       int `json:"update"`
	Unchanged    int `json:"unchanged"`
	NeedsSecrets int `json:"needs_secrets"`
}

// importPlan is a validated import: everything that will be written, decided
// before the first write, so a file with one bad monitor changes nothing.
type importPlan struct {
	report   importReport
	channels []channelStep
	monitors []monitorStep
	rules    []ruleStep
	windows  []windowStep
}

type channelStep struct {
	key         string
	id          int64 // 0 when created
	channel     store.Channel
	write       bool
	setKey      bool
	quiet       *store.QuietHours
	makeDefault bool
}

type monitorStep struct {
	item     int // index into report.Monitors
	key      string
	id       int64
	monitor  store.Monitor
	write    bool
	setKey   bool
	channels []string // channel keys; nil leaves assignments alone
}

type ruleStep struct {
	id       int64
	rule     store.RoutingRule
	channels []string // nil leaves the rule's channels alone
	exclude  []string // nil leaves exclusions alone
	write    bool
	excluded []int64 // current exclusions, for the sync
}

type windowStep struct {
	window  store.MaintenanceWindow
	monitor string // monitor key, resolved at apply time
}

// existingConfig is the instance as the plan found it.
type existingConfig struct {
	channels map[string]store.Channel // by key
	quiet    map[int64]store.QuietHours
	monitors map[string]store.Monitor // by key
	attached map[int64][]int64
	rules    map[string]store.RoutingRule // by key=value
	windows  []store.MaintenanceWindow
}

func (s *Server) loadExisting(ctx context.Context) (existingConfig, error) {
	var ex existingConfig
	channels, err := s.db.ListChannels(ctx)
	if err != nil {
		return ex, err
	}
	channelKeys, err := s.db.ChannelConfigKeys(ctx)
	if err != nil {
		return ex, err
	}
	ex.channels = map[string]store.Channel{}
	for _, c := range channels {
		if k, ok := channelKeys[c.ID]; ok {
			ex.channels[k] = c
		}
	}
	if ex.quiet, err = s.db.ListQuietHours(ctx); err != nil {
		return ex, err
	}
	monitors, err := s.db.ListMonitors(ctx)
	if err != nil {
		return ex, err
	}
	monitorKeys, err := s.db.MonitorConfigKeys(ctx)
	if err != nil {
		return ex, err
	}
	ex.monitors = map[string]store.Monitor{}
	for _, m := range monitors {
		if k, ok := monitorKeys[m.ID]; ok {
			ex.monitors[k] = m
		}
	}
	summaries, err := s.db.MonitorChannelSummaries(ctx)
	if err != nil {
		return ex, err
	}
	ex.attached = map[int64][]int64{}
	for id, cs := range summaries {
		for _, c := range cs {
			ex.attached[id] = append(ex.attached[id], c.ID)
		}
	}
	rules, err := s.db.ListRoutingRules(ctx)
	if err != nil {
		return ex, err
	}
	ex.rules = map[string]store.RoutingRule{}
	for _, r := range rules {
		ex.rules[r.TagKey+"="+r.TagValue] = r
	}
	ex.windows, err = s.db.ListMaintenance(ctx)
	return ex, err
}

// planImport validates doc against the instance and decides every write.
// A *configfile.Problem is a mistake in the file; any other error is the
// instance failing to answer.
func (s *Server) planImport(ctx context.Context, doc configfile.Document) (importPlan, error) {
	ex, err := s.loadExisting(ctx)
	if err != nil {
		return importPlan{}, err
	}
	p := importPlan{report: importReport{
		Channels: []importItem{}, Monitors: []importItem{},
		RoutingRules: []importItem{}, Maintenance: []importItem{},
	}}

	// Keys a reference may name: every object in the file, plus every keyed
	// object already on the instance. A file that attaches a monitor to a
	// channel set up by hand on the target instance is legitimate.
	channelKnown := map[string]bool{}
	for k := range ex.channels {
		channelKnown[k] = true
	}
	for _, c := range doc.Channels {
		channelKnown[c.Key] = true
	}
	monitorKnown := map[string]bool{}
	for k := range ex.monitors {
		monitorKnown[k] = true
	}
	for _, m := range doc.Monitors {
		monitorKnown[m.Key] = true
	}

	for i, c := range doc.Channels {
		if err := s.planChannel(ctx, &p, ex, fmt.Sprintf("channels[%d]", i), c); err != nil {
			return importPlan{}, err
		}
	}
	for i, m := range doc.Monitors {
		path := fmt.Sprintf("monitors[%d]", i)
		if err := checkRefs(sub(path, "channels"), m.Channels, channelKnown, "channel"); err != nil {
			return importPlan{}, err
		}
		if err := planMonitor(&p, ex, path, m); err != nil {
			return importPlan{}, err
		}
	}
	for i, r := range doc.RoutingRules {
		path := fmt.Sprintf("routing_rules[%d]", i)
		if err := checkRefs(sub(path, "channels"), r.Channels, channelKnown, "channel"); err != nil {
			return importPlan{}, err
		}
		if err := checkRefs(sub(path, "exclude"), r.Exclude, monitorKnown, "monitor"); err != nil {
			return importPlan{}, err
		}
		if err := planRule(&p, ex, path, r); err != nil {
			return importPlan{}, err
		}
	}
	for i, w := range doc.Maintenance {
		path := fmt.Sprintf("maintenance[%d]", i)
		if w.Monitor != "" && !monitorKnown[w.Monitor] {
			return importPlan{}, problemAt(sub(path, "monitor"),
				"names monitor %q, which is neither in the file nor on this instance", w.Monitor)
		}
		if err := planWindow(&p, ex, path, w); err != nil {
			return importPlan{}, err
		}
	}
	if n := len(ex.windows) + len(p.windows); len(p.windows) > 0 && n > maxMaintenanceWindows {
		return importPlan{}, problemAt("maintenance",
			"would bring this instance to %d maintenance windows; the limit is %d", n, maxMaintenanceWindows)
	}

	for _, list := range [][]importItem{p.report.Channels, p.report.Monitors, p.report.RoutingRules, p.report.Maintenance} {
		for _, it := range list {
			switch it.Action {
			case actionCreate:
				p.report.Summary.Create++
			case actionUpdate:
				p.report.Summary.Update++
			default:
				p.report.Summary.Unchanged++
			}
			if len(it.NeedsSecrets) > 0 {
				p.report.Summary.NeedsSecrets++
			}
		}
	}
	return p, nil
}

// maxMaintenanceWindows mirrors the cap CreateMaintenance enforces, so a file
// that would cross it is refused before anything is written.
const maxMaintenanceWindows = 200

// sub names a field inside an object path: sub("monitors[0]", "key").
func sub(path, field string) string { return path + "." + field }

func problemAt(path, format string, args ...any) *configfile.Problem {
	return &configfile.Problem{Path: path, Msg: fmt.Sprintf(format, args...)}
}

func checkRefs(path string, keys []string, known map[string]bool, what string) error {
	for i, k := range keys {
		if !known[k] {
			return problemAt(fmt.Sprintf("%s[%d]", path, i),
				"names %s %q, which is neither in the file nor on this instance", what, k)
		}
	}
	return nil
}

// withheldStandIn satisfies validateChannel for a required value that is
// still a placeholder. It is never stored: the channel is saved without the
// value, and switched off. The .invalid top-level domain is reserved and can
// never resolve (RFC 2606).
var withheldStandIn = map[string]string{
	"url":       "https://withheld.invalid/",
	"bot_token": "0:withheld",
	// An SMS channel's numbers, password and auth token are all withheld on
	// export, and the SMS rules require each of them.
	"numbers":    "+10000000000",
	"password":   "withheld",
	"auth_token": "withheld",
	// ntfy's topic and Gotify's application token are withheld too, and each
	// is the field its channel type cannot work without.
	"topic": "withheld",
	"token": "withheld",
}

func (s *Server) planChannel(ctx context.Context, p *importPlan, ex existingConfig, path string, c configfile.Channel) error {
	existing, found := ex.channels[c.Key]
	item := importItem{Key: c.Key, Name: c.Name, Action: actionCreate}
	step := channelStep{key: c.Key}

	next := store.Channel{Name: strings.TrimSpace(c.Name), Type: c.Type, Enabled: true}
	if found {
		step.id = existing.ID
		next = existing
		next.Config = maps.Clone(existing.Config)
		if strings.TrimSpace(c.Name) != "" {
			next.Name = strings.TrimSpace(c.Name)
		}
		if c.Type != "" {
			next.Type = c.Type
		}
	}
	if c.Enabled != nil {
		next.Enabled = *c.Enabled
	}

	// Placeholders resolve to what this instance holds for the same field.
	// Only a channel of the same type can supply one: a Slack token is not a
	// Discord token, whatever the field is called.
	var missing []string
	if c.Config != nil || !found {
		cfg := map[string]string{}
		for k, v := range c.Config {
			switch {
			case v != configfile.Placeholder:
				cfg[k] = v
			case found && existing.Type == next.Type && existing.Config[k] != "":
				cfg[k] = existing.Config[k]
			default:
				missing = append(missing, k)
			}
		}
		next.Config = cfg
	}
	slices.Sort(missing)

	req := channelRequest{Name: next.Name, Type: next.Type, Config: maps.Clone(next.Config)}
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	for _, k := range missing {
		if v, ok := withheldStandIn[k]; ok {
			req.Config[k] = v
		}
	}
	if msg := validateChannel(req); msg != "" {
		return problemAt(path, "%s", msg)
	}
	if len(missing) == 0 {
		if msg := s.checkChannelTarget(ctx, req); msg != "" {
			return problemAt(path, "%s", msg)
		}
	} else {
		next.Enabled = false
		item.NeedsSecrets = missing
	}

	if c.QuietHours != nil {
		q := store.QuietHours{ChannelID: existing.ID, Start: c.QuietHours.Start, End: c.QuietHours.End,
			Timezone: c.QuietHours.Timezone, During: c.QuietHours.During}
		if err := q.Validate(); err != nil {
			return problemAt(sub(path, "quiet_hours"), "%s", err.Error())
		}
		if cur, ok := ex.quiet[existing.ID]; !found || !ok || cur != q {
			step.quiet = &q
			item.Changes = append(item.Changes, "quiet_hours")
		}
	}
	if c.Default && (!found || !existing.IsDefault) {
		step.makeDefault = true
		item.Changes = append(item.Changes, "default")
	}

	if found {
		if next.Name != existing.Name {
			item.Changes = append(item.Changes, "name")
		}
		if next.Type != existing.Type {
			item.Changes = append(item.Changes, "type")
		}
		if next.Enabled != existing.Enabled {
			item.Changes = append(item.Changes, "enabled")
		}
		if !maps.Equal(next.Config, existing.Config) {
			item.Changes = append(item.Changes, "config")
		}
		step.write = next.Name != existing.Name || next.Type != existing.Type ||
			next.Enabled != existing.Enabled || !maps.Equal(next.Config, existing.Config)
		item.Action = actionUnchanged
		if len(item.Changes) > 0 {
			item.Action = actionUpdate
		}
		slices.Sort(item.Changes)
	} else {
		step.write, step.setKey = true, true
		item.Changes = nil
	}
	step.channel = next
	p.report.Channels = append(p.report.Channels, item)
	p.channels = append(p.channels, step)
	return nil
}

// monitorPatch turns a file entry into the PATCH request the API would accept
// for it, so an import is validated by exactly the rules a form is.
func monitorPatch(m configfile.Monitor) patchMonitorRequest {
	req := patchMonitorRequest{
		IntervalS: m.IntervalS, TimeoutS: m.TimeoutS, Retries: m.Retries,
		RecoveryThreshold: m.RecoveryThreshold, Method: m.Method,
		ExpectedStatus: m.ExpectedStatus, Keyword: m.Keyword, KeywordMode: m.KeywordMode,
		FollowRedirects: m.FollowRedirects, SSLWarnDays: m.SSLWarnDays,
		RepeatAfterS: m.RepeatAfterS, Enabled: m.Enabled, CaptureResponse: m.CaptureResponse,
		MinTLSVersion: m.MinTLSVersion, PushIntervalS: m.PushIntervalS, PushGraceS: m.PushGraceS,
		DomainWarnDays: m.DomainWarnDays,
	}
	if m.Name != "" {
		req.Name = &m.Name
	}
	if m.Type != "" {
		req.Type = &m.Type
	}
	if m.Target != "" {
		req.Target = &m.Target
	}
	if m.Tags != nil {
		tags := m.Tags
		req.Tags = &tags
	}
	req.DNS = dnsFromFile(m.DNS)
	return req
}

// dnsFromFile is a file's dns settings in the API's shape, so an import is
// checked by the same rules as a form.
func dnsFromFile(d *configfile.DNSCheck) *dnsCheckWire {
	if d == nil {
		return nil
	}
	return &dnsCheckWire{RecordType: d.RecordType, Expected: d.Expected, Resolver: d.Resolver}
}

func planMonitor(p *importPlan, ex existingConfig, path string, m configfile.Monitor) error {
	existing, found := ex.monitors[m.Key]
	item := importItem{Key: m.Key, Name: m.Name, Action: actionCreate}
	step := monitorStep{item: len(p.report.Monitors), key: m.Key, channels: m.Channels}

	var next store.Monitor
	if found {
		next = existing
		next.Headers = maps.Clone(existing.Headers)
		next.Tags = maps.Clone(existing.Tags)
		step.id = existing.ID
	} else {
		// The same checks and defaults a create request gets. An empty
		// min_tls_version means "no opinion" in a file; on create that is
		// the same as leaving the field out.
		tls := m.MinTLSVersion
		if tls != nil && *tls == "" {
			tls = nil
		}
		create := createMonitorRequest{
			Name: m.Name, Type: m.Type, Target: m.Target,
			IntervalS: deref(m.IntervalS), TimeoutS: deref(m.TimeoutS), Retries: m.Retries,
			RecoveryThreshold: m.RecoveryThreshold, Method: deref(m.Method),
			ExpectedStatus: deref(m.ExpectedStatus), Keyword: deref(m.Keyword),
			KeywordMode: deref(m.KeywordMode), FollowRedirects: m.FollowRedirects,
			SSLWarnDays: m.SSLWarnDays, RepeatAfterS: m.RepeatAfterS, Enabled: m.Enabled,
			CaptureResponse: m.CaptureResponse, MinTLSVersion: tls, Tags: m.Tags,
			PushIntervalS: m.PushIntervalS, PushGraceS: m.PushGraceS,
			DNS: dnsFromFile(m.DNS), DomainWarnDays: m.DomainWarnDays,
		}
		if a, set, err := m.Assertion(sub(path, "json_assertion")); err != nil {
			return err
		} else if set {
			create.JSONAssertion = assertionWire(a)
		}
		if pr := validateCreateMonitor(create); !pr.ok() {
			return monitorProblem(path, pr)
		}
		next = store.Monitor{
			Name: m.Name, Type: m.Type, Target: m.Target, Retries: 2,
			FollowRedirects: true, Enabled: true, CaptureResponse: true,
			RepeatAfterS: defaultRepeatAfterS,
		}
		if m.Type == store.TypePush {
			next.PushGraceS = store.DefaultPushGraceS
		}
		if m.Type == store.TypeDomain {
			// As on create: the default threshold, and a date read off the
			// registry confirms at once.
			next.DomainWarnDays = checker.DefaultDomainWarnDays
			if m.Retries == nil {
				next.Retries = 0
			}
		}
		store.ApplyMonitorDefaults(&next)
	}
	patch := monitorPatch(m)
	a, set, err := m.Assertion(sub(path, "json_assertion"))
	if err != nil {
		return err
	}
	if set {
		raw, err := json.Marshal(assertionWire(a))
		if err != nil {
			return err
		}
		patch.JSONAssertion = raw
	}
	if pr := applyMonitorPatch(&next, patch); !pr.ok() {
		return monitorProblem(path, pr)
	}

	// Headers and body are withheld on export. A placeholder keeps what the
	// monitor already sends; one with nothing to keep is dropped rather
	// than sent literally, and the monitor is paused until it is filled in.
	var missing []string
	if m.Headers != nil {
		headers := map[string]string{}
		for k, v := range m.Headers {
			switch {
			case v != configfile.Placeholder:
				headers[k] = v
			case found && existing.Headers[k] != "":
				headers[k] = existing.Headers[k]
			default:
				missing = append(missing, sub("headers", k))
			}
		}
		next.Headers = headers
	}
	if m.Body != nil {
		switch {
		case *m.Body != configfile.Placeholder:
			next.Body = *m.Body
		case found && existing.Body != "":
			next.Body = existing.Body
		default:
			next.Body = ""
			missing = append(missing, "body")
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		next.Enabled = false
		item.NeedsSecrets = missing
	}

	if found {
		item.Action = actionUnchanged
		item.Changes = monitorChanges(existing, next)
		step.write = len(item.Changes) > 0
		if m.Channels != nil {
			if sameChannels(ex, existing.ID, m.Channels) {
				step.channels = nil
			} else {
				item.Changes = append(item.Changes, "channels")
			}
		}
		if len(item.Changes) > 0 {
			item.Action = actionUpdate
		}
	} else {
		step.write, step.setKey = true, true
	}
	step.monitor = next
	p.report.Monitors = append(p.report.Monitors, item)
	p.monitors = append(p.monitors, step)
	return nil
}

// monitorProblem places an API validation problem inside the file.
func monitorProblem(path string, pr problem) *configfile.Problem {
	if pr.field != "" {
		path = sub(path, pr.field)
	}
	return &configfile.Problem{Path: path, Msg: pr.msg}
}

// sameChannels reports whether the monitor's current assignments are exactly
// the channels the file names. A key that is not on the instance yet belongs
// to a channel this import creates, so it is always a change.
func sameChannels(ex existingConfig, monitorID int64, keys []string) bool {
	return sameIDs(ex.channelIDs(), ex.attached[monitorID], keys)
}

func (ex existingConfig) channelIDs() map[string]int64 {
	out := make(map[string]int64, len(ex.channels))
	for k, c := range ex.channels {
		out[k] = c.ID
	}
	return out
}

func (ex existingConfig) monitorIDs() map[string]int64 {
	out := make(map[string]int64, len(ex.monitors))
	for k, m := range ex.monitors {
		out[k] = m.ID
	}
	return out
}

// sameIDs compares a stored id set with the keys a file names.
func sameIDs(known map[string]int64, have []int64, keys []string) bool {
	want := map[int64]bool{}
	for _, k := range keys {
		id, ok := known[k]
		if !ok {
			return false
		}
		want[id] = true
	}
	got := map[int64]bool{}
	for _, id := range have {
		got[id] = true
	}
	return maps.Equal(want, got)
}

// monitorChanges lists the fields that differ, by their name in the file.
// Fields a file never carries (ids, timestamps, the push token) are skipped.
func monitorChanges(before, after store.Monitor) []string {
	a, b := exportMonitor(before), exportMonitor(after)
	var out []string
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		switch name {
		case "key", "channels", "headers", "body":
			continue
		}
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, name)
		}
	}
	// Export withholds these values, so compare what is stored instead.
	if !maps.Equal(before.Headers, after.Headers) {
		out = append(out, "headers")
	}
	if before.Body != after.Body {
		out = append(out, "body")
	}
	slices.Sort(out)
	return out
}

func planRule(p *importPlan, ex existingConfig, path string, r configfile.RoutingRule) error {
	key, value, err := store.NormaliseRoutingTag(r.TagKey, r.TagValue)
	if err != nil {
		return problemAt(sub(path, "tag_key"), "%s", err.Error())
	}
	for _, planned := range p.rules {
		if planned.rule.TagKey == key && planned.rule.TagValue == value {
			return problemAt(path, "the tag pair %s=%s is listed twice", key, value)
		}
	}
	item := importItem{Name: key + "=" + value, Action: actionCreate}
	step := ruleStep{rule: store.RoutingRule{TagKey: key, TagValue: value},
		channels: r.Channels, exclude: r.Exclude}

	existing, found := ex.rules[key+"="+value]
	if !found {
		step.write = true
		if step.channels == nil {
			step.channels = []string{}
		}
	} else {
		item.Action = actionUnchanged
		step.id = existing.ID
		step.excluded = existing.ExcludedMonitorIDs
		if r.Channels != nil {
			if sameIDs(ex.channelIDs(), existing.ChannelIDs, r.Channels) {
				step.channels = nil
			} else {
				item.Changes = append(item.Changes, "channels")
				step.write = true
			}
		}
		if r.Exclude != nil {
			if sameIDs(ex.monitorIDs(), existing.ExcludedMonitorIDs, r.Exclude) {
				step.exclude = nil
			} else {
				item.Changes = append(item.Changes, "exclude")
			}
		}
		if len(item.Changes) > 0 {
			item.Action = actionUpdate
		}
	}
	p.report.RoutingRules = append(p.report.RoutingRules, item)
	p.rules = append(p.rules, step)
	return nil
}

func planWindow(p *importPlan, ex existingConfig, path string, w configfile.Maintenance) error {
	win := store.MaintenanceWindow{
		Name: w.Name, TagKey: w.TagKey, TagValue: w.TagValue, Timezone: w.Timezone,
		Weekdays: w.Weekdays, LocalTime: w.LocalTime, DurationMinutes: w.DurationMinutes,
	}
	if w.StartsAt != nil {
		win.StartsAt = w.StartsAt.UTC()
	}
	if w.EndsAt != nil {
		win.EndsAt = w.EndsAt.UTC()
	}
	target, onInstance := ex.monitors[w.Monitor]
	check := win
	if w.Monitor != "" {
		// Validate needs an id to know the window targets a monitor; a
		// monitor this import creates does not have one yet.
		check.MonitorID = 1
		if onInstance {
			win.MonitorID = target.ID
		}
	}
	if err := check.Validate(); err != nil {
		return problemAt(path, "%s", err.Error())
	}

	item := importItem{Name: w.Name, Action: actionCreate}
	if w.Monitor == "" || onInstance {
		for _, cur := range ex.windows {
			if sameWindow(cur, win) {
				item.Action = actionUnchanged
				break
			}
		}
	}
	p.report.Maintenance = append(p.report.Maintenance, item)
	if item.Action == actionCreate {
		p.windows = append(p.windows, windowStep{window: win, monitor: w.Monitor})
	}
	return nil
}

func sameWindow(a, b store.MaintenanceWindow) bool {
	wa, wb := slices.Clone(a.Weekdays), slices.Clone(b.Weekdays)
	slices.Sort(wa)
	slices.Sort(wb)
	return a.Name == b.Name && a.MonitorID == b.MonitorID && a.TagKey == b.TagKey &&
		a.TagValue == b.TagValue && a.StartsAt.Equal(b.StartsAt) && a.EndsAt.Equal(b.EndsAt) &&
		a.Timezone == b.Timezone && slices.Equal(wa, wb) && a.LocalTime == b.LocalTime &&
		a.DurationMinutes == b.DurationMinutes
}

// assertionWire converts a file's assertion into the API's request shape, so
// it is validated by the same code a form submission is.
func assertionWire(a *configfile.JSONAssertion) *jsonAssertionWire {
	if a == nil {
		return nil
	}
	w := &jsonAssertionWire{Path: a.Path, Operator: a.Operator}
	if a.Expected != "" {
		w.Expected = json.RawMessage(a.Expected)
	}
	return w
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// errImportIncomplete wraps a write that failed after the plan was accepted.
var errImportIncomplete = errors.New("import stopped part way")

// applyImport performs a plan. Channels go first so monitors can be attached
// to them, then monitors, then the rules and windows that refer to both.
//
// The writes are not one transaction; each goes through the same store call
// the API uses, so every invariant those calls keep still holds. The plan has
// already validated every object, so what can still fail here is the database
// itself. If it does, the error says so, and running the same file again
// finishes the job: matching is by key, so what was already written is found
// and left alone.
func (s *Server) applyImport(ctx context.Context, p *importPlan, pushURL func(token string) string) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w; importing the same file again completes it: %w", errImportIncomplete, err)
		}
	}()

	channelIDs, err := s.keyIDs(ctx, s.db.ChannelConfigKeys)
	if err != nil {
		return err
	}
	for _, st := range p.channels {
		id, err := s.applyChannel(ctx, st)
		if err != nil {
			return err
		}
		channelIDs[st.key] = id
	}

	monitorIDs, err := s.keyIDs(ctx, s.db.MonitorConfigKeys)
	if err != nil {
		return err
	}
	for _, st := range p.monitors {
		id := st.id
		if st.write && id == 0 {
			created, err := s.db.CreateMonitor(ctx, st.monitor)
			if err != nil {
				return err
			}
			id = created.ID
			if created.PushToken != "" {
				p.report.Monitors[st.item].PushURL = pushURL(created.PushToken)
			}
		} else if st.write {
			st.monitor.ID = id
			if _, err := s.db.UpdateMonitor(ctx, st.monitor); err != nil {
				return err
			}
		}
		if st.setKey {
			if err := s.db.SetMonitorConfigKey(ctx, id, st.key); err != nil {
				if st.id == 0 {
					// Without its key the next import would not find this
					// monitor and would create it a second time.
					err = errors.Join(err, s.db.DeleteMonitor(ctx, id))
				}
				return err
			}
		}
		monitorIDs[st.key] = id
		if st.channels != nil {
			ids := make([]int64, 0, len(st.channels))
			for _, k := range st.channels {
				ids = append(ids, channelIDs[k])
			}
			if err := s.db.SetMonitorChannels(ctx, id, ids); err != nil {
				return err
			}
		}
	}

	for _, st := range p.rules {
		if err := s.applyRule(ctx, st, channelIDs, monitorIDs); err != nil {
			return err
		}
	}

	for _, st := range p.windows {
		w := st.window
		if st.monitor != "" {
			w.MonitorID = monitorIDs[st.monitor]
		}
		if _, err := s.db.CreateMaintenance(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyChannel(ctx context.Context, st channelStep) (int64, error) {
	id := st.id
	switch {
	case st.write && id == 0:
		created, err := s.db.CreateChannel(ctx, st.channel)
		if err != nil {
			return 0, err
		}
		id = created.ID
	case st.write:
		st.channel.ID = id
		if _, err := s.db.UpdateChannel(ctx, st.channel); err != nil {
			return 0, err
		}
	}
	if st.setKey {
		if err := s.db.SetChannelConfigKey(ctx, id, st.key); err != nil {
			if st.id == 0 {
				// As for monitors: a created channel without its key would
				// be created again by the next import.
				err = errors.Join(err, s.db.DeleteChannel(ctx, id))
			}
			return 0, err
		}
	}
	if st.quiet != nil {
		q := *st.quiet
		q.ChannelID = id
		if err := s.db.SetQuietHours(ctx, q); err != nil {
			return 0, err
		}
	}
	if st.makeDefault {
		if err := s.db.SetDefaultChannel(ctx, id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (s *Server) applyRule(ctx context.Context, st ruleStep, channelIDs, monitorIDs map[string]int64) error {
	rule := st.rule
	for _, k := range st.channels {
		rule.ChannelIDs = append(rule.ChannelIDs, channelIDs[k])
	}
	switch {
	case st.id == 0:
		created, err := s.db.CreateRoutingRule(ctx, rule)
		if err != nil {
			return err
		}
		st.id = created.ID
	case st.write:
		rule.ID = st.id
		if _, err := s.db.UpdateRoutingRule(ctx, rule); err != nil {
			return err
		}
	}
	if st.exclude == nil {
		return nil
	}
	want := map[int64]bool{}
	for _, k := range st.exclude {
		want[monitorIDs[k]] = true
	}
	for id := range want {
		if !slices.Contains(st.excluded, id) {
			if err := s.db.ExcludeMonitorFromRule(ctx, st.id, id); err != nil {
				return err
			}
		}
	}
	for _, id := range st.excluded {
		if !want[id] {
			if err := s.db.IncludeMonitorInRule(ctx, st.id, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// keyIDs inverts an id-to-key map.
func (s *Server) keyIDs(ctx context.Context, list func(context.Context) (map[int64]string, error)) (map[string]int64, error) {
	keys, err := list(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(keys))
	for id, k := range keys {
		out[k] = id
	}
	return out, nil
}
