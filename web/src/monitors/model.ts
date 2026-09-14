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
const NAME_COLLATOR = new Intl.Collator("en", {
  sensitivity: "base",
  numeric: true,
});

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
export function filterMonitors(
  monitors: readonly Monitor[],
  query: string,
): Monitor[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") return [...monitors];
  return monitors.filter(
    (m) =>
      m.name.toLowerCase().includes(needle) ||
      m.target.toLowerCase().includes(needle),
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
 * Orders two tag strings the way the collator orders names, but totally.
 *
 * The shared `NAME_COLLATOR` is `sensitivity: "base"`, so it reports `Prod`
 * and `prod` as equal. Those are two different tag values — a facet has to
 * offer both, in a fixed order — so the raw comparison breaks the tie for the
 * same reason `byName` does: a tie left unbroken makes the rendered order
 * depend on which monitor the API happened to send first.
 */
function compareText(a: string, b: string): number {
  const byLabel = NAME_COLLATOR.compare(a, b);
  if (byLabel !== 0) return byLabel;
  return a < b ? -1 : a > b ? 1 : 0;
}

/** One tag key and every value seen for it, both in a stable order. */
export type TagFacet = {
  key: string;
  values: string[];
};

/**
 * The tag keys present in the data, each with its distinct values.
 *
 * Derived from the monitors rather than from a fixed list because there is no
 * tag registry: a key exists exactly as long as some monitor carries it. That
 * is also why a facet disappears when its last monitor loses the tag — an
 * offered filter that can only ever return nothing is worse than no filter.
 *
 * Both levels are sorted, so the controls do not reorder themselves when a
 * heartbeat arrives and the list is rebuilt in a different order.
 */
export function tagFacets(monitors: readonly Monitor[]): TagFacet[] {
  const seen = new Map<string, Set<string>>();
  for (const monitor of monitors) {
    for (const [key, value] of Object.entries(monitor.tags)) {
      const values = seen.get(key);
      if (values === undefined) seen.set(key, new Set([value]));
      else values.add(value);
    }
  }
  return [...seen.entries()]
    .map(([key, values]) => ({ key, values: [...values].sort(compareText) }))
    .sort((a, b) => compareText(a.key, b.key));
}

/** A chosen value per tag key. An absent key means "any value for this key". */
export type TagSelection = Readonly<Record<string, string>>;

/**
 * Narrows the list to monitors carrying every chosen key/value pair.
 *
 * **AND across keys, one value per key.** Picking `env: prod` and then
 * `customer: acme` means "the acme monitors in production", which is the
 * question someone with 200 monitors actually asks; OR across different keys
 * would widen the list as you add controls, which reads as the filter being
 * broken. Within a key the choice is single-valued because the backend stores
 * at most one value per key per monitor (types.ts), so "env is prod or
 * staging" is the only multi-select that would mean anything — and that needs
 * a control this screen does not have yet.
 *
 * An empty selection returns a copy, so "not filtering" costs one array copy
 * and no special case at the call site.
 */
export function filterByTags(
  monitors: readonly Monitor[],
  selected: TagSelection,
): Monitor[] {
  const pairs = Object.entries(selected).filter(([, value]) => value !== "");
  if (pairs.length === 0) return [...monitors];
  return monitors.filter((m) =>
    pairs.every(([key, value]) => m.tags[key] === value),
  );
}

/** The label shown for monitors that do not carry the grouping key at all. */
export const UNTAGGED_LABEL = "Untagged";

/** One rendered group: a tag value, or `null` for the monitors lacking the key. */
export type MonitorGroup = {
  /** The tag value, or null when the monitors in this group lack the key. */
  value: string | null;
  /** What the section heading says. */
  label: string;
  /** The group's monitors, in the shared name order. */
  monitors: Monitor[];
};

/**
 * Splits monitors into one group per value of a tag key.
 *
 * **Grouping is not filtering.** Filtering answers "show me production";
 * grouping answers "show me everything, arranged by environment". So every
 * monitor handed in comes back out exactly once — including the ones that do
 * not carry the key, which land in a final `Untagged` group rather than
 * disappearing. A monitor silently missing from a monitoring screen is the
 * worst failure this component can have, and "it had no tag" is not a reason
 * the viewer can see.
 *
 * The untagged group is last rather than first, and it is the only group whose
 * position is not alphabetical: it is a residue, not a value, and sorting it
 * in among real values under some placeholder name would claim a tag that
 * nobody assigned.
 *
 * Groups sort by value and monitors sort by name, both with the same
 * comparators the rest of the dashboard uses, so nothing reorders when a
 * heartbeat rebuilds the list in a different order. Empty groups cannot occur:
 * a group exists exactly because a monitor is in it.
 */
export function groupByTag(
  monitors: readonly Monitor[],
  key: string,
): MonitorGroup[] {
  const byValue = new Map<string, Monitor[]>();
  const untagged: Monitor[] = [];
  for (const monitor of monitors) {
    const value = monitor.tags[key];
    if (value === undefined) {
      untagged.push(monitor);
      continue;
    }
    const bucket = byValue.get(value);
    if (bucket === undefined) byValue.set(value, [monitor]);
    else bucket.push(monitor);
  }
  const groups: MonitorGroup[] = [...byValue.entries()]
    .sort(([a], [b]) => compareText(a, b))
    .map(([value, group]) => ({
      value,
      label: value,
      monitors: group.sort(byName),
    }));
  if (untagged.length > 0) {
    groups.push({
      value: null,
      label: UNTAGGED_LABEL,
      monitors: untagged.sort(byName),
    });
  }
  return groups;
}

/** One headed section of a grouped list. */
export type MonitorSection = {
  /** Stable React key; unique within one call. */
  id: string;
  /** Heading text, without the count. */
  label: string;
  monitors: Monitor[];
  /** True for the "needs attention" section, which is styled apart. */
  attention: boolean;
};

/**
 * The headed sections a grouped list renders, attention section included.
 *
 * **The attention section survives grouping, and it is taken out first.** This
 * is the decision grouping could not be built without. A down monitor sorted
 * into its environment's group would sit wherever that group happens to fall —
 * three headings down, possibly off-screen — and "needs attention" would stop
 * meaning the same thing in every layout, which is the one invariant
 * `partition` exists to hold. So the down monitors are lifted out before
 * grouping and keep their own section at the top; the groups below describe
 * the monitors that are fine, which is the question grouping is actually
 * asked: "how is production doing", not "where is the broken one".
 *
 * The cost is that a group's count excludes its down monitors, which is why
 * the attention heading carries its own count: the two numbers together always
 * add up to the list, and neither claims to be the whole.
 */
export function sectionsByTag(
  monitors: readonly Monitor[],
  key: string,
): MonitorSection[] {
  const { attention, rest } = partition(monitors);
  const sections: MonitorSection[] = [];
  if (attention.length > 0) {
    sections.push({
      id: "attention",
      label: "Needs attention",
      monitors: attention,
      attention: true,
    });
  }
  for (const group of groupByTag(rest, key)) {
    sections.push({
      // The `null` value is a residue, not a tag, so it gets its own key
      // rather than one built from a label a real tag could also produce.
      // Real values are percent-encoded: the id ends up in `aria-labelledby`,
      // and a value like `US East` would otherwise split into two id tokens
      // that match nothing, costing the section its accessible name.
      id:
        group.value === null
          ? "untagged"
          : `tag:${encodeURIComponent(group.value)}`,
      label: group.label,
      monitors: group.monitors,
      attention: false,
    });
  }
  return sections;
}

/** The chosen pairs as `key:value`, in the order `tagFacets` renders them. */
export function describeTags(selected: TagSelection): string[] {
  return Object.entries(selected)
    .filter(([, value]) => value !== "")
    .sort(([a], [b]) => compareText(a, b))
    .map(([key, value]) => `${key}:${value}`);
}

/**
 * The sentence under the search box, or null when no filter is active.
 *
 * A pure function rather than JSX with two nested ternaries in it: the hard
 * part here is the *wording* of the filter combinations, and wording is
 * exactly the kind of thing that is worth asserting on in a test without
 * mounting a component. The shape is always "<visible> of <total> monitors
 * ..." so the first two numbers land in the same place whichever filters are
 * on, and someone glancing at it does not have to re-read the sentence to
 * find them.
 *
 * Status and tags share one copula ("are down and tagged env:prod") because
 * both describe what the monitor *is*; the query gets its own verb because
 * "matches" is not something the same "are" can carry.
 */
export function describeFilter(
  visible: number,
  total: number,
  status: MonitorStatus | null,
  query: string,
  tags: TagSelection = {},
): string | null {
  const needle = query.trim();
  const pairs = describeTags(tags);
  if (status === null && needle === "" && pairs.length === 0) return null;
  // The verb agrees with the number actually on screen, so "1 of 14 monitors
  // is down" does not read like a bug report about the sentence itself.
  const one = visible === 1;
  const copula = one ? "is" : "are";
  const states: string[] = [];
  if (status !== null) states.push(status);
  // Comma-separated inside the clause, so the "and" between clauses stays the
  // only one and the sentence does not turn into a chain of them.
  if (pairs.length > 0) states.push(`tagged ${pairs.join(", ")}`);
  const clauses: string[] = [];
  if (states.length > 0) clauses.push(`${copula} ${states.join(" and ")}`);
  if (needle !== "")
    clauses.push(`${one ? "matches" : "match"} \u201C${needle}\u201D`);
  const tail = clauses.join(" and ");
  // Zero is reported as "0 of 14" rather than as prose. `EmptyState` already
  // owns the sentence about an empty result, and two different phrasings of
  // the same fact, six lines apart, read like a bug.
  return `${visible} of ${plural(total, "monitor")} ${tail}`;
}

export type Summary = Record<MonitorStatus, number> & { total: number };

/** Counts per status for the heading. */
export function summarise(monitors: readonly Monitor[]): Summary {
  const summary: Summary = {
    up: 0,
    down: 0,
    pending: 0,
    paused: 0,
    waiting: 0,
    total: monitors.length,
  };
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
  return hidden > 0
    ? `${shown.join(", ")} and ${hidden} more`
    : shown.join(", ");
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
    const parts = [
      `${plural(down, "monitor")} down: ${downNames}.`,
      `${up} up.`,
    ];
    if (pending > 0) parts.push(`${pending} pending.`);
    return parts.join(" ");
  }
  // Everything recovered. Say so explicitly: silence after an outage is
  // indistinguishable from a page that stopped updating.
  if (paused > 0 && up === 0) return `All ${plural(total, "monitor")} paused.`;
  if (pending > 0) return `No monitors down. ${up} up, ${pending} pending.`;
  return `All ${plural(up, "monitor")} up.`;
}
