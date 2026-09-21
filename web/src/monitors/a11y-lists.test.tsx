// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorTable } from "./MonitorTable";
import { MonitorCardList } from "./MonitorCardList";
import { MonitorCompactList } from "./MonitorCompactList";
import type { Monitor, MonitorStatus } from "./types";

/**
 * SUB-100: the two list-layout accessibility defects, held down from both
 * ends.
 *
 * 1. Status was hue alone outside the card layout. `down`, `pending` and
 *    `waiting-for-first-data` all drew a 2px leading edge and a filled 20x7
 *    pill, differing only in red/amber/grey, and the status word was
 *    `sr-only`. A screen reader was fine; a sighted colour-blind reader had
 *    nothing.
 * 2. Every heartbeat bar in every row was focusable and shipped its own
 *    screen-reader table. At 200 monitors that is 402 tab stops before the
 *    last row's link and ~33k DOM nodes against the ~11k that the decision
 *    not to virtualise rests on.
 *
 * The numbers below are asserted as *budgets*, not as the measured figures:
 * the point is that they cannot silently regress past the documented ceiling.
 */

afterEach(cleanup);

const T0 = 1_700_000_000_000;
const WIDTH = 168;

/**
 * A full series, not two beats. The sr-only table this ticket removes had one
 * `<tr>` per *slot*, so a thin fixture would under-count the very thing being
 * budgeted: at `beatWidth` 168 the bar buckets into 28 columns, which is the
 * width the row actually draws.
 */
const SERIES = Array.from({ length: 60 }, (_, i) => ({
  ts: T0 - (60 - i) * 60_000,
  ok: true,
  latencyMs: 100 + i,
}));

/**
 * Names and targets deliberately avoid the status words. An earlier draft
 * named them `service-down`, so the assertion that the word is *visible*
 * passed against the old sr-only markup by matching the monitor's own name —
 * a test that proved nothing.
 */
function monitor(id: string, status: MonitorStatus): Monitor {
  return {
    id,
    name: `service-${id}`,
    status,
    target: `https://${id}.example.com`,
    latencyMs: status === "down" ? null : 120,
    uptime24h: 99.5,
    beats: SERIES,
    lastCheck: T0,
    tags: {},
  };
}

/** Ids that share no substring with any status word. See `monitor`. */
const IDS: Record<MonitorStatus, string> = {
  up: "alpha",
  down: "bravo",
  warning: "foxtrot",
  pending: "charlie",
  paused: "delta",
  // A push monitor that has never reported. It shares the grey idle lamp with
  // nothing else visible, which is exactly the case this file exists for.
  waiting: "echo",
};

const ALL: MonitorStatus[] = ["up", "down", "warning", "pending", "paused", "waiting"];

/** Text a sighted reader can actually see: `sr-only` text is excluded. */
function visibleText(el: Element): string {
  const clone = el.cloneNode(true) as Element;
  clone.querySelectorAll(".sr-only, .hb-sr-only").forEach((n) => n.remove());
  return (clone.textContent ?? "").trim();
}

describe("status is never hue alone in a list layout", () => {
  it("keeps the rows layout's status word in the accessibility tree", () => {
    /*
     * `up` used to be the deliberate exception: no word was the up signal,
     * on the argument that printing "Up" down 190 rows would drown the three
     * that matter. SUB-135 then made the header visible and printed the word
     * on every row, including `up`.
     *
     * SUB-140 hid it again, all five of them: the product owner asked for the
     * lamp alone in this cell. The assertion moved with the feature rather
     * than being deleted — what this file exists to stop is the status being
     * *unavailable*, and `sr-only` is not unavailable. The word must still be
     * in the row's accessible name for every status.
     *
     * That it is genuinely clipped rather than `display: none` — which would
     * take it out of the tree along with the pixels — is a CSS claim, so it
     * is checked in a real browser by
     * `layout/status-legibility.browser.test.ts`, which also keeps a WCAG
     * measurement on what now carries status visually. DESIGN.md §9.1 states
     * the whole trade.
     */
    render(
      <MonitorTable
        monitors={ALL.map((s) => monitor(IDS[s], s))}
        beatWidth={WIDTH}
      />,
    );
    for (const status of ALL) {
      const row = screen.getByTestId(`monitor-row-${IDS[status]}`);
      // Announced…
      expect(row.textContent ?? "").toMatch(new RegExp(status, "i"));
      // …and not drawn, which is the change the owner asked for.
      expect(visibleText(row)).not.toMatch(new RegExp(status, "i"));
    }
  });

  it("shows the status word on compact lines for everything that is not up", () => {
    render(
      <MonitorCompactList monitors={ALL.map((s) => monitor(IDS[s], s))} />,
    );
    for (const status of ALL) {
      const line = screen.getByTestId(`monitor-line-${IDS[status]}`);
      const shown = visibleText(line);
      if (status === "up") {
        expect(shown).not.toMatch(/Up/);
      } else {
        expect(shown).toMatch(new RegExp(status, "i"));
      }
    }
  });

  it("keeps every status announced, whether or not the word is drawn", () => {
    render(
      <MonitorTable
        monitors={ALL.map((s) => monitor(IDS[s], s))}
        beatWidth={WIDTH}
      />,
    );
    for (const status of ALL) {
      const row = screen.getByTestId(`monitor-row-${IDS[status]}`);
      expect(row.textContent ?? "").toMatch(new RegExp(status, "i"));
    }
  });
});

describe("the heartbeat bar costs nothing in a list layout", () => {
  const many = (n: number) =>
    Array.from({ length: n }, (_, i) =>
      monitor(`m${String(i).padStart(3, "0")}`, "up"),
    );

  it("leaves the rows layout one tab stop per monitor", () => {
    const { container } = render(
      <MonitorTable monitors={many(200)} beatWidth={WIDTH} />,
    );
    const stops = container.querySelectorAll("a[href], button, [tabindex]");
    // One link per monitor and nothing else. 402 before this change.
    expect(stops.length).toBe(200);
  });

  it("leaves the cards layout one tab stop per monitor", () => {
    const { container } = render(
      <MonitorCardList monitors={many(200)} beatWidth={WIDTH} />,
    );
    expect(
      container.querySelectorAll("a[href], button, [tabindex]").length,
    ).toBe(200);
  });

  it("keeps a 200-monitor rows render inside the documented node budget", () => {
    const { container } = render(
      <MonitorTable monitors={many(200)} beatWidth={WIDTH} />,
    );
    const nodes = container.querySelectorAll("*").length;
    // MonitorRow documents ~11k as the basis for not virtualising, and that
    // number is now true again. With one sr-only table per row this fixture
    // rendered well past it.
    expect(nodes).toBeLessThan(11_000);
  });

  it("emits no per-row screen-reader table, because the row already says it", () => {
    render(<MonitorTable monitors={many(20)} beatWidth={WIDTH} />);
    // Exactly one table on the page: the monitor table itself.
    expect(screen.getAllByRole("table").length).toBe(1);
  });

  it("hides the list bar from assistive technology rather than half-labelling it", () => {
    const { container } = render(
      <MonitorTable monitors={many(1)} beatWidth={WIDTH} />,
    );
    const figure = container.querySelector("figure");
    expect(figure?.getAttribute("aria-hidden")).toBe("true");
    expect(container.querySelector(".hb-track")?.hasAttribute("tabindex")).toBe(
      false,
    );
    // The pixels are still drawn: hiding it must not mean dropping it.
    expect(container.querySelectorAll(".hb-bar").length).toBeGreaterThan(0);
  });
});

describe("the detail view keeps the readable instrument", () => {
  it("stays focusable and keeps its table when it is the only bar on screen", async () => {
    const { HeartbeatBar } = await import("../heartbeat/HeartbeatBar");
    render(<HeartbeatBar beats={SERIES} label="api" width={WIDTH} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    expect(track.getAttribute("tabindex")).toBe("0");
    expect(track.getAttribute("role")).toBe("group");
    expect(
      within(screen.getByRole("table")).getAllByRole("row").length,
    ).toBeGreaterThan(1);
  });
});
