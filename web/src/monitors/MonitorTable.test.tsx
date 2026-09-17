// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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

/** monitors.css on disk. Some of this component's contract lives in CSS. */
const monitorsCss = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "monitors.css"),
  "utf8",
);

/** card.css on disk: the frame around this table is the shared `Card` now. */
const cardCss = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "..", "components", "card.css"),
  "utf8",
);

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

  /*
   * SUB-140: the card that frames this table names and counts the list.
   *
   * The rows layout was the one list screen with no icon and no title on its
   * frame, next to a compact layout that already said "Monitors (N)" — which
   * is the inconsistency the product owner named. DESIGN.md §8.3 fixes the
   * form as `Name (N)`.
   */
  it("heads the table with a card titled `Monitors (N)`", () => {
    render(<MonitorTable monitors={tagged()} beatWidth={WIDTH} />);
    expect(
      screen.getByRole("heading", { name: "Monitors (4)", level: 2 }),
      "the frame around the rows layout needs the same titled card every other list screen has",
    ).toBeTruthy();
  });

  it("counts every monitor in the title, not one section of them", () => {
    // The fixture splits 4 into "Needs attention (1)" and "All monitors (3)".
    // The card names the whole; a title equal to either section would make the
    // two headings read as alternatives rather than as parts (§8.3).
    render(<MonitorTable monitors={tagged()} beatWidth={WIDTH} />);
    const title = screen.getByRole("heading", { level: 2 }).textContent;
    expect(title).toBe("Monitors (4)");
    expect(headings()).toEqual(["Needs attention (1)", "All monitors (3)"]);
  });

  it("keeps the title and its count when the table is grouped", () => {
    render(
      <MonitorTable monitors={tagged()} groupKey="env" beatWidth={WIDTH} />,
    );
    expect(
      screen.getByRole("heading", { name: "Monitors (4)", level: 2 }),
    ).toBeTruthy();
  });

  it("gives the card header a decorative glyph, not a second accessible name", () => {
    const { container } = render(
      <MonitorTable monitors={tagged()} beatWidth={WIDTH} />,
    );
    const glyph = container.querySelector(".icon-tile svg");
    expect(glyph, "the card header needs its icon tile").not.toBeNull();
    // The heading already says what the list is; a glyph that announces itself
    // makes a screen reader say it twice (§8.3, AGENTS.md).
    expect(glyph?.getAttribute("aria-hidden")).toBe("true");
  });
});

describe("the panel look does not cost the table its semantics", () => {
  // §10 draws each row as its own panel. The tempting way to get that look is
  // to stop using a table, or to put display:flex on its parts — both throw
  // away row and column announcements, and Safari drops table semantics
  // entirely on the second. The point of these assertions is that the visual
  // convention and the accessibility tree are not in competition: the panels
  // are drawn with border-spacing and per-cell corners, on a real table.
  it("keeps a caption, column headers and a row header per monitor", () => {
    render(
      <MonitorTable
        monitors={[monitor("api", "up"), monitor("db", "down")]}
        beatWidth={WIDTH}
      />,
    );

    const table = monTable();
    expect(table.querySelector("caption")).not.toBeNull();
    expect(table.querySelectorAll("th[scope='col']").length).toBeGreaterThan(0);
    expect(
      within(table).getAllByRole("rowheader").length,
      "every monitor row needs its own row header",
    ).toBe(2);
  });

  it("gives a down row no resting fill, in any layout", () => {
    /*
     * SUB-140. The product owner asked for the red row background to go
     * ("graag geen rode achtergrond"), and DESIGN.md §2.3 was rewritten to
     * match rather than left contradicting the code — which is exactly how a
     * later change re-adds the fill citing the old rule. This is the
     * assertion that makes the doc's new position enforceable.
     *
     * Read off the stylesheet, not computed style: jsdom applies no CSS.
     */
    const offenders: string[] = [];
    for (const match of monitorsCss.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      const selector = match[1].trim();
      if (!/\[data-status="down"\]/.test(selector)) continue;
      const body = match[2].replace(/\/\*[\s\S]*?\*\//g, "");
      const fill = /(?:^|[;{\s])background(?:-color)?:\s*([^;]+)/.exec(body);
      if (!fill) continue;
      offenders.push(`${selector} fills with ${fill[1].trim()}`);
    }
    expect(
      offenders,
      "down marks itself with a coloured leading edge like every other status (§2.3)",
    ).toEqual([]);
  });

  it("leaves no `-deep` hover fill behind either", () => {
    // The hover tint existed only to deepen a resting tint. With no resting
    // fill it would *introduce* red under the pointer, which is the signal
    // switching the deepening rule was written to forbid (§2.3).
    //
    // Comments are stripped first: this file explains at length why the token
    // went, and a naive scan would match its own reasoning.
    const live = monitorsCss.replace(/\/\*[\s\S]*?\*\//g, "");
    expect(live).not.toMatch(/var\(--[a-z]+-deep\)/);
  });

  it("leaves the table parts as table elements", () => {
    // A layout display value on a table part is the specific edit that breaks
    // Safari. Read from the stylesheet rather than from computed style:
    // jsdom applies no CSS, so a computed check here would pass no matter what
    // monitors.css says — it would be a test that cannot fail.
    const css = monitorsCss;
    const offenders: string[] = [];
    for (const match of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      const selector = match[1].trim();
      if (!/\.mon-(row|table)\b/.test(selector)) continue;
      const display = /(?:^|[;{\s])display:\s*([a-z-]+)/.exec(match[2]);
      if (!display) continue;
      if (/^(flex|grid|contents|block|inline)/.test(display[1])) {
        offenders.push(`${selector} sets display:${display[1]}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("draws the rows as separated panels, not a collapsed grid", () => {
    // border-collapse:collapse merges the edges into shared hairlines, which
    // is the ruled-table look §10 removes. This is the one declaration that
    // silently undoes the whole panel treatment.
    const table = /\.mon-table\s*\{([^}]*)\}/.exec(monitorsCss);
    expect(table, "missing the .mon-table rule").not.toBeNull();
    expect(table?.[1]).toMatch(/border-collapse:\s*separate/);
    expect(table?.[1]).toMatch(/border-spacing:/);
  });

  it("frames the panels in a card that is padding, not a second panel", () => {
    // The nesting is what makes a list read as one object: an outer card with
    // a wider radius, its own quieter fill, and — the part that does the work —
    // padding, so the panels sit inset from its border rather than flush
    // against it. A frame without padding is two edges at the same level, and
    // that is the double-framing an earlier pass rightly removed.
    //
    // Read off `.card` rather than the `.mon-board` this used to check
    // (SUB-140). That class restated, declaration for declaration, what the
    // shared `Card` component already says, and the rows layout was the only
    // list screen drawing its frame by hand — which is why it was also the
    // only one with no icon and no title in its header. The assertion did not
    // move to a weaker place: it now guards the rule for every card in the
    // product instead of one screen's copy of it.
    const board = /\.card\s*\{([^}]*)\}/.exec(cardCss);
    expect(board, "missing the .card rule").not.toBeNull();
    const body = board?.[1] ?? "";

    expect(body, "the card needs its own edge").toMatch(/border:\s*1px/);
    expect(body, "the card needs the wider radius").toMatch(
      /border-radius:\s*var\(--r-lg\)/,
    );
    expect(
      body,
      "without padding the frame sits flush on the panels and reads as a second edge",
    ).toMatch(/padding:\s*var\(--space-1h\)/);
    expect(
      body,
      "a transparent card cannot be the surface the panels rest on",
    ).toMatch(/background:\s*var\(--surface\)/);

    // And the panels inside must keep the tighter radius, or the nesting
    // inverts and the card stops reading as the thing underneath.
    const firstCell = /\.mon-row\s*>\s*:first-child\s*\{([^}]*)\}/.exec(
      monitorsCss,
    );
    expect(firstCell?.[1]).toMatch(
      /border-start-start-radius:\s*var\(--r-md\)/,
    );
  });

});

/**
 * SUB-135: the lamp column's header is a visible word.
 *
 * It was `sr-only`, so the cell was real, 104px wide and drew nothing — an
 * empty block beside four labelled headers, which is what made the top-left of
 * the table look unfinished. The existing header test above reads
 * `textContent`, which is identical whether the word is visible or clipped, so
 * it could not tell the two apart.
 *
 * Asserted as "not inside an sr-only span" rather than by measuring, because
 * jsdom has no layout and the decision under test is markup: the word is in
 * the cell for everyone, or it is hidden from the eye.
 */
describe("the status column has a visible header", () => {
  it("does not hide its label from sighted readers", () => {
    render(
      <MonitorTable monitors={[monitor("api", "up")]} beatWidth={WIDTH} />,
    );
    const first = monTable().querySelector(
      ":scope > thead > tr > th",
    ) as HTMLElement;
    expect(first.textContent).toBe("Status");
    expect(first.querySelector(".sr-only")).toBeNull();
  });
});
