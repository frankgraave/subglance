// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorCardList } from "./MonitorCardList";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 295;

const monitor = (id: string, status: MonitorStatus, over: Partial<Monitor> = {}): Monitor => ({
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
    expect(within(card).getByRole("table", { name: /^api:/ })).toBeTruthy();
    expect(facts(card)).toEqual(["87 ms", "100%"]);
  });

  it("labels each number, because a card has no column header", () => {
    render(<MonitorCardList monitors={[monitor("api", "up")]} beatWidth={WIDTH} />);
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
        monitors={[monitor("new", "pending", { latencyMs: null, uptime24h: null })]}
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
        monitors={[monitor("alpha", "up"), monitor("zulu", "down"), monitor("beta", "up")]}
        beatWidth={WIDTH}
      />,
    );

    expect(screen.getByRole("heading", { name: "Needs attention (1)" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "All monitors (2)" })).toBeTruthy();
    // The order across the whole stack, not just within a section: the phone
    // user opened this after an alert and the broken thing must be on top.
    expect(cards().map((card) => card.getAttribute("data-testid"))).toEqual([
      "monitor-card-zulu",
      "monitor-card-alpha",
      "monitor-card-beta",
    ]);
  });

  it("drops the attention heading when nothing is down", () => {
    render(<MonitorCardList monitors={[monitor("alpha", "up")]} beatWidth={WIDTH} />);
    expect(screen.queryByRole("heading", { name: /Needs attention/ })).toBeNull();
    expect(screen.getByRole("heading", { name: "Monitors (1)" })).toBeTruthy();
  });

  it("tells 'nothing matched' apart from 'nothing exists'", () => {
    const { rerender } = render(<MonitorCardList monitors={[]} beatWidth={WIDTH} />);
    expect(screen.getByRole("heading", { name: "No monitors yet" })).toBeTruthy();

    rerender(<MonitorCardList monitors={[]} query="xyz" totalCount={4} beatWidth={WIDTH} />);
    expect(screen.getByRole("heading", { name: /No monitors match/ })).toBeTruthy();
  });

  it("uses a real list, not a reflowed table", () => {
    // Setting `display` on a table element drops table semantics in Safari,
    // which is why the mobile layout is a separate component rather than the
    // usual CSS-only reflow. The sr-only heartbeat tables are allowed; a
    // monitor list table is not.
    render(<MonitorCardList monitors={[monitor("api", "up")]} beatWidth={WIDTH} />);
    expect(screen.getAllByRole("list").length).toBeGreaterThan(0);
    expect(screen.queryByRole("table", { name: /monitors/ })).toBeNull();
  });
});
