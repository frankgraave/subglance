/**
 * SUB-106: the chip family's two visual claims, in a real browser.
 *
 * Both are claims about *computed* values, which is exactly what jsdom cannot
 * answer: it applies no stylesheet, so every colour it reports is empty and
 * every border is `medium none`. `tokens.test.ts` proves the source says the
 * right thing; only this proves the build kept it.
 *
 *   1. A filled status badge is actually readable. The reason the `--on-*`
 *      pair exists is that `--ink` measures 1.59:1 on `--up` — a number no
 *      diff shows you. So the label contrast is measured here rather than
 *      asserted as a hex, which also means it keeps holding if a status
 *      colour is ever retuned.
 *   2. The dashed kinds are dashed once rendered, and carry no fill. That
 *      convention is one character of one declaration and nothing else in the
 *      pipeline looks at it.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

let server: Server;
let browser: Browser;
let page: Page;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  // The chips live in the workbench, behind its toggle. Waiting for the
  // button first: a face is only fetched once something asks for it, and a
  // click dispatched before hydration lands on nothing.
  await page.waitForSelector('[aria-label="Component workbench"]', {
    timeout: 15_000,
  });
  await page.click('[aria-label="Component workbench"]');
  await page.waitForSelector(".chip--status", { timeout: 15_000 });
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** WCAG relative luminance of a computed `rgb(...)` string. */
function contrastInPage() {
  return (a: string, b: string): number => {
    const parse = (value: string): number[] => {
      const parts = value.match(/[\d.]+/g);
      if (!parts || parts.length < 3) throw new Error(`not a colour: ${value}`);
      return parts.slice(0, 3).map(Number);
    };
    const lum = (rgb: number[]): number => {
      const [r, g, b2] = rgb.map((c) => {
        const s = c / 255;
        return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b2;
    };
    const one = lum(parse(a));
    const two = lum(parse(b));
    return (Math.max(one, two) + 0.05) / (Math.min(one, two) + 0.05);
  };
}

describe("a filled status badge is readable on its own fill", () => {
  it("clears AA for every status, measured rather than assumed", async () => {
    const measured = await page.evaluate((source) => {
      const contrast = new Function(`return (${source})()`)() as (
        a: string,
        b: string,
      ) => number;
      return [...document.querySelectorAll(".chip--status")].map((el) => {
        const style = getComputedStyle(el);
        return {
          status: el.getAttribute("data-status") ?? "?",
          ratio: contrast(style.color, style.backgroundColor),
        };
      });
    }, contrastInPage.toString());

    expect(measured.length, "expected the four status badges").toBe(4);
    for (const { status, ratio } of measured) {
      expect(ratio, `${status} label on its fill`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("actually fills the badge, so the measurement is not against the page", async () => {
    // A transparent background computes as rgba(0,0,0,0) and would make the
    // contrast check above compare the label against black and pass anyway.
    const fills = await page.evaluate(() =>
      [...document.querySelectorAll(".chip--status")].map(
        (el) => getComputedStyle(el).backgroundColor,
      ),
    );
    for (const fill of fills) {
      expect(fill).not.toMatch(/rgba\([^)]*,\s*0\)/);
    }
  });
});

describe("the dashed kinds survive the build", () => {
  it("draws the meta-state chip and the empty avatar dashed, with no fill", async () => {
    const drawn = await page.evaluate(() =>
      [".chip--state", ".chip-avatar"].map((selector) => {
        const el = document.querySelector(selector);
        if (!el) throw new Error(`missing ${selector}`);
        const style = getComputedStyle(el);
        return {
          selector,
          style: style.borderTopStyle,
          fill: style.backgroundColor,
        };
      }),
    );
    for (const { selector, style, fill } of drawn) {
      expect(style, `${selector} border style`).toBe("dashed");
      expect(fill, `${selector} fill`).toMatch(/rgba\([^)]*,\s*0\)/);
    }
  });

  it("keeps the status badge solid, so the two cannot be confused", async () => {
    const style = await page.evaluate(() => {
      const el = document.querySelector(".chip--status");
      if (!el) throw new Error("missing .chip--status");
      return getComputedStyle(el).borderTopStyle;
    });
    expect(style).not.toBe("dashed");
  });
});
