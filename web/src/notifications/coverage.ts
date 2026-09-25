/**
 * Who hears about each monitor (SUB-124).
 *
 * The notifier's rule, restated for the page: a monitor's own channels win
 * outright, a monitor with none alerts through the instance default, and a
 * disabled channel is skipped at send time (`internal/notifier`). So "who
 * hears" is not the attachment list — it is the attachment list, or the
 * default, minus whatever is switched off. Working that out in one's head for
 * forty monitors is the exact chore the ticket asks the page to remove.
 *
 * Pure, so every branch is a table test rather than a render.
 */

import type { InventoryMonitor } from "../monitors/inventory";
import type { Channel } from "./channels";

/** One destination as a monitor reaches it. */
export type Recipient = {
  name: string;
  /**
   * False when the channel is switched off, so this route delivers nothing.
   * Null when the channel list does not know the id — the two lists are
   * polled separately and can disagree for a minute. Null never counts as
   * silent: claiming a monitor alerts nobody on the strength of a stale list
   * would send someone hunting a misconfiguration that is not there.
   */
  enabled: boolean | null;
};

export type Coverage = {
  id: string;
  name: string;
  /** A paused monitor is not checked, so it alerts nobody by design. */
  paused: boolean;
  /**
   * `own`: its own channels. `default`: none of its own, so the default.
   * `none`: none of its own and no default. `unknown`: its attachments could
   * not be read, so nothing is claimed.
   */
  route: "own" | "default" | "none" | "unknown";
  recipients: readonly Recipient[];
  /** True when an alert from this monitor would reach nobody right now. */
  silent: boolean;
};

export function coverageOf(
  monitor: InventoryMonitor,
  channels: readonly Channel[],
): Coverage {
  const base = { id: monitor.id, name: monitor.name, paused: !monitor.enabled };
  const state = monitor.channels;
  if (!state.known) {
    return { ...base, route: "unknown", recipients: [], silent: false };
  }
  const lookup = (id: string | undefined, name: string): Recipient => {
    const channel =
      id === undefined ? undefined : channels.find((c) => c.id === id);
    return { name, enabled: channel === undefined ? null : channel.enabled };
  };
  let route: Coverage["route"];
  let recipients: Recipient[];
  if (state.names.length > 0) {
    route = "own";
    recipients = state.names.map((name, i) => lookup(state.ids?.[i], name));
  } else if (state.fallback !== undefined) {
    route = "default";
    recipients = [lookup(state.fallbackId, state.fallback)];
  } else {
    // The inventory says there is no default, but the channel list is polled
    // separately and may already know one was set (or not yet know it was
    // cleared). Neither list is trustworthy while they disagree, so claim
    // nothing rather than report a monitor as alerting nobody.
    if (channels.some((c) => c.isDefault)) {
      return { ...base, route: "unknown", recipients: [], silent: false };
    }
    route = "none";
    recipients = [];
  }
  const silent = !recipients.some((r) => r.enabled !== false);
  return { ...base, route, recipients, silent };
}

/**
 * The whole list, silent monitors first.
 *
 * The silent ones are the finding; the rest confirm that nothing needs doing.
 * Paused monitors sort with the covered ones — being silent is what pausing
 * means, and listing them among the problems would bury the real ones.
 * Otherwise by name, so a row stays put between polls.
 */
export function coverageList(
  monitors: readonly InventoryMonitor[],
  channels: readonly Channel[],
): Coverage[] {
  const rank = (c: Coverage) => (c.silent && !c.paused ? 0 : 1);
  return monitors
    .map((m) => coverageOf(m, channels))
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

/** The number of active monitors whose alerts would reach nobody. */
export function silentCount(list: readonly Coverage[]): number {
  return list.filter((c) => c.silent && !c.paused).length;
}

/**
 * The words in a row's recipient cell.
 *
 * A silent row says why, because "nobody" alone does not tell you what to
 * fix: attaching a channel, setting a default and switching one back on are
 * three different repairs.
 */
export function describeCoverage(c: Coverage): string {
  if (c.route === "unknown") return "not loaded";
  const named = c.recipients
    .map((r) => (r.enabled === false ? `${r.name} (disabled)` : r.name))
    .join(", ");
  if (c.route === "none") return "nobody: no channels of its own and no default";
  if (c.route === "default") {
    return c.silent
      ? `nobody: the default, ${c.recipients[0].name}, is disabled`
      : `${named} (default)`;
  }
  return c.silent ? `nobody: ${named}` : named;
}
