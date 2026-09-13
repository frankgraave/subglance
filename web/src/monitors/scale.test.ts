import { describe, expect, it } from "vitest";
import { demoMonitors } from "./demo";
import { filterMonitors, partition, summarise } from "./model";
import type { Monitor } from "./types";

/**
 * The scale guardrails for SUB-65: the dashboard is specified for 10–200
 * monitors, and every one of these functions runs again for every single
 * heartbeat that arrives over the SSE stream. With 200 monitors on a 60s
 * interval that is ~3 events per second, each one re-sorting the whole list.
 *
 * The budgets below are deliberately loose — roughly 10x the measured cost —
 * because a wall-clock assertion has to survive a loaded CI runner. They are
 * not there to police milliseconds; they are there to catch a *category* of
 * regression, namely rebuilding an `Intl.Collator` (or anything comparably
 * expensive) inside a comparator. That mistake does not fail any correctness
 * test, does not show up at the 14 monitors the fixtures use, and costs 40x.
 */

const SCALE = 200;

/** Averaged over enough runs that one GC pause cannot decide the result. */
function perCall(runs: number, fn: () => void): number {
  fn(); // warm up, so JIT compilation is not billed to the first iteration
  const start = performance.now();
  for (let i = 0; i < runs; i++) fn();
  return (performance.now() - start) / runs;
}

describe(`ordering at ${SCALE} monitors`, () => {
  const list = demoMonitors(SCALE);

  it("partitions well under budget", () => {
    const ms = perCall(200, () => partition(list));
    // Measured 0.10ms with a hoisted collator, 4.2ms with a per-comparison
    // one. 1ms sits between the two with an order of magnitude of headroom.
    expect(ms).toBeLessThan(1);
  });

  it("filters and summarises well under budget", () => {
    expect(perCall(200, () => filterMonitors(list, "api"))).toBeLessThan(1);
    expect(perCall(200, () => summarise(list))).toBeLessThan(1);
  });

  it("still orders the full list correctly", () => {
    const { attention, rest } = partition(list);
    expect(attention.every((m) => m.status === "down")).toBe(true);
    expect(rest.every((m) => m.status !== "down")).toBe(true);
    expect(attention.length + rest.length).toBe(SCALE);

    const names = rest.map((m) => m.name);
    const sorted = [...names].sort((a, b) =>
      a.localeCompare(b, "en", { sensitivity: "base", numeric: true }),
    );
    expect(names).toEqual(sorted);
  });

  it("is stable regardless of input order", () => {
    const reversed = [...list].reverse();
    expect(partition(reversed).rest.map((m) => m.id)).toEqual(
      partition(list).rest.map((m) => m.id),
    );
  });
});

describe("the id tiebreak", () => {
  const named = (id: string, name: string): Monitor => ({
    id,
    name,
    status: "up",
    tags: {},
    target: `https://${id}.example.com`,
    latencyMs: 10,
    uptime24h: 100,
    beats: [],
    lastCheck: 0,
  });

  /**
   * Two monitors sharing a name must still have exactly one order. The name
   * collator uses `sensitivity: "base"`, which calls "MON-a" and "mon-A"
   * equal — so the tiebreak cannot reuse it, or the tie stays unbroken and
   * the list order depends on the input order.
   */
  it("breaks a name tie by id, case-sensitively", () => {
    const a = named("MON-a", "api");
    const b = named("mon-A", "api");
    expect(partition([a, b]).rest.map((m) => m.id)).toEqual(["MON-a", "mon-A"]);
    expect(partition([b, a]).rest.map((m) => m.id)).toEqual(["MON-a", "mon-A"]);
  });
});
