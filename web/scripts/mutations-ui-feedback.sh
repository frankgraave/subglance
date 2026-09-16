#!/usr/bin/env bash
# Mutation verification for the UI feedback round: SUB-131 to SUB-135 plus the
# tooltip transparency and stacking fix.
#
# Same model as mutations-sub34.sh and mutations-sub122.sh. Each mutation
# reverts one decision the tests claim to protect, so the suite MUST go red. A
# mutation that leaves it green is a test that does not bite, and the fix for
# that is the test, never the mutation. The exit status is the verdict: a
# survivor, or a pattern that no longer applies, fails this script.
#
# What is different here, and why it is worth the extra machinery: five of the
# six points in this round are about CSS and composition, and jsdom has no
# layout engine. A mutation that makes the drawer transparent again, or drops
# the tooltip back under the sticky bar, cannot be caught by any assertion the
# default suite is able to make — so those mutations are run against the
# browser suite instead, which builds the bundle and measures a real Chromium.
# They are slower by roughly a minute each, and that is the price of being able
# to fail at all.
#
# Every file is restored in a trap, so an interrupt cannot leave the tree
# mutated.
set -uo pipefail
# `|| exit` is not decoration: without it a failed cd leaves the script running
# in the caller's directory, where `save`, the mutation and `restore` would
# rewrite whatever files happen to match those relative paths.
cd "$(dirname "$0")/.." || exit 1

BAK=$(mktemp -d)
# The transcript lives OUTSIDE $BAK: the restore loop copies every file in
# there back to the path its name encodes, and a plain `out.txt` encodes
# `./out.txt`, which would write a stray file into the repository on every run.
LOG=$(mktemp)
trap 'for f in "$BAK"/*; do [ -e "$f" ] || continue; n=$(basename "$f" | tr "%" "/"); cp "$f" "$n"; done; rm -rf "$BAK" "$LOG"' EXIT

save() { cp "$1" "$BAK/$(echo "$1" | tr '/' '%')"; }
restore() { cp "$BAK/$(echo "$1" | tr '/' '%')" "$1"; }

SURVIVED=()
MISSED=()

# The browser suite needs a bundle on disk, and the bundle is what it measures
# — so a browser mutation has to be rebuilt before it can be observed. Built
# once up front as well, so the first browser mutation is not the thing that
# discovers a broken build.
build() { npm run build > "$LOG" 2>&1; }

run_suite() { # kind testfiles...
  local kind="$1"; shift
  if [ "$kind" = "browser" ]; then
    if ! build; then
      echo "!!! BUILD FAILED — nothing to measure"
      tail -5 "$LOG"
      return 1
    fi
    npx vitest run --config vitest.browser.config.ts "$@" > "$LOG" 2>&1
  else
    npx vitest run "$@" > "$LOG" 2>&1
  fi
}

mutate() { # name kind file python-replace-script testfiles...
  local name="$1" kind="$2" file="$3" script="$4"; shift 4
  save "$file"
  python3 - "$file" <<PY || { echo "### $name: PATTERN MISSED"; MISSED+=("$name"); restore "$file"; return; }
import sys
p = sys.argv[1]
s = open(p).read()
$script
open(p, "w").write(s)
PY
  echo "### MUTATION ($kind): $name"
  local status=0
  run_suite "$kind" "$@" || status=$?
  grep -E "Tests +[0-9]+ (failed|passed)|AssertionError|→" "$LOG" | head -6
  #
  # A non-zero exit is not by itself proof that the mutation was caught.
  #
  # A missing node_modules, an unresolvable npx, a broken config, a failed
  # build or a TypeScript error unrelated to the mutation all exit non-zero too
  # — and a harness that read those as "caught" would report every mutation
  # green without a single assertion having run, which is the one failure mode
  # a verification tool may not have. So the log has to show that vitest got
  # far enough to print a summary before the exit status means anything.
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

APP=src/App.tsx
TOPBAR=src/shell/Topbar.tsx
SLOT=src/shell/topbarSlot.ts
VIEW=src/monitors/MonitorsView.tsx
ROW=src/monitors/MonitorInventoryRow.tsx
INC=src/incidents/IncidentsView.tsx
TOKENS=src/styles/tokens.css
HB=src/heartbeat/heartbeat.css
DRAWER=src/components/drawer.css
TABLE=src/monitors/MonitorTable.tsx
MONCSS=src/monitors/monitors.css

BROWSER_TEST=src/layout/drawer-stacking.browser.test.ts

# Build once before anything, so a browser mutation's first failure is the
# mutation rather than a stale bundle.
build || { echo "the bundle does not build before any mutation — fix that first"; exit 1; }

# ---------------------------------------------------------------------------
# SUB-131 — the toolbar is route-bound.

# 1. The layout switcher goes back to appearing on every screen, including the
#    two that have no layouts to switch between.
mutate "layout switcher returns to every screen" jsdom "$APP" '
old = """          showLayouts={
            !workbenchOpen && !onDetail && !onIncidents && !onMonitors
          }"""
assert old in s
s = s.replace(old, "          showLayouts")
' src/App.test.tsx

# 2. The other direction, which a test asserting only absence would miss: the
#    switcher disappears from the dashboard too.
mutate "layout switcher disappears entirely" jsdom "$APP" '
old = """          showLayouts={
            !workbenchOpen && !onDetail && !onIncidents && !onMonitors
          }"""
assert old in s
s = s.replace(old, "          showLayouts={false}")
' src/App.test.tsx

# 3. The page slot stops publishing itself, so every screen that contributes
#    controls to the toolbar contributes nothing and says nothing about it.
mutate "topbar slot never published" jsdom "$SLOT" '
old = """  if (slot === node) return;
  slot = node;"""
assert old in s
s = s.replace(old, """  if (slot === node) return;
  slot = null;""")
' src/monitors/MonitorsView.test.tsx

# ---------------------------------------------------------------------------
# SUB-132 — one add button, one outcome.

# 4. The dashboard goes back to replacing the whole screen with the form, which
#    is the reported inconsistency: same icon, and the monitor list vanishes.
mutate "dashboard add replaces the screen again" browser "$APP" '
old = """      {!onMonitors && (
        <Drawer open={addOpen} onClose={closeAdd} title=\"Add monitor\">"""
assert old in s
s = s.replace(old, """      {!onMonitors && false && (
        <Drawer open={addOpen} onClose={closeAdd} title=\"Add monitor\">""")
' "$BROWSER_TEST"

# 5. The form loses the surfaces it is supposed to wear inside the drawer and
#    goes back to bare fields on the panel background.
mutate "add form loses its card and panel" jsdom "$APP" '
old = """          <Card title=\"New monitor\" headingLevel={3}>
            <Panel>
              <AddMonitor onCreated={onMonitorCreated} onCancel={closeAdd} />
            </Panel>
          </Card>"""
assert old in s
s = s.replace(old, """          <AddMonitor onCreated={onMonitorCreated} onCancel={closeAdd} />""")
' src/App.test.tsx

# 6. The drawer goes back to a translucent fill, which is what made the monitor
#    list readable straight through the form. jsdom applies no CSS, so only the
#    browser suite can see this at all.
mutate "drawer becomes translucent again" browser "$DRAWER" '
old = "  background: var(--surface-float);"
assert old in s
s = s.replace(old, "  background: var(--surface);")
' "$BROWSER_TEST"

# 7. (retired) There was a mutation here that put the drawer back on its old
#     `z-index: 51` / `50` pair, on the theory recorded in SUB-132 that a
#     sticky ancestor was trapping it in a stacking context and that this was
#     why the monitor list showed through.
#
#     It survived, and that survival is the finding rather than a gap: measured
#     in a real browser on `develop`, the drawer at 51 already hit-tested and
#     painted above the page on every screen, with the scrim over the content
#     behind it. The stacking-context explanation was a plausible reading of
#     the CSS and it was wrong — what the screenshot actually showed was the
#     transparency (mutation 6), which is a different defect with a different
#     cause that happens to look identical.
#
#     So the mutation is gone rather than the test weakened: there is no defect
#     here for it to reintroduce. The z-index tokens stay, because the tooltip
#     next door genuinely was trapped (mutation 18) and one ladder in one place
#     is what stops the next number being guessed — but they are a tidying of
#     the drawer, not a fix to it, and saying otherwise would put a repair in
#     the changelog for a bug that never existed.

# ---------------------------------------------------------------------------
# SUB-133 — two surfaces, not three.

# 8. The Panel wrapper returns around the open incidents, which is exactly the
#    construction PR #42 deleted and this ticket found back.
mutate "third surface returns to the incidents list" jsdom "$INC" '
old = """          <div className=\"inc-body\">
            {ackError !== null ? ("""
assert old in s
s = s.replace(old, """          <Panel padded={false}>
            {ackError !== null ? (""")
old2 = """              </ul>
            )}
          </div>
        </Card>
      )}"""
assert old2 in s
s = s.replace(old2, """              </ul>
            )}
          </Panel>
        </Card>
      )}""")
' src/incidents/IncidentsView.test.tsx

# 9. The same wrapper on the resolved card, where the day heading was reading
#    as a card layer of its own.
mutate "third surface returns to the resolved history" jsdom "$INC" '
old = """          <div className=\"inc-body\">
            {historyTruncated ? ("""
assert old in s
s = s.replace(old, """          <Panel padded={false}>
            {historyTruncated ? (""")
old2 = """              </section>
            ))}
          </div>
        </Card>
      )}"""
assert old2 in s
s = s.replace(old2, """              </section>
            ))}
          </Panel>
        </Card>
      )}""")
' src/incidents/IncidentsView.test.tsx

# ---------------------------------------------------------------------------
# SUB-134 — filters in the toolbar, actions as icons with their names intact.

# 10. THE defect this point could most easily ship: the icons lose the monitor
#     name from their accessible names, so forty rows become forty buttons all
#     called "Pause" — unnavigable by screen reader, unaddressable by voice.
mutate "icon actions become indistinguishable" jsdom "$ROW" '
old = "              aria-label={`${pauseWord} ${monitor.name}`}"
assert old in s
s = s.replace(old, "              aria-label={pauseWord}")
' src/monitors/MonitorsView.test.tsx src/monitors/LiveMonitors.test.tsx

# 11. The same for edit, which is the action a voice user is most likely to
#     ask for by name.
mutate "edit icon loses the monitor name" jsdom "$ROW" '
old = "              aria-label={`Edit ${monitor.name}`}"
assert old in s
s = s.replace(old, "              aria-label=\"Edit\"")
' src/monitors/MonitorsView.test.tsx

# 12. Delete becomes an icon like the other three, which puts the whole of
#     "this is the destructive one" into a colour (DESIGN.md §2.3).
mutate "delete loses its word and keeps only colour" jsdom "$ROW" '
old = """              Delete
            </button>"""
assert old in s
s = s.replace(old, """              <IconPencil />
            </button>""")
' src/monitors/MonitorsView.test.tsx

# 13. The counter is left behind in the card while the filters move up, so the
#     claim "3 of 3 shown" is stranded from the controls that make it true.
mutate "filter count stranded from its filters" jsdom "$VIEW" '
old = """          <p className=\"mon-result-count\" role=\"status\">
            {loading
              ? \"Loading monitors…\"
              : `${visible.length} of ${monitors.length} shown`}
          </p>"""
assert old in s
s = s.replace(old, "")
' src/monitors/MonitorsView.test.tsx

# 14. The filters stop reaching the toolbar at all, which is the regression a
#     later refactor would most plausibly cause.
mutate "filters never reach the toolbar" jsdom "$VIEW" '
old = "      <TopbarTools>"
assert old in s
s = s.replace(old, "      <TopbarTools>{false && (")
s = s.replace("      </TopbarTools>", "      )}</TopbarTools>")
' src/monitors/MonitorsView.test.tsx

# ---------------------------------------------------------------------------
# SUB-135 — the dashboard's composition.

# 15. The lamp column's header goes back to being visually empty, which is the
#     blank block beside the column titles Frank pointed at.
mutate "status header hidden again" jsdom "$TABLE" '
old = """        <th scope=\"col\" className=\"mon-head\">
          Status
        </th>"""
assert old in s
s = s.replace(old, """        <th scope=\"col\" className=\"mon-head\">
          <span className=\"sr-only\">Status</span>
        </th>""")
' src/monitors/MonitorTable.test.tsx

# 16. The name and the target collapse back into one ink, which is the state
#     the browser measured before this round: both computed to oklch(.708).
mutate "monitor name and target share one ink" browser "$MONCSS" '
old = """.mon-line-name,
.mon-card-name > a {
  color: inherit;
}"""
assert old in s
s = s.replace(old, """.mon-name,
.mon-line-name,
.mon-card-name > a {
  color: inherit;
}""")
' "$BROWSER_TEST" src/layout/type-roles.browser.test.ts

# ---------------------------------------------------------------------------
# The tooltip: transparency and stacking.

# 17. The readout goes back to a 5% white fill, so the rows behind it are
#     readable straight through the thing that is meant to explain them.
mutate "tooltip becomes translucent again" browser "$HB" '
old = "  background: var(--surface-float);"
assert old in s
s = s.replace(old, "  background: var(--surface-2);")
' "$BROWSER_TEST"

# 18. The float rung goes and the old 10 comes back, which is the value that
#     lost to the sticky topbar and clipped the readout at the top.
mutate "tooltip returns under the sticky bar" browser "$HB" '
old = "  z-index: var(--z-float);"
assert old in s
s = s.replace(old, "  z-index: 10;")
' "$BROWSER_TEST"

# 19. The opaque token becomes an alpha again at the token level rather than at
#     a call site. This is the one that matters most: it is the change someone
#     makes while "restoring consistency with the other surfaces", and it
#     silently reintroduces the defect at all four call sites at once.
mutate "the floating token goes back to an alpha" browser "$TOKENS" '
old = "  --surface-float: oklch(.269 0 0);"
assert old in s
s = s.replace(old, "  --surface-float: rgba(255, 255, 255, .05);")
' "$BROWSER_TEST"

report
