/**
 * SUB-100 in a real browser: the status word has to be *legible*, not merely
 * present.
 *
 * jsdom proved the word is in the markup. It cannot prove anything about it:
 * it applies no stylesheet, so a label at 1px, in a colour that does not clear
 * contrast, or pushed out of a 46px column and clipped, passes every unit test
 * in this repo. That is the class of bug the browser harness exists for — a
 * WCAG ratio measured here already caught a real 3.64:1 failure jsdom missed.
 *
 * The skip link is checked here for the same reason: "hidden until focused" is
 * entirely a CSS claim, and the usual way this pattern ships broken is a
 * `display: none` that makes the link unfocusable.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
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

async function open(layout: string, ready: string, width = 1280): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    LAYOUT_STORAGE_KEY,
    layout,
  );
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { timeout: 15_000 });
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/** sRGB relative luminance, per WCAG 2.x. */
const LUMINANCE = `(rgb) => {
  const channel = (v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  const [r, g, b] = rgb;
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}`;

describe("the status word beside the lamp", () => {
  it("is painted, not clipped out of its column, in the rows layout", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      const labels = await page.evaluate(() => {
        const out: { text: string; width: number; height: number; fits: boolean }[] = [];
        for (const cell of Array.from(
          document.querySelectorAll<HTMLElement>(".mon-cell--led"),
        )) {
          const label = cell.querySelector<HTMLElement>(".led-label");
          if (!label) continue;
          const lr = label.getBoundingClientRect();
          const cr = cell.getBoundingClientRect();
          out.push({
            text: (label.textContent ?? "").trim(),
            width: Math.round(lr.width),
            height: Math.round(lr.height),
            // Scroll width beyond client width is text the column ate.
            fits: label.scrollWidth <= label.clientWidth + 1 && lr.right <= cr.right + 1,
          });
        }
        return out;
      });
      // The demo data has at least one monitor that is not up, or this file
      // is asserting nothing.
      expect(labels.length).toBeGreaterThan(0);
      for (const label of labels) {
        expect(label.text.length).toBeGreaterThan(0);
        expect(label.height).toBeGreaterThanOrEqual(10);
        expect(label.fits).toBe(true);
      }
    } finally {
      await page.close();
    }
  });

  it("clears the 4.5:1 AA floor against the surface it sits on", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      const worst = await page.evaluate(`(() => {
        const luminance = ${LUMINANCE};
        const parse = (c) => c.match(/\\d+(\\.\\d+)?/g).slice(0, 3).map(Number);
        const backdrop = (el) => {
          for (let p = el; p; p = p.parentElement) {
            const bg = window.getComputedStyle(p).backgroundColor;
            if (bg && bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") return parse(bg);
          }
          return [0, 0, 0];
        };
        let worst = 21;
        for (const label of Array.from(document.querySelectorAll(".led-label"))) {
          const fg = luminance(parse(window.getComputedStyle(label).color));
          const bg = luminance(backdrop(label));
          const ratio = (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
          worst = Math.min(worst, ratio);
        }
        return worst;
      })()`);
      // Real text, read to decide whether something is broken: AA, not the
      // 3:1 non-text floor the lamp itself is held to.
      expect(worst).toBeGreaterThanOrEqual(4.5);
    } finally {
      await page.close();
    }
  });
});

describe("the skip link", () => {
  it("is invisible at rest and a real control once focused", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      const before = await page.evaluate(() => {
        const el = document.querySelector<HTMLElement>(".shell-skip");
        if (!el) return null;
        const s = window.getComputedStyle(el);
        return { clipped: s.clipPath !== "none", display: s.display };
      });
      expect(before).not.toBeNull();
      // Clipped, not display:none — a display:none link cannot take focus,
      // which is this pattern's classic silent failure.
      expect(before!.clipped).toBe(true);
      expect(before!.display).not.toBe("none");

      // Tab from the address bar lands on it first, because it is the first
      // focusable element in the document.
      await page.keyboard.press("Tab");
      const focused = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        if (!el) return null;
        const s = window.getComputedStyle(el);
        const r = el.getBoundingClientRect();
        return {
          className: el.className,
          clipped: s.clipPath !== "none",
          width: Math.round(r.width),
          height: Math.round(r.height),
        };
      });
      expect(focused?.className).toContain("shell-skip");
      expect(focused?.clipped).toBe(false);
      // SC 2.5.8 asks 24x24 of a pointer target; this one is also a visible
      // control once it appears, so it has to be big enough to read.
      expect(focused?.width).toBeGreaterThanOrEqual(24);
      expect(focused?.height).toBeGreaterThanOrEqual(24);
    } finally {
      await page.close();
    }
  });

  it("puts focus on main, so the next Tab is inside the content", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      await page.keyboard.press("Tab");
      await page.keyboard.press("Enter");
      const landed = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        return el ? { id: el.id, tag: el.tagName } : null;
      });
      expect(landed).toEqual({ id: "shell-main", tag: "MAIN" });
    } finally {
      await page.close();
    }
  });
});
