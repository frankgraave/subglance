/**
 * How a monitor's facts are written down.
 *
 * Shared between the desktop row and the phone card so the two layouts cannot
 * disagree about what "99.95%" rounds to or what a paused monitor is called.
 * Pure strings in, pure strings out — the components decide where they go.
 */

import type { MonitorStatus } from "./types";

/** How each status is spoken and written. Colour never stands alone (DESIGN.md §2.3). */
export const STATUS_LABEL: Record<MonitorStatus, string> = {
  up: "Up",
  down: "Down",
  pending: "Pending",
  paused: "Paused",
};

export const formatLatency = (ms: number) =>
  ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${Math.round(ms)} ms`;

/** Uptime to one decimal, so 99.95 does not round up to a perfect 100%. */
export const formatUptime = (pct: number) =>
  `${pct.toFixed(pct >= 99.95 || pct === 0 ? 0 : 1)}%`;
