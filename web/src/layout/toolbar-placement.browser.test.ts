/**
 * Where a screen's controls land, measured in a real browser (SUB-136).
 *
 * Every screen portals its controls into the shell: search into the masthead
 * through `TopbarTools`, everything that narrows the list into the page
 * toolbar through `ToolbarTools`. The unit suite covers the portal wiring
 * with `ShellSlots`, which renders both targets as bare divs. That proves the
 * nodes arrive; it cannot prove any of the four things below, because each is
 * decided by layout or by the cascade, and jsdom does neither:
 *
 *  - that the two bars are painted in order, masthead first, toolbar under it,
 *    with neither covering the other;
 *  - that a control portalled into a bar is still the thing the pointer hits
 *    and still drives the list it came from — the whole argument for a portal
 *    over hoisted props (see `TopbarTools`);
 *  - that a screen with no toolbar controls collapses the bar, which is
 *    `.shell-toolbar:has(.shell-toolbar-slot:empty)` in `shell.css` — a
 *    selector jsdom does not evaluate;
 *  - that leaving a screen takes its controls out of both bars, so the next
 *    screen does not inherit a filter for a list it does not show.
 *
 * Assertions are about element identities and boxes, never screenshots, so a
 * visual change to either bar does not need blessing here.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

let server: Server;
let browser: Browser;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** Rows, cards and compact lines alike: whatever the layout renders. */
const MONITOR_ITEMS =
  "[data-testid^='monitor-row-'], [data-testid^='monitor-card-'], [data-testid^='monitor-line-']";

/**
 * Opens one route at desktop width and waits for the screen's own content,
 * not merely the shell: the controls are portalled in after the screen
 * mounts, so a wait on `.shell-topbar` alone can measure an empty bar.
 */
async function open(path: string, ready: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  // `domcontentloaded`: the dashboard holds an SSE stream open for the life
  // of the page, so waiting for network silence never returns.
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".shell-topbar", { timeout: 15_000 });
  await page.waitForSelector(ready, { timeout: 15_000 });
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/**
 * Which bar an element sits in, by the landmark the user sees rather than by
 * the slot div it was portalled into: "masthead", "toolbar", "page" (still in
 * the screen's own tree) or "missing".
 */
async function barOf(page: Page, selector: string): Promise<string> {
  return page.evaluate((sel: string) => {
    const el = document.querySelector(sel);
    if (el === null) return "missing";
    if (el.closest(".shell-topbar") !== null) return "masthead";
    if (el.closest(".shell-toolbar") !== null) return "toolbar";
    return "page";
  }, selector);
}

/**
 * Whether the pointer, aimed at the centre of the element, lands on it. An
 * element can be in the right bar and still be covered by the other one, or
 * by the content scrolling under it; only hit testing answers that.
 */
async function hittable(page: Page, selector: string): Promise<boolean> {
  return page.evaluate((sel: string) => {
    const el = document.querySelector(sel);
    if (el === null) return false;
    const box = el.getBoundingClientRect();
    if (box.width === 0 || box.height === 0) return false;
    const hit = document.elementFromPoint(
      box.left + box.width / 2,
      box.top + box.height / 2,
    );
    return hit !== null && (el === hit || el.contains(hit));
  }, selector);
}

/** The two bars' vertical extents, in viewport pixels. */
async function bars(page: Page) {
  return page.evaluate(() => {
    const box = (sel: string) => {
      const el = document.querySelector(sel);
      if (el === null) return null;
      const r = el.getBoundingClientRect();
      return {
        top: r.top,
        bottom: r.bottom,
        height: r.height,
        display: getComputedStyle(el).display,
      };
    };
    return { masthead: box(".shell-topbar"), toolbar: box(".shell-toolbar") };
  });
}

const SEARCH = ".shell-topbar input[type='search']";

describe("the dashboard", () => {
  it("puts search in the masthead and its filters in the toolbar under it", async () => {
    const page = await open("/", MONITOR_ITEMS);
    try {
      expect({
        search: await barOf(page, "input[type='search']"),
        status: await barOf(page, "[role='group'][aria-label='Filter by status']"),
        groupBy: await barOf(page, ".mon-group-select"),
      }).toEqual({ search: "masthead", status: "toolbar", groupBy: "toolbar" });

      const { masthead, toolbar } = await bars(page);
      if (masthead === null || toolbar === null) throw new Error("a bar is missing");
      expect(toolbar.display).not.toBe("none");
      expect(toolbar.height).toBeGreaterThan(0);
      // Stacked, not overlapping: the toolbar starts where the masthead ends.
      expect(toolbar.top).toBeGreaterThanOrEqual(masthead.bottom - 1);

      expect(await hittable(page, SEARCH)).toBe(true);
      expect(
        await hittable(page, "[aria-label='Filter by status'] button"),
      ).toBe(true);
    } finally {
      await page.close();
    }
  });

  it("still drives its own list from both bars", async () => {
    /*
     * The portal's promise: the node moves, the handler stays with the list.
     * Four fixture monitors, one of them down and one named "nightly-...".
     */
    const page = await open("/", MONITOR_ITEMS);
    try {
      const count = () =>
        page.evaluate((sel: string) => document.querySelectorAll(sel).length, MONITOR_ITEMS);
      expect(await count()).toBe(4);

      await page.type(SEARCH, "nightly");
      await page.waitForFunction(
        (sel: string) => document.querySelectorAll(sel).length === 1,
        { timeout: 5_000 },
        MONITOR_ITEMS,
      );

      // Clear the query, then narrow from the other bar instead.
      await page.$eval(SEARCH, (el) => (el as HTMLInputElement).select());
      await page.keyboard.press("Backspace");
      await page.waitForFunction(
        (sel: string) => document.querySelectorAll(sel).length === 4,
        { timeout: 5_000 },
        MONITOR_ITEMS,
      );

      // Found by its words, as a reader finds it: the chip reads "1 down".
      const chips = await page.$$(".shell-toolbar .mon-count");
      let clicked = false;
      for (const chip of chips) {
        const text = await chip.evaluate((el) => el.textContent ?? "");
        if (/\bdown\b/.test(text)) {
          // A real pointer click on the portalled node, not a synthetic one.
          await chip.click();
          clicked = true;
          break;
        }
      }
      expect(clicked, "no down chip in the toolbar").toBe(true);
      await page.waitForFunction(
        (sel: string) => document.querySelectorAll(sel).length === 1,
        { timeout: 5_000 },
        MONITOR_ITEMS,
      );
    } finally {
      await page.close();
    }
  });
});

describe("the incidents screen", () => {
  const READY = ".inc-list";

  it("puts its monitor filter in the masthead and scope and window in the toolbar", async () => {
    const page = await open("/incidents", READY);
    try {
      expect({
        search: await barOf(page, "input[type='search']"),
        scope: await barOf(page, ".tb-select"),
        count: await barOf(page, ".tb-count"),
      }).toEqual({ search: "masthead", scope: "toolbar", count: "toolbar" });

      const labels = await page.evaluate(() =>
        [...document.querySelectorAll(".shell-toolbar .tb-label")].map(
          (el) => el.textContent,
        ),
      );
      expect(labels).toEqual(["Show", "History"]);

      const { masthead, toolbar } = await bars(page);
      if (masthead === null || toolbar === null) throw new Error("a bar is missing");
      expect(toolbar.display).not.toBe("none");
      expect(toolbar.top).toBeGreaterThanOrEqual(masthead.bottom - 1);
      expect(await hittable(page, ".shell-toolbar .tb-select")).toBe(true);
    } finally {
      await page.close();
    }
  });

  it("narrows its own cards from the toolbar", async () => {
    const page = await open("/incidents", READY);
    try {
      const titles = () =>
        page.evaluate(() =>
          [...document.querySelectorAll(".card-title")].map((el) => el.textContent),
        );
      await page.waitForFunction(
        () =>
          [...document.querySelectorAll(".card-title")].some(
            (el) => el.textContent === "Resolved",
          ),
        { timeout: 10_000 },
      );
      expect(await titles()).toEqual(["Open incidents", "Resolved"]);

      await page.select(".shell-toolbar .tb-select", "resolved");
      await page.waitForFunction(
        () =>
          ![...document.querySelectorAll(".card-title")].some(
            (el) => el.textContent === "Open incidents",
          ),
        { timeout: 5_000 },
      );
      expect(await titles()).toEqual(["Resolved"]);
    } finally {
      await page.close();
    }
  });
});

describe("a screen with no toolbar controls", () => {
  it("collapses the toolbar instead of leaving an empty strip", async () => {
    /*
     * Notifications contributes a search and nothing else. The collapse is a
     * `:has()` rule, so this is the only suite in which it is observable.
     */
    const page = await open("/notifications", SEARCH);
    try {
      const { toolbar } = await bars(page);
      if (toolbar === null) throw new Error("no .shell-toolbar in the document");
      expect(toolbar.height).toBe(0);
      expect(await barOf(page, "input[type='search']")).toBe("masthead");
    } finally {
      await page.close();
    }
  });
});

describe("leaving a screen", () => {
  it("takes its controls out of both bars", async () => {
    const page = await open("/", MONITOR_ITEMS);
    try {
      // The sidebar is outside the routed region, so its link is not the
      // node-swapped-mid-click hazard `drawer-stacking` documents.
      await page.click("a[href='/incidents']");
      await page.waitForSelector(".inc-list", { timeout: 15_000 });

      const seen = await page.evaluate(() => ({
        searches: document.querySelectorAll(".shell-topbar input[type='search']").length,
        placeholder:
          document.querySelector<HTMLInputElement>(".shell-topbar input[type='search']")
            ?.placeholder ?? null,
        statusFilter: document.querySelectorAll("[aria-label='Filter by status']").length,
        groupBy: document.querySelectorAll(".mon-group-select").length,
        toolbarLabels: [...document.querySelectorAll(".shell-toolbar .tb-label")].map(
          (el) => el.textContent,
        ),
      }));
      expect(seen).toEqual({
        searches: 1,
        placeholder: "Filter by monitor…",
        statusFilter: 0,
        groupBy: 0,
        toolbarLabels: ["Show", "History"],
      });
    } finally {
      await page.close();
    }
  });
});
