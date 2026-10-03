/**
 * The inventory model: a monitor as the *management* page reads it.
 *
 * The dashboard's `Monitor` answers "how is it right now". This adds the
 * columns the dashboard deliberately refuses — check type, interval, timeout,
 * attached channels, tags, when it was created, whether someone paused it —
 * and nothing else. It extends `Monitor` rather than replacing it so that
 * `filterMonitors`, `filterByStatus`, `filterByTags` and `tagFacets` keep
 * working unchanged; rewriting those for a second screen is how two lists end
 * up disagreeing about what "matches prod" means.
 *
 * The versioned detail endpoint carries check settings for future editing;
 * this list model only keeps the columns the inventory currently renders.
 */

import { fromApi, toUnixMs } from "./types";
import { byName } from "./model";
import type { PreviewRequest } from "./preview";
import type { JsonAssertion } from "./jsonAssertion";
import type { ApiMonitor, Monitor } from "./types";

/**
 * The check types the API accepts. `unknown` is not a type — it is what an
 * older or newer server sent that this build has no column for, kept rather
 * than coerced so the row still says something true.
 */
export type CheckType = "http" | "tcp" | "ping" | "ssl" | "push";

const CHECK_TYPES: readonly string[] = ["http", "tcp", "ping", "ssl", "push"];

export type InventoryMonitor = Monitor & {
  /**
   * The wire type, kept verbatim. `Monitor` drops it on purpose (a dashboard
   * branches on `push` alone), but this screen's whole job is to show the
   * setting, so here it is data rather than a derived flag.
   */
  type: string;
  /** Seconds between checks, as configured. */
  intervalS: number;
  /**
   * Seconds a check may take, or null for a push monitor — SubGlance does not
   * dial anything, so there is no timeout to state. Null rather than 0: a
   * zero-second timeout is a setting somebody could have chosen.
   */
  timeoutS: number | null;
  /** False when someone paused it. The `paused` status is derived from this. */
  enabled: boolean;
  /** Unix ms, or null when the server sent a timestamp we cannot read. */
  createdAt: number | null;
  /**
   * The TLS floor as a label — "1.0" to "1.3" — or `""` for no opinion.
   *
   * `""` rather than null or undefined because that is what the select's
   * empty option carries, and one spelling of "no opinion" is what keeps the
   * edit form from turning an absent floor into a chosen one.
   */
  minTlsVersion: string;
  /** Attached channels from the same list read; missing data is unknown. */
  channels: ChannelState;
  repeatAfterS?: number;
  /** Present only when the detail read carried it; absent is unknown, not 2. */
  recoveryThreshold?: number;
  /** Detail read only. Null is "none"; absent is unknown, so not editable. */
  jsonAssertion?: JsonAssertion | null;
  checkSettings?: Omit<PreviewRequest, "type" | "target" | "timeout_s">;
};

/** One monitor as the inventory reads it. */
export function inventoryFromApi(api: ApiMonitor & {
  interval_s: number;
  timeout_s: number;
  enabled: boolean;
}): InventoryMonitor {
  const base = fromApi(api);
  return {
    ...base,
    type: api.type,
    intervalS: api.interval_s,
    // A push monitor's `timeout_s` column still holds a number; it just does
    // not describe anything, because nothing is dialled. Reporting it would
    // put a setting on screen that changing cannot affect.
    timeoutS: api.type === "push" ? null : api.timeout_s,
    enabled: api.enabled,
    createdAt: toUnixMs(api.created_at),
    // Absent means the monitor has no floor of its own. It is read as "no
    // opinion" and never as the current default: substituting "1.2" here
    // would make the edit form offer to pin a floor nobody set.
    minTlsVersion: api.min_tls_version ?? "",
    channels: channelsFromApi(api.channels, api.default_channel, api.rule_channels),
    ...(api.repeat_after_s !== undefined ? { repeatAfterS: api.repeat_after_s } : {}),
    ...(api.recovery_threshold !== undefined ? { recoveryThreshold: api.recovery_threshold } : {}),
    ...(api.json_assertion !== undefined ? { jsonAssertion: api.json_assertion } : {}),
    checkSettings: Object.fromEntries(
      (["method", "expected_status", "keyword", "keyword_mode", "follow_redirects", "headers", "body", "ssl_warn_days", "min_tls_version"] as const)
        .filter((key) => api[key] !== undefined).map((key) => [key, api[key]]),
    ),
  };
}

/** Translates a whole `{ monitors: [...] }` payload. */
export function inventoryFromPayload(
  payload: { monitors?: ApiMonitor[] } | null | undefined,
): InventoryMonitor[] {
  return (payload?.monitors ?? []).map((api) =>
    inventoryFromApi(api as Parameters<typeof inventoryFromApi>[0]),
  );
}

/** How a check type is written in the type column. Uppercase, like the badge. */
export function typeLabel(type: string): string {
  return CHECK_TYPES.includes(type) ? type.toUpperCase() : "UNKNOWN";
}

/**
 * Narrows to one check type, or returns a copy for `null`.
 *
 * Separate from `filterByStatus` for the same reason that one is separate from
 * `filterMonitors`: they answer different questions and the caller may want
 * either alone. `null` and not `"all"`, so "no type chosen" is the absence of
 * a value rather than a sixth type that does not exist on the wire.
 */
export function filterByType(
  monitors: readonly InventoryMonitor[],
  type: string | null,
): InventoryMonitor[] {
  if (type === null || type === "") return [...monitors];
  return monitors.filter((m) => m.type === type);
}

/**
 * Attachment data can be known empty, known populated, or unavailable.
 *
 * `fallback` is the instance default a known-empty list alerts through
 * instead (SUB-124). It rides on the known branch only: with the attachments
 * unknown, nothing can be said about whether the default applies.
 *
 * `ids` runs parallel to `names`, and `fallbackId` beside `fallback`. The
 * inventory column only needs names, but the notifications page crosses them
 * with the channel list to tell a disabled route from a live one, and a name
 * is not an identity: two channels may share one.
 *
 * `rules` are the tag routing rules that add channels on top of the
 * monitor's own (SUB-147). Alerts go to the union of both; the default only
 * stands in when both are empty.
 */
export type ChannelState =
  | { known: false }
  | {
      known: true;
      names: readonly string[];
      ids?: readonly string[];
      rules?: readonly RuleRoute[];
      fallback?: string;
      fallbackId?: string;
    };

/** One routing rule's channels for one monitor. */
export type RuleRoute = {
  /** How the rule's tag is written for people: `env:prod`. */
  tag: string;
  names: readonly string[];
  ids: readonly string[];
};

export const CHANNELS_UNKNOWN: ChannelState = { known: false };

/** Never turn a malformed or missing list into the assertion "none". */
function channelsFromApi(value: unknown, fallback?: unknown, ruleValue?: unknown): ChannelState {
  if (!Array.isArray(value) || !value.every(isChannelRef)) return CHANNELS_UNKNOWN;
  // Absent means a server from before routing rules, which has none. Present
  // but malformed is unknown: a rule that cannot be read may be the one that
  // routes this monitor, so nothing is claimed.
  const rules = ruleValue === undefined ? [] : rulesFromApi(ruleValue);
  if (rules === null) return CHANNELS_UNKNOWN;
  const names = value.map((channel) => channel.name);
  const ids = value.map((channel) => String(channel.id));
  const state = { known: true as const, names, ids, ...(rules.length > 0 ? { rules } : {}) };
  // The default only ever stands in for an empty union; a server that sent
  // one beside real routes is ignored rather than believed.
  if (names.length === 0 && rules.length === 0 && isChannelRef(fallback)) {
    return { ...state, fallback: fallback.name, fallbackId: String(fallback.id) };
  }
  return state;
}

function rulesFromApi(value: unknown): RuleRoute[] | null {
  if (!Array.isArray(value)) return null;
  const out: RuleRoute[] = [];
  for (const rule of value) {
    const { tag_key: key, tag_value: tag, channels } = (rule ?? {}) as Record<string, unknown>;
    if (typeof key !== "string" || typeof tag !== "string" ||
      !Array.isArray(channels) || !channels.every(isChannelRef)) return null;
    out.push({
      tag: `${key}:${tag}`,
      names: channels.map((c) => c.name),
      ids: channels.map((c) => String(c.id)),
    });
  }
  return out;
}

function isChannelRef(channel: unknown): channel is { id: number; name: string } {
  if (channel === null || typeof channel !== "object") return false;
  const { id, name } = channel as { id?: unknown; name?: unknown };
  return Number.isSafeInteger(id) && (id as number) > 0 &&
    typeof name === "string" && name.trim() !== "";
}

/**
 * The words the channels cell says.
 *
 * A string rather than JSX because the wording is the decision — "not loaded"
 * and "none" are two different claims about the same empty-looking cell, and
 * which one appears is worth a test that does not need a renderer.
 */
export function describeChannels(state: ChannelState): string {
  if (!state.known) return "not loaded";
  // A rule-routed channel names its rule, so nobody has to work out why a
  // channel they never attached is listed.
  const routed = [
    ...state.names,
    ...(state.rules ?? []).flatMap((r) => r.names.map((name) => `${name} via ${r.tag}`)),
  ];
  if (routed.length === 0) {
    // Someone does hear about it, so it must not read as the finding "none".
    return state.fallback === undefined ? "none" : `${state.fallback} (default)`;
  }
  return routed.join(", ");
}

/** True when the monitor reaches no channel at all: not its own, no rule, no default. */
export function routesNowhere(state: ChannelState): boolean {
  return state.known && state.names.length === 0 && !state.rules?.length && state.fallback === undefined;
}

/**
 * The interval, in the units a person configured it in.
 *
 * Not `formatDuration`: that renders 3600 as "1 h", which is right for "this
 * outage lasted", and here the number is a *setting* someone typed. They are
 * the same string for every value this column can hold, so the shared helper
 * is used — this wrapper exists to name the difference, and to keep the
 * push-monitor case, which has no configured interval of its own on the wire
 * outside `push`, in one place.
 */
export function intervalOf(monitor: InventoryMonitor): number {
  return monitor.push !== undefined ? monitor.push.intervalS : monitor.intervalS;
}

/**
 * Whether "Check now" can do anything for this monitor.
 *
 * Push monitors are the only refusal, and it is not a policy — SubGlance is
 * the receiver, so there is nothing for it to call. A *paused* monitor is
 * deliberately allowed: verifying before resuming is the single most common
 * reason to press it, and the endpoint runs the probe for a paused monitor
 * without recording the result (internal/api/check.go).
 */
export function canCheckNow(monitor: InventoryMonitor): boolean {
  return monitor.push === undefined;
}

/** The heading count: "7 configured · 1 paused". */
export function describeInventory(monitors: readonly InventoryMonitor[]): string {
  const paused = monitors.filter((m) => !m.enabled).length;
  const configured = `${monitors.length} configured`;
  return paused === 0 ? configured : `${configured}, ${paused} paused`;
}

/**
 * What deleting a monitor takes with it, in the sentence the retyping exists
 * to make someone read. Shared by the inventory and the detail screen so the
 * two cannot describe the same act differently.
 */
export function monitorDeleteConsequence(name: string): string {
  return `${name} and everything recorded about it — heartbeats, uptime history and its incidents, open and past — are removed. This cannot be undone. If you only want it to stop checking, pause it instead: a paused monitor keeps its history.`;
}

/** The orders the inventory offers. */
export type InventorySort = "name" | "status" | "type" | "interval";

export const INVENTORY_SORTS: readonly { value: InventorySort; label: string }[] = [
  { value: "name", label: "Name" },
  { value: "status", label: "Status" },
  { value: "type", label: "Type" },
  { value: "interval", label: "Interval" },
];

/*
 * Worst first, so sorting by status answers "what is wrong" from the top of
 * the list. The same order as the dashboard's status chips. Paused sits just
 * above up: not broken, but a state somebody chose and may have forgotten,
 * which on this page is a finding.
 */
const STATUS_RANK: Readonly<Record<string, number>> = {
  down: 0,
  recovering: 1,
  warning: 2,
  pending: 3,
  waiting: 4,
  paused: 5,
  up: 6,
};

/**
 * The inventory in the order asked for.
 *
 * Every order falls back to the name, then the id, so two monitors that tie
 * on status or type keep one position between polls. A list that reshuffled
 * its equal rows every fifteen seconds would move a row out from under the
 * pointer reaching for its pause button. A copy: the caller's array is the
 * query cache.
 */
export function sortInventory(
  monitors: readonly InventoryMonitor[],
  sort: InventorySort,
): InventoryMonitor[] {
  const key = (m: InventoryMonitor): number | string =>
    sort === "status"
      ? (STATUS_RANK[m.status] ?? STATUS_RANK.up + 1)
      : sort === "type"
        ? typeLabel(m.type)
        : sort === "interval"
          ? intervalOf(m)
          : 0;
  return [...monitors].sort((a, b) => {
    const ka = key(a);
    const kb = key(b);
    if (ka < kb) return -1;
    if (ka > kb) return 1;
    return byName(a, b);
  });
}
