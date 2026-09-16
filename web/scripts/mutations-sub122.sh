#!/usr/bin/env bash
# Mutation verification for SUB-122, the monitors inventory.
#
# Each mutation reverts one decision the tests claim to protect, so the suite
# MUST go red. A mutation that leaves it green is a test that does not bite,
# and the fix for that is the test, never the mutation. The exit status is the
# verdict: a survivor or a pattern that no longer applies fails this script.
#
# Every file is restored in a trap, so an interrupt cannot leave the tree
# mutated.
set -uo pipefail
cd "$(dirname "$0")/.."

BAK=$(mktemp -d)
# The vitest transcript lives OUTSIDE $BAK. The restore loop below copies
# every file in $BAK back to the path its name encodes, and a plain `out.txt`
# encodes `./out.txt` — so keeping the log in there silently wrote a stray
# file into the repository on every run.
LOG=$(mktemp)
trap 'for f in "$BAK"/*; do [ -e "$f" ] || continue; n=$(basename "$f" | tr "%" "/"); cp "$f" "$n"; done; rm -rf "$BAK" "$LOG"' EXIT

save() { cp "$1" "$BAK/$(echo "$1" | tr '/' '%')"; }
restore() { cp "$BAK/$(echo "$1" | tr '/' '%')" "$1"; }

SURVIVED=()
MISSED=()

mutate() { # name file python-replace-script testfiles...
  local name="$1" file="$2" script="$3"; shift 3
  save "$file"
  python3 - "$file" <<PY || { echo "### $name: PATTERN MISSED"; MISSED+=("$name"); restore "$file"; return; }
import sys
p = sys.argv[1]
s = open(p).read()
$script
open(p, "w").write(s)
PY
  echo "### MUTATION: $name"
  local status=0
  npx vitest run "$@" > "$LOG" 2>&1 || status=$?
  grep -E "Tests +[0-9]+ (failed|passed)|AssertionError|→" "$LOG" | head -6
  if [ "$status" -eq 0 ]; then
    echo "!!! SURVIVED — the suite stayed green with this defect in place"
    SURVIVED+=("$name")
  fi
  restore "$file"
  echo
}

report() {
  echo "================================================================"
  if [ ${#MISSED[@]} -eq 0 ] && [ ${#SURVIVED[@]} -eq 0 ]; then
    echo "All mutations applied, and every one of them was caught."
    return 0
  fi
  for m in ${MISSED[@]+"${MISSED[@]}"}; do
    # A missed pattern is not a pass: the code moved on and the mutation
    # silently stopped testing anything.
    echo "PATTERN MISSED (mutation never applied): $m"
  done
  for m in ${SURVIVED[@]+"${SURVIVED[@]}"}; do
    echo "SURVIVED (test does not bite): $m"
  done
  return 1
}

SRC=src/monitors
INV=$SRC/inventory.ts
API=$SRC/inventoryApi.ts
ROW=$SRC/MonitorInventoryRow.tsx
VIEW=$SRC/MonitorsView.tsx
LIVE=$SRC/LiveMonitors.tsx
EDIT=$SRC/EditMonitorForm.tsx
TAGS=$SRC/tags.ts
ROUTE=src/shell/route.ts

# ---------------------------------------------------------------------------
# 1. THE defect this page could most easily ship: a channel column that cannot
#    be found out becomes the finding "nobody hears about this monitor".
mutate "unknown channels collapse into none" "$INV" '
old = """  if (!state.known) return "not loaded";"""
assert old in s
s = s.replace(old, """  if (!state.known) return "none";""")
' $SRC/inventory.test.ts $SRC/MonitorsView.test.tsx

# 2. The same lie one layer down: a failed request contributes an empty list.
mutate "failed channel request reports no channels" "$API" '
old = """          incomplete = true;
          byMonitor[id] = { known: false };
          return;"""
new = """          byMonitor[id] = { known: true, names: [] };
          return;"""
assert old in s
s = s.replace(old, new)
' $SRC/inventoryApi.test.ts

# 3. The fan-out cap stops admitting it capped.
mutate "capped fan-out passes as complete" "$API" '
old = "  let incomplete = monitorIds.length > ids.length;"
assert old in s
s = s.replace(old, "  let incomplete = false;")
' $SRC/inventoryApi.test.ts

# 4. A manual check on a paused monitor claims to have been recorded.
mutate "unrecorded check reported as recorded" "$API" '
old = "    recorded: body.recorded === true,"
assert old in s
s = s.replace(old, "    recorded: body.recorded !== false,")
' $SRC/inventoryApi.test.ts

# 5. The row stops saying that the check was not recorded, so a green result
#    that did not move the dashboard looks like a broken dashboard.
mutate "row hides the not-recorded warning" "$ROW" '
old = """          {checkResult.recorded
            ? ""
            : " Not recorded — this monitor is paused, so the result is not part of its history."}"""
assert old in s
s = s.replace(old, "")
' $SRC/MonitorsView.test.tsx

# 6. Paused becomes colour-and-dimming only: the chip goes.
mutate "paused signalled without the word" "$ROW" '
old = """              <StateChip className="inv-paused-chip">Paused</StateChip>"""
assert old in s
s = s.replace(old, "null")
' $SRC/MonitorsView.test.tsx

# 7. The row actions lose the monitor name from their accessible names, so a
#    list of forty rows becomes forty buttons all called "Pause".
mutate "row actions become indistinguishable" "$ROW" '
old = """              aria-label={`${pauseWord} ${monitor.name}`}"""
assert old in s
s = s.replace(old, "")
' $SRC/MonitorsView.test.tsx $SRC/LiveMonitors.test.tsx

# 8. The disabled Check now button stops saying why it is disabled.
mutate "push refusal loses its reason" "$ROW" '
old = """                  : `${checkWord} ${monitor.name} — unavailable for a push monitor`"""
assert old in s
s = s.replace(old, """                  : `${checkWord} ${monitor.name}`""")
' $SRC/MonitorsView.test.tsx

# 9. Check now is blocked on a paused monitor — the rejected alternative. You
#    want to verify before resuming, and the server allows exactly that.
mutate "check now blocked while paused" "$INV" '
old = "  return monitor.push === undefined;"
assert old in s
s = s.replace(old, "  return monitor.push === undefined && monitor.enabled;")
' $SRC/inventory.test.ts $SRC/MonitorsView.test.tsx

# 10. A push monitor grows a timeout it does not have.
mutate "push monitor reports a timeout" "$INV" '
old = """    timeoutS: api.type === "push" ? null : api.timeout_s,"""
assert old in s
s = s.replace(old, "    timeoutS: api.timeout_s,")
' $SRC/inventory.test.ts $SRC/EditMonitorForm.test.tsx

# 11. An unknown check type is echoed raw into a column whose other values are
#     a fixed vocabulary.
mutate "unknown type echoed verbatim" "$INV" '
old = """  return CHECK_TYPES.includes(type) ? type.toUpperCase() : "UNKNOWN";"""
assert old in s
s = s.replace(old, "  return type.toUpperCase();")
' $SRC/inventory.test.ts

# 12. The heading stops counting paused monitors, which is the number this
#     page exists to surface and the dashboard refuses to show.
mutate "paused count dropped from the heading" "$INV" '
old = "  return paused === 0 ? configured : `${configured}, ${paused} paused`;"
assert old in s
s = s.replace(old, "  return configured;")
' $SRC/inventory.test.ts $SRC/MonitorsView.test.tsx

# 13. A failed list renders the onboarding empty state: "nothing is being
#     watched yet" on an instance with forty monitors.
mutate "failed list shows the empty state" "$VIEW" '
old = "        {loading || error !== null ? ("
assert old in s
s = s.replace(old, "        {loading ? (")
' $SRC/MonitorsView.test.tsx $SRC/LiveMonitors.test.tsx

# 14. Delete becomes one click, with nothing said about the history it takes.
mutate "delete without confirmation" "$VIEW" '
old = "                onDelete={onDelete === undefined ? undefined : setConfirming}"
assert old in s
s = s.replace(old, "                onDelete={onDelete}")
' $SRC/MonitorsView.test.tsx

# 15. The truncation banner disappears, leaving "not loaded" unexplained.
mutate "truncated channels unexplained" "$VIEW" '
old = "      {channelsTruncated && ("
assert old in s
s = s.replace(old, "      {false && (")
' $SRC/MonitorsView.test.tsx

# 16. The page defaults to hiding paused monitors, like the dashboard — which
#     is the one thing it must not do.
mutate "inventory hides paused by default" "$VIEW" '
old = """  const [pausedFilter, setPausedFilter] = useState<string>("");"""
assert old in s
s = s.replace(old, """  const [pausedFilter, setPausedFilter] = useState<string>("active");""")
' $SRC/MonitorsView.test.tsx

# 17. A viewer is offered write controls that will 403.
mutate "write controls offered to a viewer" "$LIVE" '
old = "      onTogglePaused={canWrite ? onTogglePaused : undefined}"
assert old in s
s = s.replace(old, "      onTogglePaused={onTogglePaused}")
s = s.replace("      onDelete={canWrite ? onDelete : undefined}", "      onDelete={onDelete}")
' $SRC/LiveMonitors.test.tsx

# 18. Pausing is applied optimistically: the row claims SubGlance stopped
#     watching before the server ever said so.
mutate "pause flipped without the server" "$LIVE" '
old = """      void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
    },
  });

  const deleteMutation"""
new = """    },
  });

  const deleteMutation"""
assert old in s
s = s.replace(old, new)
' $SRC/LiveMonitors.test.tsx

# 19. The edit drops the If-Match, so two people tidying the inventory silently
#     overwrite each other.
mutate "edit becomes last-write-wins" "$LIVE" '
old = """      const etag = await version(id);
      await patch(id, body, etag);"""
assert old in s
s = s.replace(old, """      await patch(id, body);""")
' $SRC/LiveMonitors.test.tsx

# 20. The same, one layer down: the header is built but never sent.
mutate "If-Match header dropped in the request" "$API" '
old = """      ...(version !== undefined && version !== null && version !== ""
        ? { "If-Match": version }
        : {}),"""
assert old in s
s = s.replace(old, "")
' $SRC/inventoryApi.test.ts

# 21. A failed write is attributed to the whole page rather than to its row.
mutate "row errors merged into one" "$LIVE" '
old = """      [id]:
        error instanceof Error
          ? error.message
          : "the change could not be saved","""
new = """      all:
        error instanceof Error
          ? error.message
          : "the change could not be saved","""
assert old in s
s = s.replace(old, new)
' $SRC/LiveMonitors.test.tsx

# 22. An unrecorded check triggers a refetch anyway, making a button that
#     changed nothing on the server feel like it did.
mutate "unrecorded check refetches the list" "$LIVE" '
old = """      if (result.recorded) {
        void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
      }"""
assert old in s
s = s.replace(old, """      void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });""")
' $SRC/LiveMonitors.test.tsx

# 23. The edit form sends every field on every save, so two people editing
#     different fields overwrite each other with values neither touched.
mutate "edit sends the whole monitor" "$EDIT" '
old = "    if (trimmed !== monitor.name) patch.name = trimmed;"
assert old in s
s = s.replace(old, "    patch.name = trimmed;")
s = s.replace("    if (interval !== monitor.intervalS) patch.interval_s = interval;",
              "    patch.interval_s = interval;")
' $SRC/EditMonitorForm.test.tsx

# 24. Client-side range checks go, so an impossible interval reaches the API
#     and comes back as a message about a field name.
mutate "interval range check removed" "$EDIT" '
old = "    if (!Number.isInteger(interval) || interval < 20 || interval > 86400) {"
assert old in s
s = s.replace(old, "    if (false) {")
' $SRC/EditMonitorForm.test.tsx

# 25. Unparseable tag text is sent as a guess rather than refused.
mutate "bad tag text sent anyway" "$EDIT" '
old = """    if (tags === null) {"""
assert old in s
s = s.replace(old, """    if (false) {""")
s = s.replace("    if (tagsToText(tags) !== tagsToText(monitor.tags)) patch.tags = tags;",
              "    if (tags !== null && tagsToText(tags) !== tagsToText(monitor.tags)) patch.tags = tags;")
' $SRC/EditMonitorForm.test.tsx

# 26. The tag parser splits on every colon, so a value containing one is cut in
#     half — the exact reason the API carries tags as an object.
mutate "tag value split on every colon" "$TAGS" '
old = """    const at = entry.indexOf(":");"""
assert old in s
s = s.replace(old, """    const at = entry.lastIndexOf(":");""")
s = s.replace("""    if (at <= 0) return null;""", """    if (at <= 0) return null;
    if (entry.indexOf(":") !== at) return null;""")
' $SRC/tags.test.ts

# 27. The timeout control appears for a push monitor, offering a setting that
#     changing cannot affect.
mutate "push monitor offered a timeout control" "$EDIT" '
old = "        {monitor.timeoutS !== null && ("
assert old in s
s = s.replace(old, "        {true && (")
' $SRC/EditMonitorForm.test.tsx

# 28. The form stops explaining why target, type and the HTTP settings are
#     absent, so it reads as a form that forgot them.
mutate "edit form stops explaining what it omits" "$EDIT" '
old = "        Target and check type are not editable here"
assert old in s
i = s.index("      <p className=\"add-help\">\n        Target and check type")
j = s.index("</p>", i) + len("</p>\n")
s = s[:i] + s[j:]
' $SRC/EditMonitorForm.test.tsx

# 29. The monitors route stops being a place.
mutate "monitors route removed" "$ROUTE" '
old = """  if (segments.length === 1 && segments[0] === "monitors") {
    return { name: "monitors", create: false };
  }
"""
assert old in s
s = s.replace(old, "")
' src/shell/route.test.ts

# 30. /monitors/new stops opening the form, so the empty state has nothing to
#     link to and a reload loses the drawer.
mutate "create address stops opening the drawer" "$ROUTE" '
old = """    if (segments[1] === CREATE_SEGMENT) {
      return { name: "monitors", create: true };
    }
"""
assert old in s
s = s.replace(old, "")
' src/shell/route.test.ts

# 31. routePath forgets the create flag, so closing the drawer leaves the URL
#     on /monitors/new and Back reopens a form the user dismissed.
mutate "create flag dropped from the path" "$ROUTE" '
old = """  if (route.name === "monitors")
    return route.create ? MONITOR_CREATE_PATH : MONITORS_PATH;"""
assert old in s
s = s.replace(old, """  if (route.name === "monitors") return MONITORS_PATH;""")
' src/shell/route.test.ts

# 32. The sidebar goes back to promising a screen that now exists.
mutate "monitors demoted to a Soon label" src/shell/Sidebar.tsx '
old = """  {
    id: "monitors",
    label: "Monitors",
    Icon: MonitorsIcon,
    href: MONITORS_PATH,
    route: "monitors",
  },
"""
assert old in s
s = s.replace(old, "")
s = s.replace("""const PLANNED_CONFIG: readonly Destination[] = [""",
              """const PLANNED_CONFIG: readonly Destination[] = [
  { id: "monitors", label: "Monitors", Icon: MonitorsIcon },""")
' src/shell/shell.test.tsx

report
