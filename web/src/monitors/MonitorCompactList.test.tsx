// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorCompactList } from "./MonitorCompactList";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

const monitor = (id: string, status: MonitorStatus, over: Partial<Monitor> = {}): Monitor => ({
  id,
  name: id,
  status,
  target: `https://${id}.example.com`,
  latencyMs: 120,
  uptime24h: 99.9,
  beats: [{ ts: 1_700_000_000_000, ok: true, latencyMs: 120 }],
  lastCheck: 1_700_000_000_000,
  ...over,
});

const lines = () => screen.getAllByRole("listitem");
const line = (id: string) => screen.getByTestId(`monitor-line-${id}`);

/**
 * The two right-hand slots of a line.
 *
 * Read from `.mon-line-num` specifically rather than by text: a bare text
 * query would also match the name or target of a monitor named after a
 * number, and prove nothing about which column the value landed in.
 */
const numbers = (el: HTMLElement) =>
  [...el.querySelectorAll(".mon-line-num")].map((n) => n.textContent);

describe("MonitorCompactList", () => {
  it("keeps the four facts it claims to keep, minus the heartbeat", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "up", { latencyMs: 87, uptime24h: 99.95 })]}
      />,
    );

    const el = line("api");
    // Lamp, name, target, latency, uptime. The heartbeat is dropped by
    // design; anything else going missing makes this a list of names.
    expect(within(el).getByText("Up")).toBeTruthy();
    expect(within(el).getByText("api")).toBeTruthy();
    expect(within(el).getByText("https://api.example.com")).toBeTruthy();
    expect(numbers(el)).toEqual(["87 ms", "100%"]);
  });

  it("orders down monitors first, then alphabetically", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("zulu", "up"), monitor("alpha", "up"), monitor("mike", "down")]}
      />,
    );
    // The same `partition` rule the table and the cards use: this is the one
    // promise the three layouts share, so a change here is a change to all.
    expect(lines().map((el) => el.getAttribute("data-testid"))).toEqual([
      "monitor-line-mike",
      "monitor-line-alpha",
      "monitor-line-zulu",
    ]);
  });

  it("says why a monitor is down instead of leaving colour to carry it", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "down", { error: "connection refused", latencyMs: null })]}
      />,
    );

    const el = line("api");
    // A red lamp plus a dash says "broken, no idea why". The row layout puts
    // the reason in the latency slot for exactly this case and the dense
    // layout has no excuse to be quieter (DESIGN.md §2.3).
    expect(within(el).getByText("connection refused")).toBeTruthy();
    expect(within(el).getByTitle("connection refused")).toBeTruthy();
  });

  it("keeps the latency reading when a down monitor has no error text", () => {
    render(<MonitorCompactList monitors={[monitor("api", "down", { latencyMs: 40 })]} />);
    // Down without a reason is possible — a check can fail on a status code
    // and still have timed the response. The slot falls back to the number
    // rather than going blank.
    expect(numbers(line("api"))[0]).toBe("40 ms");
  });

  it("gives a screen reader words where the eye gets an em dash", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "pending", { latencyMs: null, uptime24h: null })]}
      />,
    );

    const el = line("api");
    // A bare "—" is announced as nothing at all by most screen readers, so a
    // missing value and a value of zero become indistinguishable. `Unknown`
    // pairs the dash with hidden text; the other two layouts already do this.
    expect(within(el).getByText("No latency data")).toBeTruthy();
    expect(within(el).getByText("No uptime data")).toBeTruthy();
    expect(numbers(el)).toEqual(["—No latency data", "—No uptime data"]);
  });

  it("shows the empty state rather than an empty box", () => {
    render(<MonitorCompactList monitors={[]} />);
    expect(screen.getByText("No monitors yet")).toBeTruthy();
  });

  it("tells a filtered-to-nothing list apart from an empty one", () => {
    render(<MonitorCompactList monitors={[]} query="nope" totalCount={12} />);
    expect(screen.getByText(/No monitors match/)).toBeTruthy();
  });
});
