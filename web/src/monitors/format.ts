/**
 * How a monitor's facts are written down.
 *
 * Shared between the desktop row and the phone card so the two layouts cannot
 * disagree about what 99.95% rounds to or what a paused monitor is called.
 * Pure strings in, pure strings out — the components decide where they go.
 */

import { formatDuration } from "./detail";
import type { Monitor, MonitorStatus, PushWaitingReason, Recovery } from "./types";

/** How each status is spoken and written. Colour never stands alone (DESIGN.md §2.3). */
export const STATUS_LABEL: Record<MonitorStatus, string> = {
  up: "Up",
  down: "Down",
  recovering: "Recovering",
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
  recovering: "Was recovering",
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
export const statusWord = (
  status: MonitorStatus,
  stale = false,
  recovery?: Recovery,
): string => {
  const word = stale ? STATUS_LABEL_LAST_KNOWN[status] : STATUS_LABEL[status];
  // The count belongs to the word, not beside it: "Recovering" alone does not
  // say whether the all-clear is one check away or nine, and that is the
  // question someone watching a recovery is asking.
  if (status !== "recovering" || recovery === undefined) return word;
  return `${word} (${recovery.passes} of ${recovery.threshold})`;
};

export const formatLatency = (ms: number) =>
  ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${Math.round(ms)} ms`;

/**
 * A share written to `decimals` places, rounded down, never up.
 *
 * Uptime is a claim, and rounding to nearest lets it claim more than happened:
 * 21 confirmed-down checks out of 43,138 is 99.951%, which `toFixed` turns
 * into "100%" at one place and "99.95%" at two. The first is the number
 * people read as "never down", printed right above the line that counts the
 * outages. Rounding down keeps every digit shown true: "99.9%" understates by
 * a hair, "100%" contradicts the page.
 *
 * So 100 is reserved for exactly 100, which only happens with zero down
 * checks (the API divides up by total, and n / n is exactly 1). The tiny
 * epsilon absorbs float noise: 29 of 100 comes out of up / total * 100 as
 * 28.999999999999996, which a bare floor would print as 28.9. The clamp
 * catches the one case the epsilon can push the wrong way, a share a
 * billionth short of 100.
 */
export function floorPercent(pct: number, decimals: number): string {
  if (pct >= 100) return (100).toFixed(decimals);
  const scale = 10 ** decimals;
  const floored = Math.floor(pct * scale + 1e-9) / scale;
  return Math.min(floored, 100 - 1 / scale).toFixed(decimals);
}

/**
 * Uptime as the dashboard and the detail page write it: one decimal, rounded
 * down (see `floorPercent`), with the two exact ends as whole numbers. "100%"
 * therefore means no confirmed-down check in the window, and "0%" means no
 * passing one; anything in between keeps its decimal, so 99.95% reads "99.9%".
 */
export const formatUptime = (pct: number) =>
  pct >= 100 || pct <= 0 ? `${pct >= 100 ? 100 : 0}%` : `${floorPercent(pct, 1)}%`;

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

/**
 * Why a push monitor is waiting, in one sentence for its page.
 *
 * Three different waits, and each sends the reader somewhere else. A monitor
 * that has never reported is waiting on somebody to wire up its URL. One that
 * was resumed, or whose window closed while SubGlance was not running, is
 * waiting on its job's next run: the earlier window proved nothing, because
 * reports for a paused monitor are not recorded and a stopped SubGlance
 * could not receive them, so a fresh one started.
 *
 * On a stale stream the sentence is the last known state, not a present-tense
 * claim, for the same reason the status word beside it turns into "Was
 * waiting": nothing on the page knows whether the report has arrived since.
 */
export function waitingReason(
  since: PushWaitingReason | undefined,
  stale = false,
): string {
  switch (since) {
    case "resumed":
      return stale
        ? "When last heard, it was waiting for the first report since the monitor was resumed."
        : "Waiting for the first report since the monitor was resumed.";
    case "restarted":
      return stale
        ? "When last heard, it was waiting for the first report since SubGlance started."
        : "Waiting for the first report since SubGlance started.";
    default:
      return stale
        ? "When last heard, nothing had reported in yet."
        : "Nothing has reported in yet.";
  }
}
