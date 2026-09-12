// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Monitor } from "./types";
import type { LayoutId } from "../shell/preferences";

/**
 * The render-cost guardrail for SUB-65.
 *
 * `partition`, `filterMonitors` and `summarise` already have a cost budget in
 * `scale.test.ts`, but they are the cheap half. The expensive half is the DOM:
 * at 200 monitors a single heartbeat arriving over the stream produces a new
 * list array, and if that re-rendered every row the browser would rebuild
 * ~11k nodes and 200 SVGs three times a second (200 monitors on a 60s
 * interval). Virtualisation was rejected for this list (MonitorRow, DESIGN.md
 * §10), which makes `memo` plus a stable key the *only* thing keeping the
 * update cost proportional to what actually changed.
 *
 * Nothing failed when those comparators were absent: correctness tests pass
 * either way and the 14-monitor fixtures are too small to feel it. This file
 * is the test that fails — it counts how many rows React actually re-renders
 * for a one-monitor update, and the answer must be one.
 *
 * It counts renders by stubbing `HeartbeatBar`, the leaf every row and card
 * draws exactly once. Stubbing the leaf rather than the row leaves the
 * memoised boundary under test intact; spying on the row itself would replace
 * the very thing being measured.
 */

const renders = vi.hoisted(() => ({ count: 0 }));

vi.mock("../heartbeat/HeartbeatBar", () => ({
  HeartbeatBar: ({ label }: { label: string }) => {
    renders.count += 1;
    return <span data-testid="beat" data-label={label} />;
  },
}));

const { Dashboard } = await import("./Dashboard");

afterEach(cleanup);
beforeEach(() => {
  renders.count = 0;
});

const SCALE = 200;
const WIDTH = 168;
const T0 = 1_700_000_000_000;

function monitors(count: number): Monitor[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `mon-${String(i).padStart(3, "0")}`,
    name: `service-${String(i).padStart(3, "0")}`,
    status: "up" as const,
    target: `https://service-${i}.example.com`,
    latencyMs: 100 + i,
    uptime24h: 99.9,
    beats: [{ ts: T0, ok: true, latencyMs: 100 + i }],
    lastCheck: T0,
  }));
}

/**
 * Applies one heartbeat the way the live layer does: a brand-new array of
 * brand-new objects, of which exactly one monitor has new data. Every row's
 * props are referentially different, so a memo that compares by identity
 * alone would let all 200 through — which is the regression this catches.
 */
function oneBeatLater(list: readonly Monitor[], index: number): Monitor[] {
  return list.map((m, i) =>
    i === index
      ? { ...m, latencyMs: m.latencyMs! + 7, lastCheck: T0 + 60_000, beats: [...m.beats, { ts: T0 + 60_000, ok: true, latencyMs: m.latencyMs! + 7 }] }
      : { ...m, beats: [...m.beats] },
  );
}

function Harness({ list, layout }: { list: Monitor[]; layout: LayoutId }) {
  const [query, setQuery] = useState("");
  return (
    <Dashboard monitors={list} query={query} onQueryChange={setQuery} beatWidth={WIDTH} layout={layout} />
  );
}

describe.each<LayoutId>(["rows", "cards"])("one heartbeat in the %s layout", (layout) => {
  it(`re-renders 1 of ${SCALE} monitors`, () => {
    const list = monitors(SCALE);
    const { rerender } = render(<Harness list={list} layout={layout} />);

    expect(renders.count).toBe(SCALE); // the first paint draws everything
    renders.count = 0;

    rerender(<Harness list={oneBeatLater(list, 42)} layout={layout} />);

    // Not "less than 200": a budget that tolerates 199 tolerates the bug.
    expect(renders.count).toBe(1);
  });

  it("re-renders nothing when the payload is identical", () => {
    const list = monitors(SCALE);
    const { rerender } = render(<Harness list={list} layout={layout} />);
    renders.count = 0;

    // A poll that found no change still hands down fresh arrays and objects.
    rerender(<Harness list={list.map((m) => ({ ...m, beats: [...m.beats] }))} layout={layout} />);

    expect(renders.count).toBe(0);
  });
});
