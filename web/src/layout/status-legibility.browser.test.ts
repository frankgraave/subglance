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
  /*
   * SUB-140 moved this cell's word, and this file moved with it rather than
   * being deleted.
   *
   * The product owner asked for the lamp alone in the rows layout's status
   * cell ("graag alleen de Led, geen tekst er achter"), so the word is
   * `sr-only` there now. The thing this file was written to protect — that the
   * status is not merely *present* in the markup but actually reaches a reader
   * — has not gone away, it has split in two, and so have the assertions:
   *
   *   1. the word is still in the accessibility tree, correctly associated
   *      with its lamp, and genuinely hidden the accessible way (clipped, not
   *      `display: none`, which would take it out of the tree with the pixels);
   *   2. a real WCAG contrast measurement still runs, on whatever now carries
   *      status visually — the lamp, which WCAG 1.4.11 holds to 3:1 as a
   *      non-text indicator, and the visible status word in the compact
   *      layout, which is text and holds the 4.5:1 AA floor.
   *
   * Deleting the contrast check because the element it measured moved would be
   * deleting the only thing in this repo that has ever caught a real ratio
   * failure (3.64:1, in the rows layout, before SUB-100).
   */
  it("keeps the word in the accessibility tree, hidden the accessible way", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      const labels = await page.evaluate(() => {
        const out: {
          text: string;
          display: string;
          clipped: boolean;
          width: number;
          height: number;
          hiddenFromAT: boolean;
          sharesWrapperWithLamp: boolean;
        }[] = [];
        for (const cell of Array.from(
          document.querySelectorAll<HTMLElement>(".mon-cell--led"),
        )) {
          const label = cell.querySelector<HTMLElement>(".sr-only");
          if (!label) continue;
          const s = window.getComputedStyle(label);
          const r = label.getBoundingClientRect();
          out.push({
            text: (label.textContent ?? "").trim(),
            display: s.display,
            clipped: s.clipPath !== "none",
            width: Math.round(r.width),
            height: Math.round(r.height),
            hiddenFromAT:
              label.closest("[aria-hidden='true']") !== null ||
              s.visibility === "hidden",
            // The lamp and its text alternative must be one object, or a
            // screen reader reads a loose word with no mark attached to it.
            sharesWrapperWithLamp:
              label.parentElement?.querySelector(".led") !== null &&
              label.parentElement?.classList.contains("led-wrap") === true,
          });
        }
        return out;
      });
      // The fixture has to contain rows, or this file asserts nothing.
      expect(labels.length).toBeGreaterThan(0);
      for (const label of labels) {
        // Present and a real word: an empty accessible name is the regression
        // that "remove the text" most easily ships.
        expect(label.text.length, "the status word must survive").toBeGreaterThan(0);
        // Clipped, not display:none. display:none removes it from the
        // accessibility tree along with the pixels, which is exactly what
        // this cell may not do.
        expect(label.display, "sr-only text must not be display:none").not.toBe(
          "none",
        );
        expect(label.clipped, "sr-only text must be clipped").toBe(true);
        expect(label.hiddenFromAT, "the word must still be announced").toBe(
          false,
        );
        expect(
          label.sharesWrapperWithLamp,
          "the word must be the lamp's own text alternative, not a loose span",
        ).toBe(true);
        // And it must genuinely not be drawn: the whole point of the change.
        expect(label.width, "the word must not be painted").toBeLessThanOrEqual(1);
        expect(label.height, "the word must not be painted").toBeLessThanOrEqual(1);
      }
    } finally {
      await page.close();
    }
  });

  it("clears the 3:1 non-text floor on the lamp that now carries status alone", async () => {
    const page = await open("rows", "[data-testid^='monitor-row-']");
    try {
      const worst = await page.evaluate(`(() => {
        const luminance = ${LUMINANCE};
        const parse = (c) => c.match(/\\d+(\\.\\d+)?/g).slice(0, 3).map(Number);
        const backdrop = (el) => {
          for (let p = el.parentElement; p; p = p.parentElement) {
            const bg = window.getComputedStyle(p).backgroundColor;
            if (bg && bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") return parse(bg);
          }
          return [0, 0, 0];
        };
        let worst = 21;
        let counted = 0;
        for (const lamp of Array.from(document.querySelectorAll(".mon-cell--led .led"))) {
          const style = window.getComputedStyle(lamp);
          const bgRaw = style.backgroundColor;
          // A hollow lamp (paused) is a ring, not a fill; its contrast is the
          // ring's, which led.css sets from --ink-2 and DESIGN.md §3 measures.
          if (!bgRaw || bgRaw === "rgba(0, 0, 0, 0)" || bgRaw === "transparent") continue;
          const fg = luminance(parse(bgRaw));
          const bg = luminance(backdrop(lamp));
          const ratio = (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
          worst = Math.min(worst, ratio);
          counted += 1;
        }
        return { worst, counted };
      })()`);
      // Without a lamp to measure this assertion is vacuous, so say so.
      expect((worst as { counted: number }).counted).toBeGreaterThan(0);
      // WCAG 1.4.11: a non-text indicator carrying information needs 3:1
      // against what is adjacent to it. This is the floor the lamp is held to
      // now that it is the only thing in the cell.
      expect((worst as { worst: number }).worst).toBeGreaterThanOrEqual(3);
    } finally {
      await page.close();
    }
  });

  it("clears the 4.5:1 AA floor on the status word the compact layout still draws", async () => {
    // The visible status word did not leave the product, it left one layout.
    // The compact list still prints it for everything that is not `up`, and
    // that word is real text read to decide whether something is broken — so
    // it keeps the AA floor, and the measurement that once caught 3.64:1
    // keeps running against a live element.
    const page = await open("compact", "[data-testid^='monitor-line-']");
    try {
      const measured = await page.evaluate(`(() => {
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
        let counted = 0;
        for (const label of Array.from(document.querySelectorAll(".mon-line-led .led-label"))) {
          const r = label.getBoundingClientRect();
          // Only what is actually painted: the sr-only variant carries no
          // contrast obligation and would drag the worst case to nonsense.
          if (r.width < 2 || r.height < 2) continue;
          const fg = luminance(parse(window.getComputedStyle(label).color));
          const bg = luminance(backdrop(label));
          const ratio = (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
          worst = Math.min(worst, ratio);
          counted += 1;
        }
        return { worst, counted };
      })()`);
      expect(
        (measured as { counted: number }).counted,
        "the compact layout must still draw a visible status word to measure",
      ).toBeGreaterThan(0);
      expect((measured as { worst: number }).worst).toBeGreaterThanOrEqual(4.5);
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
