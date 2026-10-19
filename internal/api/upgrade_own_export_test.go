package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Settings a release before the target, resolver and channel sender rules
// accepted, and every save now refuses. An instance upgraded with them has to
// keep working: its objects can be renamed and switched off, and its own
// export imports back unchanged. The values are seeded through the store, as
// the release that accepted them left them in the database.

func seedPreRuleMonitors(t *testing.T, db *store.DB) (portZero, badHost, badResolver store.Monitor) {
	t.Helper()
	portZero = seedMonitor(t, db, store.Monitor{Name: "Port zero", Type: "http", Target: "https://example.com:0/"})
	badHost = seedMonitor(t, db, store.Monitor{Name: "Bad host", Type: "tcp", Target: "exa..mple.com:443"})
	badResolver = seedMonitor(t, db, store.Monitor{Name: "Bad resolver", Type: store.TypeDNS, Target: "example.com",
		DNS: &store.DNSCheck{RecordType: "A", Resolver: "reso..lver.lan:53"}})
	return portZero, badHost, badResolver
}

func seedPreRuleChannels(t *testing.T, db *store.DB) (email, hook store.Channel) {
	t.Helper()
	email = seedChannel(t, db, store.ChannelEmail, map[string]string{
		"host": "smtp.example.com", "port": "99999", "from": "sg@example.com", "to": "ops@example.com",
		"password": "relay-secret"})
	hook = seedChannel(t, db, store.ChannelWebhook, map[string]string{"url": "https://hooks.example.com:0/in"})
	return email, hook
}

func TestAMonitorSavedBeforeATargetRuleKeepsItsTarget(t *testing.T) {
	srv, db := testServerWithDB(t)
	portZero, badHost, _ := seedPreRuleMonitors(t, db)
	path := func(m store.Monitor) string { return "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) }

	// What an edit or an import sends: the stored type and target back.
	for _, m := range []store.Monitor{portZero, badHost} {
		body, err := json.Marshal(map[string]any{"name": m.Name + " renamed", "type": m.Type, "target": m.Target})
		must(t, err)
		mustStatus(t, patch(t, srv, path(m), string(body)), http.StatusOK, "send "+m.Target+" back")
	}

	// A changed target is held to every rule, and so is a changed type.
	rec := patch(t, srv, path(portZero), `{"target":"https://example.com:99999/"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "target", "port 99999 is out of range")
	rec = patch(t, srv, path(badHost), `{"type":"ssl"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "target", "empty label")
	got, err := db.GetMonitor(t.Context(), badHost.ID)
	must(t, err)
	if got.Type != "tcp" || got.Target != badHost.Target {
		t.Errorf("a refused patch left %s %q", got.Type, got.Target)
	}
}

func TestADNSMonitorSavedBeforeTheResolverRuleKeepsItsResolver(t *testing.T) {
	srv, db := testServerWithDB(t)
	_, _, m := seedPreRuleMonitors(t, db)
	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10)

	// The resolver sent back as stored, with another record type: the
	// edit is taken and the resolver kept.
	mustStatus(t, patch(t, srv, path, `{"dns":{"record_type":"AAAA","expected":[],"resolver":"reso..lver.lan:53"}}`),
		http.StatusOK, "send the stored resolver back")
	got, err := db.GetMonitor(t.Context(), m.ID)
	must(t, err)
	if got.DNS == nil || got.DNS.RecordType != "AAAA" || got.DNS.Resolver != "reso..lver.lan:53" {
		t.Fatalf("dns after the edit = %+v", got.DNS)
	}

	// The other settings are still judged beside it, and a changed
	// resolver is judged in full.
	rec := patch(t, srv, path, `{"dns":{"record_type":"NOPE","expected":[],"resolver":"reso..lver.lan:53"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "dns.record_type", "dns.record_type must be one of")
	rec = patch(t, srv, path, `{"dns":{"record_type":"A","expected":[],"resolver":"other..lan:53"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "dns.resolver", "empty label")
}

func TestAChannelSavedBeforeASenderRuleCanBeRenamedAndSwitchedOff(t *testing.T) {
	srv, db := testServerWithDB(t)
	email, hook := seedPreRuleChannels(t, db)

	// The Notifications screen's Disable: the stored name, type and the
	// config as it was shown, masked, with enabled false.
	for _, ch := range []store.Channel{email, hook} {
		body, err := json.Marshal(map[string]any{"name": "Renamed", "type": ch.Type,
			"config": maskConfig(ch.Type, ch.Config, true), "enabled": false})
		must(t, err)
		rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(ch.ID, 10), string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("switch off %s = %d: %s", ch.Type, rec.Code, rec.Body.String())
		}
		got, err := db.GetChannel(t.Context(), ch.ID)
		must(t, err)
		if got.Enabled || got.Name != "Renamed" || len(got.Config) != len(ch.Config) {
			t.Errorf("%s after switching off: enabled %v, name %q, config %v", ch.Type, got.Enabled, got.Name, got.Config)
		}
		for k, v := range ch.Config {
			if got.Config[k] != v {
				t.Errorf("%s config.%s = %q, want the stored %q", ch.Type, k, got.Config[k], v)
			}
		}
	}

	// Any changed setting holds the channel to every rule, including the
	// one the stored setting breaks.
	changed := maskConfig(store.ChannelEmail, email.Config, true)
	changed["to"] = "oncall@example.com"
	rec := putChannel(t, srv, email.ID, store.ChannelEmail, changed)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.port", "config.port must be a number between 1 and 65535")
	rec = putChannel(t, srv, hook.ID, store.ChannelWebhook, map[string]string{"url": "https://hooks.example.com:0/in", "method": "PUT"})
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.url", "port 0 is out of range")
}

func TestAnUpgradedInstanceImportsItsOwnExport(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedPreRuleMonitors(t, db)
	seedPreRuleChannels(t, db)
	doc := exportYAML(t, srv)

	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import of its own export = %d: %.400s", code, body)
	}
	for _, item := range append(rep.Monitors, rep.Channels...) {
		if item.Action != actionUnchanged {
			t.Errorf("%s: action %s, changes %v, want unchanged", item.Key, item.Action, item.Changes)
		}
	}
	if len(rep.Monitors) != 3 || len(rep.Channels) != 2 {
		t.Errorf("report holds %d monitors and %d channels, want 3 and 2", len(rep.Monitors), len(rep.Channels))
	}

	// Into another instance there is nothing to keep: each setting is a
	// new one, and refused at its own path.
	for _, tc := range []struct{ name, keep, field string }{
		{"port zero", "port-zero", "monitors[0].target"},
		{"bad host", "bad-host", "monitors[0].target"},
		{"bad resolver", "bad-resolver", "monitors[0].dns.resolver"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh, _ := testServerWithDB(t)
			one := onlyMonitor(t, doc, tc.keep)
			code, _, body := importYAML(t, fresh, one, true)
			if code != http.StatusBadRequest || !strings.Contains(body, `"field":"`+tc.field+`"`) {
				t.Errorf("import into a fresh instance = %d %s, want 400 at %s", code, body, tc.field)
			}
		})
	}
	fresh, _ := testServerWithDB(t)
	code, _, body = importYAML(t, fresh, doc, true)
	if code != http.StatusBadRequest || !strings.Contains(body, `"field":"channels[`) {
		t.Errorf("the whole file into a fresh instance = %d %s, want a channel refusal first", code, body)
	}
}

// onlyMonitor cuts an exported file down to its version line and the one
// monitor with the given key, without its channel list.
func onlyMonitor(t *testing.T, doc, key string) string {
	t.Helper()
	var out strings.Builder
	out.WriteString("version: 1\nmonitors:\n")
	keep := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "  - ") || !strings.HasPrefix(line, " ") {
			keep = line == "  - key: "+key
		}
		if keep && line != "    channels: []" {
			out.WriteString(line + "\n")
		}
	}
	if !strings.Contains(out.String(), "key: "+key) {
		t.Fatalf("no monitor %q in the export:\n%s", key, doc)
	}
	return out.String()
}
