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
 * The masthead holds the same controls on every route (SUB-182), so every
 * route is walked anyway: the point of the check is that a route cannot
 * quietly add something that breaks it.
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
    // Pages share the default context's localStorage, and the rail case below
    // stores the sidebar collapsed. Start every page beside the expanded
    // sidebar so a case measures the layout it names, whatever ran before it.
    await page.evaluateOnNewDocument(() => localStorage.setItem("subglance:sidebar", "expanded"));
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
   * The bar is one line from 641px up. It used to be two below 900px, by
   * decision, because the layout switcher made it about 760px wide; with the
   * switcher in the dashboard's own toolbar (SUB-182) the search button takes
   * the slack instead, and the toggle, search, workbench and theme share one
   * line. When the right-hand group wrapped inside itself the theme toggle
   * landed alone on a row of its own, which is how the bar once grew to three
   * rows at 641px.
   */
  it("keeps the toggle, search, workbench and theme on one line", async () => {
    const page = await open(width, ROUTES[0]);
    try {
      // Centres rather than tops: the three are different heights and the
      // group centres them, so their tops never agree even on one line.
      const centres = await page.evaluate(() =>
        Array.from(
          document.querySelectorAll<HTMLElement>(
            ".shell-topbar > .shell-icon-btn, .shell-command-launcher, .shell-topbar-right > *",
          ),
        ).map((el) => {
          const r = el.getBoundingClientRect();
          return Math.round(r.top + r.height / 2);
        }),
      );
      expect(centres).toHaveLength(4);
      expect(centres).toEqual([centres[0], centres[0], centres[0], centres[0]]);
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
 * rows apart.
 *
 * The floor is rung 3 (104px), the one the container rungs are built from,
 * for a row whose name has the room to itself. A row with a selection box
 * (the inventory, for someone who can edit) gives the box 16px and a gap out
 * of that rung, so its name cell is held to 76px: what it measures at the
 * 836px wrap. That is the floor `inventory.css` states and argues for
 * (SUB-170). Until then this check added the box's width to the name's and
 * asserted 104 of the sum, which held while the link itself was 76px wide.
 *
 * Measured on the name cell, `.inv-main`, which is the width a name gets
 * before its ellipsis: the link inside it is only as wide as its own text, so
 * a short name would read as a narrow floor.
 *
 * 1130 is the narrowest window where the inventory row is still on one line
 * beside the expanded sidebar: the list is exactly 836px there, so the name
 * cell is measured where it is tightest rather than somewhere near it.
 */
const LIST_ROUTES = ROUTES.filter((route) => route.name === "monitors" || route.name === "notifications");
const NAME_FLOOR = 104;
const NAME_FLOOR_BESIDE_BOX = 76;

describe.each([641, 700, 901, 960, 1040, 1100, 1130, 1440])("list rows at %ipx", (width) => {
  it.each(LIST_ROUTES)(
    `keep every name at least ${NAME_FLOOR}px wide, ${NAME_FLOOR_BESIDE_BOX}px beside a selection box, on $name`,
    async (route) => {
      const page = await open(width, route);
      try {
        const cells = await page.evaluate(() =>
          Array.from(document.querySelectorAll<HTMLElement>(".inv-main")).map((el) => ({
            width: el.getBoundingClientRect().width,
            boxed: el.parentElement?.querySelector(":scope > .choice") != null,
          })),
        );
        // An empty list would make every check below pass vacuously.
        expect(cells.length).toBeGreaterThan(0);
        for (const cell of cells) {
          const floor = cell.boxed ? NAME_FLOOR_BESIDE_BOX : NAME_FLOOR;
          expect(cell.width, `a name cell ${cell.boxed ? "beside a selection box " : ""}at ${width}px`)
            .toBeGreaterThanOrEqual(floor);
        }
      } finally {
        await page.close();
      }
    },
  );
});
