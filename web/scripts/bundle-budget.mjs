/*
 * Bundle budget.
 *
 * The container image has a 30 MB gate in docker.yml; the JavaScript that ships
 * inside the binary had none, so a dependency that doubled it would land
 * silently. This is the equivalent gate for the frontend: it fails the build
 * when a bundle crosses its budget.
 *
 * Sizes are gzipped, because that is what a browser downloads and therefore the
 * only number a user experiences. Raw bytes are printed too, for context.
 *
 * The entry files are read out of index.html rather than globbed off disk.
 * `emptyOutDir` is deliberately false in vite.config.ts, so the assets
 * directory accumulates every build ever made on a working copy; a glob would
 * measure whichever stale chunk sorted first.
 *
 * Fonts are measured separately and NOT gzipped: woff2 is already Brotli
 * inside, so gzipping it again measures a number no browser ever downloads.
 * They are also the one category where the budget is the point rather than a
 * tripwire — an unsubsetted face is 350 kB, and the whole reason the subset
 * step exists is that nothing in the build would otherwise say so.
 */

import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const dist = resolve(dirname(fileURLToPath(import.meta.url)), "../../internal/webui/dist");

/*
 * Budgets in gzipped kilobytes, seeded a little above what the bundle measures
 * today so ordinary feature work does not trip them. Raising one is allowed —
 * it is meant to be a deliberate, reviewed act rather than an accident.
 */
const budgets = {
  /*
   * 112 -> 115 kB gzip, raised deliberately for SUB-122 (the monitors page).
   *
   * The measurement: 109,148 bytes before, 113,689 after — 4,541 bytes, of
   * which 2,920 were the headroom left under the old 112 kB ceiling. That is a
   * whole management screen: an inventory row with five settings columns and
   * four inline actions, a toolbar with three filters, an edit form, a delete
   * confirmation, a create drawer, and the data owner that drives pause,
   * resume, delete, check-now and a conditional PATCH.
   *
   * It is stated rather than nudged, and it is the smaller half of the story:
   * the CSS budget was NOT raised for the same screen. The page wears
   * `.mon-detail`'s column, `Card`, `Drawer`, `Value`, `StateChip`, `Led`,
   * `.add-button`, `.add-input` and `.mon-facet-select`, so the entire screen
   * cost 0.4 kB of CSS and stayed under the 13 kB ceiling SUB-34 set. The
   * behaviour is where a management screen's weight actually lives.
   *
   * Raised to 115 rather than 114 because the gate reads whole kilobytes and
   * 114 would leave 0.3 kB — a ceiling that has to move again on the next
   * bugfix is not a ceiling, it is a tripwire.
   *
   * 115 -> 120 kB gzip, raised deliberately for SUB-123 (the notifications
   * page).
   *
   * The measurement: 117,031 bytes before, 121,113 after — 4,082 bytes, of
   * which 729 were the headroom left under the old 115 kB ceiling. That is the
   * second management screen: a channel list, a five-type add/edit form whose
   * field set changes with the type and whose secret fields have a write-only
   * replace interaction, a delete confirmation, a per-row test button that
   * reports the upstream error, and the data owner that drives create, update,
   * delete and test.
   *
   * The CSS budget was NOT raised for the same screen, and that is the
   * interesting half: the page cost 37 bytes of CSS gzip, because it wears
   * `.mon-detail`'s column, `.inv-row`, `.inv-list`, `Card`, `Drawer`,
   * `StateChip`, `StatusChip`, `.add-*` and `.mon-facet-select` rather than
   * declaring a second copy of any of them. A whole screen for 0.04 kB of CSS
   * and 4 kB of behaviour is what reuse looks like when it works; the weight
   * of a management screen lives in what it does, not in how it is drawn.
   *
   * Raised to 120 rather than 119 because the gate reads whole kilobytes and
   * 119 would leave 0.7 kB, which is a tripwire rather than a ceiling — the
   * same argument that took the last raise to 115 rather than 114.
   *
   * 120 -> 122 kB gzip, raised deliberately for SUB-140 (the dashboard
   * chrome).
   *
   * The measurement: 122,771 bytes on develop, 122,918 after — 147 bytes,
   * against a 120 kB ceiling sitting at 122,880, so the change cleared the
   * remaining 109 bytes of headroom and went 38 over. This is the smallest
   * raise in the file's history and it is worth being explicit about what it
   * buys, because 147 bytes is the kind of number that gets waved through
   * without a reason: two new icons in `icons.tsx` (a tag for the facet
   * filters, a different glyph for Group by, because a control that looks
   * like a filter while narrowing nothing gets pressed twice), the `Card`
   * component replacing the rows layout's hand-drawn `.mon-board` frame so
   * the default list screen has the icon and counted title every other list
   * screen already had, and `aria-hidden` moving onto the `<svg>` itself
   * where it cannot be forgotten by a new consumer.
   *
   * The CSS budget was NOT raised: the framed toolbar, the sidebar's active
   * edge and the centred status cell cost 0.1 kB of CSS, and deleting the
   * down row's resting fill and `.mon-board`'s duplicate frame paid most of
   * that back.
   *
   * 122 rather than 121 for the reason the last two raises give: 121 would
   * leave 962 bytes, which this file has twice refused to call a ceiling.
   */
  /*
   * 122 -> 126 KiB gzip for the monitor/account/diagnostics batch.
   * The index.html entry measured 122,972 bytes at f0c52fa and 127,325 in
   * the combined build, including the existing TLS-form and resolved-history
   * changes: 4,353 bytes for working password settings, dirty-form navigation,
   * detail checks, stable response disclosures and their API/client plumbing.
   * Independent branches fit 122, but their combination does not; keeping that
   * ceiling would make individually green reviews fail as soon as they meet.
   * 126 KiB leaves 1,699 bytes rather than tracking the measured size exactly.
   * No new runtime dependency was added: axe remains dev-only. CSS and font
   * ceilings are unchanged. This is one shared raise, not one per feature.
   */
  /*
   * SUB-33: 126 -> 128 KiB. Before: 127,984 bytes gzip; after: 129,747
   * bytes (entry assets measured with this script's Node gzip). The 1,763
   * bytes add one-off/weekly maintenance controls, timezone and tag inputs,
   * cancellation, errors and historical exclusion labels. No runtime dependency
   * was added; existing forms and cards keep CSS below its unchanged 15 KiB.
   * 128 KiB leaves 1,325 bytes, rather than setting a ceiling at today's size.
   */
  /*
   * 126 -> 130 KiB gzip for SUB-68 / SUB-91.
   * The current develop (05974de) entry measures 127,401 bytes; this combined
   * build measures 130,955: 3,554 bytes for expanded versioned detail editing,
   * reusable previews, conflict/reload handling, repeat controls and reminder
   * status. Both builds include the merged TLS, incident and response-history
   * UI, so their weight is not attributed to this change. No runtime dependency
   * was added; editor/drawer/preview controls are shared, not duplicated.
   * 128 KiB would leave only 117 bytes. 130 leaves 2,165 for ordinary fixes
   * instead of tracking today's measurement. CSS and font ceilings stay put.
   */
  /*
   * 126 -> 136 KiB JS and 15 -> 16 KiB CSS for SUB-25/84/87/113 together.
   * At pinned develop 05974de the gzip entries measured JS 127,401 / CSS
   * 14,770 bytes. The four-ticket build measures 132,119 / 15,386; alongside
   * the existing edit/reminder PR83 it measures 135,270 / 15,444. Independent
   * branches fit unevenly (bulk tags alone is 379 JS bytes over); a shared
   * ceiling keeps merge order from deciding which reviewed feature fails CI.
   * These bytes buy keyboard commands, atomic bulk-tag workflows, watchdog
   * diagnostics and measured surface consistency, without a runtime dependency.
   * 136/16 KiB leaves 3,994 JS / 940 CSS bytes for fixes in the full combination,
   * not a ceiling nudged to today's exact size. Font ceiling stays unchanged.
   */
  /*
   * Integration of c89a4b0 with SUB-145/44/109/116/115/33 retains the existing
   * 136/16 KiB ceilings. That historical entry measured 135,989 JS / 15,352 CSS
   * gzip bytes (Node gzip, index.html assets), versus 127,401 / 14,770 at the
   * pinned 05974de base. That build included bulk tags, versioned editing/reminders,
   * watchdog status, warning/history and maintenance with truthful reminder
   * suppression reads (before command-palette integration). Its headroom was
   * 3,275 JS / 1,032 CSS bytes; neither a higher ceiling nor feature removal
   * was needed for that build. Fonts measured 69,412 bytes against the
   * unchanged 80 KiB ceiling. Run this script for current measurements.
   */
  /*
   * 136 -> 140 KiB gzip for SUB-23 (the latency chart on the detail page).
   * Develop at 3b94355 measures 134.7 KiB JS; this branch measures 137.2, so
   * the chart costs about 2.5 KiB (gzip -9 of the entry: 137,483 -> 140,026
   * bytes). That buys the stepped-series model, the hand-drawn plot with its
   * keyboard readout and screen-reader table, and the window control, with no
   * chart library (DESIGN.md §10) and no new runtime dependency. 138 would
   * leave under 1 KiB for the next fix; 140 leaves about 2.8 KiB, the same
   * order of headroom the previous raise left. CSS and fonts stay put.
   */
  /*
   * 140 -> 142 KiB gzip for SUB-124 (quiet hours in the channel form).
   * Develop at 13edb6e measures 142,369 bytes of entry JS (Node gzip); this
   * branch measures 144,014: 1,645 bytes for the quiet-hours fieldset, its
   * client-side validation, the change detection that keeps an edit from
   * releasing a held night early, the row chip and the partial-save message.
   * No runtime dependency: the timezone list comes from the browser's own
   * `Intl.supportedValuesOf`. 141 would leave 370 bytes; 142 leaves 1,394,
   * enough for a fix without tracking today's size. CSS and fonts stay put.
   *
   * 142 -> 144 KiB gzip for SUB-28 (the retention card on /settings).
   * Develop at ff476c3 measures 144,203 bytes of entry JS (Node gzip); this
   * branch measures 146,329: 2,126 bytes for a card that reads the windows
   * in force and the four tables' measured size and growth, a form with a
   * per-window "forever" switch, the estimate beside each field, the
   * preview that counts what a shorter window removes before it is saved,
   * pinned-by-flag read-only states, and a shape check on the response so a
   * malformed window is never shown (and saved back) as "forever". No new
   * dependency. 143 would leave 103 bytes; 144 leaves 1,127. CSS measures
   * 16,283 of 16,384 and stays put, because the card reuses the auth form's
   * field, label, error and button rules rather than declaring its own.
   *
   * 144 -> 147 KiB gzip for SUB-28 (the API tokens card on /settings).
   * Develop at 4916aee measures 146,392 bytes of entry JS (Node gzip); this
   * branch measures 148,372: 1,980 bytes for a card that lists the session's
   * tokens with role, prefix, last use and expiry, a create form with a role
   * capped at the account's own and an expiry, the shown-once secret panel
   * with copy and an explicit dismissal, a two-step revoke, and a shape check
   * that refuses a malformed list instead of drawing a revoked token as live.
   * No new dependency. 146 would leave 1,132 bytes, which the instance card
   * open beside this branch (1,489 bytes by its own measurement) would not
   * fit in; 147 covers both, so neither fails on whichever merges second.
   * CSS measures 16,374 of 16,384 and stays put: the card declares two
   * rules and reuses the auth form, the push-URL reveal and retention notes.
   */
  js: 147,
  /*
   * 12 -> 13 kB gzip, raised deliberately for SUB-34 (the incidents screen).
   *
   * The measurement: 12,259 bytes before, 12,715 after — 456 bytes, of which
   * 29 were the headroom left under the old 12 kB ceiling. That is a whole new
   * screen: a row with seven columns, an inline-expanding detail with a
   * timeline and a code block, a cluster wrapper, and a day-grouped history —
   * after being made to reuse everything that already existed. The row borrows
   * `Card`, `Panel`, `StatusChip`, `Value`, `Led` and `.add-button`; the screen
   * wears the detail page's own column classes rather than declaring a second
   * copy; and the incident rules deleted from `detail.css` paid for part of it.
   *
   * Raised to 13 rather than to 12.5 because the gate reads whole kilobytes.
   * It is stated here rather than nudged, which is the whole point of the
   * budget: a ceiling that moves quietly is not a ceiling.
   */
  /*
   * 13 -> 14 kB gzip, raised deliberately for SUB-137 (the size ladder and
   * the living style guide).
   *
   * The measurement: 13,213 bytes on develop, 13,510 after — 297 bytes, and
   * the 13 kB ceiling sat at 13,312, so 99 bytes of it were headroom already
   * spent. What the 297 bytes buy: 33 named size rungs, three breakpoint
   * rungs, and the replacement of 88 literal pixel values across 26
   * stylesheets with var() references.
   *
   * It is worth stating plainly that the ladder COSTS bytes rather than
   * saving them. `var(--size-col-md)` is sixteen characters where `88px` is
   * four, and gzip only partly closes that gap because the literals were
   * already repeating. The trade is deliberate: 297 bytes against a class of
   * drift that no review reliably catches, measured at 88 occurrences in a
   * codebase that had zero literal colours under an equivalent guard.
   *
   * The ceiling moves to 14 rather than to 13.5 because a budget that tracks
   * the current measurement to the byte is not a budget, it is a record of
   * what happened.
   *
   * 14 -> 15 kB gzip, raised deliberately for SUB-140 and SUB-142 (draining
   * the status rail, and giving it its own rung).
   *
   * The measurement: 14,325 bytes on develop, 14,479 after — 154 bytes,
   * against a 14 kB ceiling sitting at 14,336, so the change spent the last
   * 11 bytes of headroom and went 143 over. What the 154 bytes buy:
   *
   *   - the §6 drain for the one mark that was still asserting at full
   *     strength after the stream died. PR #63 removed the `--down-dim` row
   *     fill, which promoted the 2px coloured edge to the row's main visual
   *     carrier, and the edge was the single carrier §6's filter-based drain
   *     could not reach. Six selectors across three layouts plus the detail
   *     pill, and two `-drained` tones per theme.
   *   - `.mon-error` and `.mon-card-error` joining the ink-drain, which two
   *     of the three layouts were missing — a full-strength red failure
   *     sentence beside an already-drained lamp.
   *   - `--size-status-rail`, replacing a literal `2px` in eight border
   *     shorthands and one `var(--outline-w)` that named a focus outline.
   *
   * Consolidating the four `transition` declarations into one selector list
   * was tried first, to stay under the ceiling, and it was measured at 14,510
   * — 31 bytes WORSE. An extra four-selector rule does not compress as well
   * as a declaration gzip has already seen; the duplication is what is
   * cheap. That is recorded here because "just dedupe it" is the obvious
   * review suggestion and it makes the number go the wrong way.
   *
   * The ceiling moves to 15 rather than to 14.5 for the same reason the last
   * raise went to 14: 14.5 would leave 369 bytes, which is two more bugfixes
   * and then another raise. It is also the first CSS raise since SUB-137 —
   * two whole management screens (SUB-122, SUB-123) landed in between and
   * cost 0.4 kB and 37 bytes respectively, because they reused what was
   * already declared. A ceiling that holds through two screens and moves for
   * a change to what colour means is a ceiling doing its job.
   *
   * 16 -> 17 kB gzip, raised deliberately for SUB-28 (the settings page's
   * section index). Measured: 16,380 bytes on develop against a ceiling of
   * 16,384, so four bytes of headroom were left before this change, and
   * 16,527 after it — 147 bytes. They buy the two-column layout (a sticky
   * index beside the cards), its single-column form below the tablet rung,
   * and the `scroll-margin-top` that lands a followed link below the sticky
   * masthead instead of under it. The "you are here" paint is the sidebar's
   * own `[data-state="current"]` rule, reused rather than restated. At 16.5
   * the next card on this page would move it again.
   *
   * SUB-149 (list rows that fit the column they are given) spends about 140
   * bytes more inside the same ceiling: two container queries that replace
   * two viewport media queries, the container declarations, and the channel
   * row's own copy of the wrap rules (a container condition cannot be a
   * variable, so the inventory's block cannot serve a second width).
   */
  css: 17,
  fonts: 80,
};

/*
 * A font only counts once the link that fetches it is complete. A `.woff2`
 * href alone measures a file the browser may never request in the first round
 * trip: without `rel="preload"` it is not preloaded at all, without
 * `as="font"` it is fetched at the wrong priority and again by the stylesheet,
 * and without `crossorigin` the preload is discarded and fetched twice. So the
 * budget is spent on links that are actually doing the job, and a link that
 * loses one of those attributes reads here as a font that stopped being
 * preloaded.
 */
function fontPreloads(html) {
  const preloaded = [];
  for (const tag of html.matchAll(/<link\b[^>]*>/g)) {
    const attrs = tag[0];
    const href = /\shref="\/?((?:assets|fonts)\/[^"]+\.woff2)"/.exec(attrs);
    if (!href) continue;
    if (!/\srel="preload"/.test(attrs)) continue;
    if (!/\sas="font"/.test(attrs)) continue;
    if (!/\scrossorigin(?:="(?:anonymous|use-credentials)?")?[\s/>]/.test(attrs)) continue;
    preloaded.push(href[1]);
  }
  return preloaded;
}

function entries(html) {
  const found = { js: [], css: [], fonts: fontPreloads(html) };
  for (const match of html.matchAll(/(?:src|href)="\/?((?:assets|fonts)\/[^"]+)"/g)) {
    const path = match[1];
    if (path.endsWith(".js")) found.js.push(path);
    else if (path.endsWith(".css")) found.css.push(path);
  }
  return found;
}

const html = readFileSync(join(dist, "index.html"), "utf8");
const found = entries(html);

if (found.js.length === 0) {
  console.error("bundle-budget: index.html references no JavaScript — did the build run?");
  process.exit(1);
}

/*
 * The fonts reach index.html through preload links. If someone drops those the
 * faces still load, from the stylesheet — so an empty list here is a real
 * regression in loading behaviour, not an absence to skip over. The check sits
 * after the filtering above, so an incomplete link counts as no preload.
 */
if (found.fonts.length === 0) {
  console.error(
    "bundle-budget: index.html has no complete font preload " +
      '(rel="preload" as="font" crossorigin) — see src/styles/fonts.css',
  );
  process.exit(1);
}

let failed = false;
for (const [kind, budget] of Object.entries(budgets)) {
  let raw = 0;
  let gzipped = 0;
  for (const path of found[kind]) {
    const bytes = readFileSync(join(dist, path));
    raw += bytes.byteLength;
    gzipped += kind === "fonts" ? bytes.byteLength : gzipSync(bytes).byteLength;
  }
  if (found[kind].length === 0) continue;

  const kb = gzipped / 1024;
  const rawKb = raw / 1024;
  const line =
    kind === "fonts"
      ? `${kind}: ${kb.toFixed(1)} kB over the wire, budget ${budget} kB`
      : `${kind}: ${kb.toFixed(1)} kB gzip (${rawKb.toFixed(1)} kB raw), budget ${budget} kB`;
  if (kb > budget) {
    console.error(`::error::${line} — over budget`);
    failed = true;
  } else {
    console.log(`${line} — ok`);
  }
}

process.exit(failed ? 1 : 0);
