// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Dashboard } from "./Dashboard";
import type { Monitor, MonitorStatus } from "./types";

afterEach(cleanup);

/** jsdom has no layout, so the heartbeat bar needs an explicit width. */
const WIDTH = 168;

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

/**
 * The dashboard is controlled, so the test owns the query state — which is
 * also the point: typing must drive the list through props, not local state.
 */
function Harness({
  monitors,
  announcement = null,
  compact,
}: {
  monitors: Monitor[];
  announcement?: string | null;
  compact?: boolean;
}) {
  const [query, setQuery] = useState("");
  return (
    <Dashboard
      monitors={monitors}
      compact={compact}
      query={query}
      onQueryChange={setQuery}
      announcement={announcement}
      beatWidth={WIDTH}
    />
  );
}

const rowIds = () =>
  [...document.querySelectorAll<HTMLElement>("tr.mon-row")].map(
    (row) => row.dataset.testid ?? "",
  );

const search = () => screen.getByRole("searchbox", { name: /search monitors/i });

describe("Dashboard", () => {
  it("filters rows out of the DOM as you search", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "up", { name: "API gateway" }),
          monitor("db", "up", { name: "Postgres" }),
          monitor("cdn", "up", { name: "CDN edge" }),
        ]}
      />,
    );
    expect(rowIds()).toHaveLength(3);

    fireEvent.change(search(), { target: { value: "postgres" } });

    expect(rowIds()).toEqual(["monitor-row-db"]);
    expect(screen.queryByText("API gateway")).toBeNull();
  });

  it("searches the target as well as the name", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "up", { name: "API gateway", target: "https://api.example.com" }),
          monitor("db", "up", { name: "Postgres", target: "db.internal:5432" }),
        ]}
      />,
    );
    fireEvent.change(search(), { target: { value: "5432" } });
    expect(rowIds()).toEqual(["monitor-row-db"]);
  });

  it("tells you how many of how many matched", () => {
    render(<Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />);
    fireEvent.change(search(), { target: { value: "api" } });
    expect(screen.getByText(/1 of 2 monitors match/)).toBeTruthy();
  });

  it("explains an empty search result rather than showing a blank table", () => {
    render(<Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />);
    fireEvent.change(search(), { target: { value: "kubernetes" } });
    expect(rowIds()).toEqual([]);
    expect(screen.getByText(/No monitors match/)).toBeTruthy();
  });

  it("restores every row when the search is cleared", () => {
    render(<Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />);
    fireEvent.change(search(), { target: { value: "api" } });
    expect(rowIds()).toHaveLength(1);
    fireEvent.change(search(), { target: { value: "" } });
    expect(rowIds()).toHaveLength(2);
  });

  it("puts the live region outside the table, never on it", () => {
    render(
      <Harness
        monitors={[monitor("api", "down"), monitor("db", "up")]}
        announcement="1 monitor down: api. 1 up."
      />,
    );

    const status = screen.getByRole("status");
    expect(status.textContent).toBe("1 monitor down: api. 1 up.");
    expect(status.getAttribute("aria-atomic")).toBe("true");

    // The whole point of research note 3: a live region wrapping the rows
    // would re-read the table on every heartbeat tick.
    const table = screen.getByRole("table", { name: /alphabetically by name/ });
    expect(table.contains(status)).toBe(false);
    expect(table.getAttribute("aria-live")).toBeNull();
    expect(table.closest("[aria-live]")).toBeNull();
  });

  it("keeps the live region silent when there is nothing to announce", () => {
    render(<Harness monitors={[monitor("api", "up")]} announcement={null} />);
    expect(screen.getByRole("status").textContent).toBe("");
  });

  it("summarises the counts in the heading", () => {
    render(
      <Harness
        monitors={[
          monitor("a", "up"),
          monitor("b", "up"),
          monitor("c", "down"),
          monitor("d", "paused"),
        ]}
      />,
    );
    const counts = document.querySelector(".mon-counts")!.textContent ?? "";
    expect(counts).toContain("1 down");
    expect(counts).toContain("2 up");
    expect(counts).toContain("1 paused");
    // No pending monitors, so no "0 pending" noise.
    expect(counts).not.toContain("pending");
  });

  it("has a real label on the search field, not just a placeholder", () => {
    render(<Harness monitors={[monitor("api", "up")]} />);
    expect(search()).toBeTruthy();
  });

  describe("layout", () => {
    it("renders rows on a wide viewport", () => {
      render(<Harness monitors={[monitor("api", "up")]} compact={false} />);
      expect(rowIds()).toEqual(["monitor-row-api"]);
      expect(screen.queryByTestId("monitor-card-api")).toBeNull();
    });

    it("renders cards on a narrow viewport", () => {
      render(<Harness monitors={[monitor("api", "up")]} compact />);
      expect(screen.getByTestId("monitor-card-api")).toBeTruthy();
      // Exactly one of the two, never both: two copies of every monitor would
      // double the DOM and hand a screen reader each one twice.
      expect(rowIds()).toEqual([]);
    });

    it("searches the same list in either layout", () => {
      render(
        <Harness
          monitors={[monitor("api", "up", { name: "API gateway" }), monitor("db", "up")]}
          compact
        />,
      );
      fireEvent.change(search(), { target: { value: "gateway" } });
      expect(screen.getByTestId("monitor-card-api")).toBeTruthy();
      expect(screen.queryByTestId("monitor-card-db")).toBeNull();
    });

    it("keeps the live region outside the card list too", () => {
      render(
        <Harness monitors={[monitor("api", "down")]} announcement="1 monitor down: api." compact />,
      );
      const status = screen.getByRole("status");
      expect(status.textContent).toBe("1 monitor down: api.");
      expect(document.querySelector(".mon-cards")!.contains(status)).toBe(false);
    });
  });
});
