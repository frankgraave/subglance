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
  { name: "dashboard", path: "/", ready: "[data-testid^='monitor-row-']" },
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
 * Page-level scroll is asserted only where the page content is known to fit.
 * At 641px the dashboard rows and at 901–960px the monitors inventory still
 * overflow on their own (SUB-149); the masthead checks below hold at those
 * widths regardless, which is what this file owns.
 */
const PAGE_WIDTHS = [768, 900, 1024];

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
  return page;
}

describe.each(BAR_WIDTHS)("masthead at %ipx", (width) => {
  it.each(ROUTES)("keeps every control inside the viewport on $name", async (route) => {
    const page = await open(width, route);
    try {
      const outside = await page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        return Array.from(document.querySelectorAll<HTMLElement>(".shell-topbar *"))
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
