package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A routing rule sends the alerts of every monitor carrying one tag to a set
// of channels (migration 0018). Rules add up; see AlertChannels for how they
// combine with a monitor's own channels and the instance default.

// ErrRoutingRuleExists is returned when a rule for the same tag pair already
// exists. One rule per pair keeps "which rule sent this alert" a question with
// one answer; a second set of channels belongs on the existing rule.
var ErrRoutingRuleExists = errors.New("store: a routing rule for this tag already exists")

// RoutingRule routes monitors tagged TagKey=TagValue to ChannelIDs, except the
// monitors listed in ExcludedMonitorIDs.
type RoutingRule struct {
	ID       int64
	TagKey   string
	TagValue string

	// ChannelIDs is sorted ascending. A rule with none is legal: it matches
	// but adds nothing, which is what a rule whose only channel was deleted
	// becomes.
	ChannelIDs []int64

	// ExcludedMonitorIDs is sorted ascending. Written only through
	// ExcludeMonitorFromRule and IncludeMonitorInRule, never by Create or
	// Update, so saving a rule's tag or channels cannot drop an exclusion.
	ExcludedMonitorIDs []int64

	CreatedAt time.Time
}

// NormaliseRoutingTag applies the monitor tag rules to a rule's tag, so a rule
// can only name a tag a monitor could actually carry. A key a monitor can
// never have would make a rule that silently matches nothing.
func NormaliseRoutingTag(key, value string) (string, string, error) {
	norm, err := NormaliseTags(map[string]string{key: value})
	if err != nil {
		return "", "", err
	}
	for k, v := range norm {
		return k, v, nil
	}
	return "", "", fmt.Errorf("tag key must not be empty")
}

// ListRoutingRules returns every rule, oldest first.
func (db *DB) ListRoutingRules(ctx context.Context) ([]RoutingRule, error) {
	rows, err := db.Reader.QueryContext(ctx,
		"SELECT id, tag_key, tag_value, created_at FROM routing_rules ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query routing rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []RoutingRule{}
	index := map[int64]int{}
	for rows.Next() {
		var (
			r       RoutingRule
			created int64
		)
		if err := rows.Scan(&r.ID, &r.TagKey, &r.TagValue, &created); err != nil {
			return nil, fmt.Errorf("scan routing rule: %w", err)
		}
		r.CreatedAt = time.Unix(created, 0).UTC()
		index[r.ID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	if err := db.collectRuleIDs(ctx,
		"SELECT rule_id, channel_id FROM routing_rule_channels ORDER BY rule_id, channel_id",
		func(rule, id int64) {
			if i, ok := index[rule]; ok {
				out[i].ChannelIDs = append(out[i].ChannelIDs, id)
			}
		}); err != nil {
		return nil, fmt.Errorf("query routing rule channels: %w", err)
	}
	if err := db.collectRuleIDs(ctx,
		"SELECT rule_id, monitor_id FROM routing_rule_exclusions ORDER BY rule_id, monitor_id",
		func(rule, id int64) {
			if i, ok := index[rule]; ok {
				out[i].ExcludedMonitorIDs = append(out[i].ExcludedMonitorIDs, id)
			}
		}); err != nil {
		return nil, fmt.Errorf("query routing rule exclusions: %w", err)
	}
	return out, nil
}

func (db *DB) collectRuleIDs(ctx context.Context, query string, add func(rule, id int64)) error {
	rows, err := db.Reader.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rule, id int64
		if err := rows.Scan(&rule, &id); err != nil {
			return err
		}
		add(rule, id)
	}
	return rows.Err()
}

// GetRoutingRule returns one rule. It reports ErrNotFound when absent.
func (db *DB) GetRoutingRule(ctx context.Context, id int64) (RoutingRule, error) {
	rules, err := db.ListRoutingRules(ctx)
	if err != nil {
		return RoutingRule{}, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, nil
		}
	}
	return RoutingRule{}, fmt.Errorf("%w: routing rule %d", ErrNotFound, id)
}

// CreateRoutingRule stores a rule and returns it with its ID. The tag must
// already be normalised (NormaliseRoutingTag). It reports ErrRoutingRuleExists
// for a tag pair that already has a rule and ErrUnknownChannel for a channel id
// that names nothing; either way nothing is stored.
func (db *DB) CreateRoutingRule(ctx context.Context, r RoutingRule) (RoutingRule, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return RoutingRule{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		"INSERT INTO routing_rules (tag_key, tag_value, created_at) VALUES (?, ?, ?)",
		r.TagKey, r.TagValue, time.Now().Unix())
	if isUniqueViolation(err) {
		return RoutingRule{}, fmt.Errorf("%w: %s=%s", ErrRoutingRuleExists, r.TagKey, r.TagValue)
	}
	if err != nil {
		return RoutingRule{}, fmt.Errorf("insert routing rule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return RoutingRule{}, fmt.Errorf("last insert id: %w", err)
	}
	if err := replaceRuleChannels(ctx, tx, id, r.ChannelIDs); err != nil {
		return RoutingRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return RoutingRule{}, fmt.Errorf("commit: %w", err)
	}
	return db.GetRoutingRule(ctx, id)
}

// UpdateRoutingRule replaces a rule's tag and channels in one transaction and
// leaves its exclusions alone. It reports ErrNotFound, ErrRoutingRuleExists
// or ErrUnknownChannel, and changes nothing when it does.
func (db *DB) UpdateRoutingRule(ctx context.Context, r RoutingRule) (RoutingRule, error) {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return RoutingRule{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		"UPDATE routing_rules SET tag_key = ?, tag_value = ? WHERE id = ?",
		r.TagKey, r.TagValue, r.ID)
	if isUniqueViolation(err) {
		return RoutingRule{}, fmt.Errorf("%w: %s=%s", ErrRoutingRuleExists, r.TagKey, r.TagValue)
	}
	if err != nil {
		return RoutingRule{}, fmt.Errorf("update routing rule %d: %w", r.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return RoutingRule{}, fmt.Errorf("update routing rule %d: %w", r.ID, err)
	}
	if n == 0 {
		return RoutingRule{}, fmt.Errorf("%w: routing rule %d", ErrNotFound, r.ID)
	}
	if err := replaceRuleChannels(ctx, tx, r.ID, r.ChannelIDs); err != nil {
		return RoutingRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return RoutingRule{}, fmt.Errorf("commit: %w", err)
	}
	return db.GetRoutingRule(ctx, r.ID)
}

func replaceRuleChannels(ctx context.Context, tx *sql.Tx, ruleID int64, ids []int64) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM routing_rule_channels WHERE rule_id = ?", ruleID); err != nil {
		return fmt.Errorf("clear routing rule channels: %w", err)
	}
	for _, id := range ids {
		var found int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM notif_channels WHERE id = ?", id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrUnknownChannel, id)
		}
		if err != nil {
			return fmt.Errorf("look up channel %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO routing_rule_channels (rule_id, channel_id) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, ruleID, id); err != nil {
			return fmt.Errorf("add channel %d to routing rule: %w", id, err)
		}
	}
	return nil
}

// DeleteRoutingRule removes a rule with its channels and exclusions.
func (db *DB) DeleteRoutingRule(ctx context.Context, id int64) error {
	res, err := db.Writer.ExecContext(ctx, "DELETE FROM routing_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete routing rule %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete routing rule %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: routing rule %d", ErrNotFound, id)
	}
	return nil
}

// ExcludeMonitorFromRule stops one rule routing one monitor's alerts. The
// monitor keeps its own channels and every other rule. Excluding a monitor
// that is already excluded is not an error. It reports ErrNotFound, naming
// which of the two is missing, when the rule or the monitor does not exist.
//
// A monitor that does not carry the rule's tag may still be excluded: the
// exclusion then takes effect if the tag is added later, which is what
// someone who muted it would expect.
func (db *DB) ExcludeMonitorFromRule(ctx context.Context, ruleID, monitorID int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	checks := []struct {
		query, what string
		id          int64
	}{
		{"SELECT 1 FROM routing_rules WHERE id = ?", "routing rule", ruleID},
		{"SELECT 1 FROM monitors WHERE id = ?", "monitor", monitorID},
	}
	for _, check := range checks {
		var found int
		err := tx.QueryRowContext(ctx, check.query, check.id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s %d", ErrNotFound, check.what, check.id)
		}
		if err != nil {
			return fmt.Errorf("look up %s %d: %w", check.what, check.id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO routing_rule_exclusions (rule_id, monitor_id) VALUES (?, ?)
		ON CONFLICT DO NOTHING`, ruleID, monitorID); err != nil {
		return fmt.Errorf("exclude monitor %d from routing rule %d: %w", monitorID, ruleID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// IncludeMonitorInRule undoes ExcludeMonitorFromRule. It reports ErrNotFound
// when there was no such exclusion, so a client cannot believe it re-enabled
// a route that was never muted.
func (db *DB) IncludeMonitorInRule(ctx context.Context, ruleID, monitorID int64) error {
	res, err := db.Writer.ExecContext(ctx,
		"DELETE FROM routing_rule_exclusions WHERE rule_id = ? AND monitor_id = ?", ruleID, monitorID)
	if err != nil {
		return fmt.Errorf("include monitor %d in routing rule %d: %w", monitorID, ruleID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("include monitor %d in routing rule %d: %w", monitorID, ruleID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: monitor %d is not excluded from routing rule %d", ErrNotFound, monitorID, ruleID)
	}
	return nil
}

// ruleChannelsForMonitor selects the ids of the channels that rules add for
// one monitor: rules whose tag the monitor carries, minus the rules it is
// excluded from. It takes the monitor id twice.
const ruleChannelsForMonitor = `
	SELECT rc.channel_id
	  FROM routing_rule_channels rc
	  JOIN routing_rules r ON r.id = rc.rule_id
	  JOIN monitor_tags t ON t.key = r.tag_key AND t.value = r.tag_value
	 WHERE t.monitor_id = ?
	   AND NOT EXISTS (SELECT 1 FROM routing_rule_exclusions e
	                    WHERE e.rule_id = r.id AND e.monitor_id = ?)`

// RuleRoute is one rule's contribution to one monitor's routing.
type RuleRoute struct {
	RuleID   int64
	TagKey   string
	TagValue string
	Channels []ChannelSummary
}

// MonitorRuleRoutes reads, for every monitor at once, which rules route it
// and to which channels. Rules the monitor is excluded from and rules with no
// channels are left out, because neither sends it anything. A missing monitor
// key means no rule routes it only when err is nil.
func (db *DB) MonitorRuleRoutes(ctx context.Context) (map[int64][]RuleRoute, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT t.monitor_id, r.id, r.tag_key, r.tag_value, c.id, c.name
		  FROM routing_rules r
		  JOIN monitor_tags t ON t.key = r.tag_key AND t.value = r.tag_value
		  JOIN routing_rule_channels rc ON rc.rule_id = r.id
		  JOIN notif_channels c ON c.id = rc.channel_id
		 WHERE NOT EXISTS (SELECT 1 FROM routing_rule_exclusions e
		                    WHERE e.rule_id = r.id AND e.monitor_id = t.monitor_id)
		 ORDER BY t.monitor_id, r.id, c.id`)
	if err != nil {
		return nil, fmt.Errorf("query monitor rule routes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]RuleRoute)
	for rows.Next() {
		var (
			monitorID int64
			route     RuleRoute
			ch        ChannelSummary
		)
		if err := rows.Scan(&monitorID, &route.RuleID, &route.TagKey, &route.TagValue, &ch.ID, &ch.Name); err != nil {
			return nil, fmt.Errorf("scan monitor rule route: %w", err)
		}
		routes := out[monitorID]
		if n := len(routes); n > 0 && routes[n-1].RuleID == route.RuleID {
			routes[n-1].Channels = append(routes[n-1].Channels, ch)
		} else {
			route.Channels = []ChannelSummary{ch}
			routes = append(routes, route)
		}
		out[monitorID] = routes
	}
	return out, rows.Err()
}
