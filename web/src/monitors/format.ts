/**
 * How a monitor's facts are written down.
 *
 * Shared between the desktop row and the phone card so the two layouts cannot
 * disagree about what "99.95%" rounds to or what a paused monitor is called.
 * Pure strings in, pure strings out — the components decide where they go.
 */

import { formatDuration } from "./detail";
import type { Monitor, MonitorStatus } from "./types";

/** How each status is spoken and written. Colour never stands alone (DESIGN.md §2.3). */
export const STATUS_LABEL: Record<MonitorStatus, string> = {
  up: "Up",
  down: "Down",
  pending: "Pending",
  paused: "Paused",
  waiting: "Waiting",
};

export const formatLatency = (ms: number) =>
  ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${Math.round(ms)} ms`;

/** Uptime to one decimal, so 99.95 does not round up to a perfect 100%. */
export const formatUptime = (pct: number) =>
  `${pct.toFixed(pct >= 99.95 || pct === 0 ? 0 : 1)}%`;

/**
 * What a monitor watches, in one line.
 *
 * A push monitor has no target — it is reported to, not probed — so the column
 * that shows the address would be blank for it, which reads as a monitor that
 * failed to save. Its window is the equivalent fact: it is what the monitor is
 * waiting for.
 */
export function describeTarget(monitor: Monitor): string {
  if (monitor.push === undefined) return monitor.target;
  return `expects a report every ${formatDuration(monitor.push.intervalS)}`;
}

/** The push window spelled out: interval and how late is still acceptable. */
export function describePushWindow(intervalS: number, graceS: number): string {
  const every = `Expected every ${formatDuration(intervalS)}`;
  return graceS > 0
    ? `${every}, ${formatDuration(graceS)} grace`
    : `${every}, no grace`;
}
