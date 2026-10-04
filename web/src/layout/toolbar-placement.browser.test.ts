/**
 * Where a screen's controls land, measured in a real browser (SUB-136,
 * SUB-207).
 *
 * Every screen's controls stand at the head of the list they act on: the
 * dashboard's, the inventory's and the incidents screen's in their cards'
 * headers. The masthead above holds only what works on every screen, search
 * included, which opens the command menu. The page toolbar that used to sit
 * between the two is gone. jsdom can say which element a control is in; it
 * cannot say any of the things below, because each is decided by layout or
 * by the cascade:
 *
 *  - that a control in a list's header is the thing the pointer hits, and
 *    drives the list under it;
 *  - that the masthead's search is not covered by anything;
 *  - that leaving a screen takes its controls with it, so the next screen
 *    does not inherit a filter for a list it does not show.
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
 * Where an element sits, by the landmark the user sees: "masthead", the
 * list's "header", elsewhere on the "page", or "missing".
 */
async function placeOf(page: Page, selector: string, head: string): Promise<string> {
  return page.evaluate((s: string, h: string) => {
    const el = document.querySelector(s);
    if (el === null) return "missing";
    if (el.closest(".shell-topbar") !== null) return "masthead";
    return el.closest(h) !== null ? "header" : "page";
  }, selector, head);
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
      const where = (sel: string) => placeOf(page, sel, HEAD);
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

describe("the monitors inventory", () => {
  /*
   * The inventory has left the toolbar too (SUB-207): its text filter,
   * Filter (type, paused, tags) and Sort head its card beside its actions.
   * Held from both sides, as the dashboard is: nothing comes back into a
   * bar, and the header's controls still drive the list with a real pointer.
   */
  const HEAD = ".inv-board > .card-head";
  const ROWS = ".inv-list > li";

  it("puts its filter field, Filter, Sort and actions in its list header, not in a bar", async () => {
    const page = await open("/monitors", ROWS);
    try {
      const where = (sel: string) => placeOf(page, sel, HEAD);
      expect({
        filter: await where("input[type='search']"),
        filters: await where("button[aria-haspopup='dialog'][aria-label^='Filter']"),
        sort: await where("button[aria-haspopup='dialog'][aria-label^='Sort']"),
        add: await where("button[aria-label='Add monitor']"),
        search: await where(".shell-command-launcher"),
      }).toEqual({
        filter: "header",
        filters: "header",
        sort: "header",
        add: "header",
        search: "masthead",
      });
      expect(await hittable(page, `${HEAD} input[type='search']`)).toBe(true);
      expect(await hittable(page, `${HEAD} button[aria-label^='Sort']`)).toBe(true);
    } finally {
      await page.close();
    }
  });

  it("drives its own list from the header", async () => {
    /* Four fixture monitors: three HTTP, one TCP, which is also paused. */
    const page = await open("/monitors", ROWS);
    const count = () => page.evaluate((sel: string) => document.querySelectorAll(sel).length, ROWS);
    const settle = (n: number) =>
      page.waitForFunction((sel: string, want: number) => document.querySelectorAll(sel).length === want, { timeout: 5_000 }, ROWS, n);
    try {
      expect(await count()).toBe(4);
      await page.type(`${HEAD} input[type='search']`, "auth");
      await settle(1);
      await page.$eval(`${HEAD} input[type='search']`, (el) => (el as HTMLInputElement).select());
      await page.keyboard.press("Backspace");
      await settle(4);

      // Filter > Type opens first; TCP leaves the one monitor it counts.
      await page.click(`${HEAD} button[aria-label^='Filter']`);
      const panel = "[role='dialog'][aria-label='Filter monitors']";
      await page.waitForSelector(panel);
      let picked = false;
      for (const label of await page.$$(`${panel} .choice-label`)) {
        const text = await label.evaluate((el) => el.textContent ?? "");
        if (/^TCP\s*1$/.test(text)) {
          await label.click();
          picked = true;
          break;
        }
      }
      expect(picked, "no 'TCP 1' value in the Type group").toBe(true);
      await settle(1);
      await page.keyboard.press("Escape");
      expect(await page.$eval(`${HEAD} button[aria-label^='Filter']`, (el) => el.getAttribute("aria-label"))).toBe("Filter, 1 active");
      // The chip under the header drops it again.
      await page.click(".inv-filter-row button[aria-label='Remove filter Type: TCP']");
      await settle(4);
    } finally {
      await page.close();
    }
  });
});

describe("the incidents screen", () => {
  /*
   * The last screen to leave the toolbar (SUB-207): All · Open · Resolved,
   * the monitor filter and History head its card. Held from both sides, as
   * the other lists are.
   */
  const HEAD = ".inc-board > .card-head";
  const READY = ".inc-list";

  it("puts its tabs, monitor filter and window in its list header, not in a bar", async () => {
    const page = await open("/incidents", READY);
    try {
      const where = (sel: string) => placeOf(page, sel, HEAD);
      expect({
        filter: await where("input[type='search']"),
        tabs: await where("[role='group'][aria-label='Filter by state']"),
        history: await where("button[aria-haspopup='dialog'][aria-label^='History']"),
        search: await where(".shell-command-launcher"),
      }).toEqual({ filter: "header", tabs: "header", history: "header", search: "masthead" });
      expect(await hittable(page, `${HEAD} input[type='search']`)).toBe(true);
      expect(await hittable(page, `${HEAD} .mon-tab`)).toBe(true);
      expect(await hittable(page, LAUNCHER)).toBe(true);
    } finally {
      await page.close();
    }
  });

  it("narrows its own lists from the header", async () => {
    const page = await open("/incidents", READY);
    try {
      const titles = () =>
        page.evaluate(() =>
          [...document.querySelectorAll(".inc-section-title")].map((el) => el.textContent),
        );
      await page.waitForFunction(
        () =>
          [...document.querySelectorAll(".inc-section-title")].some(
            (el) => el.textContent === "Resolved",
          ),
        { timeout: 10_000 },
      );
      expect(await titles()).toEqual(["Open incidents", "Resolved"]);

      // Found by its word, as a reader finds it: the tab reads "Resolved n".
      let clicked = false;
      for (const tab of await page.$$(`${HEAD} .mon-tab`)) {
        if (/^Resolved\b/.test(await tab.evaluate((el) => el.textContent ?? ""))) {
          await tab.click();
          clicked = true;
          break;
        }
      }
      expect(clicked, "no Resolved tab in the header").toBe(true);
      await page.waitForFunction(
        () =>
          ![...document.querySelectorAll(".inc-section-title")].some(
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

describe("a screen with no list controls", () => {
  it("keeps search in the masthead, uncovered", async () => {
    /*
     * A monitor's detail page has nothing to filter or arrange. It is also
     * the page that used to lose search, so the masthead's is checked here.
     */
    const page = await open("/monitors/1", ".mon-detail-windows");
    try {
      expect(await placeOf(page, ".shell-command-launcher", ".card-head")).toBe("masthead");
      expect(await hittable(page, LAUNCHER)).toBe(true);
    } finally {
      await page.close();
    }
  });
});

describe("leaving a screen", () => {
  it("takes its controls with it", async () => {
    const page = await open("/", MONITOR_ITEMS);
    try {
      // The sidebar is outside the routed region, so its link is not the
      // node-swapped-mid-click hazard `drawer-stacking` documents.
      await page.click("a[href='/incidents']");
      await page.waitForSelector(".inc-list", { timeout: 15_000 });

      const seen = await page.evaluate(() => ({
        searches: document.querySelectorAll("input[type='search']").length,
        placeholder:
          document.querySelector<HTMLInputElement>("input[type='search']")?.placeholder ?? null,
        statusFilter: document.querySelectorAll("[aria-label='Filter by status']").length,
        stateFilter: document.querySelectorAll("[aria-label='Filter by state']").length,
        view: document.querySelectorAll("button[aria-label^='View']").length,
        layouts: document.querySelectorAll("[aria-label='Dashboard layout']").length,
      }));
      expect(seen).toEqual({
        searches: 1,
        placeholder: "Filter by monitor…",
        statusFilter: 0,
        stateFilter: 1,
        view: 0,
        layouts: 0,
      });
    } finally {
      await page.close();
    }
  });
});
