import { describe, expect, it } from "vitest";
import {
  measuredRuns,
  nearestPoint,
  seriesFromApi,
  summariseLatency,
  xOf,
  yOf,
  type ApiLatencyPoint,
  type LatencySeries,
} from "./latency";

const HOUR = 3_600_000;
const FROM = Date.parse("2026-09-20T00:00:00Z");

const point = (i: number, avg: number | null, over: Partial<ApiLatencyPoint> = {}): ApiLatencyPoint => ({
  t: new Date(FROM + i * HOUR).toISOString(),
  checks: 60,
  samples: avg === null ? 0 : 60,
  down: 0,
  avg_ms: avg,
  min_ms: avg,
  max_ms: avg,
  ...over,
});

const series = (points: ApiLatencyPoint[]): LatencySeries =>
  seriesFromApi({
    window: "24h",
    step_s: 3600,
    from: new Date(FROM).toISOString(),
    to: new Date(FROM + 24 * HOUR).toISOString(),
    points,
  });

describe("seriesFromApi", () => {
  it("keeps null latency as null rather than turning it into 0ms", () => {
    const s = series([point(0, null, { down: 60 })]);
    expect(s.points[0]).toMatchObject({ avgMs: null, minMs: null, maxMs: null, down: 60 });
  });

  it("sorts by time and drops a step it cannot place", () => {
    const s = series([point(2, 30), { ...point(1, 20), t: "not a time" }, point(0, 10)]);
    expect(s.points.map((p) => p.avgMs)).toEqual([10, 30]);
  });

  it("rejects a response without a usable range", () => {
    expect(() =>
      seriesFromApi({ window: "24h", step_s: 0, from: "", to: "", points: [] }),
    ).toThrow(/Invalid latency/);
  });
});

describe("measuredRuns", () => {
  it("draws one line through adjacent measured steps", () => {
    expect(measuredRuns(series([point(0, 10), point(1, 20), point(2, 30)]))).toHaveLength(1);
  });

  it("breaks the line at a missing step instead of bridging it", () => {
    const runs = measuredRuns(series([point(0, 10), point(1, 20), point(5, 30), point(6, 40)]));
    expect(runs.map((r) => r.map((p) => p.avgMs))).toEqual([[10, 20], [30, 40]]);
  });

  it("breaks the line at a step whose checks all failed", () => {
    const runs = measuredRuns(series([point(0, 10), point(1, null, { down: 60 }), point(2, 30)]));
    expect(runs.map((r) => r.map((p) => p.avgMs))).toEqual([[10], [30]]);
  });
});

describe("summariseLatency", () => {
  it("weights the average by samples, not by steps", () => {
    // One busy step at 100ms and one quiet step at 1000ms: the step average
    // would be 550ms; the true average over every check is ~109ms.
    const s = series([
      point(0, 100, { samples: 99, checks: 99 }),
      point(1, 1000, { samples: 1, checks: 1 }),
    ]);
    const summary = summariseLatency(s);
    expect(summary.averageMs).toBeCloseTo(109, 0);
    expect(summary.peakMs).toBe(1000);
    expect(summary.lowMs).toBe(100);
    expect(summary.checks).toBe(100);
  });

  it("counts the steps that held confirmed downtime", () => {
    const s = series([point(0, 10), point(1, null, { down: 3 }), point(2, 30, { down: 1 })]);
    expect(summariseLatency(s).downSteps).toBe(2);
  });

  it("has no average when nothing was measured", () => {
    expect(summariseLatency(series([point(0, null)])).averageMs).toBeNull();
  });
});

describe("geometry", () => {
  const s = series([point(0, 10), point(12, 20), point(23, 30)]);

  it("places the peak at the top and zero at the baseline", () => {
    expect(yOf(200, 200)).toBe(0);
    expect(yOf(0, 200)).toBe(1);
    expect(yOf(100, 200)).toBe(0.5);
    // A value over the ceiling clamps instead of escaping the plot.
    expect(yOf(400, 200)).toBe(0);
  });

  it("places steps across the window", () => {
    expect(xOf(s, FROM)).toBe(0);
    expect(xOf(s, FROM + 12 * HOUR)).toBe(0.5);
  });

  it("finds the step under the pointer", () => {
    expect(nearestPoint(s, 0)).toBe(0);
    expect(nearestPoint(s, 0.52)).toBe(1);
    expect(nearestPoint(s, 1)).toBe(2);
    expect(nearestPoint(series([]), 0.5)).toBeNull();
  });
});
