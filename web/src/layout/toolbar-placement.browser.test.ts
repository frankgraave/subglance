/**
 * Where a screen's controls land, measured in a real browser (SUB-136).
 *
 * Monitors and Incidents portal their controls into the shell's page
 * toolbar through `ToolbarTools` — the filter field and everything else that
 * narrows the list (SUB-182). The dashboard, Notifications and Settings have
 * moved theirs to the head of the list they act on (SUB-207, SUB-183). The masthead above holds only what works on every screen, search
 * included, which opens the command menu. The unit suite covers the portal
 * wiring with `ShellSlots`, which renders the target as a bare div. That proves the
 * nodes arrive; it cannot prove any of the four things below, because each is
 * decided by layout or by the cascade, and jsdom does neither:
 *
 *  - that the two bars are painted in order, masthead first, toolbar under it,
 *    with neither covering the other;
 *  - that a control portalled into a bar is still the thing the pointer hits
 *    and still drives the list it came from — the whole argument for a portal
 *    over hoisted props (see `ToolbarTools`);
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

/** The masthead's search: one button, on every screen. */
const LAUNCHER = ".shell-topbar .shell-command-launcher";

describe("the dashboard", () => {
  /*
   * The dashboard has left the toolbar (SUB-183): its status tabs, text
   * filter, Filter and View head its card of monitors. These two cases hold
   * the other side of that move — nothing of the dashboard's comes back into
   * a bar, the toolbar collapses, and the controls in the card's header still
   * drive the list under them with a real pointer.
   */
  const HEAD = ".mon-board > .card-head";

  it("puts its status tabs, filter field, Filter and View in its list header, not in a bar", async () => {
    const page = await open("/", MONITOR_ITEMS);
    try {
      const where = (sel: string) =>
        page.evaluate((s: string, head: string) => {
          const el = document.querySelector(s);
          if (el === null) return "missing";
          if (el.closest(".shell-topbar") !== null) return "masthead";
          if (el.closest(".shell-toolbar") !== null) return "toolbar";
          return el.closest(head) !== null ? "header" : "page";
        }, sel, HEAD);
      expect({
        filter: await where("input[type='search']"),
        status: await where("[role='group'][aria-label='Filter by status']"),
        tags: await where("button[aria-haspopup='dialog'][aria-label^='Filter']"),
        view: await where("button[aria-haspopup='dialog'][aria-label^='View']"),
        search: await where(".shell-command-launcher"),
      }).toEqual({
        filter: "header",
        status: "header",
        tags: "header",
        view: "header",
        search: "masthead",
      });

      const { toolbar } = await bars(page);
      expect(toolbar?.height ?? 0, "the page toolbar is collapsed").toBe(0);
      expect(await hittable(page, `${HEAD} input[type='search']`)).toBe(true);
      expect(await hittable(page, LAUNCHER)).toBe(true);
      expect(await hittable(page, "[aria-label='Filter by status'] button")).toBe(true);
    } finally {
      await page.close();
    }
  });

  it("drives its own list from the header", async () => {
    /* Four fixture monitors, one of them down and one named "nightly-...". */
    const page = await open("/", MONITOR_ITEMS);
    const field = `${HEAD} input[type='search']`;
    try {
      const count = () =>
        page.evaluate((sel: string) => document.querySelectorAll(sel).length, MONITOR_ITEMS);
      expect(await count()).toBe(4);

      await page.type(field, "nightly");
      await page.waitForFunction(
        (sel: string) => document.querySelectorAll(sel).length === 1,
        { timeout: 5_000 },
        MONITOR_ITEMS,
      );

      // Clear the query, then narrow from the status tabs instead.
      await page.$eval(field, (el) => (el as HTMLInputElement).select());
      await page.keyboard.press("Backspace");
      await page.waitForFunction(
        (sel: string) => document.querySelectorAll(sel).length === 4,
        { timeout: 5_000 },
        MONITOR_ITEMS,
      );

      // Found by its words, as a reader finds it: the tab reads "Down 1".
      const tabs = await page.$$(`${HEAD} .mon-tab`);
      let clicked = false;
      for (const tab of tabs) {
        const text = await tab.evaluate((el) => el.textContent ?? "");
        if (/^Down\b/.test(text)) {
          await tab.click();
          clicked = true;
          break;
        }
      }
      expect(clicked, "no Down tab in the header").toBe(true);
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

  it("puts its monitor filter, scope and window in the toolbar", async () => {
    const page = await open("/incidents", READY);
    try {
      expect({
        filter: await barOf(page, "input[type='search']"),
        scope: await barOf(page, ".tb-select"),
        count: await barOf(page, ".tb-count"),
      }).toEqual({ filter: "toolbar", scope: "toolbar", count: "toolbar" });

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
  it("collapses the toolbar instead of leaving an empty strip, and keeps search", async () => {
    /*
     * A monitor's detail page contributes nothing to the toolbar: it is one
     * monitor, with nothing to filter or arrange. The collapse is a `:has()`
     * rule, so this is the only suite in which it is observable. It is also
     * the page that used to lose search, so the masthead's is checked here.
     */
    const page = await open("/monitors/1", ".mon-detail-windows");
    try {
      const { toolbar } = await bars(page);
      if (toolbar === null) throw new Error("no .shell-toolbar in the document");
      expect(toolbar.height).toBe(0);
      expect(await barOf(page, ".shell-command-launcher")).toBe("masthead");
      expect(await hittable(page, LAUNCHER)).toBe(true);
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
        searches: document.querySelectorAll("input[type='search']").length,
        placeholder:
          document.querySelector<HTMLInputElement>(".shell-toolbar input[type='search']")
            ?.placeholder ?? null,
        statusFilter: document.querySelectorAll("[aria-label='Filter by status']").length,
        groupBy: document.querySelectorAll(".mon-group-select").length,
        layouts: document.querySelectorAll("[aria-label='Dashboard layout']").length,
        toolbarLabels: [...document.querySelectorAll(".shell-toolbar .tb-label")].map(
          (el) => el.textContent,
        ),
      }));
      expect(seen).toEqual({
        searches: 1,
        placeholder: "Filter by monitor…",
        statusFilter: 0,
        groupBy: 0,
        layouts: 0,
        toolbarLabels: ["Show", "History"],
      });
    } finally {
      await page.close();
    }
  });
});
