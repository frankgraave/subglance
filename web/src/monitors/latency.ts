/**
 * The latency chart's data: the API shape, its translation, and the geometry.
 *
 * Kept apart from `LatencyChart.tsx` so the parts that decide what the chart
 * *says* — where a line breaks, what the headline averages, how tall the plot
 * is — can be tested without a renderer.
 */

import { apiJSON } from "../api/http";
import { detailQueryKey } from "./detail";
import { toUnixMs } from "./types";

/** The windows the chart offers, in the order the control shows them. */
export const LATENCY_WINDOWS = ["24h", "7d", "30d"] as const;
export type LatencyWindow = (typeof LATENCY_WINDOWS)[number];

/** One step as GET /api/v1/monitors/:id/latency returns it. */
export type ApiLatencyPoint = {
  t: string;
  checks: number;
  samples: number;
  down: number;
  avg_ms: number | null;
  min_ms: number | null;
  max_ms: number | null;
};

export type ApiLatencySeries = {
  window: string;
  step_s: number;
  from: string;
  to: string;
  points: ApiLatencyPoint[];
};

export type LatencyPoint = {
  /** Start of the step, Unix milliseconds. */
  t: number;
  checks: number;
  samples: number;
  down: number;
  /** Null when no check in the step carried a latency — all of them failed. */
  avgMs: number | null;
  minMs: number | null;
  maxMs: number | null;
};

export type LatencySeries = {
  stepMs: number;
  from: number;
  to: number;
  points: LatencyPoint[];
};

/**
 * Under the monitor's detail key, so a recorded "Check now" — which
 * invalidates that whole family — refreshes the chart along with the rest.
 */
export const latencyQueryKey = (id: string, window: LatencyWindow) =>
  [...detailQueryKey(id), "latency", window] as const;

const finite = (v: unknown): number | null =>
  typeof v === "number" && Number.isFinite(v) ? v : null;

export function seriesFromApi(api: ApiLatencySeries): LatencySeries {
  const from = toUnixMs(api.from);
  const to = toUnixMs(api.to);
  if (from === null || to === null || !(api.step_s > 0) || !Array.isArray(api.points)) {
    throw new Error("Invalid latency history response");
  }
  const points: LatencyPoint[] = [];
  for (const p of api.points) {
    const t = toUnixMs(p.t);
    // A step without a time cannot be placed; drop it rather than draw it at
    // the epoch, which would squash the whole line against the right edge.
    if (t === null) continue;
    points.push({
      t,
      checks: finite(p.checks) ?? 0,
      samples: finite(p.samples) ?? 0,
      down: finite(p.down) ?? 0,
      avgMs: finite(p.avg_ms),
      minMs: finite(p.min_ms),
      maxMs: finite(p.max_ms),
    });
  }
  points.sort((a, b) => a.t - b.t);
  return { stepMs: api.step_s * 1000, from, to, points };
}

export async function fetchLatency(
  id: string,
  window: LatencyWindow,
  signal?: AbortSignal,
): Promise<LatencySeries> {
  const body = await apiJSON<ApiLatencySeries>(
    `/api/v1/monitors/${encodeURIComponent(id)}/latency?window=${window}`,
    { signal },
  );
  return seriesFromApi(body);
}

/** One unbroken run of measured steps: the line is drawn per run. */
export type LatencyRun = LatencyPoint[];

/**
 * Splits the series wherever a step is missing or carried no latency.
 *
 * The line must not bridge a gap. A straight segment across six hours with no
 * checks draws a latency nobody measured, and across an outage it draws the
 * service as answering when it was not answering at all.
 */
export function measuredRuns(series: LatencySeries): LatencyRun[] {
  const runs: LatencyRun[] = [];
  let current: LatencyRun = [];
  let previous: number | null = null;
  for (const p of series.points) {
    const adjacent = previous !== null && p.t - previous === series.stepMs;
    if (p.avgMs === null || !adjacent) {
      if (current.length > 0) runs.push(current);
      current = [];
    }
    if (p.avgMs !== null) current.push(p);
    previous = p.t;
  }
  if (current.length > 0) runs.push(current);
  return runs;
}

export type LatencySummary = {
  /** Mean over every sample in the window, weighted by each step's samples. */
  averageMs: number | null;
  /** The highest step average: the top of the plot. */
  peakMs: number | null;
  /** The lowest step average. */
  lowMs: number | null;
  checks: number;
  /** Steps holding at least one confirmed-down check. */
  downSteps: number;
};

export function summariseLatency(series: LatencySeries): LatencySummary {
  let sum = 0;
  let samples = 0;
  let checks = 0;
  let downSteps = 0;
  let peak: number | null = null;
  let low: number | null = null;
  for (const p of series.points) {
    checks += p.checks;
    if (p.down > 0) downSteps += 1;
    if (p.avgMs === null || p.samples <= 0) continue;
    // Weighted by samples, not by steps: an average of step averages
    // over-weights the quiet steps, which on a monitor that was paused for
    // half the window is half the answer.
    sum += p.avgMs * p.samples;
    samples += p.samples;
    peak = peak === null ? p.avgMs : Math.max(peak, p.avgMs);
    low = low === null ? p.avgMs : Math.min(low, p.avgMs);
  }
  return {
    averageMs: samples > 0 ? sum / samples : null,
    peakMs: peak,
    lowMs: low,
    checks,
    downSteps,
  };
}

/** Horizontal position of a step's start, 0..1 across the window. */
export function xOf(series: LatencySeries, t: number): number {
  const span = series.to - series.from;
  return span > 0 ? (t - series.from) / span : 0;
}

/**
 * Vertical position of a latency, 0 at the top and 1 at the baseline.
 *
 * The ceiling is the peak step average, so the highest point of the line
 * touches the top of the plot and the breakdown's "peak" figure *is* the
 * scale — the chart chrome draws no axis, so the scale has to be readable
 * from a number that is already on screen. Values above it (a step's max)
 * clamp to the top instead of escaping the plot.
 */
export function yOf(ms: number, ceiling: number): number {
  if (!(ceiling > 0)) return 1;
  return 1 - Math.min(Math.max(ms / ceiling, 0), 1);
}

/** The step index nearest to a horizontal fraction of the plot. */
export function nearestPoint(series: LatencySeries, fraction: number): number | null {
  if (series.points.length === 0) return null;
  const t = series.from + fraction * (series.to - series.from);
  let best = 0;
  let bestDistance = Infinity;
  series.points.forEach((p, index) => {
    const distance = Math.abs(p.t + series.stepMs / 2 - t);
    if (distance < bestDistance) {
      best = index;
      bestDistance = distance;
    }
  });
  return best;
}
