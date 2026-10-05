/**
 * A screen's controls stand at the head of the list they act on, not in a bar
 * under the masthead (SUB-207), measured in a real browser.
 *
 * There used to be a page toolbar: a third layer between the masthead and the
 * first card — bar, toolbar, card. Every screen has moved out of it and it is
 * gone: the dashboard's status tabs, text filter, Filter and View head its
 * one card of monitors, the inventory's text filter, Filter and Sort head its
 * card beside its actions, the incidents screen's All · Open · Resolved tabs,
 * monitor filter and History head its card, the channel filter is in the
 * Channels card's header beside "Add channel", and the settings filter stands
 * at the head of the section index it narrows. This suite holds them there
 * at a phone, a tablet and a desktop width:
 *
 *  - nothing stands between the masthead and the first card: no element sits
 *    in that band at all, toolbar or otherwise;
 *  - the header holding the filter starts directly under the masthead, one
 *    content gutter and one card edge below it at most;
 *  - at 1440px the header is one line;
 *  - at 390px nothing scrolls sideways.
 *
 * A last block walks every route, the detail page included, and fails on a
 * bar between the masthead and `<main>`, so a new screen cannot bring one
 * back under another name.
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
  /** The boxes that must share that line, when not `line`'s children. */
  parts?: string;
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
  {
    name: "monitors",
    path: "/monitors",
    ready: ".inv-list > li",
    header: ".inv-board > .card-head",
    line: ".inv-board > .card-head",
    // The title and every control, not the header's two halves: the action
    // group wraps inside itself, so its box alone would read as one line.
    parts: ".inv-board > .card-head :is(.card-head-lead, .card-head-action > .shell-search, .card-head-action > .menu, .bulk-tags-actions > *)",
    phoneControl: "button[aria-haspopup='dialog'][aria-label^='Filter']",
  },
  {
    name: "incidents",
    path: "/incidents",
    ready: ".inc-line",
    header: ".inc-board > .card-head",
    line: ".inc-board > .card-head",
    // The tabs, the filter and History, not the header's two halves.
    parts: ".inc-board > .card-head :is(.mon-tabs, .shell-search, button[aria-haspopup='dialog'])",
  },
  { name: "notifications", path: "/notifications", ready: ".inv-row", header: ".nt-card > .card-head", line: ".nt-card > .card-head" },
  { name: "settings", path: "/settings", ready: 'input[name="current_password"]', header: ".settings-aside", line: ".settings-aside > .shell-search" },
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

async function measure(page: Page, header: string, line = header, control = "input[type='search']", partsSel = "") {
  return page.evaluate((sel: string, lineSel: string, controlSel: string, partsSel: string) => {
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
      // Whatever stands between the masthead and the content column.
      between: [...(document.querySelector(".shell-main")?.children ?? [])]
        .filter((el) => !el.matches(".shell-topbar, main"))
        .map((el) => el.className || el.tagName),
      header: box(head),
      filterInHeader: head?.querySelector(controlSel) != null,
      // Each direct child's box, to tell one line from two.
      parts: [...(partsSel === "" ? (document.querySelector(lineSel)?.children ?? []) : document.querySelectorAll(partsSel))]
        .filter((child) => child.getBoundingClientRect().width > 1)
        .map((child) => box(child)!),
      field: box(head?.querySelector(".shell-search") ?? null),
      gutter: px(content, "paddingTop"),
      cardEdge: px(card, "paddingTop") + px(card, "borderTopWidth"),
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    };
  }, header, line, control, partsSel);
}

describe.each(HEADED)("$name", ({ path, ready, header, line, phoneControl, parts }) => {
  it.each(WIDTHS)("heads its list with its own controls, directly under the masthead, at %ipx", async (width) => {
    const page = await open(path, ready, width);
    try {
      const m = await measure(page, header, line, width === 390 ? phoneControl : undefined, parts);
      if (m.masthead === null || m.header === null) throw new Error(`no masthead or no ${header}`);
      expect(m.filterInHeader, "the filter stands in the list's header").toBe(true);
      // No bar between: nothing stands between the masthead and <main>.
      expect(m.between, "elements between the masthead and the content").toEqual([]);
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

describe("no screen draws a bar under the masthead", () => {
  /*
   * The page toolbar is deleted, and this is what keeps it deleted: on every
   * route, the monitor's page and the workbench included, the masthead is
   * followed directly by `<main>`, and the first thing in the page starts
   * one content gutter under the masthead. A new bar — by any class name —
   * fails here before anyone has to notice it in review.
   */
  it.each([
    { path: "/", ready: "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']" },
    { path: "/monitors", ready: ".inv-list > li" },
    { path: "/monitors/1", ready: ".mon-detail-windows" },
    { path: "/incidents", ready: ".inc-line" },
    { path: "/notifications", ready: ".inv-row" },
    { path: "/settings", ready: 'input[name="current_password"]' },
    { path: "/workbench", ready: ".chip--status" },
    { path: "/this-does-not-exist", ready: ".page .card a[href='/']" },
  ])("$path", async ({ path, ready }) => {
    const page = await open(path, ready, 1440);
    try {
      const seen = await page.evaluate(() => {
        const main = document.querySelector("main")!;
        const masthead = document.querySelector(".shell-topbar")!.getBoundingClientRect();
        return {
          between: [...(document.querySelector(".shell-main")?.children ?? [])]
            .filter((el) => !el.matches(".shell-topbar, main"))
            .map((el) => el.className || el.tagName),
          gap: main.getBoundingClientRect().top - masthead.bottom,
          toolbar: document.querySelectorAll(".shell-toolbar, .shell-toolbar-slot, [data-testid='page-toolbar']").length,
        };
      });
      expect(seen.between).toEqual([]);
      expect(seen.toolbar).toBe(0);
      // `<main>` starts at the masthead's bottom edge, give or take a pixel.
      expect(Math.abs(seen.gap)).toBeLessThanOrEqual(1);
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
