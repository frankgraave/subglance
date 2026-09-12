/**
 * Ordering, grouping, filtering and announcing for the dashboard.
 *
 * All of it is pure and DOM-free, for the same reason the heartbeat maths is:
 * the hard decisions here are about *order and wording*, not markup. Whether a
 * broken monitor jumps to the top, whether a list re-sorts under the cursor,
 * and what a screen reader is told when 200 rows tick over are the questions
 * this file answers, and each of them is testable without a browser.
 */

import type { Monitor, MonitorStatus } from "./types";

/**
 * The one collator every ordering in the dashboard shares.
 *
 * Built once at module scope, not per comparison. `String.prototype.localeCompare`
 * with an options object is specified to construct a fresh `Intl.Collator` on
 * every call, and a sort of 200 names calls it ~1500 times: measured at 200
 * monitors, `partition` cost 4.2ms per call that way and 0.10ms with a hoisted
 * collator — a 40x difference on the function that runs for every single
 * heartbeat that arrives over the stream.
 *
 * A fixed locale rather than the visitor's: the tests, the server and two
 * different browsers must agree on the order, and `localeCompare` without an
 * explicit locale does not guarantee that.
 */
const NAME_COLLATOR = new Intl.Collator("en", { sensitivity: "base", numeric: true });

/**
 * Deterministic name ordering.
 *
 * The id breaks ties so two monitors sharing a name still have exactly one
 * correct order. That tiebreak is a plain `<` comparison, deliberately *not*
 * the collator: ids are opaque machine strings, and `sensitivity: "base"`
 * would call two ids differing only in case equal — leaving the tie unbroken,
 * which is the one thing a tiebreak may not do.
 *
 * `<` on strings orders by UTF-16 code unit, which differs from code-point
 * order for characters above the BMP (U+E000 sorts after U+10000 by code
 * unit, before it by code point). That is irrelevant here: ids arrive as
 * `String(api.id)` of an int64 (types.ts), so they are ASCII digits and the
 * two orders coincide. The only property the tiebreak needs is that it is
 * total and identical in every browser, which `<` is and `localeCompare`
 * — which without an explicit locale follows the visitor's — is not.
 */
function byName(a: Monitor, b: Monitor): number {
  const byLabel = NAME_COLLATOR.compare(a.name, b.name);
  if (byLabel !== 0) return byLabel;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export type Partitioned = {
  /** The short, volatile section at the top. Usually empty. */
  attention: Monitor[];
  /** Everything else, in a stable alphabetical order. */
  rest: Monitor[];
};

/**
 * Splits the list into "needs attention" and "everything else".
 *
 * **Only `down` is attention, deliberately — not `pending`.** Pending is the
 * ordinary state of a monitor that has not finished its first check yet, so a
 * fresh install or a restarted scheduler would fill the attention section with
 * monitors that are merely young. The section earns its position by being
 * almost always empty; diluting it with routine states teaches people to skip
 * it, which is exactly the failure mode it exists to prevent. Pending stays in
 * the main list, where its LED and label still say what is going on.
 *
 * **Paused is not attention either**, for a blunter reason: nobody is
 * watching it because the user said so.
 *
 * The main list never re-sorts on status, only on name (research note 4). A
 * row that moves out from under the cursor while you are reaching for it is
 * worse than a row in a slightly stale position — and with the attention
 * section on top, the thing you needed to see moved anyway.
 */
export function partition(monitors: readonly Monitor[]): Partitioned {
  const attention: Monitor[] = [];
  const rest: Monitor[] = [];
  for (const monitor of monitors) {
    (monitor.status === "down" ? attention : rest).push(monitor);
  }
  // Both halves sort by the same stable comparator, so the same input always
  // produces the same output — no dependence on the caller's array order.
  return { attention: attention.sort(byName), rest: rest.sort(byName) };
}

/**
 * Substring match on name and target, case-insensitive.
 *
 * Target is searched as well as name because at 200 monitors people look for
 * "the one on api.example.com" at least as often as they look for one by
 * name. Substring rather than fuzzy matching: fuzzy scoring turns "no match"
 * into "a bad match", and on a monitoring screen an unexpected row is worse
 * than an empty result (DESIGN.md §12 lists search as a blocking gap).
 *
 * An empty or whitespace-only query returns the input untouched, so "not
 * searching" costs nothing.
 */
export function filterMonitors(monitors: readonly Monitor[], query: string): Monitor[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") return [...monitors];
  return monitors.filter(
    (m) =>
      m.name.toLowerCase().includes(needle) || m.target.toLowerCase().includes(needle),
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

/**
 * Narrows the list to one status, or returns it untouched for `null`.
 *
 * Separate from `filterMonitors` rather than folded into it because the two
 * answer different questions and a caller may want either alone. `null` and
 * not the string `"all"`: "no status chosen" is the absence of a value, and
 * encoding it as a fifth status would leak a UI concept into a type that
 * mirrors what the API actually reports.
 */
export function filterByStatus(
  monitors: readonly Monitor[],
  status: MonitorStatus | null,
): Monitor[] {
  if (status === null) return [...monitors];
  return monitors.filter((m) => m.status === status);
}

/**
 * The sentence under the search box, or null when no filter is active.
 *
 * A pure function rather than JSX with two nested ternaries in it: the hard
 * part here is the *wording* of four combinations, and wording is exactly the
 * kind of thing that is worth asserting on in a test without mounting a
 * component. The shape is always "<visible> of <total> monitors ..." so the
 * first two numbers land in the same place whichever filters are on, and
 * someone glancing at it does not have to re-read the sentence to find them.
 */
export function describeFilter(
  visible: number,
  total: number,
  status: MonitorStatus | null,
  query: string,
): string | null {
  const needle = query.trim();
  if (status === null && needle === "") return null;
  // The verb agrees with the number actually on screen, so "1 of 14 monitors
  // is down" does not read like a bug report about the sentence itself.
  const one = visible === 1;
  const clauses: string[] = [];
  if (status !== null) clauses.push(`${one ? "is" : "are"} ${status}`);
  if (needle !== "") clauses.push(`${one ? "matches" : "match"} \u201C${needle}\u201D`);
  const tail = clauses.join(" and ");
  // Zero is reported as "0 of 14" rather than as prose. `EmptyState` already
  // owns the sentence about an empty result, and two different phrasings of
  // the same fact, six lines apart, read like a bug.
  return `${visible} of ${plural(total, "monitor")} ${tail}`;
}

export type Summary = Record<MonitorStatus, number> & { total: number };

/** Counts per status for the heading. */
export function summarise(monitors: readonly Monitor[]): Summary {
  const summary: Summary = { up: 0, down: 0, pending: 0, paused: 0, total: monitors.length };
  for (const monitor of monitors) summary[monitor.status] += 1;
  return summary;
}

/** How many names a live announcement will read out before summarising. */
export const MAX_ANNOUNCED_NAMES = 5;

function names(monitors: readonly Monitor[]): string {
  const shown = monitors.slice(0, MAX_ANNOUNCED_NAMES).map((m) => m.name);
  const hidden = monitors.length - shown.length;
  // A screen reader reading 200 names is not an announcement, it is a
  // filibuster. Past a handful the count carries the same information.
  return hidden > 0 ? `${shown.join(", ")} and ${hidden} more` : shown.join(", ");
}

/**
 * A sentence for the live region, or null when nothing worth saying happened.
 *
 * Null is the important half. The scheduler produces a result per monitor per
 * interval, and every one of those updates the latency, the uptime and the
 * heartbeat bar without changing anything a listener needs to hear. Only a
 * *status transition* is announced; routine ticks are silent. Without that
 * rule the live region would talk continuously and be turned off, taking the
 * outage announcement with it.
 *
 * The sentence describes the resulting state rather than the delta ("2
 * monitors down: api, db. 198 up.") because that is what someone who missed
 * the previous announcement needs — a delta is only meaningful if you heard
 * all the ones before it.
 */
export function describeTransitions(
  prev: readonly Monitor[],
  next: readonly Monitor[],
): string | null {
  const before = new Map(prev.map((m) => [m.id, m.status]));
  const changed = next.filter((m) => {
    const was = before.get(m.id);
    // A monitor that was not in the previous list is new, not a transition.
    // Announcing the whole list on first render would speak over the page.
    return was !== undefined && was !== m.status;
  });
  if (changed.length === 0) return null;

  const { down, up, pending, paused, total } = summarise(next);
  const downNames = names(next.filter((m) => m.status === "down").sort(byName));

  if (down > 0) {
    const parts = [`${plural(down, "monitor")} down: ${downNames}.`, `${up} up.`];
    if (pending > 0) parts.push(`${pending} pending.`);
    return parts.join(" ");
  }
  // Everything recovered. Say so explicitly: silence after an outage is
  // indistinguishable from a page that stopped updating.
  if (paused > 0 && up === 0) return `All ${plural(total, "monitor")} paused.`;
  if (pending > 0) return `No monitors down. ${up} up, ${pending} pending.`;
  return `All ${plural(up, "monitor")} up.`;
}
