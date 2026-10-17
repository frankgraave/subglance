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

describe("phone header targets", () => {
  /*
   * On a phone every control in a list's header keeps its 36px face
   * (`--control-h`) and takes a 44 x 44px target (`--size-target`), the
   * Apple HIG thumb size and WCAG 2.5.5 (AAA): the face stays the one the
   * headers were measured against, the thumb gets the room (SUB-216,
   * DESIGN.md §8.4).
   *
   * Measured the way a tap lands: the browser's own hit test at the corners
   * and edges of the 44px square around each control's centre must find
   * that control. A target that is too small finds the header behind it; two
   * targets that overlap find the neighbour, so a gap too narrow for two
   * grown targets fails here too.
   */
  const PHONE = [
    { name: "dashboard", path: "/", ready: "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']", header: ".mon-board > .card-head" },
    { name: "monitors", path: "/monitors", ready: ".inv-list > li", header: ".inv-board > .card-head" },
    { name: "incidents", path: "/incidents", ready: ".inc-line", header: ".inc-board > .card-head" },
    { name: "notifications", path: "/notifications", ready: ".inv-row", header: ".nt-card > .card-head" },
  ];

  it.each(PHONE)("$name: every header control has a 44px target around its 36px face at 390px", async ({ path, ready, header }) => {
    const page = await open(path, ready, 390);
    try {
      const seen = await page.evaluate((sel: string) => {
        const head = document.querySelector(sel)!;
        const probe = document.createElement("div");
        probe.style.width = "var(--size-target)";
        probe.style.height = "var(--control-h)";
        document.body.append(probe);
        const target = probe.getBoundingClientRect().width;
        const face = probe.getBoundingClientRect().height;
        probe.remove();
        return {
          target,
          face,
          controls: [...head.querySelectorAll(".card-head-action button")]
            .filter((el) => el.getBoundingClientRect().width > 1)
            .map((el) => {
              const r = el.getBoundingClientRect();
              const cx = r.left + r.width / 2;
              const cy = r.top + r.height / 2;
              // One pixel inside the square: the hit test rounds a point to
              // the nearest whole pixel, and the far edge of a box belongs to
              // its neighbour, so a probe half a pixel in lands on the next
              // button's target. Two pixels short of 44 still fails.
              const h = target / 2 - 1;
              const points = [
                [cx - h, cy - h], [cx, cy - h], [cx + h, cy - h],
                [cx - h, cy], [cx + h, cy],
                [cx - h, cy + h], [cx, cy + h], [cx + h, cy + h],
              ];
              const misses = points
                .filter(([x, y]) => {
                  const hit = document.elementFromPoint(x!, y!);
                  return hit === null || !el.contains(hit);
                })
                .map(([x, y]) => `${Math.round(x! - cx)},${Math.round(y! - cy)}`);
              const glyphOnly = (el.textContent ?? "").trim() === "";
              return { name: el.getAttribute("aria-label") ?? el.textContent ?? "", width: r.width, height: r.height, glyphOnly, misses };
            }),
        };
      }, header);
      expect(seen.target, "--size-target").toBe(44);
      expect(seen.face, "--control-h").toBe(36);
      expect(seen.controls.length, "controls in the header").toBeGreaterThanOrEqual(1);
      for (const control of seen.controls) {
        expect(control.misses, `${control.name}: points of its 44px target that miss it`).toEqual([]);
        if (control.glyphOnly) {
          // The face does not grow: only the target does.
          expect(control.width, `${control.name}: face width`).toBe(seen.face);
          expect(control.height, `${control.name}: face height`).toBe(seen.face);
        }
      }
    } finally {
      await page.close();
    }
  });
});

describe("phone tab rows", () => {
  /*
   * On a phone a list's tabs scroll sideways when they do not fit (SUB-327).
   * A row cut off at its edge with nothing to say so reads as the whole
   * list: on the dashboard the last visible tab stopped half way through a
   * word, and the tabs past it went unseen unless someone happened to swipe
   * the row. So, for every list headed by `ListTabs`:
   *
   *  - no tab is covered by a header button: the row ends before them;
   *  - a tab the row cuts off has a fade on the side it is cut, read from
   *    the computed mask, so a row without the hint fails here;
   *  - scrolled to its end, the row stops fading there and fades at the
   *    start instead, where the first tabs now stand past the edge.
   *
   * 320px as well as 390: the narrowest phone still in use is where a row
   * that only just fits at 390 is cut. A row that fits draws no fade. The
   * fixture cuts the dashboard's row at both widths and fits the incidents
   * row at both, so the two tests after the loop hold that each row is
   * really cut somewhere: the dashboard's as it stands, the incidents row
   * once it is narrower than its tabs.
   */
  const ROWS = [
    { name: "dashboard", path: "/", ready: "[data-testid^='monitor-row-'], [data-testid^='monitor-card-']", header: ".mon-board > .card-head" },
    { name: "incidents", path: "/incidents", ready: ".inc-line", header: ".inc-board > .card-head" },
  ];

  type Row = {
    row: { left: number; right: number; top: number; bottom: number };
    cut: ("start" | "end")[];
    covered: string[];
    fades: { start: boolean; end: boolean };
  };

  // Everything is read in the page: the boxes, and which ends of the row the
  // computed mask makes transparent. A mask is alpha only, so a transparent
  // stop is the fade and an opaque one is no fade, whatever the theme.
  async function read(page: Page, header: string): Promise<Row> {
    return page.evaluate((sel: string) => {
      const head = document.querySelector(sel)!;
      const tabs = head.querySelector(".mon-tabs")!;
      const r = tabs.getBoundingClientRect();
      const cut: ("start" | "end")[] = [];
      for (const tab of tabs.children) {
        const t = tab.getBoundingClientRect();
        // Half a pixel either way is subpixel layout, not a cut.
        if (t.left < r.left - 0.5 && t.right > r.left) cut.push("start");
        if (t.right > r.right + 0.5 && t.left < r.right) cut.push("end");
      }
      const covered = [...head.querySelectorAll(".card-head-action :is(button, input)")]
        .map((el) => ({ el, b: el.getBoundingClientRect() }))
        .filter(({ b }) => b.width > 1 && b.top < r.bottom && r.top < b.bottom && b.left < r.right && r.left < b.right)
        .map(({ el }) => el.getAttribute("aria-label") ?? el.tagName);
      // The computed gradient, its stops in paint order: `to left` reversed.
      const mask = getComputedStyle(tabs).maskImage;
      const fades = { start: false, end: false };
      const gradient = /^linear-gradient\((.*)\)$/.exec(mask);
      if (gradient !== null) {
        const parts = gradient[1]!.split(/,(?![^(]*\))/).map((part) => part.trim());
        const reversed = parts[0] === "to left";
        const stops = /^(?:to |-?[\d.]+deg)/.test(parts[0]!) ? parts.slice(1) : parts;
        const clear = (stop: string) => /^(?:transparent|rgba\(0, 0, 0, 0\))/.test(stop);
        const [first, last] = [clear(stops[0]!), clear(stops[stops.length - 1]!)];
        fades.start = reversed ? last : first;
        fades.end = reversed ? first : last;
      }
      return { row: { left: r.left, right: r.right, top: r.top, bottom: r.bottom }, cut, covered, fades };
    }, header);
  }

  async function scrollToEnd(page: Page, header: string): Promise<void> {
    await page.evaluate((sel: string) => {
      const tabs = document.querySelector(`${sel} .mon-tabs`)!;
      tabs.scrollLeft = tabs.scrollWidth;
    }, header);
    await settle(page);
  }

  async function settle(page: Page): Promise<void> {
    await page.evaluate(
      () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
    );
  }

  for (const { name, path, ready, header } of ROWS) {
    it.each([320, 390])(`${name}: a cut-off tab is never covered and fades on the side it is cut at %ipx`, async (width) => {
      const page = await open(path, ready, width);
      try {
        const seen = await read(page, header);
        expect(seen.covered, "header controls standing over the tabs").toEqual([]);
        for (const side of seen.cut) {
          expect(seen.fades[side], `a tab is cut at the ${side} of the row with no fade there`).toBe(true);
        }
        // A row that fits draws no fade at all: a faded last tab that is not
        // cut would read as one that is.
        if (seen.cut.length === 0) expect(seen.fades).toEqual({ start: false, end: false });

        if (!seen.cut.includes("end")) return;
        await scrollToEnd(page, header);
        const end = await read(page, header);
        expect(end.cut, "scrolled to the end, only the start is cut").not.toContain("end");
        expect(end.fades.end, "scrolled to the end, the end stops fading").toBe(false);
        expect(end.fades.start, "scrolled to the end, the start fades").toBe(true);
      } finally {
        await page.close();
      }
    });
  }

  it("cuts the dashboard's row at 390px, so the check above is not vacuous", async () => {
    // The fixture's four status tabs do not fit beside Filter and View at a
    // phone width. If they ever do, the hint is no longer exercised and the
    // fixture needs a longer row.
    const page = await open("/", ROWS[0]!.ready, 390);
    try {
      expect((await read(page, ROWS[0]!.header)).cut).toContain("end");
    } finally {
      await page.close();
    }
  });

  it("fades the incidents row once it is cut, so its hint is exercised too", async () => {
    // The incidents screen's three tabs fit at every phone width the fixture
    // reaches, so the loop above only ever sees that row whole. Narrowing the
    // row to half its width cuts it the way longer counts would, and holds
    // that this instance marks and fades its cut sides like the dashboard's.
    const { path, ready, header } = ROWS[1]!;
    const page = await open(path, ready, 390);
    try {
      expect((await read(page, header)).cut, "the fixture's incidents row fits at 390px").toEqual([]);
      await page.evaluate((sel: string) => {
        const tabs = document.querySelector<HTMLElement>(`${sel} .mon-tabs`)!;
        tabs.style.maxWidth = `${tabs.getBoundingClientRect().width / 2}px`;
      }, header);
      await settle(page);
      const cut = await read(page, header);
      expect(cut.cut, "half its width, the row is cut at the end").toContain("end");
      expect(cut.fades, "cut at the end only, the row fades at the end only").toEqual({ start: false, end: true });

      await scrollToEnd(page, header);
      const end = await read(page, header);
      expect(end.cut, "scrolled to the end, only the start is cut").not.toContain("end");
      expect(end.fades, "scrolled to the end, the row fades at the start only").toEqual({ start: true, end: false });
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
