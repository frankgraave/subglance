// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorCardList } from "./MonitorCardList";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 295;

const monitor = (
  id: string,
  status: MonitorStatus,
  over: Partial<Monitor> = {},
): Monitor => ({
  id,
  name: id,
  status,
  tags: {},
  target: `https://${id}.example.com`,
  latencyMs: 120,
  uptime24h: 99.9,
  beats: [{ ts: 1_700_000_000_000, ok: true, latencyMs: 120 }],
  lastCheck: 1_700_000_000_000,
  ...over,
});

const cards = () => screen.getAllByRole("listitem");

/**
 * The two numbers at the foot of a card.
 *
 * Read from the <dd>s specifically. The heartbeat bar renders its own sr-only
 * table of per-check latencies, so a loose text query inside a card happily
 * matches "120 ms" from a beat and proves nothing about the summary row.
 */
const facts = (card: HTMLElement) =>
  [...card.querySelectorAll(".mon-card-fact dd")].map((dd) => dd.textContent);

describe("MonitorCardList", () => {
  it("keeps every fact the desktop row shows", () => {
    render(
      <MonitorCardList
        monitors={[monitor("api", "up", { latencyMs: 87, uptime24h: 99.95 })]}
        beatWidth={WIDTH}
      />,
    );

    const card = cards()[0];
    // Status, name, target, heartbeat, latency and uptime: dropping any of
    // these is what DESIGN.md §12 calls "a list of names", which is the
    // failure this layout exists to avoid.
    expect(within(card).getByText("Up")).toBeTruthy();
    expect(within(card).getByRole("heading", { name: "api" })).toBeTruthy();
    expect(within(card).getByText("https://api.example.com")).toBeTruthy();
    // The heartbeat is present but no longer announced (SUB-100): in a list
    // layout the bar is aria-hidden and the card's own text carries the
    // facts, so it is asserted by its pixels rather than by its table.
    expect(card.querySelector(".hb-track")).toBeTruthy();
    expect(within(card).queryByRole("table")).toBeNull();
    expect(facts(card)).toEqual(["87 ms", "100%"]);
  });

  it("labels each number, because a card has no column header", () => {
    render(
      <MonitorCardList monitors={[monitor("api", "up")]} beatWidth={WIDTH} />,
    );
    // Scoped to the <dt>s: the heartbeat bar's sr-only table also has a
    // "Latency" column header, and matching that would prove nothing.
    const labels = [...cards()[0].querySelectorAll(".mon-card-fact dt")].map(
      (dt) => dt.textContent,
    );
    expect(labels).toEqual(["Latency", "24h uptime"]);
  });

  it("says the status in words as well as colour", () => {
    render(
      <MonitorCardList
        monitors={[monitor("db", "down", { error: "connection refused" })]}
        beatWidth={WIDTH}
      />,
    );
    const card = cards()[0];
    expect(within(card).getByText("Down")).toBeTruthy();
    expect(facts(card)).toHaveLength(2);
    expect(within(card).getByText("connection refused")).toBeTruthy();
  });

  it("renders an em dash, never a zero, for a value it does not have", () => {
    render(
      <MonitorCardList
        monitors={[
          monitor("new", "pending", { latencyMs: null, uptime24h: null }),
        ]}
        beatWidth={WIDTH}
      />,
    );
    const card = cards()[0];
    // The dash, and nothing that could be read as a measurement.
    expect(facts(card)).toEqual(["—No latency data", "—No uptime data"]);
  });

  it("puts down monitors first, under their own heading", () => {
    render(
      <MonitorCardList
        monitors={[
          monitor("alpha", "up"),
          monitor("zulu", "down"),
          monitor("beta", "up"),
        ]}
        beatWidth={WIDTH}
      />,
    );

    expect(
      screen.getByRole("heading", { name: "Needs attention (1)" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "All monitors (2)" }),
    ).toBeTruthy();
    // The order across the whole stack, not just within a section: the phone
    // user opened this after an alert and the broken thing must be on top.
    expect(cards().map((card) => card.getAttribute("data-testid"))).toEqual([
      "monitor-card-zulu",
      "monitor-card-alpha",
      "monitor-card-beta",
    ]);
  });

  it("drops the attention heading when nothing is down", () => {
    render(
      <MonitorCardList monitors={[monitor("alpha", "up")]} beatWidth={WIDTH} />,
    );
    expect(
      screen.queryByRole("heading", { name: /Needs attention/ }),
    ).toBeNull();
    expect(screen.getByRole("heading", { name: "Monitors (1)" })).toBeTruthy();
  });

  it("tells 'nothing matched' apart from 'nothing exists'", () => {
    const { rerender } = render(
      <MonitorCardList monitors={[]} beatWidth={WIDTH} />,
    );
    expect(
      screen.getByRole("heading", { name: "Nothing is being watched yet" }),
    ).toBeTruthy();

    rerender(
      <MonitorCardList
        monitors={[]}
        query="xyz"
        totalCount={4}
        beatWidth={WIDTH}
      />,
    );
    expect(
      screen.getByRole("heading", { name: /No monitors match/ }),
    ).toBeTruthy();
  });

  it("uses a real list, not a reflowed table", () => {
    // Setting `display` on a table element drops table semantics in Safari,
    // which is why the mobile layout is a separate component rather than the
    // usual CSS-only reflow. The sr-only heartbeat tables are allowed; a
    // monitor list table is not.
    render(
      <MonitorCardList monitors={[monitor("api", "up")]} beatWidth={WIDTH} />,
    );
    expect(screen.getAllByRole("list").length).toBeGreaterThan(0);
    expect(screen.queryByRole("table", { name: /monitors/ })).toBeNull();
  });
});

describe("MonitorCardList grouped by a tag", () => {
  const tagged = () => [
    monitor("api", "up", { tags: { env: "Prod" } }),
    monitor("db", "down", { tags: { env: "prod" } }),
    monitor("cdn", "up", { tags: { env: "prod" } }),
    monitor("legacy", "up", {}),
  ];

  // Section headings come from the shared Card component now, so they carry
  // its class rather than a per-layout one — which is the point of moving the
  // pattern into a component: one selector, one treatment, every screen.
  const headings = () =>
    [...document.querySelectorAll(".card-title")].map((h) => h.textContent);

  it("heads one section per tag value, untagged last", () => {
    render(
      <MonitorCardList monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(headings()).toEqual([
      "Needs attention (1)",
      "Prod (1)",
      "prod (1)",
      "Untagged (1)",
    ]);
  });

  it("gives each section a unique heading id even for values differing in case", () => {
    render(
      <MonitorCardList monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    const ids = [...document.querySelectorAll(".card-title")].map((h) => h.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const section of document.querySelectorAll(
      "section.mon-cards-section",
    )) {
      const labelledBy = section.getAttribute("aria-labelledby")!;
      expect(document.getElementById(labelledBy)).not.toBeNull();
    }
  });

  it("renders every monitor exactly once", () => {
    render(
      <MonitorCardList monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(cards()).toHaveLength(4);
  });

  it("falls back to the flat split without a key", () => {
    render(<MonitorCardList monitors={tagged()} beatWidth={WIDTH} />);
    expect(headings()).toEqual(["Needs attention (1)", "All monitors (3)"]);
  });
});

describe("cards per row", () => {
  /*
   * The column count reaches CSS through two channels, and the split is the
   * thing worth guarding: a fixed count is arithmetic `repeat()` can do from a
   * custom property, while "auto" is a different `grid-template-columns`
   * altogether, selected by the attribute. Encoding auto as a number would
   * mean inventing one, and any number is the wrong answer on some window.
   */
  const stack = (container: HTMLElement) =>
    container.querySelector(".mon-card-stack") as HTMLElement;

  it("passes a fixed count to CSS as a custom property", () => {
    const { container } = render(
      <MonitorCardList
        monitors={[monitor("api", "up")]}
        columns="3"
        beatWidth={WIDTH}
      />,
    );
    expect(stack(container).style.getPropertyValue("--mon-card-cols")).toBe("3");
    expect(stack(container).dataset.cols).toBe("3");
  });

  it("passes auto as an attribute and no number at all", () => {
    const { container } = render(
      <MonitorCardList
        monitors={[monitor("api", "up")]}
        columns="auto"
        beatWidth={WIDTH}
      />,
    );
    expect(stack(container).dataset.cols).toBe("auto");
    expect(
      stack(container).style.getPropertyValue("--mon-card-cols"),
      "auto must not be smuggled in as a made-up column count",
    ).toBe("");
  });

  it("defaults to one column, which is what this layout always was", () => {
    const { container } = render(
      <MonitorCardList monitors={[monitor("api", "up")]} beatWidth={WIDTH} />,
    );
    expect(stack(container).style.getPropertyValue("--mon-card-cols")).toBe("1");
  });

  it("applies the count to every stack when the list is grouped", () => {
    // Two sections, two stacks. A count applied to the first only would put
    // one group in a grid and leave the next as a column.
    const grouped = [
      monitor("api", "up", { tags: { env: "prod" } }),
      monitor("db", "up", { tags: { env: "staging" } }),
    ];
    const { container } = render(
      <MonitorCardList
        monitors={grouped}
        groupKey="env"
        columns="2"
        beatWidth={WIDTH}
      />,
    );
    const stacks = [...container.querySelectorAll(".mon-card-stack")];
    expect(stacks.length).toBeGreaterThan(1);
    for (const s of stacks) {
      expect((s as HTMLElement).style.getPropertyValue("--mon-card-cols")).toBe("2");
    }
  });
});
