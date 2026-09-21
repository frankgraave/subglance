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
  warning: "Warning",
  pending: "Pending",
  paused: "Paused",
  waiting: "Waiting",
};

/**
 * The same five statuses, in the past tense.
 *
 * DESIGN.md §6 drains colour when the stream dies, because colour is a claim
 * about reality and we have stopped being able to make one. The word was left
 * out of that rule, and a word is a louder claim than a hue: "Up" beside a
 * dimmed lamp still reads as *is up*, at exactly the moment somebody opened
 * the screen because they suspect it is not.
 *
 * The status itself is **not** dropped. Which state we lost the monitor in is
 * the most useful thing left on a dead screen — "it was down when we went
 * deaf" and "it was up when we went deaf" send you to two different places —
 * so the honesty goes into the tense rather than into a blank. "Status stale"
 * would be honest and useless.
 *
 * Sentence case after "Was" on purpose, and load-bearing: the present-tense
 * labels are capitalised, so a guard can search a stale render for a
 * capitalised status word and find nothing. Writing "Was Up" here would make
 * that check unwritable.
 */
export const STATUS_LABEL_LAST_KNOWN: Record<MonitorStatus, string> = {
  up: "Was up",
  down: "Was down",
  warning: "Was warning",
  pending: "Was pending",
  paused: "Was paused",
  waiting: "Was waiting",
};

/**
 * The status in words, in the tense the connection has earned.
 *
 * One function rather than two tables at the call sites: every view that says
 * a status — the detail pill, the row's label, the card, the compact line, the
 * wall — has to make the same switch, and a view that forgets is a view that
 * lies. See `Led`, which routes its text alternative through here for exactly
 * that reason.
 */
export const statusWord = (status: MonitorStatus, stale = false): string =>
  stale ? STATUS_LABEL_LAST_KNOWN[status] : STATUS_LABEL[status];

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
