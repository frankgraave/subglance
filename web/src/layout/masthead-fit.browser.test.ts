/**
 * The masthead fits the viewport between the phone rung and the desktop
 * (SUB-148).
 *
 * `phone-layout.browser.test.ts` covers 320–414px, where the sidebar is a
 * drawer and the bar has its own rules. Nothing covered the band above it,
 * where the sidebar is still expanded and takes 232px: at 768px the bar's
 * right-hand group ended 117px past the viewport on every route, because the
 * search slot could not shrink below the field's fixed width.
 *
 * The masthead is the same on every route, so every route is walked anyway:
 * the point of the check is that a route cannot quietly add something that
 * breaks it.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

const ROUTES = [
  // Rows or cards: beside the expanded sidebar the rows preference gives way
  // to cards up to 816px (SUB-149), and this file walks both sides of that.
  { name: "dashboard", path: "/", ready: "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']" },
  { name: "monitors", path: "/monitors", ready: ".inv-list > li" },
  { name: "incidents", path: "/incidents", ready: ".inc-line" },
  { name: "notifications", path: "/notifications", ready: ".inv-row" },
  { name: "settings", path: "/settings", ready: 'input[name="current_password"]' },
];

/*
 * 641 is the first width with a sidebar, 768 the width the bug was reported
 * at, 900 the tablet rung itself, 901 and 1024 above it. 901 is where the bar
 * returns to one row and is at its tightest.
 */
const BAR_WIDTHS = [641, 768, 900, 901, 1024];

/*
 * Page-level scroll at every masthead width, plus 700 and 960. Those two are
 * where SUB-149 measured the last few pixels of overflow — 3px of rows table
 * at 700 and 22px of inventory row at 960 — so a fix that only moved the
 * failure off the rung widths would show up there.
 */
const PAGE_WIDTHS = [641, 700, 768, 900, 901, 960, 1024];

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

async function open(width: number, route: (typeof ROUTES)[number]): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 800, deviceScaleFactor: 1 });
    await page.goto(server.url + route.path, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(route.ready, { timeout: 15_000 });
    // The bundled face is `font-display: block`, so until it arrives the bar is
    // laid out with the host fallback, whose width differs per machine, and at
    // 641px the preferences row has only a few pixels to spare.
    // Measure the typeface the product ships, not whichever face won the race.
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    await page.evaluate(
      () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
    );
  } catch (err) {
    // The caller's `finally` only starts once `open` has returned the page.
    await page.close();
    throw err;
  }
  return page;
}

describe.each(BAR_WIDTHS)("masthead at %ipx", (width) => {
  it.each(ROUTES)("keeps every control inside the viewport on $name", async (route) => {
    const page = await open(width, route);
    try {
      const outside = await page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        return Array.from(document.querySelectorAll<HTMLElement>(".shell-topbar, .shell-topbar *"))
          .map((el) => ({ el, r: el.getBoundingClientRect() }))
          .filter(({ r }) => r.width > 0 && (r.left < -1 || r.right > vw + 1))
          .map(({ el, r }) => `${el.tagName.toLowerCase()}.${el.className.toString().slice(0, 40)} ${Math.round(r.left)}-${Math.round(r.right)}`);
      });
      expect(outside).toEqual([]);
    } finally {
      await page.close();
    }
  });

  /*
   * The preferences group reads as one control strip. When it wrapped inside
   * itself the theme toggle landed alone under the layout switcher, which is
   * how the bar grew to three rows at 641px and to two at 901–1040px.
   */
  it("keeps the layout, workbench and theme controls on one line", async () => {
    const page = await open(width, ROUTES[0]);
    try {
      // Centres rather than tops: the three are different heights and the
      // group centres them, so their tops never agree even on one line.
      const centres = await page.evaluate(() =>
        Array.from(document.querySelectorAll<HTMLElement>(".shell-topbar-right > *")).map((el) => {
          const r = el.getBoundingClientRect();
          return Math.round(r.top + r.height / 2);
        }),
      );
      expect(centres).toHaveLength(3);
      expect(centres).toEqual([centres[0], centres[0], centres[0]]);
    } finally {
      await page.close();
    }
  });
});

describe.each(PAGE_WIDTHS)("page at %ipx", (width) => {
  it.each(ROUTES)("does not scroll sideways on $name", async (route) => {
    const page = await open(width, route);
    try {
      const seen = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
      }));
      expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth });
    } finally {
      await page.close();
    }
  });
});

/*
 * The sidebar veto must not reach past the sidebar (SUB-149). Beside the rail
 * the column at 641px is 537px wide and the rows table fits, so rows is what
 * renders there — cards would be a veto with nothing to protect.
 */
describe("beside the collapsed rail at 641px", () => {
  it("keeps the rows layout and does not scroll sideways", async () => {
    const page = await browser.newPage();
    try {
      await page.evaluateOnNewDocument(() => localStorage.setItem("subglance:sidebar", "collapsed"));
      await page.setViewport({ width: 641, height: 800, deviceScaleFactor: 1 });
      await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-testid^='monitor-row-']", { timeout: 15_000 });
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      const seen = await page.evaluate(() => ({
        cards: document.querySelectorAll("[data-testid^='monitor-card-']").length,
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      }));
      expect(seen).toEqual({ cards: 0, overflow: 0 });
    } finally {
      await page.close();
    }
  });
});

/*
 * A row that fits by squeezing its name is not a fix (SUB-149). Before the
 * rows wrapped by container width, the monitors inventory at 1040px beside
 * the sidebar drew every name 14px wide: no overflow, and no way to tell the
 * rows apart. The floor is rung 3 (104px), the same one the container rungs
 * are built from, so this fails if either rung drifts below its row.
 */
const LIST_ROUTES = ROUTES.filter((route) => route.name === "monitors" || route.name === "notifications");
const NAME_FLOOR = 104;

describe.each([641, 700, 901, 960, 1040, 1100, 1440])("list rows at %ipx", (width) => {
  it.each(LIST_ROUTES)("keep every name at least rung 3 wide on $name", async (route) => {
    const page = await open(width, route);
    try {
      const narrowest = await page.evaluate(() =>
        Math.min(...Array.from(document.querySelectorAll<HTMLElement>(".inv-main")).map((el) => el.getBoundingClientRect().width)),
      );
      expect(narrowest).toBeGreaterThanOrEqual(NAME_FLOOR);
    } finally {
      await page.close();
    }
  });
});
