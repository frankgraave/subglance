package store

import (
	"errors"
	"testing"
	"time"
)

// renameEstate is one monitor tagged env=prod and every kind of configuration
// that names that pair: a routing rule, a one-off maintenance window and a tag
// status page. Bystanders on other pairs show that a rename moves only what
// named the old one.
type renameEstate struct {
	monitor Monitor
	rule    RoutingRule
	other   RoutingRule
	window  MaintenanceWindow
	single  MaintenanceWindow
	page    StatusPage
	bystand StatusPage
}

func seedRenameEstate(t *testing.T, db *DB) renameEstate {
	t.Helper()
	ctx := t.Context()
	var e renameEstate
	var err error
	if e.monitor, err = db.CreateMonitor(ctx, Monitor{Name: "api", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}}); err != nil {
		t.Fatal(err)
	}
	if e.rule, err = db.CreateRoutingRule(ctx, RoutingRule{TagKey: "env", TagValue: "prod"}); err != nil {
		t.Fatal(err)
	}
	if e.other, err = db.CreateRoutingRule(ctx, RoutingRule{TagKey: "team", TagValue: "ops"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if e.window, err = db.CreateMaintenance(ctx, MaintenanceWindow{Name: "deploy", TagKey: "env", TagValue: "prod", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if e.single, err = db.CreateMaintenance(ctx, MaintenanceWindow{Name: "one", MonitorID: e.monitor.ID, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if e.page, err = db.CreateStatusPage(ctx, StatusPage{Slug: "prod", Title: "Prod", Selection: StatusPageSelectTag, TagKey: "env", TagValue: "prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetStatusPageEntries(ctx, e.page.ID, []StatusPageEntryInput{{MonitorID: e.monitor.ID, DisplayName: "API"}}); err != nil {
		t.Fatal(err)
	}
	if e.page, err = db.GetStatusPage(ctx, e.page.ID); err != nil {
		t.Fatal(err)
	}
	if e.bystand, err = db.CreateStatusPage(ctx, StatusPage{Slug: "staging", Title: "Staging", Selection: StatusPageSelectTag, TagKey: "env", TagValue: "staging"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ExcludeMonitorFromRule(ctx, e.rule.ID, e.monitor.ID); err != nil {
		t.Fatal(err)
	}
	return e
}

func renameTags(t *testing.T, db *DB, op TagOperation) TagOperationResult {
	t.Helper()
	preview, etag, err := db.TransformTags(t.Context(), op, "", true)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := db.TransformTags(t.Context(), op, etag, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview != result {
		t.Fatalf("preview %+v promised something other than the commit %+v", preview, result)
	}
	return result
}

func TestTagRenameCarriesRulesWindowsAndPages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		op         TagOperation
		key, value string
		bystander  string // the staging page after the rename
	}{
		{"value", TagOperation{Action: "rename_value", Key: "env", Value: "prod", NewValue: "production"}, "env", "production", "env=staging"},
		{"key", TagOperation{Action: "rename_key", Key: "env", NewKey: "stage"}, "stage", "prod", "stage=staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := t.Context()
			e := seedRenameEstate(t, db)
			result := renameTags(t, db, tc.op)
			pages := 1
			if tc.op.Action == "rename_key" {
				pages = 2 // A key rename moves every value under the key.
			}
			if result.RoutingRules != 1 || result.MaintenanceWindows != 1 || result.StatusPages != pages {
				t.Fatalf("followers = rules %d, windows %d, pages %d; want 1, 1, %d",
					result.RoutingRules, result.MaintenanceWindows, result.StatusPages, pages)
			}

			rule, err := db.GetRoutingRule(ctx, e.rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if rule.TagKey != tc.key || rule.TagValue != tc.value {
				t.Fatalf("rule = %s=%s; want %s=%s", rule.TagKey, rule.TagValue, tc.key, tc.value)
			}
			if len(rule.ExcludedMonitorIDs) != 1 || rule.ExcludedMonitorIDs[0] != e.monitor.ID {
				t.Fatalf("rename dropped the rule's exclusion: %v", rule.ExcludedMonitorIDs)
			}
			if other, _ := db.GetRoutingRule(ctx, e.other.ID); other.TagKey != "team" || other.TagValue != "ops" {
				t.Fatalf("unrelated rule moved: %s=%s", other.TagKey, other.TagValue)
			}

			windows, err := db.ListMaintenance(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range windows {
				switch w.ID {
				case e.window.ID:
					if w.TagKey != tc.key || w.TagValue != tc.value || w.Name != "deploy" || !w.StartsAt.Equal(e.window.StartsAt) || !w.CreatedAt.Equal(e.window.CreatedAt) {
						t.Fatalf("window = %+v; want %s=%s and everything else kept", w, tc.key, tc.value)
					}
					if err := w.Validate(); err != nil {
						t.Fatalf("renamed window no longer validates: %v", err)
					}
				case e.single.ID:
					if w.MonitorID != e.monitor.ID || w.TagKey != "" {
						t.Fatalf("monitor window changed: %+v", w)
					}
				}
			}
			in, err := db.InMaintenance(ctx, e.monitor.ID, time.Now())
			if err != nil || !in {
				t.Fatalf("monitor left its maintenance window after the rename: %v, %v", in, err)
			}

			page, err := db.GetStatusPage(ctx, e.page.ID)
			if err != nil {
				t.Fatal(err)
			}
			if page.TagKey != tc.key || page.TagValue != tc.value {
				t.Fatalf("page = %s=%s; want %s=%s", page.TagKey, page.TagValue, tc.key, tc.value)
			}
			if !page.UpdatedAt.After(e.page.UpdatedAt) {
				t.Fatalf("page updated_at did not move: %v, was %v", page.UpdatedAt, e.page.UpdatedAt)
			}
			shown, err := db.ShownStatusPageEntries(ctx, page)
			if err != nil || len(shown) != 1 || shown[0].MonitorID != e.monitor.ID {
				t.Fatalf("renamed page no longer shows its monitor: %+v, %v", shown, err)
			}
			bystander, _ := db.GetStatusPage(ctx, e.bystand.ID)
			if got := bystander.TagKey + "=" + bystander.TagValue; got != tc.bystander {
				t.Fatalf("staging page = %s; want %s", got, tc.bystander)
			}
		})
	}
}

func TestTagRenameOntoAnotherRulesPairIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		taken RoutingRule
		op    TagOperation
	}{
		{"value", RoutingRule{TagKey: "env", TagValue: "production"}, TagOperation{Action: "rename_value", Key: "env", Value: "prod", NewValue: "production"}},
		{"key", RoutingRule{TagKey: "stage", TagValue: "prod"}, TagOperation{Action: "rename_key", Key: "env", NewKey: "stage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := t.Context()
			e := seedRenameEstate(t, db)
			if _, err := db.CreateRoutingRule(ctx, tc.taken); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.TransformTags(ctx, tc.op, "", true); !errors.Is(err, ErrTagRenameRuleConflict) {
				t.Fatalf("preview err = %v; want ErrTagRenameRuleConflict", err)
			}
			// A commit with any validator is refused for the same reason, not
			// because the validator is stale.
			if _, _, err := db.TransformTags(ctx, tc.op, `"tags-0"`, false); !errors.Is(err, ErrTagRenameRuleConflict) {
				t.Fatalf("commit err = %v; want ErrTagRenameRuleConflict", err)
			}
			m, err := db.GetMonitor(ctx, e.monitor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if m.Tags["env"] != "prod" {
				t.Fatalf("refused rename still wrote the monitor: %v", m.Tags)
			}
			if rule, _ := db.GetRoutingRule(ctx, e.rule.ID); rule.TagKey != "env" || rule.TagValue != "prod" {
				t.Fatalf("refused rename still moved the rule: %s=%s", rule.TagKey, rule.TagValue)
			}
		})
	}
}

func TestTagRenamePreviewGoesStaleWhenAFollowerChanges(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	seedRenameEstate(t, db)
	op := TagOperation{Action: "rename_value", Key: "env", Value: "prod", NewValue: "production"}
	_, etag, err := db.TransformTags(ctx, op, "", true)
	if err != nil {
		t.Fatal(err)
	}
	// A second page on the pair appears after the preview: the operator was
	// told about one, so the commit must not quietly move two.
	if _, err := db.CreateStatusPage(ctx, StatusPage{Slug: "prod-2", Title: "Prod 2", Selection: StatusPageSelectTag, TagKey: "env", TagValue: "prod"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TransformTags(ctx, op, etag, false); !errors.Is(err, ErrTagPreviewChanged) {
		t.Fatalf("commit err = %v; want ErrTagPreviewChanged", err)
	}
}

func TestTagApplyAndRemoveMoveNoConfiguration(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	e := seedRenameEstate(t, db)
	for _, op := range []TagOperation{
		{Action: "apply", MonitorIDs: []int64{e.monitor.ID}, Key: "env", Value: "staging"},
		{Action: "remove", MonitorIDs: []int64{e.monitor.ID}, Key: "env", Value: "staging"},
	} {
		result := renameTags(t, db, op)
		if result.RoutingRules != 0 || result.MaintenanceWindows != 0 || result.StatusPages != 0 {
			t.Fatalf("%s reported followers: %+v", op.Action, result)
		}
	}
	if rule, _ := db.GetRoutingRule(ctx, e.rule.ID); rule.TagValue != "prod" {
		t.Fatalf("apply/remove moved the rule to %s", rule.TagValue)
	}
	if page, _ := db.GetStatusPage(ctx, e.page.ID); page.TagValue != "prod" {
		t.Fatalf("apply/remove moved the page to %s", page.TagValue)
	}
}
