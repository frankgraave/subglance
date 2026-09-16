#!/usr/bin/env bash
# Mutation verification for SUB-34. Each mutation reverts one decision the
# tests claim to protect; a mutation that leaves the suite green is a test that
# does not bite. Restores every file in a trap, so an interrupt cannot leave
# the tree mutated.
set -uo pipefail
cd "$(dirname "$0")/.."

BAK=$(mktemp -d)
trap 'for f in "$BAK"/*; do [ -e "$f" ] || continue; n=$(basename "$f" | tr "%" "/"); cp "$f" "$n"; done; rm -rf "$BAK"' EXIT

save() { cp "$1" "$BAK/$(echo "$1" | tr '/' '%')"; }
restore() { cp "$BAK/$(echo "$1" | tr '/' '%')" "$1"; }

mutate() { # name file python-replace-script testfiles...
  local name="$1" file="$2" script="$3"; shift 3
  save "$file"
  python3 - "$file" <<PY || { echo "### $name: PATTERN MISSED"; restore "$file"; return; }
import sys
p = sys.argv[1]
s = open(p).read()
$script
open(p, "w").write(s)
PY
  echo "### MUTATION: $name"
  npx vitest run "$@" 2>&1 | grep -E "Tests +[0-9]+ (failed|passed)|AssertionError|→" | head -6
  restore "$file"
  echo
}

SRC=src/incidents
STORY=$SRC/story.ts
ITEM=$SRC/IncidentStoryItem.tsx
CLUSTER=$SRC/cluster.ts
VIEW=$SRC/IncidentsView.tsx

# 1. THE defect the ticket names: acked collapses into resolved.
mutate "acked is treated as resolved" "$STORY" '
old = """  if (incident.resolved) return "resolved";
  return incident.acked ? "acked" : "open";"""
new = """  if (incident.resolved || incident.acked) return "resolved";
  return "open";"""
assert old in s
s = s.replace(old, new)
' $SRC/story.test.ts $SRC/IncidentsView.test.tsx

# 2. The reassuring-half-only label the whole ticket is about.
mutate "chip says only Acknowledged" "$STORY" '
old = """  acked: "Acked, still down","""
new = """  acked: "Acknowledged","""
assert old in s
s = s.replace(old, new, 1)
' $SRC/story.test.ts $SRC/IncidentsView.test.tsx

# 3. The rejected alternative: give the middle state the healthy colour.
mutate "acked takes the up tone" "$STORY" '
old = """  acked: "warn","""
assert old in s
s = s.replace(old, """  acked: "up","""      , 1)
' $SRC/story.test.ts

# 4. Drop "still down" from the ack clause.
mutate "ack clause loses still-down" "$STORY" '
old = """    ? "was still down, repeat alerts muted"
    : "still down, repeat alerts muted";"""
new = """    ? "repeat alerts muted"
    : "repeat alerts muted";"""
assert old in s
s = s.replace(old, new)
' $SRC/story.test.ts $SRC/IncidentsView.test.tsx

# 5. SUB-111: let the row assert the present tense on a dead stream.
mutate "stale rows keep the present tense" "$STORY" '
old = "export const stateBadge = (state: IncidentState, stale = false): string =>\n  stale ? STATE_BADGE_LAST_KNOWN[state] : STATE_BADGE[state];"
new = "export const stateBadge = (state: IncidentState, _stale = false): string =>\n  STATE_BADGE[state];"
assert old in s
s = s.replace(old, new)
' $SRC/story.test.ts src/monitors/stale-tense.test.tsx

# 6. The eye/ear pairing: drop the sr-only sentence entirely.
#    `hidden` was tried first and does NOT work as a mutation: jsdom still
#    reports hidden text in textContent, so the assertion passed against a
#    sentence no screen reader would reach. Deleting it is the real defect.
mutate "screen reader loses the sentence" "$ITEM" '
old = """        {story.sentence}"""
assert old in s
s = s.replace(old, "")
' $SRC/IncidentsView.test.tsx

# 7. Cause rendered as the raw database key.
mutate "cause printed as the raw kind" "$STORY" '
old = "  return CAUSE_WORDS[cause] ?? cause;"
assert old in s
s = s.replace(old, "  return cause;")
' $SRC/story.test.ts $SRC/IncidentsView.test.tsx

# 8. The button label loses its promise.
mutate "button says only Acknowledge" "$ITEM" '
old = """Mute repeat alerts"""
assert old in s
s = s.replace(old, "Acknowledge")
' $SRC/IncidentsView.test.tsx

# 9. The side panel the design rejected: detail as a sibling, not a child.
mutate "detail leaves the row" "$ITEM" '
old = """      {open ? (
        <div className="inc-detail" id={detailId}>"""
new = """      {false ? (
        <div className="inc-detail" id={detailId}>"""
assert old in s
s = s.replace(old, new)
' $SRC/IncidentsView.test.tsx

# 10. Clustering tells a lie: claim a shared cause.
mutate "cluster claims a shared cause" "$CLUSTER" '
old = """    `${cluster.monitorCount} monitors started failing within ${span} of ` +
    "each other — often one shared cause, but SubGlance is inferring that " +
    "from the timing alone. Open the group to judge for yourself."""
new = """    `${cluster.monitorCount} monitors failed together within ${span} — ` +
    "one outage, caused by a shared dependency."""
assert old in s
s = s.replace(old, new)
' $SRC/cluster.test.ts $SRC/IncidentsView.test.tsx

# 11. Clustering hides incidents: one monitor flapping becomes a "cluster".
mutate "one monitor flapping is clustered" "$CLUSTER" '
old = "    if (run.length > 1 && monitors.size >= CLUSTER_MIN_MONITORS) {"
assert old in s
s = s.replace(old, "    if (run.length > 1) {")
' $SRC/cluster.test.ts

# 12. The cluster count hides behind the disclosure.
mutate "cluster count only inside the group" "$SRC/IncidentClusterItem.tsx" '
old = """        aria-label={`${label} — ${describeCluster(cluster)}`}"""
new = """        aria-label="Incident group\""""
assert old in s
s = s.replace(old, new)
' $SRC/IncidentsView.test.tsx

# 13. Flapping stops being explained.
mutate "flapping note removed" "$STORY" '
old = "  if (recent.length < CHURN_THRESHOLD) return null;"
assert old in s
s = s.replace(old, "  return null;\n  if (recent.length < CHURN_THRESHOLD) return null;")
' $SRC/story.test.ts $SRC/IncidentsView.test.tsx

# 14. The plumbing: MonitorDetail stops forwarding the ack handler.
mutate "detail page drops the ack control" src/monitors/MonitorDetail.tsx '
old = "                  onAck={onAck}"
assert old in s
s = s.replace(old, "                  onAck={undefined}")
' src/monitors/MonitorDetail.test.tsx $SRC/IncidentsView.test.tsx

# 15. The monitor id the translation used to drop.
mutate "monitorId dropped in translation" src/monitors/detail.ts '
old = "    monitorId: String(api.monitor_id),"
assert old in s
s = s.replace(old, "    monitorId: \"\",")
' src/monitors/detail.test.ts $SRC/IncidentsView.test.tsx

# 16. The empty state loses its proof.
mutate "empty state loses the monitor count" "$VIEW" '
old = """                : `${monitorCount} ${
                    monitorCount === 1 ? "monitor" : "monitors"
                  } watched, zero confirmed outages.`}"""
new = """                : "All clear."}"""
assert old in s
s = s.replace(old, new)
' $SRC/IncidentsView.test.tsx

# 17. History stops admitting it is partial.
mutate "truncated history looks complete" "$VIEW" '
old = "            {historyTruncated ? ("
assert old in s
s = s.replace(old, "            {false ? (")
' $SRC/IncidentsView.test.tsx

# 18. The timeline invents alert delivery.
mutate "timeline invents an alert channel" "$STORY" '
old = """Confirmed — this is when a human was told"""
assert old in s
s = s.replace(old, "Alert sent — slack #ops")
' $SRC/story.test.ts

# 19. The route stops being a place.
mutate "incidents route removed" src/shell/route.ts '
old = """  if (segments.length === 1 && segments[0] === "incidents") {
    return { name: "incidents" };
  }
"""
assert old in s
s = s.replace(old, "")
' src/shell/route.test.ts

# 20. The sidebar goes back to promising a screen that now exists.
mutate "incidents demoted to a Soon label" src/shell/Sidebar.tsx '
old = """  {
    id: "incidents",
    label: "Incidents",
    Icon: IncidentsIcon,
    href: INCIDENTS_PATH,
    route: "incidents",
  },
"""
assert old in s
s = s.replace(old, "")
' src/shell/shell.test.tsx
