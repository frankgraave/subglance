/**
 * One page frame on every route, measured in a real browser (SUB-182,
 * SUB-207).
 *
 * The UI assessment of 1 October found the dashboard using the full width,
 * three screens stopping at about 860px, Settings at about 1050px, three
 * visually hidden titles, two card titles standing in as the page's `h1`, a
 * dashboard titled "Monitors" in a tab that said "Dashboard", and the rail
 * lighting Dashboard on `/monitors/{id}`. Each was a choice one screen made
 * for itself. This file pins the frame they share, per route and at two
 * widths, so a screen that starts deciding for itself again fails here:
 *
 *  - exactly one `h1`, painted (not visually hidden), and it is the
 *    masthead's: the title role at the heavy weight, one line, starting at
 *    the same distance after the sidebar toggle on every route (SUB-207);
 *  - the `h1`, the lit sidebar item and the tab title say the same word
 *    (a monitor's page: its name in the `h1` and the tab, Monitors lit and
 *    linked before the name);
 *  - the content starts with the screen's own content: no title above the
 *    first card any more;
 *  - the column of cards is the reading measure on every screen but the
 *    dashboard, and stays that width when the window grows; the dashboard
 *    takes the whole content column.
 *
 * Widths are read from `tokens.css` through the cascade rather than written
 * as numbers here, so a deliberate change to the measure moves this test
 * with it and an accidental `max-width` in a feature stylesheet does not.
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

type Route = {
  path: string;
  /** The screen's own content, so the measurement is not of the shell alone. */
  ready: string;
  /** The page title: a monitor's page is titled with the monitor's name. */
  title: string;
  /** The section linked before the title, on a child page. */
  parent?: string;
  /** The sidebar item lit on this route; none on an address of no section. */
  lit?: string;
  /** What the tab says before the product name. */
  tab: string;
  width: "full" | "measure" | "indexed";
};

const ROUTES: Route[] = [
  { path: "/", ready: "[data-testid^='monitor-row-']", title: "Dashboard", lit: "Dashboard", tab: "Dashboard", width: "full" },
  { path: "/monitors", ready: ".inv-list > li", title: "Monitors", lit: "Monitors", tab: "Monitors", width: "measure" },
  { path: "/incidents", ready: ".inc-line", title: "Incidents", lit: "Incidents", tab: "Incidents", width: "measure" },
  { path: "/notifications", ready: ".inv-row", title: "Notifications", lit: "Notifications", tab: "Notifications", width: "measure" },
  { path: "/settings", ready: "#users li .segmented", title: "Settings", lit: "Settings", tab: "Settings", width: "indexed" },
  // The fixture estate's monitor 1 (harness/server.ts).
  {
    path: "/monitors/1",
    ready: ".mon-detail-windows",
    title: "checkout-api-eu-west-1.internal.acme-corporation.example",
    parent: "Monitors",
    lit: "Monitors",
    tab: "checkout-api-eu-west-1.internal.acme-corporation.example",
    width: "measure",
  },
  // An address that names no screen (SUB-177): titled, framed at the
  // measure, and lighting nothing, because it belongs to no section.
  {
    path: "/this-does-not-exist",
    ready: ".page .card a[href='/']",
    title: "Page not found",
    tab: "Page not found",
    width: "measure",
  },
];

async function open(path: string, ready: string, width: number): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  // `domcontentloaded`: the dashboard holds an SSE stream open for the life
  // of the page, so waiting for network silence never returns.
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { timeout: 15_000 });
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/** Everything this file asserts, read in one pass. */
function measure(page: Page) {
  return page.evaluate(() => {
    const px = (expr: string) => {
      const probe = document.createElement("div");
      probe.style.width = expr;
      probe.style.position = "absolute";
      document.body.append(probe);
      const w = probe.getBoundingClientRect().width;
      probe.remove();
      return w;
    };
    const main = document.querySelector("#shell-main")!;
    const pad = getComputedStyle(main);
    const column =
      main.getBoundingClientRect().width -
      parseFloat(pad.paddingLeft) -
      parseFloat(pad.paddingRight);
    const bar = document.querySelector(".shell-topbar")!;
    const toggle = bar.querySelector(".shell-icon-btn")!.getBoundingClientRect();
    const heading = bar.querySelector(".shell-heading")?.getBoundingClientRect() ?? null;
    const h1s = [...document.querySelectorAll("h1")].map((h) => {
      const box = h.getBoundingClientRect();
      const style = getComputedStyle(h);
      return {
        text: h.textContent?.trim() ?? "",
        painted:
          box.width > 1 &&
          box.height > 1 &&
          style.clipPath === "none" &&
          style.visibility === "visible",
        inMasthead: bar.contains(h),
        oneLine: Math.round(box.height) === Math.round(parseFloat(style.lineHeight)),
        size: style.fontSize,
        leading: style.lineHeight,
        weight: style.fontWeight,
      };
    });
    const crumbs = [...bar.querySelectorAll(".shell-heading a")].map((a) => ({
      text: a.textContent?.trim() ?? "",
      href: a.getAttribute("href"),
    }));
    /* The first thing in the page frame, so a title drawn above the cards
       again shows up as the first child rather than as a second `h1` only. */
    const first = main.querySelector(".page")?.firstElementChild ?? null;
    const cards = [...main.querySelectorAll(".card")].filter(
      (c) => c.getBoundingClientRect().width > 0 && c.closest(".settings-index, .drawer-panel") === null,
    );
    const pageEl = main.querySelector(".page")!;
    const pageLeft = pageEl.getBoundingClientRect().left;
    const settings = main.querySelector(".settings-sections");
    return {
      measure: px("var(--size-pane-lg)"),
      titleSize: px("var(--type-row)"),
      titleLeading: px("var(--lead-row)"),
      gap: parseFloat(getComputedStyle(bar).columnGap),
      afterToggle: heading === null ? null : Math.round(heading.left - toggle.right),
      crumbs,
      firstInPage: first === null ? null : first.tagName.toLowerCase(),
      column,
      h1s,
      lit: [...document.querySelectorAll(".shell-sidebar [aria-current='page']")].map(
        (a) => a.textContent?.trim() ?? "",
      ),
      title: document.title,
      /* How far the widest card reaches from the page's left edge. */
      reach: Math.max(...cards.map((c) => c.getBoundingClientRect().right - pageLeft)),
      cardColumn: settings === null ? null : settings.getBoundingClientRect().width,
    };
  });
}

describe("the page frame", () => {
  for (const route of ROUTES) {
    it(`frames ${route.path} with one visible title and the ${route.width} width`, async () => {
      const at: Record<number, Awaited<ReturnType<typeof measure>>> = {};
      for (const width of [1440, 1920]) {
        const page = await open(route.path, route.ready, width);
        try {
          at[width] = await measure(page);
        } finally {
          await page.close();
        }
      }
      for (const width of [1440, 1920]) {
        const m = at[width];
        const where = `${route.path} at ${width}px`;

        expect(m.h1s, where).toHaveLength(1);
        const [h1] = m.h1s;
        expect(h1.painted, `${where}: the h1 is visible`).toBe(true);
        expect(h1.inMasthead, `${where}: the h1 is the masthead's`).toBe(true);
        expect(h1.oneLine, `${where}: the title is one line`).toBe(true);
        expect(h1.size, `${where}: the h1 is on the title role`).toBe(`${m.titleSize}px`);
        expect(h1.leading, where).toBe(`${m.titleLeading}px`);
        expect(h1.weight, where).toBe("600");
        expect(h1.text, where).toBe(route.title);
        // Every title starts one gap after the toggle, so moving between
        // screens does not move the first word you read.
        expect(m.afterToggle, `${where}: one gap after the toggle`).toBe(m.gap);
        expect(m.crumbs, `${where}: the breadcrumb`).toEqual(
          route.parent === undefined ? [] : [{ text: route.parent, href: "/monitors" }],
        );
        // Nothing titles the page inside it any more.
        expect(m.firstInPage, `${where}: the page starts with its content`).not.toBe("h1");

        expect(m.lit, `${where}: the rail lights the section`).toEqual(
          route.lit === undefined ? [] : [route.lit],
        );
        expect(m.title, where).toBe(`${route.tab} \u2014 SubGlance`);

        if (route.width === "full") {
          expect(m.reach, `${where}: the dashboard fills the column`).toBeCloseTo(m.column, 0);
        } else if (route.width === "measure") {
          expect(m.reach, `${where}: cards stop at the measure`).toBeLessThanOrEqual(m.measure + 0.5);
          expect(m.reach, `${where}: and reach it`).toBeGreaterThanOrEqual(m.measure - 0.5);
        } else {
          expect(m.cardColumn, `${where}: settings cards are the measure`).toBeCloseTo(m.measure, 0);
        }
      }
      // A wider window widens the dashboard and nothing else.
      if (route.width === "full") {
        expect(at[1920].reach).toBeGreaterThan(at[1440].reach);
      } else {
        expect(at[1920].reach).toBeCloseTo(at[1440].reach, 0);
      }
    }, 60_000);
  }
});
