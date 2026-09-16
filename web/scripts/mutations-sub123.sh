#!/usr/bin/env bash
# Mutation verification for SUB-123, the notifications page.
#
# Each mutation reverts one decision the tests claim to protect, so the suite
# MUST go red. A mutation that leaves it green is a test that does not bite,
# and the fix for that is the test, never the mutation. The exit status is the
# verdict: a survivor or a pattern that no longer applies fails this script.
#
# Every file is restored in a trap, so an interrupt cannot leave the tree
# mutated.
set -uo pipefail
# `|| exit` is not decoration here: without it a failed cd leaves the script
# running in the caller's directory, where `save`, the Python mutation and
# `restore` would rewrite whatever files happen to match those relative paths.
cd "$(dirname "$0")/.." || exit 1

BAK=$(mktemp -d)
# The vitest transcript lives OUTSIDE $BAK, because the restore loop copies
# every file in $BAK back to the path its name encodes.
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
  #
  # A non-zero exit is not by itself proof that the mutation was caught: a
  # missing node_modules, an unresolvable npx or a TypeScript error unrelated
  # to the mutation all exit non-zero too. So the log has to show that vitest
  # got far enough to print a test summary before the exit status means
  # anything.
  #
  if ! grep -qE "Tests +[0-9]+ (failed|passed)" "$LOG"; then
    echo "!!! NO RESULT — vitest printed no test summary, so this mutation proves nothing"
    tail -5 "$LOG"
    MISSED+=("$name (vitest produced no summary)")
  elif [ "$status" -eq 0 ]; then
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

SRC=src/notifications
MODEL=$SRC/channels.ts
API=$SRC/channelsApi.ts
ROW=$SRC/ChannelRow.tsx
FORM=$SRC/ChannelForm.tsx
VIEW=$SRC/NotificationsView.tsx
LIVE=$SRC/LiveNotifications.tsx
ROUTE=src/shell/route.ts

MODEL_T=$SRC/channels.test.ts
API_T=$SRC/channelsApi.test.ts
FORM_T=$SRC/ChannelForm.test.tsx
VIEW_T=$SRC/NotificationsView.test.tsx
LIVE_T=$SRC/LiveNotifications.test.tsx

# ---------------------------------------------------------------------------
# THE DEFECT THIS PAGE COULD MOST EASILY SHIP: a channel nobody has verified
# is presented as healthy. The API supplies no delivery history at all, so
# every one of these is an invention.

# 1. The unverified state is renamed into a claim of health.
mutate "unverified channel reported as delivering" "$MODEL" '
old = """      return "Not verified";"""
assert old in s
s = s.replace(old, """      return "Delivered";""")
' $MODEL_T $VIEW_T

# 2. The same lie one layer up: the row draws the untested state as a green
#    status chip instead of the dashed "about the data" chip.
mutate "untested state drawn as a healthy status" "$ROW" '
old = """              <StateChip>Not verified</StateChip>"""
assert old in s
s = s.replace(old, """              <StatusChip status="up">{deliveryWord}</StatusChip>""")
' $VIEW_T

# 3. A test whose reply cannot be read is taken as a pass.
mutate "unreadable test reply read as success" "$API" '
old = "  if (res.ok && body.ok === true) return { ok: true };"
assert old in s
s = s.replace(old, "  if (res.ok) return { ok: true };")
' $API_T

# 4. The upstream error is swallowed and replaced with a generic sentence —
#    exactly the trip to the server log this button exists to save.
mutate "upstream error replaced with a generic failure" "$API" '
old = """  const error =
    body.error !== undefined && body.error !== ""
      ? body.error
      : `the server rejected the test (HTTP ${res.status}) without saying why`;"""
assert old in s
s = s.replace(old, """  const error = "the test could not be delivered";""")
' $API_T $VIEW_T $LIVE_T

# 5. The row stops printing the error verbatim.
mutate "row hides the upstream error text" "$ROW" '
old = """            : `The test message was not delivered. The far end said: ${delivery.error}`}"""
assert old in s
s = s.replace(old, """            : "The test message was not delivered."}""")
' $VIEW_T $LIVE_T

# 6. A failed request (dead network, expired session) is recorded as a failed
#    channel, which sends someone re-pasting a webhook that works.
mutate "failed request recorded as a failed channel" "$LIVE" '
old = """      noteError(id, error);"""
assert old in s
s = s.replace(old, """      noteError(id, error);
      setDeliveries((current) => ({
        ...current,
        [id]: { kind: "failed", error: "the test failed" },
      }));""")
' $LIVE_T

# 7. A test result survives an edit, so a green tick sits beside a credential
#    that has never been exercised.
mutate "test result survives the edit it no longer describes" "$LIVE" '
old = """      if (id !== null) {
        setDeliveries((current) => {
          if (!(id in current)) return current;
          const next = { ...current };
          delete next[id];
          return next;
        });
      }"""
assert old in s
s = s.replace(old, "")
' $LIVE_T

# 8. A deleted channel leaves its result behind for whoever inherits the id.
mutate "test result outlives the channel it was about" "$LIVE" '
old = """      setDeliveries((current) => {
        if (!(id in current)) return current;
        const next = { ...current };
        delete next[id];
        return next;
      });
      deleteMutation.mutate(id);"""
assert old in s
s = s.replace(old, "      deleteMutation.mutate(id);")
' $LIVE_T

# ---------------------------------------------------------------------------
# SECRETS. A stored credential must be unreadable through this page, and an
# edit that does not touch it must leave it exactly as it was.

# 9. The mask shape stops being recognised, so an untouched secret is treated
#    as a value the user typed.
mutate "mask no longer recognised as a mask" "$MODEL" '
old = """  return /^\\*{4}.{4}$/.test(value);"""
assert old in s
s = s.replace(old, "  return false;")
' $MODEL_T

# 10. An untouched secret is sent as an empty string, which wipes the stored
#     credential on every save that did not mean to change it.
mutate "untouched secret cleared on save" "$FORM" '
old = """        config[spec.key] = channel?.config[spec.key] ?? "";
        continue;"""
assert old in s
s = s.replace(old, """        continue;""")
' $FORM_T

# 11. The API layer stops sending config on PUT at all.
mutate "PUT stops carrying the config" "$API" '
old = "    body: JSON.stringify(input),"
assert old in s
s = s.replace(old, """    body: JSON.stringify({ name: input.name, type: input.type }),""", 1)
' $API_T

# 12. The control that accepts a new secret becomes a plain text box, so the
#     value stands on screen in the clear during a screen share.
mutate "secret typed into a visible text field" "$FORM" '
old = """                  type={spec.secret ? "password" : "text"}"""
assert old in s
s = s.replace(old, """                  type="text\"""")
' $FORM_T

# 13. The stored secret is rendered into an input — a reveal with no button.
mutate "stored secret rendered into a field" "$FORM" '
old = "            {stored && !open ? ("
assert old in s
s = s.replace(old, "            {false ? (")
' $FORM_T

# 14. A field the API masks is declared public, so the mask lands in a text box
#     and is saved back as a literal row of asterisks.
mutate "masked field declared public" "$MODEL" '
old = """      key: "bot_token",
      label: "Bot token",
      secret: true,"""
assert old in s
s = s.replace(old, """      key: "bot_token",
      label: "Bot token",
      secret: false,""")
' $MODEL_T $FORM_T

# 15. A masked webhook URL is printed as though it were the destination.
mutate "masked endpoint printed as a destination" "$MODEL" '
old = """      return isMasked(url) ? `endpoint ending ${url}` : url;"""
assert old in s
s = s.replace(old, "      return url;")
' $MODEL_T

# ---------------------------------------------------------------------------
# HONESTY ABOUT WHAT THE API SUPPLIES.

# 16. A 200 with no channel list reads as an instance with no channels, so the
#     page tells a working install that its alerting is gone.
mutate "unreadable payload reported as no channels" "$API" '
old = "  if (!Array.isArray(body?.channels)) {"
assert old in s
s = s.replace(old, "  if (false) {")
' $API_T

# 17. A failed list renders the "alerts are going nowhere" headline.
mutate "failed list shows the empty state" "$VIEW" '
old = "        {loading || error !== null ? ("
assert old in s
s = s.replace(old, "        {loading ? (")
' $VIEW_T $LIVE_T

# 18. The page stops explaining that delivery history does not exist, leaving
#     "Not verified" on every row reading as a bug in the page.
mutate "missing delivery history left unexplained" "$VIEW" '
i = s.index("        SubGlance cannot yet tell you whether")
j = s.index("</p>", i) + len("</p>")
k = s.rindex("<p className=\"add-help\">", 0, i)
s = s[:k] + s[j:]
' $VIEW_T

# 19. The form grows the fields the mockup drew and the notifier never reads.
mutate "form offers settings nothing reads" "$MODEL" '
old = """  slack: [
    {
      key: "url","""
assert old in s
s = s.replace(old, """  slack: [
    {
      key: "channel",
      label: "Channel label",
      secret: false,
      required: false,
    },
    {
      key: "url",""")
' $MODEL_T $FORM_T

# 20. An unknown channel type is echoed into a label whose other values are a
#     fixed vocabulary.
mutate "unknown channel type echoed verbatim" "$MODEL" '
old = """    default:
      return "Unknown type";"""
assert old in s
s = s.replace(old, """    default:
      return type;""")
' $MODEL_T

# ---------------------------------------------------------------------------
# ACCESSIBILITY AND PERMISSIONS.

# 21. Row actions lose the channel name, so four rows become four buttons all
#     called "Send test".
mutate "row actions become indistinguishable" "$ROW" '
old = """              aria-label={`${testWord} ${label}`}"""
assert old in s
s = s.replace(old, "")
' $VIEW_T $LIVE_T

# 22. Disabled becomes dimming alone: the word goes.
mutate "disabled signalled without the word" "$ROW" '
old = """              <StateChip className="inv-paused-chip">Disabled</StateChip>"""
assert old in s
s = s.replace(old, "null")
' $VIEW_T

# 23. A viewer is offered the test button, which sends a real message to
#     somebody else's inbox and comes back 403.
mutate "test offered to a viewer" "$LIVE" '
old = "      onTest={canWrite ? onTest : undefined}"
assert old in s
s = s.replace(old, "      onTest={onTest}")
s = s.replace("      onDelete={canWrite ? onDelete : undefined}", "      onDelete={onDelete}")
' $LIVE_T $VIEW_T

# 24. Deleting becomes one click, with nothing said about the monitors that go
#     silent with the channel.
mutate "delete without confirmation" "$VIEW" '
old = "                onDelete={onDelete === undefined ? undefined : setConfirming}"
assert old in s
s = s.replace(old, "                onDelete={onDelete}")
' $VIEW_T

# 25. The type of an existing channel becomes editable, which carries a masked
#     webhook URL into a form that has no field for it.
mutate "existing channel type made editable" "$FORM" '
old = "        {editing ? ("
assert old in s
s = s.replace(old, "        {false ? (")
' $FORM_T

# 26. A required field is no longer required, so the form sends a request the
#     server refuses with a message about a config key name.
mutate "required field check removed" "$FORM" '
old = "      if (spec.required && value === \"\") {"
assert old in s
s = s.replace(old, "      if (false) {")
' $FORM_T

# ---------------------------------------------------------------------------
# ROUTING AND NAVIGATION.

# 27. The notifications route stops being a place.
mutate "notifications route removed" "$ROUTE" '
old = """  if (segments.length === 1 && segments[0] === "notifications") {
    return { name: "notifications", create: false };
  }
"""
assert old in s
s = s.replace(old, "")
' src/shell/route.test.ts

# 28. /notifications/new stops opening the form, so a reload loses the drawer
#     and the empty state has nothing to link to.
mutate "channel create address stops opening the drawer" "$ROUTE" '
old = """  if (
    segments.length === 2 &&
    segments[0] === "notifications" &&
    segments[1] === CREATE_SEGMENT
  ) {
    return { name: "notifications", create: true };
  }
"""
assert old in s
s = s.replace(old, "")
' src/shell/route.test.ts

# 29. routePath forgets the create flag, so closing the drawer leaves the URL
#     on /notifications/new and Back reopens a form the user dismissed.
mutate "channel create flag dropped from the path" "$ROUTE" '
old = """  if (route.name === "notifications")
    return route.create ? NOTIFICATION_CREATE_PATH : NOTIFICATIONS_PATH;"""
assert old in s
s = s.replace(old, """  if (route.name === "notifications") return NOTIFICATIONS_PATH;""")
' src/shell/route.test.ts

# 30. The sidebar goes back to promising a screen that now exists.
mutate "notifications demoted to a Soon label" src/shell/Sidebar.tsx '
old = """const BUILT_CONFIG: readonly BuiltDestination[] = [
  {
    id: "notifications",
    label: "Notifications",
    Icon: NotificationsIcon,
    href: NOTIFICATIONS_PATH,
    route: "notifications",
  },
];
"""
assert old in s
s = s.replace(old, """const BUILT_CONFIG: readonly BuiltDestination[] = [];
""")
s = s.replace("""const PLANNED_CONFIG: readonly Destination[] = [""",
              """const PLANNED_CONFIG: readonly Destination[] = [
  { id: "notifications", label: "Notifications", Icon: NotificationsIcon },""")
' src/shell/shell.test.tsx

report
