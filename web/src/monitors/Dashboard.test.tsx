// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./Dashboard";
import type { Monitor, MonitorStatus } from "./types";
import type { CardColumns, LayoutId } from "../shell/preferences";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

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
  withLayouts = false,
  onLayoutChange,
}: {
  monitors: Monitor[];
  announcement?: string | null;
  layout?: LayoutId;
  /* Passing this is what makes the column switcher render: the control is
     hidden unless the dashboard is given a way to change the value. */
  cardColumns?: CardColumns;
  /* The same for the layout switcher: a no-op handler, for a test that only
     looks at where the switcher is. */
  withLayouts?: boolean;
  onLayoutChange?: (next: LayoutId) => void;
}) {
  const [query, setQuery] = useState("");
  const [columns, setColumns] = useState<CardColumns>(cardColumns ?? "1");
  return (
    <>
      <Dashboard
        monitors={monitors}
        layout={layout}
        query={query}
        onQueryChange={setQuery}
        announcement={announcement}
        beatWidth={WIDTH}
        cardColumns={columns}
        onCardColumnsChange={cardColumns === undefined ? undefined : setColumns}
        onLayoutChange={onLayoutChange ?? (withLayouts ? () => {} : undefined)}
      />
    </>
  );
}

const rowIds = () =>
  [...document.querySelectorAll<HTMLElement>("tr.mon-row")].map(
    (row) => row.dataset.testid ?? "",
  );

const search = () =>
  screen.getByRole("searchbox", { name: /filter monitors/i });

/** A status tab by its word, whatever its count: "Down 1". */
const tab = (word: string) =>
  screen.getByRole("button", { name: new RegExp(`^${word} \\d+$`) });

/** Opens the Filter panel, which holds the tag filters (SUB-183). */
const openFilter = () =>
  fireEvent.click(screen.getByRole("button", { name: /^Filter/ }));

/** Opens the View panel: layout, cards per row, grouping. */
const openView = () =>
  fireEvent.click(screen.getByRole("button", { name: /^View: / }));

/** Chooses one value of a tag key in the Filter panel, opening it first. */
function pick(key: string, value: string) {
  if (screen.queryByRole("dialog", { name: "Filter monitors" }) === null) openFilter();
  const panel = screen.getByRole("dialog", { name: "Filter monitors" });
  fireEvent.click(within(panel).getByRole("button", { name: new RegExp(`^${key}`) }));
  const values = panel.querySelector(`fieldset[data-facet-key="${key}"]`) as HTMLElement;
  fireEvent.click(within(values).getByRole("radio", { name: new RegExp(`^${value === "" ? "Any" : value}\\b`) }));
}

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

  it("narrows the list to one status when its tab is pressed", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "up"),
          monitor("db", "down"),
          monitor("cdn", "up"),
        ]}
      />,
    );
    fireEvent.click(tab("Down"));
    expect(rowIds()).toEqual(["monitor-row-db"]);
    expect(screen.getByText(/1 of 3 monitors is down/)).toBeTruthy();
  });

  it("clears the status filter when the pressed tab is pressed again, or All", () => {
    render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    expect(tab("All").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(tab("Down"));
    expect(tab("Down").getAttribute("aria-pressed")).toBe("true");
    expect(tab("All").getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(tab("Down"));
    expect(tab("Down").getAttribute("aria-pressed")).toBe("false");
    expect(rowIds()).toHaveLength(2);
    fireEvent.click(tab("Down"));
    fireEvent.click(tab("All"));
    expect(tab("All").getAttribute("aria-pressed")).toBe("true");
    expect(rowIds()).toHaveLength(2);
  });

  it("combines the status tab with the search box", () => {
    render(
      <Harness
        monitors={[
          monitor("api", "down"),
          monitor("db", "down"),
          monitor("cdn", "up"),
        ]}
      />,
    );
    fireEvent.click(tab("Down"));
    fireEvent.change(search(), { target: { value: "api" } });
    expect(rowIds()).toEqual(["monitor-row-api"]);
    expect(
      screen.getByText(/1 of 3 monitors is down and matches/),
    ).toBeTruthy();
  });

  it("keeps the counts whole while a status is filtered", () => {
    // The tabs are the map of the whole list; recomputing them from the
    // filtered list would erase every other status the moment you pressed one,
    // leaving no way back and no idea what else is going on.
    render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    fireEvent.click(tab("Down"));
    expect(screen.getByRole("button", { name: "Up 1" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "All 2" })).toBeTruthy();
  });

  it("keeps the pressed tab when a live update empties its status", () => {
    // A data update, not a click: the last down monitor recovers while "down"
    // is the active filter. Drop the tab and the list is empty with one way
    // back less; keep it and one press restores the full list.
    const { rerender } = render(
      <Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />,
    );
    fireEvent.click(tab("Down"));
    expect(rowIds()).toEqual(["monitor-row-db"]);

    rerender(
      <Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />,
    );
    const down = screen.getByRole("button", { name: "Down 0" });
    expect(down.getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(down);
    expect(rowIds()).toHaveLength(2);
  });

  it("drops a tab whose count is zero unless it is the one selected", () => {
    render(<Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />);
    expect(screen.queryByRole("button", { name: /^Down / })).toBeNull();
    expect(screen.getByRole("button", { name: "All 2" })).toBeTruthy();
  });

  it("offers one way back from an empty filtered list", () => {
    render(<Harness monitors={[monitor("api", "up"), monitor("db", "down")]} />);
    fireEvent.click(tab("Down"));
    fireEvent.change(search(), { target: { value: "api" } });
    expect(rowIds()).toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: "Clear all filters" }));
    expect(rowIds()).toHaveLength(2);
    expect(tab("All").getAttribute("aria-pressed")).toBe("true");
    expect((search() as HTMLInputElement).value).toBe("");
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

  it("counts every status in its tab, All first with the total", () => {
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
    const tabs = within(screen.getByRole("group", { name: "Filter by status" }))
      .getAllByRole("button")
      .map((button) => button.textContent);
    // No pending monitors, so no "Pending 0" noise.
    expect(tabs).toEqual(["All 4", "Down 1", "Paused 1", "Up 2"]);
  });

  it("keeps the tabs a set of toggles, not a radio group or a tablist", () => {
    /*
     * They narrow one list in place, so they are not tabs that switch panels
     * (no `role="tab"`) and not radios either: the chips' behaviour carried
     * over, and pressing the selected one is a way back to All.
     */
    render(
      <Harness monitors={[monitor("a", "up"), monitor("c", "down")]} />,
    );
    const group = screen.getByRole("group", { name: "Filter by status" });
    expect(group.querySelectorAll('[role="radio"], [role="tab"]')).toHaveLength(0);
    const buttons = within(group).getAllByRole("button");
    expect(buttons.map((b) => b.getAttribute("aria-pressed"))).toEqual(["true", "false", "false"]);
  });

  it("heads the list's card with its own controls", () => {
    /*
     * Where a control belongs (AGENTS.md, SUB-183): the tabs on the left of
     * the card's header, the text filter, Filter and View on the right.
     * Asserted by where each lands and
     * in what order rather than by geometry, because jsdom has no layout.
     */
    render(
      <Harness
        monitors={[monitor("a", "up", { tags: { env: "prod" } })]}
        layout="cards"
        cardColumns="2"
        withLayouts
      />,
    );
    const head = document.querySelector(".mon-board > .card-head")!;
    expect(head.querySelector(".card-head-lead [aria-label='Filter by status']")).not.toBeNull();
    expect(
      [...head.querySelector(".card-head-action")!.querySelectorAll("input, button")].map(
        (el) => el.getAttribute("aria-label") ?? el.getAttribute("type"),
      ),
    ).toEqual(["search", "Filter", "View: Cards"]);
    // The card is still headed for a screen reader, by a heading it does not
    // print: All carries the count it used to show.
    expect(screen.getByRole("heading", { level: 2, name: "Monitors" }).className).toBe("sr-only");
  });

  it("offers no layout switcher when nothing can change the layout", () => {
    /*
     * The workbench draws this dashboard beside a switcher of its own, so the
     * switcher here appears only when the dashboard is handed a way to change
     * the layout: two switchers for one setting would be one too many.
     */
    render(<Harness monitors={[monitor("a", "up")]} />);
    expect(screen.queryByRole("button", { name: /^View: / })).toBeNull();
    expect(
      screen.queryByRole("group", { name: "Dashboard layout" }),
    ).toBeNull();
  });

  it("reports the layout chosen in its View panel", () => {
    const chosen: LayoutId[] = [];
    render(
      <Harness
        monitors={[monitor("a", "up")]}
        layout="rows"
        onLayoutChange={(next) => chosen.push(next)}
      />,
    );
    openView();
    fireEvent.click(screen.getByRole("button", { name: "Compact" }));
    expect(chosen).toEqual(["compact"]);
  });

  it("names the view on its button, grouping included", () => {
    render(
      <Harness
        monitors={[monitor("a", "up", { tags: { team: "core" } })]}
        layout="rows"
        withLayouts
      />,
    );
    expect(screen.getByRole("button", { name: "View: Rows" }).textContent).toContain("Rows");
    openView();
    fireEvent.change(screen.getByRole("combobox", { name: "Group by" }), {
      target: { value: "team" },
    });
    expect(
      screen.getByRole("button", { name: "View: Rows, grouped by team" }).textContent,
    ).toContain("Rows \u00b7 by team");
  });

  it("offers cards per row only while Cards is on screen", () => {
    const { unmount } = render(
      <Harness monitors={[monitor("a", "up")]} layout="rows" cardColumns="2" withLayouts />,
    );
    openView();
    expect(screen.queryByRole("group", { name: "Cards per row" })).toBeNull();
    unmount();
    render(<Harness monitors={[monitor("a", "up")]} layout="cards" cardColumns="2" withLayouts />);
    openView();
    expect(screen.getByRole("group", { name: "Cards per row" })).toBeTruthy();
  });

  describe("the Filter and View panels", () => {
    it("close on Escape and hand focus back to their button", () => {
      render(
        <Harness monitors={[monitor("a", "up", { tags: { env: "prod" } })]} withLayouts />,
      );
      for (const [open, name] of [[openFilter, /^Filter/], [openView, /^View: /]] as const) {
        open();
        const dialog = screen.getByRole("dialog");
        expect(dialog.contains(document.activeElement)).toBe(true);
        fireEvent.keyDown(dialog, { key: "Escape" });
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(document.activeElement).toBe(screen.getByRole("button", { name }));
      }
    });

    it("close on a press outside them, without taking focus back", () => {
      render(<Harness monitors={[monitor("a", "up", { tags: { env: "prod" } })]} />);
      openFilter();
      expect(screen.getByRole("dialog")).toBeTruthy();
      fireEvent.mouseDown(document.body);
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    it("say they open a dialog, and whether it is open", () => {
      render(<Harness monitors={[monitor("a", "up", { tags: { env: "prod" } })]} />);
      const button = screen.getByRole("button", { name: /^Filter/ });
      expect(button.getAttribute("aria-haspopup")).toBe("dialog");
      expect(button.getAttribute("aria-expanded")).toBe("false");
      openFilter();
      expect(button.getAttribute("aria-expanded")).toBe("true");
      expect(button.getAttribute("aria-controls")).toBe(screen.getByRole("dialog").id);
    });
  });

  describe("on a phone", () => {
    function phone() {
      vi.stubGlobal("matchMedia", (query: string) => ({
        matches: query.includes("max-width: 640px"),
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }));
    }

    it("moves the text filter into the Filter sheet and counts it on the badge", () => {
      phone();
      render(<Harness monitors={[monitor("api", "up"), monitor("db", "up")]} />);
      // Not in the header: there is no room for it beside the tabs.
      expect(document.querySelector(".mon-board > .card-head input")).toBeNull();
      openFilter();
      fireEvent.change(search(), { target: { value: "api" } });
      expect(rowIds().length + document.querySelectorAll("[data-testid^='monitor-card-']").length).toBe(1);
      expect(screen.getByRole("button", { name: "Filter, 1 active" })).toBeTruthy();
      // The sheet's own way out says how many monitors it leaves.
      expect(screen.getByRole("button", { name: "Show 1 monitor" })).toBeTruthy();
      // And the query stands as a chip under the header, out of the sheet.
      fireEvent.click(screen.getByRole("button", { name: "Show 1 monitor" }));
      expect(screen.getByRole("button", { name: "Remove filter name: api" })).toBeTruthy();
    });

    it("offers only the layouts a phone can draw", () => {
      phone();
      render(
        <Harness monitors={[monitor("api", "up")]} layout="rows" cardColumns="2" withLayouts />,
      );
      openView();
      const layouts = within(screen.getByRole("group", { name: "Dashboard layout" }))
        .getAllByRole("button")
        .map((button) => button.textContent);
      expect(layouts).toEqual(["Cards", "Status wall"]);
      // One column is all a phone fits, whatever is chosen.
      expect(screen.queryByRole("group", { name: "Cards per row" })).toBeNull();
    });
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
      // "Needs attention" / "Other monitors" sections would be grouping by
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

    /** A key's value radios in the open panel, as "value count". */
    const values = (key: string) => {
      const panel = screen.getByRole("dialog", { name: "Filter monitors" });
      fireEvent.click(within(panel).getByRole("button", { name: new RegExp(`^${key}`) }));
      return [...panel.querySelectorAll(`fieldset[data-facet-key="${key}"] .mon-option`)].map(
        (option) => option.textContent,
      );
    };

    /*
     * SUB-183: one Filter button instead of a select per key. What must not
     * change from the selects it replaces is that each choice is a native,
     * labelled control: a radio per value inside a fieldset named by its key.
     */
    it("keeps every value a native radio inside a labelled group", () => {
      render(<Harness monitors={tagged()} />);
      openFilter();
      values("env");
      const group = screen.getByRole("group", { name: "env" });
      const radios = within(group).getAllByRole("radio");
      expect(radios.map((radio) => radio.tagName)).toEqual(["INPUT", "INPUT", "INPUT"]);
      for (const radio of radios) expect(radio.closest("label")).not.toBeNull();
    });

    it("lists every key with its current value, and Any first under each", () => {
      render(<Harness monitors={tagged()} />);
      openFilter();
      const keys = [...document.querySelectorAll(".mon-filter-key")].map((k) => k.textContent);
      expect(keys).toEqual(["customer Any", "env Any"]);
      expect(values("env")).toEqual(["Any 3", "prod 2", "staging 1"]);
    });

    it("counts each value against the other filters, not against its own key", () => {
      render(<Harness monitors={tagged()} />);
      pick("customer", "acme");
      // acme leaves api (prod) and cdn (staging): one each, and Any is both.
      expect(values("env")).toEqual(["Any 2", "prod 1", "staging 1"]);
      // Its own key is counted without its own choice, so switching to globex
      // says where it would land.
      expect(values("customer")).toEqual(["Any 3", "acme 2", "globex 1"]);
      fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
      fireEvent.click(tab("All"));
      fireEvent.change(search(), { target: { value: "cdn" } });
      openFilter();
      expect(values("env")).toEqual(["Any 1", "prod 0", "staging 1"]);
      // A value that would empty the list is still offered, marked empty.
      expect(document.querySelector('.mon-option[data-empty="true"]')?.textContent).toBe("prod 0");
    });

    it("opens on the first key that is filtering", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
      openFilter();
      expect(document.querySelector('.mon-filter-key[aria-pressed="true"]')?.textContent).toBe("env prod");
    });

    it("renders no Filter button at all when nothing is tagged", () => {
      render(<Harness monitors={[monitor("api", "up")]} />);
      expect(screen.queryByRole("button", { name: /^Filter/ })).toBeNull();
    });

    it("narrows the list to the chosen value", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "staging");
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
    });

    it("ANDs two keys together", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      pick("customer", "acme");
      expect(rowIds()).toEqual(["monitor-row-api"]);
      expect(screen.getByRole("button", { name: "Filter, 2 active" })).toBeTruthy();
    });

    it("keeps offering every value of a key after one is chosen", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      // The facets come from the unfiltered list, so "staging" must still be
      // reachable — otherwise choosing it once removes the way back.
      expect(values("env").map((v) => v?.replace(/ \d+$/, ""))).toEqual(["Any", "prod", "staging"]);
    });

    it("returns to the full list via Any, and via Clear tags", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "staging");
      pick("env", "");
      expect(rowIds()).toHaveLength(3);
      pick("env", "staging");
      fireEvent.click(screen.getByRole("button", { name: "Clear tags" }));
      expect(rowIds()).toHaveLength(3);
    });

    it("shows each chosen tag as a chip under the header that drops it", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      pick("customer", "acme");
      fireEvent.click(screen.getByRole("button", { name: "Done" }));
      expect(screen.queryByRole("dialog")).toBeNull();
      const chips = [...document.querySelectorAll(".mon-filter-chip")].map((c) => c.getAttribute("aria-label"));
      // In the keys' own order, as the panel lists them, not in the order
      // they were chosen: a row of chips that reorders itself is re-read.
      expect(chips).toEqual(["Remove filter customer: acme", "Remove filter env: prod"]);
      fireEvent.click(screen.getByRole("button", { name: "Remove filter env: prod" }));
      expect(rowIds()).toEqual(["monitor-row-api", "monitor-row-cdn"]);
      fireEvent.click(screen.getByRole("button", { name: "Clear all" }));
      expect(rowIds()).toHaveLength(3);
      expect(document.querySelector(".mon-filter-chip")).toBeNull();
    });

    it("names the tag filter in the sentence under the header", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      expect(document.querySelector(".mon-result-count")!.textContent).toBe(
        "2 of 3 monitors are tagged env:prod",
      );
      expect(document.querySelector(".mon-panel-count")!.textContent).toBe("2 of 3 monitors");
    });

    it("says the filter is empty, not that there are no monitors", () => {
      render(<Harness monitors={tagged()} />);
      pick("env", "prod");
      pick("customer", "globex");
      pick("env", "staging");
      expect(rowIds()).toEqual([]);
      expect(document.querySelector(".mon-empty-title")!.textContent).toBe(
        "No monitors match these filters",
      );
    });

    it("drops a selection whose key disappears from the data", () => {
      const { rerender } = render(<Harness monitors={tagged()} />);
      pick("env", "staging");
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
      pick("env", "staging");
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
      rerender(
        <Harness
          monitors={[
            monitor("api", "up", { tags: { env: "prod" } }),
            monitor("db", "up", { tags: { env: "prod" } }),
          ]}
        />,
      );
      expect(values("env")).toEqual(["Any 2", "prod 2"]);
      expect(rowIds()).toEqual(["monitor-row-api", "monitor-row-db"]);
    });

    it("forgets a dropped selection rather than reapplying it when the value returns", () => {
      const { rerender } = render(<Harness monitors={tagged()} />);
      pick("env", "staging");
      expect(rowIds()).toEqual(["monitor-row-cdn"]);
      rerender(
        <Harness
          monitors={[
            monitor("api", "up", { tags: { env: "prod" } }),
            monitor("db", "up", { tags: { env: "prod" } }),
          ]}
        />,
      );
      // "staging" comes back. Nobody chose it again, so it must not filter.
      rerender(<Harness monitors={tagged()} />);
      expect(rowIds()).toHaveLength(3);
      expect(screen.getByRole("button", { name: "Filter" })).toBeTruthy();
    });
  });
});

describe("Dashboard grouping", () => {
  const tagged = () => [
    monitor("api", "up", { tags: { env: "prod" } }),
    monitor("db", "up", { tags: { env: "staging" } }),
    monitor("legacy", "up", {}),
  ];

  const groupSelect = () => {
    if (screen.queryByRole("dialog", { name: "View" }) === null) openView();
    return screen.getByRole("combobox", { name: "Group by" }) as HTMLSelectElement;
  };
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
    pick("env", "prod");
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

  it("heads the sections of the card and compact layouts inside the one card", () => {
    render(<Harness monitors={tagged()} layout="cards" />);
    fireEvent.change(groupSelect(), { target: { value: "env" } });
    // One card around the whole list; the sections are parts of it.
    expect(document.querySelectorAll(".card")).toHaveLength(1);
    expect(
      [...document.querySelectorAll(".mon-group-title")].map((h) => [h.tagName, h.textContent]),
    ).toEqual([["H3", "prod (1)"], ["H3", "staging (1)"], ["H3", "Untagged (1)"]]);
  });
});
