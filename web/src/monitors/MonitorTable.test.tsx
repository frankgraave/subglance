// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MonitorTable } from "./MonitorTable";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 168;

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
  beats: Array.from({ length: 8 }, (_, i) => ({
    ts: 1_700_000_000_000 + i * 60_000,
    ok: true,
    latencyMs: 120,
  })),
  lastCheck: 1_700_000_000_000,
  ...over,
});

/**
 * The monitor table, found by the accessible name its <caption> gives it.
 *
 * `getByRole("table")` alone is ambiguous here: every HeartbeatBar renders its
 * own screen-reader-only table as its text alternative. Matching the caption
 * pins down the right one and asserts the caption is doing its job.
 */
function monTable(): HTMLTableElement {
  return screen.getByRole("table", {
    name: /alphabetically by name/,
  }) as HTMLTableElement;
}

/** Row order as the DOM has it, read off the row header of each monitor row. */
function rowNames(): string[] {
  return [
    ...document.querySelectorAll<HTMLElement>(
      "tr.mon-row th[scope='row'] .mon-name",
    ),
  ].map((node) => node.textContent ?? "");
}

describe("MonitorTable", () => {
  it("puts a down monitor above an up monitor in DOM order", () => {
    render(
      <MonitorTable
        monitors={[monitor("alpha", "up"), monitor("zulu", "down")]}
        beatWidth={WIDTH}
      />,
    );
    // "zulu" sorts after "alpha" alphabetically, so only the attention
    // section can be putting it first.
    expect(rowNames()).toEqual(["zulu", "alpha"]);
  });

  it("keeps the main list alphabetical rather than sorting it by status", () => {
    render(
      <MonitorTable
        monitors={[
          monitor("cache", "paused"),
          monitor("api", "pending"),
          monitor("db", "up"),
        ]}
        beatWidth={WIDTH}
      />,
    );
    expect(rowNames()).toEqual(["api", "cache", "db"]);
  });

  it("uses real table semantics: th scope=col in the head, th scope=row per row", () => {
    render(
      <MonitorTable
        monitors={[monitor("api", "up"), monitor("db", "up")]}
        beatWidth={WIDTH}
      />,
    );
    const table = monTable();

    // Direct-child selectors throughout: the nested heartbeat tables have
    // their own thead/th, and a descendant query would count those too.
    const head = table.querySelector(":scope > thead");
    expect(head).not.toBeNull();
    const colHeaders = [...head!.querySelectorAll(":scope > tr > th")];
    expect(colHeaders).toHaveLength(5);
    expect(colHeaders.every((th) => th.getAttribute("scope") === "col")).toBe(
      true,
    );
    expect(colHeaders.map((th) => th.textContent)).toEqual([
      "Status",
      "Monitor",
      "Last checks",
      "Latency",
      "24h",
    ]);

    const rowHeaders = [...table.querySelectorAll("tr.mon-row > th")];
    expect(rowHeaders).toHaveLength(2);
    expect(rowHeaders.every((th) => th.getAttribute("scope") === "row")).toBe(
      true,
    );

    // A colgroup, because the layout is table-layout: fixed.
    expect(table.querySelectorAll(":scope > colgroup > col")).toHaveLength(5);
    expect(table.querySelector(":scope > caption")).not.toBeNull();

    // Explicitly NOT a grid or a list: those promise behaviour we do not have.
    expect(table.getAttribute("role")).toBeNull();
  });

  it("does not carry aria-live anywhere inside the table", () => {
    render(
      <MonitorTable
        monitors={[monitor("api", "down"), monitor("db", "up")]}
        beatWidth={WIDTH}
      />,
    );
    const table = monTable();
    expect(table.getAttribute("aria-live")).toBeNull();
    // The heartbeat bar's own live region sits in the row's <figure>; what
    // must never exist is a live region wrapping the rows themselves.
    expect(
      table.querySelectorAll("tbody[aria-live], tr[aria-live], td[aria-live]"),
    ).toHaveLength(0);
  });

  it("shows an em dash and an explanation instead of 0% for missing uptime", () => {
    render(
      <MonitorTable
        monitors={[
          monitor("api", "pending", {
            uptime24h: null,
            latencyMs: null,
            beats: [],
          }),
        ]}
        beatWidth={WIDTH}
      />,
    );
    const row = screen.getByTestId("monitor-row-api");
    expect(row.textContent).not.toContain("0%");
    expect(row.textContent).not.toContain("0 ms");
    expect(within(row).getByText("No uptime data")).toBeTruthy();
    expect(within(row).getByText("No latency data")).toBeTruthy();
  });

  it("still renders a real 0% uptime, which is a fact and not missing data", () => {
    render(
      <MonitorTable
        monitors={[monitor("api", "down", { uptime24h: 0 })]}
        beatWidth={WIDTH}
      />,
    );
    const row = screen.getByTestId("monitor-row-api");
    expect(row.textContent).toContain("0%");
    expect(within(row).queryByText("No uptime data")).toBeNull();
  });

  it("names the status of every monitor in text, never colour alone", () => {
    render(
      <MonitorTable
        monitors={[
          monitor("a", "up"),
          monitor("b", "down"),
          monitor("c", "pending"),
          monitor("d", "paused"),
        ]}
        beatWidth={WIDTH}
      />,
    );
    for (const label of ["Up", "Down", "Pending", "Paused"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
  });

  it("offers onboarding copy, not a shrug, when there are no monitors at all", () => {
    render(<MonitorTable monitors={[]} beatWidth={WIDTH} />);
    expect(screen.getByText("Nothing is being watched yet")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("distinguishes an empty search result from an empty install", () => {
    render(
      <MonitorTable
        monitors={[]}
        query="kubernetes"
        totalCount={12}
        beatWidth={WIDTH}
      />,
    );
    expect(screen.getByText(/No monitors match/)).toBeTruthy();
    expect(
      screen.getByText(/clear the search to see all 12 monitors/),
    ).toBeTruthy();
    expect(screen.queryByText("Nothing is being watched yet")).toBeNull();
  });

  it("tells the user to clear the filters too when one is also active", () => {
    render(
      <MonitorTable
        monitors={[]}
        query="kubernetes"
        totalCount={12}
        filtered
        beatWidth={WIDTH}
      />,
    );
    // Clearing only the search would still leave the list narrowed, so the
    // copy must not promise all 12 monitors back.
    expect(
      screen.getByText(
        /clear the search and the active filters to see all 12 monitors/,
      ),
    ).toBeTruthy();
  });

  it("renders all 200 rows: the deliberate choice against virtualisation", () => {
    const many = Array.from({ length: 200 }, (_, i) =>
      monitor(`mon-${String(i).padStart(3, "0")}`, i === 7 ? "down" : "up"),
    );
    render(<MonitorTable monitors={many} beatWidth={WIDTH} />);

    // Every monitor is really in the DOM — no windowing, so Ctrl-F and a
    // screen reader's row count both tell the truth.
    expect(document.querySelectorAll("tr.mon-row")).toHaveLength(200);
    expect(screen.getByTestId("monitor-row-mon-199")).toBeTruthy();
    expect(rowNames()[0]).toBe("mon-007");
  });
});

describe("MonitorTable grouped by a tag", () => {
  const tagged = () => [
    monitor("api", "up", { tags: { env: "prod" } }),
    monitor("db", "down", { tags: { env: "prod" } }),
    monitor("cdn", "up", { tags: { env: "staging" } }),
    monitor("legacy", "up", {}),
  ];

  const headings = () =>
    [...document.querySelectorAll(".mon-section-title")].map(
      (h) => h.textContent,
    );

  it("puts one tbody per tag value, with the untagged group last", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(headings()).toEqual([
      "Needs attention (1)",
      "prod (1)",
      "staging (1)",
      "Untagged (1)",
    ]);
  });

  it("keeps a down monitor in the attention section, not in its tag group", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    const bodies = [...document.querySelectorAll("tbody")];
    expect(
      bodies[0].querySelector('[data-testid="monitor-row-db"]'),
    ).not.toBeNull();
    expect(
      bodies[1].querySelector('[data-testid="monitor-row-db"]'),
    ).toBeNull();
  });

  it("renders every monitor exactly once", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(document.querySelectorAll("tr.mon-row")).toHaveLength(4);
  });

  it("scopes a section heading to its row group, not to a colgroup", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    // Each section is its own tbody, so the heading describes the rows below
    // it rather than a set of columns.
    for (const th of document.querySelectorAll(".mon-section-title")) {
      expect(th.getAttribute("scope")).toBe("rowgroup");
    }
  });

  it("says in the caption that the table is grouped", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(document.querySelector("caption")!.textContent).toContain(
      "grouped by env",
    );
  });

  it("falls back to the flat attention/all split without a key", () => {
    render(<MonitorTable monitors={tagged()} beatWidth={WIDTH} />);
    expect(headings()).toEqual(["Needs attention (1)", "All monitors (3)"]);
  });
});
