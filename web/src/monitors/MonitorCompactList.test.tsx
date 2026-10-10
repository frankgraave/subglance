// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorCompactList } from "./MonitorCompactList";
import type { Monitor, MonitorStatus } from "./types";

/** monitors.css on disk: part of this layout's contract lives in CSS. */
const monitorsCss = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "monitors.css"),
  "utf8",
);

afterEach(cleanup);

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
    expect(numbers(el)).toEqual(["87 ms", "99.95%"]);
  });

  it("orders down monitors first, then alphabetically", () => {
    render(
      <MonitorCompactList
        monitors={[
          monitor("zulu", "up"),
          monitor("alpha", "up"),
          monitor("mike", "down"),
        ]}
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
        monitors={[
          monitor("api", "down", {
            error: "dial tcp: connect: connection refused",
            failureKind: "connection",
            latencyMs: null,
          }),
        ]}
      />,
    );

    const el = line("api");
    // A red lamp plus a dash says "broken, no idea why" (DESIGN.md §2.3). The
    // why is the failure kind in the incident row's words, in the row
    // layout's chip, with the server's full message as its title (SUB-203).
    const chip = el.querySelector(".mon-line-sub .mon-error") as HTMLElement;
    expect(chip.textContent).toBe("connection refused");
    expect(chip.title).toBe("dial tcp: connect: connection refused");
    expect(chip.classList.contains("chip--state")).toBe(true);
    // The chip stands before the address, in the same slot: the why is read
    // first, and the address is the one that gives way.
    const sub = el.querySelector(".mon-line-sub") as HTMLElement;
    expect([...sub.children].map((child) => child.className)).toEqual([
      "chip chip--state mon-error",
      "mon-line-target",
    ]);
  });

  it("leaves the latency slot to a latency", () => {
    render(
      <MonitorCompactList
        monitors={[
          monitor("api", "down", {
            error: "dial tcp: connect: connection refused",
            failureKind: "connection",
            latencyMs: null,
          }),
        ]}
      />,
    );
    // The raw error used to stand in for the number here, cut after eight
    // letters at 64px. The slot now says what it says on every other line.
    expect(numbers(line("api"))).toEqual(["—No latency data", "99.90%"]);
  });

  it("shows an unclassed failure's own message", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "down", { error: "connection refused", latencyMs: null })]}
      />,
    );
    // A failure the server did not class has no kind to translate, so its
    // own message is still more than a red lamp alone.
    const chip = line("api").querySelector(".mon-error") as HTMLElement;
    expect(chip.textContent).toBe("connection refused");
    expect(chip.title).toBe("connection refused");
  });

  it("says why an expiring monitor warns, as the row does", () => {
    render(
      <MonitorCompactList
        monitors={[
          monitor("tls", "expiring", {
            error: "certificate expires in 6 days",
            failureKind: "cert_expiry",
          }),
        ]}
      />,
    );
    // The rows layout has named this since SUB-186; the compact line said
    // nothing, because its own rule only covered `down`.
    expect(line("tls").querySelector(".mon-error")!.textContent).toBe("certificate expiring");
  });

  it("says nothing for a failure still under its threshold", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "warning", { error: "timeout", failureKind: "timeout" })]}
      />,
    );
    expect(line("api").querySelector(".mon-error")).toBeNull();
  });

  it("redraws when only the kind changes", () => {
    const down = (failureKind: string) =>
      monitor("api", "down", { error: "i/o timeout", failureKind, latencyMs: null });
    const { rerender } = render(<MonitorCompactList monitors={[down("connection")]} />);
    rerender(<MonitorCompactList monitors={[down("timeout")]} />);
    // The memo compares only what the line draws, and the kind is drawn.
    expect(line("api").querySelector(".mon-error")!.textContent).toBe("timed out");
  });

  it("keeps the latency reading when a down monitor has no error text", () => {
    render(
      <MonitorCompactList
        monitors={[monitor("api", "down", { latencyMs: 40 })]}
      />,
    );
    // Down without a reason is possible — a check can fail on a status code
    // and still have timed the response. The slot keeps the number, and no
    // empty chip claims a why that is not there.
    expect(numbers(line("api"))[0]).toBe("40 ms");
    expect(line("api").querySelector(".mon-error")).toBeNull();
  });

  it("gives a screen reader words where the eye gets an em dash", () => {
    render(
      <MonitorCompactList
        monitors={[
          monitor("api", "pending", { latencyMs: null, uptime24h: null }),
        ]}
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
    expect(screen.getByText("Nothing is being watched yet")).toBeTruthy();
  });

  it("draws each monitor as its own panel, not as a row in a ruled table", () => {
    // SUB-106 §10, the largest single layout difference: a list drawn with
    // shared dividers reads as a spreadsheet. Asserted on the structure the
    // panel stylesheet hangs off, because jsdom has no layout and computed
    // borders here would be the empty string either way.
    const { container } = render(
      <MonitorCompactList
        monitors={[monitor("api", "up"), monitor("db", "up")]}
      />,
    );
    expect(container.querySelectorAll(".panel-row")).toHaveLength(2);
    // And the container no longer wears the single box the lines used to sit
    // inside: the edge belongs to the row now.
    const stack = container.querySelector(".mon-line-stack");
    expect(stack?.classList.contains("panel-list")).toBe(true);
  });

  it("dims a measured zero without dressing it as a missing reading", () => {
    // 0 ms is an answer; an absent latency is not. The two must not render
    // alike, which is the whole reason Value carries both attributes.
    render(
      <MonitorCompactList
        monitors={[
          monitor("api", "up", { latencyMs: 0 }),
          monitor("db", "up", { latencyMs: null }),
        ]}
      />,
    );
    const zero = line("api").querySelector(".value");
    expect(zero?.getAttribute("data-zero")).toBe("true");
    expect(zero?.getAttribute("data-empty")).toBeNull();
    // The absent one does not go through Value at all — it is the em dash
    // plus its screen-reader words — so there is no zero marking to find.
    expect(line("db").querySelector("[data-zero]")).toBeNull();
  });

  it("tells a filtered-to-nothing list apart from an empty one", () => {
    render(<MonitorCompactList monitors={[]} query="nope" totalCount={12} />);
    expect(screen.getByText(/No monitors match/)).toBeTruthy();
  });
});

describe("MonitorCompactList grouped by a tag", () => {
  const tagged = () => [
    monitor("api", "up", { tags: { env: "prod" } }),
    monitor("db", "down", { tags: { env: "prod" } }),
    monitor("cdn", "up", { tags: { env: "staging" } }),
    monitor("legacy", "up", {}),
  ];

  const headings = () =>
    [...document.querySelectorAll(".mon-group-title")].map((h) => h.textContent);

  it("heads one section per tag value, untagged last", () => {
    render(<MonitorCompactList monitors={tagged()} groupKey="env" />);
    expect(headings()).toEqual([
      "Needs attention (1)",
      "prod (1)",
      "staging (1)",
      "Untagged (1)",
    ]);
  });

  it("keeps the lines list items, so the item count still matches the monitors", () => {
    render(<MonitorCompactList monitors={tagged()} groupKey="env" />);
    expect(lines()).toHaveLength(4);
  });

  it("puts the lines straight on the dashboard's card, with no panel between", () => {
    /*
     * The nesting rule, as a test, because it was found by eye.
     *
     * A line already carries its own border, radius and fill, so a `.panel`
     * wrapped around the list is a third surface framing a second one. That
     * structure shipped: measured against the reference it stepped the fill
     * one rung too light and pushed the first line 67px below the card's edge
     * where the reference puts it at 61px — the 6px inset spent twice, once by
     * the card and once by the wrapper.
     *
     * The card is the dashboard's now (SUB-183), so the list is rendered here
     * without one and nothing between it and its caller may be a panel or a
     * card.
     */
    const { container } = render(<MonitorCompactList monitors={tagged()} />);
    expect(container.querySelector(".panel, .card")).toBeNull();
    const list = container.querySelector(".mon-line-stack");
    expect(list?.parentElement, "the list is the component's root").toBe(container);
  });

  it("spends the six-pixel inset once, on the card", () => {
    // The stack gave up its own padding when the wrapper went. Leaving it
    // would reinstate half the bug — the inset doubled — without the extra
    // border to make it obvious.
    const stack = /\.mon-line-stack\s*\{([^}]*)\}/.exec(monitorsCss);
    expect(stack, "missing the .mon-line-stack rule").not.toBeNull();
    expect(
      stack?.[1],
      "the card already holds the lines 6px clear of its border",
    ).toMatch(/padding:\s*0/);
  });

  it("stays one flat list without a grouping key", () => {
    render(<MonitorCompactList monitors={tagged()} />);
    // No section heading at all: the one run of lines stands under the
    // dashboard card's own heading (SUB-183), and what makes it "flat" is
    // that there is exactly one list.
    expect(headings()).toEqual([]);
    expect(document.querySelectorAll("ul")).toHaveLength(1);
  });
});
