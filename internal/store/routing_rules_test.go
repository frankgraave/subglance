package store

import (
	"errors"
	"slices"
	"testing"
)

func seedTaggedMonitor(t *testing.T, db *DB, name string, tags map[string]string) Monitor {
	t.Helper()
	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: name, Type: "http", Target: "https://example.com/" + name, Enabled: true, Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

func seedRule(t *testing.T, db *DB, key, value string, channels ...int64) RoutingRule {
	t.Helper()
	r, err := db.CreateRoutingRule(t.Context(), RoutingRule{TagKey: key, TagValue: value, ChannelIDs: channels})
	if err != nil {
		t.Fatalf("CreateRoutingRule: %v", err)
	}
	return r
}

func alertNames(t *testing.T, db *DB, monitorID int64) ([]string, bool) {
	t.Helper()
	got, usedDefault, err := db.AlertChannels(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("AlertChannels: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	return names, usedDefault
}

// Rules add up: every matching rule contributes, and the monitor's own
// channels stay. First-match-wins would drop "team" here.
func TestAlertChannelsIsTheUnionOfOwnChannelsAndEveryMatchingRule(t *testing.T) {
	db := openTestDB(t)
	own := seedChannel(t, db, "own")
	env := seedChannel(t, db, "env")
	team := seedChannel(t, db, "team")
	other := seedChannel(t, db, "other")
	def := seedChannel(t, db, "default")
	if err := db.SetDefaultChannel(t.Context(), def.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	m := seedTaggedMonitor(t, db, "api", map[string]string{"env": "prod", "team": "core"})
	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{own.ID}); err != nil {
		t.Fatalf("SetMonitorChannels: %v", err)
	}
	seedRule(t, db, "env", "prod", env.ID)
	seedRule(t, db, "team", "core", team.ID, own.ID)
	seedRule(t, db, "env", "staging", other.ID)

	names, usedDefault := alertNames(t, db, m.ID)
	if want := []string{"own", "env", "team"}; !slices.Equal(names, want) || usedDefault {
		t.Fatalf("channels = %v (default %v), want %v without the default; a channel reached twice is listed once", names, usedDefault, want)
	}
}

// The default is the floor: it applies only when the union is empty, and a
// rule match alone is enough to keep it out.
func TestTheDefaultAppliesOnlyWhenNoRuleOrOwnChannelMatches(t *testing.T) {
	db := openTestDB(t)
	def := seedChannel(t, db, "default")
	env := seedChannel(t, db, "env")
	if err := db.SetDefaultChannel(t.Context(), def.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	matched := seedTaggedMonitor(t, db, "matched", map[string]string{"env": "prod"})
	unmatched := seedTaggedMonitor(t, db, "unmatched", map[string]string{"env": "dev"})
	seedRule(t, db, "env", "prod", env.ID)

	if names, usedDefault := alertNames(t, db, matched.ID); !slices.Equal(names, []string{"env"}) || usedDefault {
		t.Fatalf("matched: %v (default %v), want [env] without the default", names, usedDefault)
	}
	if names, usedDefault := alertNames(t, db, unmatched.ID); !slices.Equal(names, []string{"default"}) || !usedDefault {
		t.Fatalf("unmatched: %v (default %v), want [default] via the default", names, usedDefault)
	}
}

// An exclusion removes exactly one rule's channels for exactly one monitor.
// The same channel reached through the monitor's own list or another rule
// stays, and other monitors on the rule keep it.
func TestAnExclusionRemovesOneRulesChannelsForOneMonitorOnly(t *testing.T) {
	db := openTestDB(t)
	own := seedChannel(t, db, "own")
	pager := seedChannel(t, db, "pager")
	chat := seedChannel(t, db, "chat")
	shared := seedChannel(t, db, "shared")
	m := seedTaggedMonitor(t, db, "noisy", map[string]string{"env": "prod", "team": "core"})
	peer := seedTaggedMonitor(t, db, "peer", map[string]string{"env": "prod"})
	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{own.ID}); err != nil {
		t.Fatalf("SetMonitorChannels: %v", err)
	}
	prod := seedRule(t, db, "env", "prod", pager.ID, shared.ID)
	seedRule(t, db, "team", "core", chat.ID, shared.ID)

	if err := db.ExcludeMonitorFromRule(t.Context(), prod.ID, m.ID); err != nil {
		t.Fatalf("ExcludeMonitorFromRule: %v", err)
	}
	if names, _ := alertNames(t, db, m.ID); !slices.Equal(names, []string{"own", "chat", "shared"}) {
		t.Fatalf("excluded monitor: %v, want [own chat shared]: only pager, reached through the excluded rule alone, goes", names)
	}
	if names, _ := alertNames(t, db, peer.ID); !slices.Equal(names, []string{"pager", "shared"}) {
		t.Fatalf("peer: %v, want [pager shared]: the exclusion is per monitor", names)
	}

	if err := db.IncludeMonitorInRule(t.Context(), prod.ID, m.ID); err != nil {
		t.Fatalf("IncludeMonitorInRule: %v", err)
	}
	if names, _ := alertNames(t, db, m.ID); !slices.Equal(names, []string{"own", "pager", "chat", "shared"}) {
		t.Fatalf("after including again: %v", names)
	}
	if err := db.IncludeMonitorInRule(t.Context(), prod.ID, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("including a monitor that is not excluded: err = %v, want ErrNotFound", err)
	}
}

// A monitor excluded from its only rule falls back to the default rather than
// to nobody: muting a rule must not take a monitor off the floor.
func TestAMonitorExcludedFromItsOnlyRuleFallsBackToTheDefault(t *testing.T) {
	db := openTestDB(t)
	def := seedChannel(t, db, "default")
	env := seedChannel(t, db, "env")
	if err := db.SetDefaultChannel(t.Context(), def.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	m := seedTaggedMonitor(t, db, "api", map[string]string{"env": "prod"})
	r := seedRule(t, db, "env", "prod", env.ID)
	if err := db.ExcludeMonitorFromRule(t.Context(), r.ID, m.ID); err != nil {
		t.Fatalf("ExcludeMonitorFromRule: %v", err)
	}
	if names, usedDefault := alertNames(t, db, m.ID); !slices.Equal(names, []string{"default"}) || !usedDefault {
		t.Fatalf("channels = %v (default %v), want the default", names, usedDefault)
	}
}

func TestRoutingRuleCRUD(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	b := seedChannel(t, db, "b")
	m := seedTaggedMonitor(t, db, "api", nil)

	r := seedRule(t, db, "env", "prod", b.ID, a.ID, a.ID)
	if !slices.Equal(r.ChannelIDs, []int64{a.ID, b.ID}) {
		t.Fatalf("channels = %v, want sorted and deduplicated", r.ChannelIDs)
	}
	if _, err := db.CreateRoutingRule(t.Context(), RoutingRule{TagKey: "env", TagValue: "prod"}); !errors.Is(err, ErrRoutingRuleExists) {
		t.Fatalf("duplicate tag pair: err = %v, want ErrRoutingRuleExists", err)
	}
	if _, err := db.CreateRoutingRule(t.Context(), RoutingRule{TagKey: "env", TagValue: "dev", ChannelIDs: []int64{4242}}); !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("unknown channel: err = %v, want ErrUnknownChannel", err)
	}
	if rules, err := db.ListRoutingRules(t.Context()); err != nil || len(rules) != 1 {
		t.Fatalf("a refused create must store nothing: rules = %+v, err = %v", rules, err)
	}

	if err := db.ExcludeMonitorFromRule(t.Context(), r.ID, m.ID); err != nil {
		t.Fatalf("ExcludeMonitorFromRule: %v", err)
	}
	if err := db.ExcludeMonitorFromRule(t.Context(), r.ID, m.ID); err != nil {
		t.Fatalf("excluding twice must be a no-op, got %v", err)
	}
	if err := db.ExcludeMonitorFromRule(t.Context(), r.ID, 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown monitor: err = %v, want ErrNotFound", err)
	}

	// Updating the tag and channels keeps the exclusion; a failed update
	// changes nothing.
	up, err := db.UpdateRoutingRule(t.Context(), RoutingRule{ID: r.ID, TagKey: "env", TagValue: "live", ChannelIDs: []int64{b.ID}})
	if err != nil {
		t.Fatalf("UpdateRoutingRule: %v", err)
	}
	if up.TagValue != "live" || !slices.Equal(up.ChannelIDs, []int64{b.ID}) || !slices.Equal(up.ExcludedMonitorIDs, []int64{m.ID}) {
		t.Fatalf("updated = %+v", up)
	}
	if _, err := db.UpdateRoutingRule(t.Context(), RoutingRule{ID: r.ID, TagKey: "env", TagValue: "x", ChannelIDs: []int64{4242}}); !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("update with unknown channel: err = %v", err)
	}
	if got, err := db.GetRoutingRule(t.Context(), r.ID); err != nil || got.TagValue != "live" {
		t.Fatalf("a failed update must leave the rule alone: %+v, %v", got, err)
	}
	if _, err := db.UpdateRoutingRule(t.Context(), RoutingRule{ID: 4242, TagKey: "a", TagValue: "b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of unknown rule: err = %v", err)
	}

	// Deleting a channel empties it out of the rule; deleting the rule takes
	// its exclusions with it.
	if err := db.DeleteChannel(t.Context(), b.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if got, _ := db.GetRoutingRule(t.Context(), r.ID); len(got.ChannelIDs) != 0 {
		t.Fatalf("channels after the channel was deleted = %v", got.ChannelIDs)
	}
	if err := db.DeleteRoutingRule(t.Context(), r.ID); err != nil {
		t.Fatalf("DeleteRoutingRule: %v", err)
	}
	if err := db.DeleteRoutingRule(t.Context(), r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
	var n int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM routing_rule_exclusions").Scan(&n); err != nil || n != 0 {
		t.Fatalf("exclusions left after the rule was deleted: %d (%v)", n, err)
	}
}

func TestNormaliseRoutingTagFollowsTheMonitorTagRules(t *testing.T) {
	k, v, err := NormaliseRoutingTag("  Env ", " Prod ")
	if err != nil || k != "env" || v != "Prod" {
		t.Fatalf("got %q=%q, %v; want env=Prod", k, v, err)
	}
	for _, bad := range [][2]string{{"", "x"}, {"env", ""}, {"has space", "x"}} {
		if _, _, err := NormaliseRoutingTag(bad[0], bad[1]); err == nil {
			t.Errorf("NormaliseRoutingTag(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

// The coverage read groups channels per rule and leaves out the rules a
// monitor is excluded from, so the page can name the rule behind each route.
func TestMonitorRuleRoutesNamesTheRuleBehindEachChannel(t *testing.T) {
	db := openTestDB(t)
	pager := seedChannel(t, db, "pager")
	chat := seedChannel(t, db, "chat")
	m := seedTaggedMonitor(t, db, "api", map[string]string{"env": "prod", "team": "core"})
	peer := seedTaggedMonitor(t, db, "peer", map[string]string{"env": "prod"})
	prod := seedRule(t, db, "env", "prod", pager.ID, chat.ID)
	core := seedRule(t, db, "team", "core", chat.ID)
	seedRule(t, db, "team", "empty")
	if err := db.ExcludeMonitorFromRule(t.Context(), prod.ID, peer.ID); err != nil {
		t.Fatalf("ExcludeMonitorFromRule: %v", err)
	}

	got, err := db.MonitorRuleRoutes(t.Context())
	if err != nil {
		t.Fatalf("MonitorRuleRoutes: %v", err)
	}
	routes := got[m.ID]
	if len(routes) != 2 || routes[0].RuleID != prod.ID || routes[1].RuleID != core.ID {
		t.Fatalf("routes for api = %+v, want prod then core", routes)
	}
	if len(routes[0].Channels) != 2 || routes[0].Channels[0].Name != "pager" || routes[0].Channels[1].Name != "chat" {
		t.Fatalf("prod channels = %+v", routes[0].Channels)
	}
	if _, ok := got[peer.ID]; ok {
		t.Fatalf("peer is excluded from its only rule but has routes %+v", got[peer.ID])
	}
}
