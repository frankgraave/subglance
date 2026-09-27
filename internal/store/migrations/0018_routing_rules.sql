-- 0018_routing_rules.sql — route alerts by monitor tag.
--
-- A rule says "monitors tagged key=value alert through these channels". Rules
-- add up: an alert goes to the union of the monitor's own channels and the
-- channels of every rule whose tag it carries. First-match-wins was rejected
-- because an over-broad rule placed early would silently swallow alerts meant
-- for a narrower one, and nothing on screen would say so.
--
-- The default channel stays the floor. It is used only when that union is
-- empty, so a monitor that matches no rule still alerts somebody.
--
-- One rule per tag pair. Two rules on the same pair would mean the same thing
-- as one rule with both channel sets, and would make "which rule sent this"
-- a question with two answers.
CREATE TABLE routing_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tag_key    TEXT    NOT NULL,
    tag_value  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (tag_key, tag_value)
) STRICT;

-- Deleting a channel removes it from every rule; a rule left with no channels
-- adds nothing and the monitor falls back as if it had not matched.
CREATE TABLE routing_rule_channels (
    rule_id    INTEGER NOT NULL REFERENCES routing_rules (id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES notif_channels (id) ON DELETE CASCADE,
    PRIMARY KEY (rule_id, channel_id)
) STRICT, WITHOUT ROWID;

-- An exclusion takes one monitor out of one rule. It is a named row rather
-- than a side effect of rule order, so muting a noisy monitor for one team
-- cannot quietly mute it for another rule or for its own channels.
CREATE TABLE routing_rule_exclusions (
    rule_id    INTEGER NOT NULL REFERENCES routing_rules (id) ON DELETE CASCADE,
    monitor_id INTEGER NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    PRIMARY KEY (rule_id, monitor_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_routing_rule_exclusions_monitor ON routing_rule_exclusions (monitor_id);
