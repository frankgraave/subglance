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
import { RAIL_VETO_MAX_WIDTH } from "./useMediaQuery";

const ROUTES = [
  // Rows or cards: beside the expanded sidebar the rows preference gives way
  // to cards up to 925px (SUB-149, SUB-194), and this file walks both sides
  // of that.
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
   * the slack instead, and the toggle, search and theme share one line. When the right-hand group wrapped inside itself the theme toggle
   * landed alone on a row of its own, which is how the bar once grew to three
   * rows at 641px.
   */
  it("keeps the toggle, search and theme on one line", async () => {
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
 * Beside the collapsed rail the rows table comes back as soon as its name
 * cell reaches the floor (SUB-149, SUB-194): at 750px, not at the 641px
 * SUB-149 first allowed, where the table fitted only by cutting every name
 * to a 59px cell. The first width that takes rows must not scroll sideways.
 */
describe("beside the collapsed rail", () => {
  it.each([
    [RAIL_VETO_MAX_WIDTH, 0],
    [RAIL_VETO_MAX_WIDTH + 1, 1],
  ])("at %ipx renders %i rows layout without scrolling sideways", async (width, rowsLayout) => {
    const page = await browser.newPage();
    try {
      await page.evaluateOnNewDocument(() => localStorage.setItem("subglance:sidebar", "collapsed"));
      await page.setViewport({ width, height: 800, deviceScaleFactor: 1 });
      await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-testid^='monitor-row-'], [data-testid^='monitor-card-']", { timeout: 15_000 });
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      const seen = await page.evaluate(() => ({
        rows: document.querySelectorAll("[data-testid^='monitor-row-']").length > 0 ? 1 : 0,
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      }));
      expect(seen).toEqual({ rows: rowsLayout, overflow: 0 });
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
 * A channel row's floor is rung 3 (104px), the one the container rungs are
 * built from. The inventory's is rung 4 (168px), for every role (SUB-194):
 * on one line its name no longer shares a line with the address and gets the
 * row less the settings columns, 250px at the 836px wrap for someone with a
 * selection box and four actions, and wrapped it has the full width. Until
 * then the inventory's floor was 76px beside a selection box, which is what
 * the name measured at the wrap (SUB-170).
 *
 * Measured on the name itself, `.inv-name`, which is the width a name gets
 * before its ellipsis: the link inside it is only as wide as its own text, so
 * a short name would read as a narrow floor. (Not `.inv-main`: on one line it
 * lets go of its box so the name and address can sit on different tracks.)
 *
 * 1130 is the narrowest window where the inventory row is still on one line
 * beside the expanded sidebar: the list is exactly 836px there, so the name
 * is measured where it is tightest rather than somewhere near it.
 */
const LIST_ROUTES = ROUTES.filter((route) => route.name === "monitors" || route.name === "notifications");
const NAME_FLOOR: Record<string, number> = { monitors: 168, notifications: 104 };

describe.each([641, 700, 901, 960, 1040, 1100, 1130, 1440])("list rows at %ipx", (width) => {
  it.each(LIST_ROUTES)(
    "keep every name at least its list's floor wide on $name",
    async (route) => {
      const page = await open(width, route);
      try {
        const widths = await page.evaluate(() =>
          Array.from(document.querySelectorAll<HTMLElement>(".inv-name")).map((el) => el.getBoundingClientRect().width),
        );
        // An empty list would make every check below pass vacuously.
        expect(widths.length).toBeGreaterThan(0);
        for (const cell of widths) {
          expect(cell, `a name on ${route.name} at ${width}px`).toBeGreaterThanOrEqual(NAME_FLOOR[route.name]!);
        }
      } finally {
        await page.close();
      }
    },
  );
});
