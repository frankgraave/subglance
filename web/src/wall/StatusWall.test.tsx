// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StatusWall } from "./StatusWall";
import type { Monitor, MonitorStatus } from "../monitors/types";

afterEach(cleanup);

const monitor = (id: string, status: MonitorStatus, name = id): Monitor => ({
  id,
  name,
  status,
  tags: {},
  target: `https://${id}.example.com`,
  latencyMs: 120,
  uptime24h: 99.9,
  beats: [],
  lastCheck: 1_700_000_000_000,
});

/** A fixed instant, so the assertion does not depend on when the suite runs. */
const NOON = new Date(2026, 8, 11, 12, 34, 56).getTime();

const cardNames = () =>
  [...document.querySelectorAll(".wall-card-name")].map((el) => el.textContent);

describe("StatusWall", () => {
  it("shows a lamp and a name per monitor, and nothing else", () => {
    render(<StatusWall monitors={[monitor("api", "up")]} now={NOON} />);
    const card = document.querySelector(".wall-card")!;
    expect(card.querySelector(".led")).toBeTruthy();
    // No latency, no uptime, no target: from across a room they are unreadable
    // anyway, and they would compete with the lamps.
    expect(card.textContent).toContain("api");
    expect(card.textContent).not.toContain("120");
    expect(card.textContent).not.toContain("example.com");
  });

  it("puts down monitors first, then alphabetical — the shared ordering", () => {
    render(
      <StatusWall
        monitors={[monitor("c", "up"), monitor("a", "up"), monitor("z", "down")]}
        now={NOON}
      />,
    );
    expect(cardNames()).toEqual(["z", "a", "c"]);
  });

  it("gives a broken card a warm border, never a coloured fill", () => {
    render(<StatusWall monitors={[monitor("api", "down")]} now={NOON} />);
    // The rule is enforced in CSS via [data-status]; the component's job is to
    // expose the status as an attribute rather than baking a colour in.
    expect(document.querySelector('.wall-card[data-status="down"]')).toBeTruthy();
  });

  it("ticks a clock, because a frozen tab looks exactly like a calm wall", () => {
    render(<StatusWall monitors={[monitor("api", "up")]} now={NOON} />);
    expect(screen.getByText("12:34:56")).toBeTruthy();
  });

  it("carries the stale warning on the canvas, not in a banner", () => {
    render(<StatusWall monitors={[monitor("api", "up")]} stale now={NOON} />);
    // There is no chrome to hold a banner, so the wall itself is marked and
    // the header line takes a suffix (DESIGN.md §6).
    expect(document.querySelector('.wall[data-stale="true"]')).toBeTruthy();
    expect(screen.getByText(/connection lost, not updating/i)).toBeTruthy();
  });

  it("says nothing about the connection while it is healthy", () => {
    render(<StatusWall monitors={[monitor("api", "up")]} now={NOON} />);
    expect(screen.queryByText(/connection lost/i)).toBeNull();
    expect(document.querySelector('.wall[data-stale="true"]')).toBeNull();
  });

  it("stops asserting the down count once the stream is stale", () => {
    const monitors = [monitor("api", "down"), monitor("db", "up")];
    const { unmount } = render(<StatusWall monitors={monitors} now={NOON} />);
    expect(screen.getByText("1 down")).toBeTruthy();
    unmount();

    // Stale means we stopped hearing, so the count is history. Saying "1 down"
    // in the present tense is the confident lie DESIGN.md §6 forbids: it may
    // have been fixed, or nine more may have joined it.
    render(<StatusWall monitors={monitors} stale now={NOON} />);
    expect(screen.queryByText("1 down")).toBeNull();
    expect(screen.getByText(/1 down, last known/)).toBeTruthy();
  });

  it("renders its own frame around a first-load notice, not a bare sentence", () => {
    const exit = vi.fn();
    render(<StatusWall monitors={[]} onExit={exit} now={NOON} notice="Loading monitors…" />);
    // A wall display is usually unattended: leaving it on a bare sentence with
    // no header, no clock and no way out is the worst it can become.
    expect(screen.getByText("12:34:56")).toBeTruthy();
    expect(screen.getAllByText(/Loading monitors…/).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: /leave the status wall/i })).toBeTruthy();
    // The notice replaces the count rather than sitting next to it: there is
    // no list to count yet.
    expect(screen.queryByText(/0 monitors/)).toBeNull();
  });

  it("offers a visible way out, not only Esc", () => {
    const exit = vi.fn();
    render(<StatusWall monitors={[monitor("api", "up")]} onExit={exit} now={NOON} />);
    // A wall display is usually a machine nobody is sitting at; a keyboard-only
    // exit strands whoever walks up to it.
    fireEvent.click(screen.getByRole("button", { name: /leave the status wall/i }));
    expect(exit).toHaveBeenCalledTimes(1);
  });

  it("counts what is down in the whispered header", () => {
    render(
      <StatusWall monitors={[monitor("api", "down"), monitor("db", "up")]} now={NOON} />,
    );
    expect(screen.getByText(/2 monitors/)).toBeTruthy();
    expect(screen.getByText("1 down")).toBeTruthy();
  });
});
