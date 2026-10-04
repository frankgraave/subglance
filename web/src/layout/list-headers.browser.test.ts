/**
 * A screen's controls stand at the head of the list they act on, not in a bar
 * under the masthead (SUB-207), measured in a real browser.
 *
 * The page toolbar put a third layer between the masthead and the first card:
 * bar, toolbar, card. The dashboard, Notifications and Settings have moved
 * out of it — the dashboard's status tabs, text filter, Filter and View head
 * its one card of monitors, the channel filter is in the Channels card's
 * header beside "Add channel", the settings filter stands at the head of the
 * section index it narrows — and this suite holds them there at a phone, a
 * tablet and a desktop width:
 *
 *  - the page toolbar is collapsed, so nothing stands between the masthead
 *    and the first card;
 *  - the header holding the filter starts directly under the masthead, one
 *    content gutter and one card edge below it at most;
 *  - at 1440px the header is one line;
 *  - at 390px nothing scrolls sideways.
 *
 * Monitors and Incidents still fill the toolbar. They are
 * listed below so that the list cannot quietly grow: a migrated screen moves
 * from STILL_IN_TOOLBAR to HEADED, and a screen that puts controls back into
 * the toolbar fails the first describe block.
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

type Headed = {
  name: string;
  path: string;
  ready: string;
  /** The element that heads the list: what the filter stands in. */
  header: string;
  /** The row that must be one line at 1440px: its children share a line. */
  line: string;
  /**
   * What must stand in the header at a phone width, when it is not the
   * text filter. The dashboard's text filter moves into its Filter sheet
   * there, so the header carries the button that opens it.
   */
  phoneControl?: string;
};

const HEADED: Headed[] = [
  {
    name: "dashboard",
    path: "/",
    ready: "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']",
    header: ".mon-board > .card-head",
    line: ".mon-board > .card-head",
    phoneControl: "button[aria-haspopup='dialog'][aria-label^='Filter']",
  },
  { name: "notifications", path: "/notifications", ready: ".inv-row", header: ".nt-card > .card-head", line: ".nt-card > .card-head" },
  { name: "settings", path: "/settings", ready: 'input[name="current_password"]', header: ".settings-aside", line: ".settings-aside > .shell-search" },
];

/** Screens whose controls have not left the page toolbar yet. */
const STILL_IN_TOOLBAR = [
  { path: "/monitors", ready: ".inv-list > li" },
  { path: "/incidents", ready: ".inc-line" },
];

const WIDTHS = [390, 820, 1440];

async function open(path: string, ready: string, width: number): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    // `domcontentloaded`: the dashboard holds an SSE stream open for the life
    // of the page, so waiting for network silence never returns.
    await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".shell-topbar", { timeout: 15_000 });
    await page.waitForSelector(ready, { timeout: 15_000 });
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    await page.evaluate(
      () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
    );
  } catch (err) {
    await page.close();
    throw err;
  }
  return page;
}

async function measure(page: Page, header: string, line = header, control = "input[type='search']") {
  return page.evaluate((sel: string, lineSel: string, controlSel: string) => {
    const box = (el: Element | null) => {
      if (el === null) return null;
      const r = el.getBoundingClientRect();
      return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: r.height };
    };
    const head = document.querySelector(sel);
    const content = document.querySelector(".shell-content");
    const card = head?.closest(".card") ?? null;
    const px = (el: Element | null, prop: "paddingTop" | "borderTopWidth") =>
      el === null ? 0 : parseFloat(getComputedStyle(el)[prop]);
    return {
      masthead: box(document.querySelector(".shell-topbar")),
      toolbar: box(document.querySelector(".shell-toolbar")),
      header: box(head),
      filterInHeader: head?.querySelector(controlSel) != null,
      // Each direct child's box, to tell one line from two.
      parts: [...(document.querySelector(lineSel)?.children ?? [])]
        .filter((child) => child.getBoundingClientRect().width > 1)
        .map((child) => box(child)!),
      field: box(head?.querySelector(".shell-search") ?? null),
      gutter: px(content, "paddingTop"),
      cardEdge: px(card, "paddingTop") + px(card, "borderTopWidth"),
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    };
  }, header, line, control);
}

describe.each(HEADED)("$name", ({ path, ready, header, line, phoneControl }) => {
  it.each(WIDTHS)("heads its list with its own controls, directly under the masthead, at %ipx", async (width) => {
    const page = await open(path, ready, width);
    try {
      const m = await measure(page, header, line, width === 390 ? phoneControl : undefined);
      if (m.masthead === null || m.header === null) throw new Error(`no masthead or no ${header}`);
      expect(m.filterInHeader, "the filter stands in the list's header").toBe(true);
      // No bar between: the toolbar is collapsed or gone.
      expect(m.toolbar?.height ?? 0, "page toolbar height").toBe(0);
      expect(m.header.top).toBeGreaterThanOrEqual(m.masthead.bottom);
      expect(m.header.top, "header starts one gutter and one card edge under the masthead").toBeLessThanOrEqual(
        m.masthead.bottom + m.gutter + m.cardEdge + 1,
      );
      if (width === 1440) {
        // One line: every part of the row overlaps every other vertically.
        expect(m.parts.length).toBeGreaterThanOrEqual(2);
        for (const part of m.parts) {
          for (const other of m.parts) {
            expect(part.top, "one line at 1440px").toBeLessThan(other.bottom);
          }
        }
      }
      if (width === 390) {
        expect(m.scrollWidth, "no sideways scroll at 390px").toBe(m.clientWidth);
        expect(m.field?.right ?? 0).toBeLessThanOrEqual(m.clientWidth);
      }
    } finally {
      await page.close();
    }
  });
});

describe("screens still on the page toolbar", () => {
  it.each(STILL_IN_TOOLBAR)("$path still fills it, until its controls move into its list header", async ({ path, ready }) => {
    /*
     * The other half of the ratchet. When one of these moves its controls,
     * this fails, and the screen moves to HEADED above with a header to
     * measure — rather than leaving a stale entry that proves nothing.
     */
    const page = await open(path, ready, 1440);
    try {
      const { toolbar } = await measure(page, "body");
      expect(toolbar?.height ?? 0).toBeGreaterThan(0);
    } finally {
      await page.close();
    }
  });
});

describe("dashboard header", () => {
  /*
   * The dashboard's own acceptance widths (SUB-183): its header holds more
   * than any other — status tabs, the text filter, Filter and View — so the
   * generic one-line check above is not the whole claim. Lines are counted
   * over the header's controls, not its two halves, because the half on the
   * right can wrap inside itself and still look like one box.
   */
  const READY = "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']";
  const CONTROLS = ".mon-board > .card-head :is(.mon-tabs, .shell-search, button[aria-haspopup='dialog'])";

  async function lines(page: Page): Promise<number> {
    const tops = await page.evaluate((sel: string) =>
      [...document.querySelectorAll(sel)]
        .map((el) => el.getBoundingClientRect())
        .filter((r) => r.width > 1)
        .map((r) => ({ top: r.top, bottom: r.bottom })),
    CONTROLS);
    // Two controls share a line when their boxes overlap vertically.
    const rows: { top: number; bottom: number }[] = [];
    for (const box of tops) {
      const row = rows.find((r) => box.top < r.bottom && r.top < box.bottom);
      if (row === undefined) rows.push({ ...box });
      else Object.assign(row, { top: Math.min(row.top, box.top), bottom: Math.max(row.bottom, box.bottom) });
    }
    return rows.length;
  }

  it.each([1280, 1440])("is one line at %ipx", async (width) => {
    const page = await open("/", READY, width);
    try {
      expect(await lines(page)).toBe(1);
    } finally {
      await page.close();
    }
  });

  it("breaks into two lines at most at 820px", async () => {
    const page = await open("/", READY, 820);
    try {
      expect(await lines(page)).toBeLessThanOrEqual(2);
    } finally {
      await page.close();
    }
  });

  it("starts the first monitor in the top quarter of a 390px phone", async () => {
    const page = await browser.newPage();
    try {
      await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 1, isMobile: true });
      await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
      await page.waitForSelector(READY, { timeout: 15_000 });
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      const seen = await page.evaluate((sel: string) => ({
        first: document.querySelector(sel)!.getBoundingClientRect().top,
        quarter: innerHeight / 4,
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
      }), READY);
      expect(seen.first, "first monitor's top edge").toBeLessThanOrEqual(seen.quarter);
      expect(seen.scrollWidth, "no sideways scroll").toBe(seen.clientWidth);
    } finally {
      await page.close();
    }
  });
});

describe("notifications header", () => {
  it.each([320, 390, 820, 1440])("reads in focus order at %ipx", async (width) => {
    /*
     * Keyboard focus walks the header's controls in markup order: the filter,
     * then Add channel. Each must stand after the one before it on screen —
     * on a later line, or further right on the same line — or focus jumps
     * backwards across the header (CodeRabbit, PR #189).
     */
    const page = await open("/notifications", ".inv-row", width);
    try {
      const boxes = await page.evaluate(() =>
        [...document.querySelectorAll(".nt-card > .card-head :is(input, button)")].map((el) => {
          const r = el.getBoundingClientRect();
          return { top: r.top, bottom: r.bottom, left: r.left };
        }),
      );
      expect(boxes.length).toBe(2);
      for (let i = 1; i < boxes.length; i++) {
        const [prev, next] = [boxes[i - 1]!, boxes[i]!];
        const laterLine = next.top >= prev.bottom - 1;
        const sameLineRight = next.top < prev.bottom && prev.top < next.bottom && next.left > prev.left;
        expect(laterLine || sameLineRight, `control ${i} stands after control ${i - 1}`).toBe(true);
      }
    } finally {
      await page.close();
    }
  });
});
