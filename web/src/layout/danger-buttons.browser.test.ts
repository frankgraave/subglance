/**
 * The worded Delete buttons, enabled, measured in a real browser.
 *
 * The reset card's "Delete all data" and the delete drawer's "Delete {name}"
 * drew their label in the failure red, which axe measured at 3.9:1 on the
 * button's surface in the dark theme, under the 4.5:1 a label needs. The
 * accessibility gate never saw it: both buttons stay disabled until a phrase
 * or a name is retyped, a disabled control is exempt from contrast, and no
 * audited screen types one. So this file types it, and then measures:
 *
 * - axe on the enabled button, in both themes;
 * - the label's ink against the button's real backdrop, at least 4.5:1;
 * - the bin glyph, a graphic, at least 3:1, and that it is drawn at all,
 *   since the words are no longer the coloured part.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { BACKDROP, LUMINANCE, OVER_BACKDROP } from "./harness/contrast";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { RESET_PHRASE } from "../reset/api";

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

/** Replaces a field's value the way typing would leave it, so React sees the change. */
async function fill(page: Page, selector: string, value: string): Promise<void> {
  await page.waitForSelector(selector, { visible: true, timeout: 15_000 });
  await page.$eval(selector, (el, next) => {
    const field = el as HTMLInputElement;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, next);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
}

async function open(theme: string, path: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
  await page.goto(`${server.url}${path}`, { waitUntil: "domcontentloaded" });
  return page;
}

const settle = (page: Page) => page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations()
    .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
    .map((animation) => animation.finished.catch(() => undefined)));
  await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
});

type Enabled = {
  name: string;
  /** The enabled button, once `enable` has run. */
  button: string;
  path: string;
  /** Takes the screen from rest to the button enabled. */
  enable: (page: Page) => Promise<void>;
};

const BUTTONS: Enabled[] = [
  {
    name: "the reset card's Delete all data",
    button: '#reset form[aria-label="Reset this instance"] button[type="submit"]',
    path: "/settings#reset",
    enable: async (page) => {
      await fill(page, '#reset form[aria-label="Reset this instance"] input[type="text"]', RESET_PHRASE);
    },
  },
  {
    name: "the delete drawer's Delete {name}",
    button: ".drawer-panel .button--danger",
    path: "/notifications",
    enable: async (page) => {
      const bin = ".inv-row .icon-button.button--danger";
      await page.waitForSelector(bin, { visible: true, timeout: 15_000 });
      await page.click(bin);
      await page.waitForSelector(".drawer-panel .phrase", { visible: true, timeout: 10_000 });
      const name = await page.$eval(".drawer-panel .phrase", (el) => el.textContent ?? "");
      await fill(page, '.drawer-panel input[type="text"]', name);
    },
  },
];

/** The label's ink and the glyph's stroke, each against what is really behind it. */
const MEASURE = `(selector) => {
  const luminance = ${LUMINANCE};
  const backdrop = ${BACKDROP};
  const over = ${OVER_BACKDROP};
  const ratio = (a, b) => {
    const x = luminance(a);
    const y = luminance(b);
    // Unrounded: a 4.496:1 rounded to two places would pass the 4.5:1 floor.
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  };
  const button = document.querySelector(selector);
  // The words are a text node; a probe inside the button inherits their ink
  // and stands on the button's own surface, which BACKDROP walks from a child.
  const probe = document.createElement("span");
  button.append(probe);
  const words = over(probe, getComputedStyle(probe).color);
  const wordsRatio = words ? ratio(words, backdrop(probe)) : 0;
  probe.remove();
  // A glyph that is not drawn marks nothing, whatever colour it computes to.
  const svg = button.querySelector(":scope > svg");
  const box = svg && svg.getBoundingClientRect();
  const glyph = box && box.width >= 1 && box.height >= 1 ? svg : null;
  const style = glyph && getComputedStyle(glyph);
  const mark = glyph && over(glyph, style.stroke === "none" ? style.color : style.stroke);
  return {
    disabled: button.disabled,
    words: wordsRatio,
    glyph: mark ? ratio(mark, backdrop(glyph)) : 0,
  };
}`;

/** axe on the enabled button alone, with its summary so a red run states the ratio. */
async function audit(page: Page, selector: string) {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async (within) => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include: [[within]] }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => `${node.target.join(" ")}: ${node.failureSummary ?? ""}`) }));
  }, selector);
}

describe.each(["light", "dark"])("%s theme", (theme) => {
  it.each(BUTTONS.map((button) => [button.name, button] as const))("draws %s legibly once enabled", async (_, target) => {
    const page = await open(theme, target.path);
    try {
      await target.enable(page);
      await page.waitForSelector(`${target.button}:not(:disabled)`, { visible: true, timeout: 10_000 });
      await settle(page);
      const seen = await page.evaluate(`(${MEASURE})(${JSON.stringify(target.button)})`) as
        { disabled: boolean; words: number; glyph: number };
      // Enabled, or the measurement below is of a control axe would exempt.
      expect(seen.disabled).toBe(false);
      // The label is text at the body size: 4.5:1. The bin is a graphic that
      // marks the button as destructive: 3:1, and present.
      expect(seen.words, "words").toBeGreaterThanOrEqual(4.5);
      expect(seen.glyph, "glyph").toBeGreaterThanOrEqual(3);
      expect(await audit(page, target.button)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});
