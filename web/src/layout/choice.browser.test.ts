/**
 * The checkbox as it is drawn, in the real build (SUB-167, DESIGN.md §8.9).
 *
 * `Choice.test.tsx` proves the element is a native checkbox and
 * `tokens.test.ts` proves no other file writes one. Neither can see the
 * picture: `appearance: none` hands the drawing to two pseudo-elements, and a
 * rule that fails to reach them leaves an invisible box that still passes
 * every DOM assertion. So this opens the monitors page — the screen with the
 * most boxes, one per row plus the group box — in both themes, and asks the
 * browser what it computed and the compositor what it painted.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { BACKDROP, LUMINANCE, OVER_BACKDROP, TO_RGBA } from "./harness/contrast";
import { colourAt } from "./harness/pixel";

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

const ROW_BOX = ".inv-list .choice";
const GROUP_BOX = ".bulk-tags-selection .choice";

async function monitors(theme: "dark" | "light"): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.evaluateOnNewDocument(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    THEME_STORAGE_KEY,
    theme,
  );
  await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ROW_BOX, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  return page;
}

/** Waits out the 150ms mark transition, so a sample sees the settled frame. */
async function settle(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)));
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
    );
  });
}

/** The drawn box's corner and centre, in viewport pixels. */
async function boxGeometry(page: Page, selector: string, index = 0) {
  return page.$$eval(
    selector,
    (els, i) => {
      const el = els[i as number] as HTMLElement;
      const r = el.getBoundingClientRect();
      const box = parseFloat(getComputedStyle(el, "::before").width);
      const left = r.left + (r.width - box) / 2;
      const top = r.top + (r.height - box) / 2;
      return { target: { w: r.width, h: r.height }, box, left, top, cx: left + box / 2, cy: top + box / 2 };
    },
    index,
  );
}

/** WCAG contrast of the box's ring against what is outside and inside it. */
async function ringContrast(page: Page, selector: string) {
  return page.evaluate(
    `(() => {
      const toRgba = ${TO_RGBA};
      const backdrop = ${BACKDROP};
      const over = ${OVER_BACKDROP};
      const lum = ${LUMINANCE};
      const ratio = (a, b) => {
        const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
        return (hi + 0.05) / (lo + 0.05);
      };
      const el = document.querySelector(${JSON.stringify(selector)});
      const before = getComputedStyle(el, "::before");
      const ring = before.boxShadow.match(/^(.*?)\\s+0px\\s+0px\\s+0px\\s+[\\d.]+px\\s+inset$/)
        ?? before.boxShadow.match(/^inset\\s+0px\\s+0px\\s+0px\\s+[\\d.]+px\\s+(.*)$/);
      if (!ring) return { error: "no inset ring: " + before.boxShadow };
      const outside = backdrop(el);
      const fillRgba = toRgba(before.backgroundColor);
      const inside = fillRgba && fillRgba[3] > 0
        ? [0, 1, 2].map((k) => fillRgba[k] * fillRgba[3] + outside[k] * (1 - fillRgba[3]))
        : outside;
      const ringRgb = over(el, ring[1]);
      return {
        outside: Math.round(ratio(ringRgb, outside) * 100) / 100,
        inside: Math.round(ratio(ringRgb, inside) * 100) / 100,
      };
    })()`,
  ) as Promise<{ outside: number; inside: number } | { error: string }>;
}

function near(sample: string, want: string, tolerance = 12): boolean {
  const a = sample.split(",").map(Number);
  const b = want.split(",").map(Number);
  return a.every((v, i) => Math.abs(v - b[i]) <= tolerance);
}

/** A CSS custom property on the root, resolved to `r,g,b` by the browser. */
async function token(page: Page, name: string): Promise<string> {
  const rgba = (await page.evaluate(
    `(() => {
      const toRgba = ${TO_RGBA};
      return toRgba(getComputedStyle(document.documentElement).getPropertyValue(${JSON.stringify(name)}).trim());
    })()`,
  )) as number[] | null;
  if (rgba === null) throw new Error(`could not resolve ${name}`);
  return rgba.slice(0, 3).join(",");
}

describe("the checkbox", () => {
  for (const theme of ["dark", "light"] as const) {
    it(`keeps a 24px target and draws a 16px box inside it (${theme})`, async () => {
      const page = await monitors(theme);
      try {
        for (const selector of [ROW_BOX, GROUP_BOX]) {
          const g = await boxGeometry(page, selector);
          expect(g.target.w, selector).toBeGreaterThanOrEqual(24);
          expect(g.target.h, selector).toBeGreaterThanOrEqual(24);
          expect(g.box, selector).toBe(16);
        }
      } finally {
        await page.close();
      }
    }, 60_000);

    it(`draws an unchecked edge that clears 3:1 on both sides (${theme})`, async () => {
      const page = await monitors(theme);
      try {
        const measured = await ringContrast(page, ROW_BOX);
        expect(measured).not.toHaveProperty("error");
        const { outside, inside } = measured as { outside: number; inside: number };
        expect(outside, "ring against the row").toBeGreaterThanOrEqual(3);
        expect(inside, "ring against the box's own fill").toBeGreaterThanOrEqual(3);
      } finally {
        await page.close();
      }
    }, 60_000);

    it(`paints the accent and the mark once checked, and nothing before (${theme})`, async () => {
      const page = await monitors(theme);
      try {
        const accent = await token(page, "--accent");
        const mark = await token(page, "--accent-ink");
        const before = await boxGeometry(page, ROW_BOX);
        // Just inside the ring, where only the fill shows.
        const fillSpot = { x: before.left + 4, y: before.top + 4 };
        const unchecked = await colourAt(page, fillSpot.x, fillSpot.y);
        expect(near(unchecked, accent), `unchecked fill ${unchecked} is the accent`).toBe(false);

        await page.click(ROW_BOX);
        await settle(page);
        const g = await boxGeometry(page, ROW_BOX);
        const filled = await colourAt(page, g.left + 4, g.top + 4);
        expect(near(filled, accent), `checked fill ${filled}, accent ${accent}`).toBe(true);
        // A pixel in the middle of the tick's long stroke (9, 7 in the 16px
        // box), clear of its anti-aliased edges.
        const tick = await colourAt(page, g.left + 9, g.top + 7);
        expect(near(tick, mark, 40), `tick ${tick}, --accent-ink ${mark}`).toBe(true);
      } finally {
        await page.close();
      }
    }, 60_000);
  }

  it("shows the group box as mixed when some visible rows are selected", async () => {
    const page = await monitors("dark");
    try {
      const state = () =>
        page.$eval(GROUP_BOX, (el) => {
          const box = el as HTMLInputElement;
          return { checked: box.checked, mixed: box.indeterminate };
        });
      expect(await state()).toEqual({ checked: false, mixed: false });
      await page.click(ROW_BOX);
      expect(await state()).toEqual({ checked: false, mixed: true });
      await settle(page);
      const g = await boxGeometry(page, GROUP_BOX);
      const mark = await token(page, "--accent-ink");
      // A pixel the dash covers (x 4-12, y 7-9) and the tick does not: at
      // x 11 the tick's long stroke ends above y 7.1. A tick drawn in place
      // of the dash leaves this pixel on the accent.
      const dash = await colourAt(page, g.left + 11, g.top + 8);
      expect(near(dash, mark, 40), `dash ${dash}, --accent-ink ${mark}`).toBe(true);

      await page.click(GROUP_BOX);
      expect(await state()).toEqual({ checked: true, mixed: false });
      const rows = await page.$$eval(ROW_BOX, (els) => els.every((el) => (el as HTMLInputElement).checked));
      expect(rows).toBe(true);
      await page.click(GROUP_BOX);
      expect(await state()).toEqual({ checked: false, mixed: false });
      const none = await page.$$eval(ROW_BOX, (els) => els.some((el) => (el as HTMLInputElement).checked));
      expect(none).toBe(false);
    } finally {
      await page.close();
    }
  }, 60_000);

  it("moves the keyboard outline to the drawn box", async () => {
    const page = await monitors("dark");
    try {
      // Arrive by keyboard, which is what :focus-visible answers to: onto the
      // row's name link, then Shift+Tab back to the box before it.
      await page.focus(ROW_BOX);
      await page.keyboard.press("Tab");
      await page.keyboard.down("Shift");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Shift");
      const outline = await page.$eval(ROW_BOX, (el) => ({
        focused: el === document.activeElement && el.matches(":focus-visible"),
        own: getComputedStyle(el).outlineStyle,
        box: getComputedStyle(el, "::before").outlineStyle,
      }));
      expect(outline).toEqual({ focused: true, own: "none", box: "solid" });
    } finally {
      await page.close();
    }
  }, 60_000);

  it("hands the control back to the platform under forced colours", async () => {
    const page = await monitors("dark");
    try {
      // Puppeteer's own helper refuses this feature; the protocol accepts it.
      const cdp = await page.createCDPSession();
      await cdp.send("Emulation.setEmulatedMedia", {
        features: [{ name: "forced-colors", value: "active" }],
      });
      expect(await page.evaluate(() => matchMedia("(forced-colors: active)").matches)).toBe(true);
      const drawn = await page.$eval(ROW_BOX, (el) => ({
        appearance: getComputedStyle(el).appearance,
        pseudo: getComputedStyle(el, "::before").content,
      }));
      expect(drawn).toEqual({ appearance: "auto", pseudo: "none" });
    } finally {
      await page.close();
    }
  }, 60_000);
});
