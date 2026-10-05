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
  /** The window this series answers, as the server named it ("24h", "7d"…). */
  window: string;
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
  return { window: api.window, stepMs: api.step_s * 1000, from, to, points };
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
 * The latency plot's vertical scale: the range it draws and the round values
 * its gridlines stand at.
 *
 * It used to run from zero to the peak step average. That kept the scale
 * readable from the "peak" figure without an axis, and it put a service that
 * answers in 120 to 170 ms into the top quarter of the plot, flat, under three
 * gridlines that named no value (SUB-184). Now the range is fitted to the data
 * and the gridlines are labelled, so a slope is visible and can be read.
 */
export type LatencyScale = {
  /** The value at the plot's bottom edge, in ms. */
  min: number;
  /** The value at the plot's top edge, in ms. */
  max: number;
  /** The gridlines' values, top to bottom, evenly spaced inside the range. */
  ticks: number[];
};

/** Gridlines inside the plot: the chart chrome's default of three. */
export const LATENCY_GRID_LINES = 3;

/**
 * Round steps, as 1, 2, 2.5 and 5 per decade. Never below 1 ms, and 2.5 only
 * from 25 ms up: the labels are whole milliseconds, and a 2.5 ms step would
 * print "3 ms" on a line that stands at 2.5.
 */
const NICE = [1, 2, 2.5, 5];

/**
 * Fits round gridlines around the measured step averages.
 *
 * The range is `lines + 1` equal steps from a multiple of the step, so the
 * gridlines the chrome spaces evenly land exactly on round values. The step is
 * the smallest round one that holds the data with a little room underneath,
 * which keeps the line's lowest point off the down ticks along the bottom.
 *
 * A flat series is given a band a fifth of its value wide, centred on it.
 * Without that, a service steady at 120 ms would be drawn on a 2 ms scale and
 * its noise would fill the plot as if it were a trend.
 */
export function latencyScale(
  lowMs: number,
  peakMs: number,
  lines = LATENCY_GRID_LINES,
): LatencyScale {
  const intervals = lines + 1;
  let low = Math.max(Math.min(lowMs, peakMs), 0);
  let high = Math.max(lowMs, peakMs);
  // At least one millisecond per interval, so every label is a different
  // whole number.
  const minimumSpan = Math.max(high * 0.2, intervals);
  if (high - low < minimumSpan) {
    const centre = (high + low) / 2;
    low = Math.max(centre - minimumSpan / 2, 0);
    high = low + minimumSpan;
  } else {
    low = Math.max(low - (high - low) * 0.05, 0);
  }
  // The span is at least one millisecond per interval, so the first decade is
  // at least 1 and the search ends within a few decades. Bounded all the same:
  // a scale is not worth an endless loop on a value nobody expected.
  const raw = (high - low) / intervals;
  let step = 0;
  let min = 0;
  let decade = raw >= 1 && Number.isFinite(raw) ? 10 ** Math.floor(Math.log10(raw)) : 1;
  for (let tries = 0; step === 0 && tries < 32; tries += 1, decade *= 10) {
    for (const nice of NICE) {
      const candidate = nice * decade;
      if (!Number.isInteger(candidate)) continue;
      const start = Math.floor(low / candidate) * candidate;
      if (start + intervals * candidate >= high) {
        step = candidate;
        min = start;
        break;
      }
    }
  }
  if (step === 0) {
    step = Math.max(Math.ceil(raw), 1);
    min = Math.floor(low);
  }
  const ticks = Array.from({ length: lines }, (_, i) => min + (lines - i) * step);
  return { min, max: min + intervals * step, ticks };
}

/**
 * Vertical position of a latency, 0 at the top and 1 at the baseline.
 *
 * Values outside the scale (a step's slowest check, in the readout) clamp to
 * its edge instead of escaping the plot.
 */
export function yOf(ms: number, scale: Pick<LatencyScale, "min" | "max">): number {
  const span = scale.max - scale.min;
  if (!(span > 0)) return 1;
  return 1 - Math.min(Math.max((ms - scale.min) / span, 0), 1);
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
