// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Dashboard } from "./Dashboard";
import type { Monitor, MonitorStatus } from "./types";
import type { CardColumns, LayoutId } from "../shell/preferences";

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
  layout,
  cardColumns,
}: {
  monitors: Monitor[];
  announcement?: string | null;
  layout?: LayoutId;
  /* Passing this is what makes the column switcher render: the control is
     hidden unless the dashboard is given a way to change the value. */
  cardColumns?: CardColumns;
}) {
  const [query, setQuery] = useState("");
  const [columns, setColumns] = useState<CardColumns>(cardColumns ?? "1");
  return (
    <Dashboard
      monitors={monitors}
      layout={layout}
      query={query}
      onQueryChange={setQuery}
      announcement={announcement}
      beatWidth={WIDTH}
      cardColumns={columns}
      onCardColumnsChange={cardColumns === undefined ? undefined : setColumns}
    />
  );
}

const rowIds = () =>
  [...document.querySelectorAll<HTMLElement>("tr.mon-row")].map(
    (row) => row.dataset.testid ?? "",
  );

const search = () =>
  screen.getByRole("searchbox", { name: /search monitors/i });

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
          monitor("api", "up", {
            name: "API gateway",
            target: "https://api.example.com",
          }),
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

  it("narrows the list to one status when its count chip is pressed", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "up"),
          monitor("db", "down"),
          monitor("cdn", "up"),
        ]}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /1 down/ }));
    expect(rowIds()).toEqual(["monitor-row-db"]);
    expect(screen.getByText(/1 of 3 monitors is down/)).toBeTruthy();
  });

  it("clears the status filter when the pressed chip is pressed again", () => {
    render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    const chip = () => screen.getByRole("button", { name: /1 down/ });
    fireEvent.click(chip());
    expect(chip().getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(chip());
    expect(chip().getAttribute("aria-pressed")).toBe("false");
    expect(rowIds()).toHaveLength(2);
  });

  it("combines the status chip with the search box", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "down"),
          monitor("db", "down"),
          monitor("cdn", "up"),
        ]}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /2 down/ }));
    fireEvent.change(search(), { target: { value: "api" } });
    expect(rowIds()).toEqual(["monitor-row-api"]);
    expect(
      screen.getByText(/1 of 3 monitors is down and matches/),
    ).toBeTruthy();
  });

  it("keeps the counts whole while a status is filtered", () => {
    // The chips are the map of the whole list; recomputing them from the
    // filtered list would erase every other status the moment you pressed one,
    // leaving no way back and no idea what else is going on.
    render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /1 down/ }));
    expect(screen.getByRole("button", { name: /1 up/ })).toBeTruthy();
  });

  it("keeps the pressed chip when a live update empties its status", () => {
    // A data update, not a click: the last down monitor recovers while "down"
    // is the active filter. Drop the chip and the list is empty with no way
    // back; keep it and one press restores the full list.
    const { rerender } = render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /1 down/ }));
    expect(rowIds()).toEqual(["monitor-row-db"]);

    rerender(
      <Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />,
    );
    const chip = screen.getByRole("button", { name: /0 down/ });
    expect(chip.getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(chip);
    expect(rowIds()).toHaveLength(2);
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

  it("summarises the counts in the toolbar's status filter", () => {
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
    // `.mon-filter` since the counts moved out of a page heading and into the
    // toolbar, where they are the status filter rather than a summary line.
    const counts = document.querySelector(".mon-filter")!.textContent ?? "";
    expect(counts).toContain("1 down");
    expect(counts).toContain("2 up");
    expect(counts).toContain("1 paused");
    // No pending monitors, so no "0 pending" noise.
    expect(counts).not.toContain("pending");
  });

  it("keeps the filter a set of toggles, not a radio group", () => {
    /*
     * The filter is framed like the segmented control beside it so the two
     * read as siblings. That frame is the one thing about this design that
     * could mislead: a segmented control means one-of-N, and this is not.
     * "None selected" is a real state and pressing the active chip is how you
     * get back to the full list, so the chips must stay buttons carrying
     * aria-pressed — never role="radio", never a required selection.
     */
    render(
      <Harness monitors={[monitor("a", "up"), monitor("c", "down")]} />,
    );
    const filter = document.querySelector(".mon-filter")!;
    expect(filter.getAttribute("role")).toBe("group");
    expect(filter.querySelectorAll('[role="radio"]')).toHaveLength(0);

    const chips = [...filter.querySelectorAll("button")];
    expect(chips.length).toBeGreaterThan(1);
    // Nothing is pressed until the user presses something.
    expect(chips.every((c) => c.getAttribute("aria-pressed") === "false")).toBe(
      true,
    );

    fireEvent.click(chips[0]);
    expect(chips[0].getAttribute("aria-pressed")).toBe("true");
    // ...and pressing it again clears the filter rather than leaving one
    // option stuck on, which is what a radio group would do.
    fireEvent.click(chips[0]);
    expect(chips[0].getAttribute("aria-pressed")).toBe("false");
  });

  it("puts search at one end of the toolbar and the tools at the other", () => {
    /*
     * The order the toolbar exists for: search hard left, then the filter and
     * the view tools together at the right. Asserted as document order rather
     * than geometry, because jsdom has no layout — but document order is what
     * `margin-left: auto` and the flex row turn into position, and it is also
     * the order a keyboard walks them in.
     */
    render(
      <Harness monitors={[monitor("a", "up")]} layout="cards" cardColumns="2" />,
    );
    const bar = document.querySelector(".mon-topbar")!;
    const parts = [...bar.children].map((el) => el.className);
    expect(parts).toEqual(["sr-only", "mon-search", "mon-view-tools"]);

    // Both clusters live in the right-hand group, in that order.
    const tools = bar.querySelector(".mon-view-tools")!;
    expect([...tools.children].map((el) => el.className.split(" ")[0])).toEqual([
      "mon-filter",
      "segmented",
    ]);
  });

  it("has a real label on the search field, not just a placeholder", () => {
    render(<Harness monitors={[monitor("api", "up")]} />);
    expect(search()).toBeTruthy();
  });

  describe("layout", () => {
    it("renders rows on a wide viewport", () => {
      render(<Harness monitors={[monitor("api", "up")]} layout="rows" />);
      expect(rowIds()).toEqual(["monitor-row-api"]);
      expect(screen.queryByTestId("monitor-card-api")).toBeNull();
    });

    it("renders cards on a narrow viewport", () => {
      render(<Harness monitors={[monitor("api", "up")]} layout="cards" />);
      expect(screen.getByTestId("monitor-card-api")).toBeTruthy();
      // Exactly one of the two, never both: two copies of every monitor would
      // double the DOM and hand a screen reader each one twice.
      expect(rowIds()).toEqual([]);
    });

    it("searches the same list in either layout", () => {
      render(
        <Harness
          monitors={[
            monitor("api", "up", { name: "API gateway" }),
            monitor("db", "up"),
          ]}
          layout="cards"
        />,
      );
      fireEvent.change(search(), { target: { value: "gateway" } });
      expect(screen.getByTestId("monitor-card-api")).toBeTruthy();
      expect(screen.queryByTestId("monitor-card-db")).toBeNull();
    });

    it("renders one dense line per monitor in the compact layout", () => {
      render(
        <Harness
          monitors={[monitor("api", "up"), monitor("db", "down")]}
          layout="compact"
        />,
      );
      expect(screen.getByTestId("monitor-line-api")).toBeTruthy();
      // One list layout at a time, never two copies of the same monitor.
      expect(rowIds()).toEqual([]);
      expect(screen.queryByTestId("monitor-card-api")).toBeNull();
    });

    it("renders compact as one ungrouped list, not headed sections", () => {
      // DESIGN.md §7 grouping waits for tags to exist (§12). Until then the
      // layout is one dense stack, ordered by the shared partition — headed
      // "Needs attention" / "All monitors" sections would be grouping by
      // another name.
      render(
        <Harness
          monitors={[
            monitor("api", "up"),
            monitor("db", "down"),
            monitor("cache", "up"),
          ]}
          layout="compact"
        />,
      );
      expect(document.querySelectorAll(".mon-line-stack")).toHaveLength(1);
      expect(screen.queryByText(/needs attention/i)).toBeNull();
      expect(screen.queryByText(/all monitors/i)).toBeNull();
      // Ordering survives the flattening: down first, then alphabetical.
      expect(
        [...document.querySelectorAll(".mon-line-name")].map(
          (el) => el.textContent,
        ),
      ).toEqual(["db", "api", "cache"]);
    });

    it("drops the heartbeat bar in the compact layout, and only there", () => {
      const { unmount } = render(
        <Harness monitors={[monitor("api", "up")]} layout="compact" />,
      );
      // 40 rects x 200 monitors is the cost this layout exists to avoid.
      expect(document.querySelector(".mon-line svg")).toBeNull();
      unmount();

      render(<Harness monitors={[monitor("api", "up")]} layout="rows" />);
      expect(document.querySelector(".mon-row svg")).toBeTruthy();
    });

    it("keeps the live region outside the card list too", () => {
      render(
        <Harness
          monitors={[monitor("api", "down")]}
          announcement="1 monitor down: api."
          layout="cards"
        />,
      );
      const status = screen.getByRole("status");
      expect(status.textContent).toBe("1 monitor down: api.");
      expect(document.querySelector(".mon-cards")!.contains(status)).toBe(
        false,
      );
    });
  });

  describe("tag filtering", () => {
    const tagged = () => [
      monitor("api", "up", { tags: { env: "prod", customer: "acme" } }),
      monitor("db", "up", { tags: { env: "prod", customer: "globex" } }),
      monitor("cdn", "up", { tags: { env: "staging", customer: "acme" } }),
    ];

    const facet = (key: string) =>
      document.querySelector<HTMLSelectElement>(
        `[data-facet-key="${key}"] .mon-facet-select`,
      )!;

    it("offers one select per tag key, with Any first", () => {
      render(<Harness monitors={tagged()} />);
      const selects = [...document.querySelectorAll(".mon-facet-select")];
      expect(selects).toHaveLength(2);
      expect([...facet("env").options].map((o) => o.value)).toEqual([
        "",
        "prod",
        "staging",
      ]);
    });

    it("renders no facets at all when nothing is tagged", () => {
      render(<Harness monitors={[monitor("api", "up")]} />);
      expect(document.querySelector(".mon-facets")).toBeNull();
    });

    it("narrows the list to the chosen value", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "staging" } });
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
    });

    it("ANDs two keys together", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "prod" } });
      fireEvent.change(facet("customer"), { target: { value: "acme" } });
      expect(rowIds()).toEqual(["monitor-row-api"]);
    });

    it("keeps offering every value of a key after one is chosen", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "prod" } });
      // The facets come from the unfiltered list, so "staging" must still be
      // reachable — otherwise choosing it once removes the way back.
      expect([...facet("env").options].map((o) => o.value)).toEqual([
        "",
        "prod",
        "staging",
      ]);
    });

    it("returns to the full list via Any", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "staging" } });
      fireEvent.change(facet("env"), { target: { value: "" } });
      expect(rowIds()).toHaveLength(3);
    });

    it("names the tag filter in the sentence under the search box", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "prod" } });
      expect(document.querySelector(".mon-result-count")!.textContent).toBe(
        "2 of 3 monitors are tagged env:prod",
      );
    });

    it("says the filter is empty, not that there are no monitors", () => {
      render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "prod" } });
      fireEvent.change(facet("customer"), { target: { value: "globex" } });
      fireEvent.change(facet("env"), { target: { value: "staging" } });
      expect(rowIds()).toEqual([]);
      expect(document.querySelector(".mon-empty-title")!.textContent).toBe(
        "No monitors match this filter",
      );
    });

    it("drops a selection whose key disappears from the data", () => {
      const { rerender } = render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "staging" } });
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
      // A live update strips the tags. The stale selection must not survive
      // and hide every monitor with no control left to clear it.
      rerender(
        <Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />,
      );
      expect(rowIds()).toEqual(["monitor-row-api", "monitor-row-db"]);
    });

    it("drops a selection whose value disappears while the key stays", () => {
      const { rerender } = render(<Harness monitors={tagged()} />);
      fireEvent.change(facet("env"), { target: { value: "staging" } });
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
      // The key survives, so the select stays on screen, but it no longer
      // offers "staging". A filter you cannot see or clear must not keep
      // hiding rows.
      rerender(
        <Harness
          monitors={[
            monitor("api", "up", { tags: { env: "prod" } }),
            monitor("db", "up", { tags: { env: "prod" } }),
          ]}
        />,
      );
      expect([...facet("env").options].map((o) => o.value)).toEqual([
        "",
        "prod",
      ]);
      expect(rowIds()).toEqual(["monitor-row-api", "monitor-row-db"]);
    });
  });
});

describe("Dashboard grouping", () => {
  const tagged = () => [
    monitor("api", "up", { tags: { env: "prod" } }),
    monitor("db", "up", { tags: { env: "staging" } }),
    monitor("legacy", "up", {}),
  ];

  const groupSelect = () =>
    document.querySelector<HTMLSelectElement>(".mon-group-select")!;
  const headings = () =>
    [...document.querySelectorAll(".mon-section-title")].map(
      (h) => h.textContent,
    );

  it("offers one grouping option per tag key, plus None", () => {
    render(<Harness monitors={tagged()} />);
    expect([...groupSelect().options].map((o) => o.value)).toEqual(["", "env"]);
  });

  it("is flat until a key is chosen", () => {
    render(<Harness monitors={tagged()} />);
    expect(headings()).toEqual([]);
  });

  it("groups the list by the chosen key", () => {
    render(<Harness monitors={tagged()} />);
    fireEvent.change(groupSelect(), { target: { value: "env" } });
    expect(headings()).toEqual(["prod (1)", "staging (1)", "Untagged (1)"]);
    expect(rowIds()).toHaveLength(3);
  });

  it("groups only what the filters left visible", () => {
    render(<Harness monitors={tagged()} />);
    fireEvent.change(groupSelect(), { target: { value: "env" } });
    fireEvent.change(
      document.querySelector<HTMLSelectElement>(
        '[data-facet-key="env"] .mon-facet-select',
      )!,
      { target: { value: "prod" } },
    );
    expect(headings()).toEqual(["prod (1)"]);
    expect(rowIds()).toEqual(["monitor-row-api"]);
  });

  it("falls back to flat when the grouping key disappears from the data", () => {
    const { rerender } = render(<Harness monitors={tagged()} />);
    fireEvent.change(groupSelect(), { target: { value: "env" } });
    expect(headings()).toEqual(["prod (1)", "staging (1)", "Untagged (1)"]);
    // A live update strips the tags; the list must not stay headed by a key
    // that nothing carries any more.
    rerender(
      <Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />,
    );
    expect(headings()).toEqual([]);
    expect(rowIds()).toHaveLength(2);
  });
});
