/**
 * SUB-106: the segmented control's two structural claims, in a real browser.
 *
 * Both of them are about computed geometry, which is exactly what jsdom cannot
 * answer: it applies no stylesheet, so every box it reports is 0x0 and every
 * border is `medium none`. The claims:
 *
 * 1. An inactive segment already carries its border, in `transparent`, so the
 *    control does not resize when the selection moves. This is the failure
 *    that is invisible in a screenshot and obvious in use — every label beside
 *    the control shifting a pixel on each press.
 * 2. The inner radius is the outer minus the padding (§2.7). `tokens.test.ts`
 *    proves the source says 2px; only the browser proves the build kept it.
 *
 * ## Both assertions are scoped to ONE control
 *
 * They were written when the dashboard had a single segmented bar and selected
 * with a bare `.segmented`, which silently became "whichever control renders
 * first" as soon as the theme switcher and the column picker joined it. The
 * width check then compared eleven segments across three groups and clicked a
 * resting segment that could belong to a different one than the widths it had
 * just recorded — a test that fails on a layout change it does not measure.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

/**
 * The control under test: the layout switcher.
 *
 * Named explicitly rather than taken by position. It is the one segmented bar
 * present on every dashboard regardless of preference — the column picker
 * belongs to the Cards layout alone — so it is the stable subject for a claim
 * that is about the shared component rather than about any one of its uses.
 */
const CONTROL = '.segmented[aria-label="Dashboard layout"]';
const OPTION = `${CONTROL} .segmented-option`;

let server: Server;
let browser: Browser;
let page: Page;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(OPTION, { timeout: 15_000 });
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** Widths of every segment in the control under test, in order. */
function widths(): Promise<number[]> {
  return page.evaluate(
    (selector) =>
      [...document.querySelectorAll(selector)].map(
        (el) => el.getBoundingClientRect().width,
      ),
    OPTION,
  );
}

describe("the segmented control holds its size when the selection moves", () => {
  it("gives an inactive segment the same border box as the pressed one", async () => {
    const border = await page.evaluate((selector) => {
      const options = [...document.querySelectorAll(selector)];
      const pressed = options.find((el) => el.getAttribute("aria-pressed") === "true");
      const resting = options.find((el) => el.getAttribute("aria-pressed") !== "true");
      if (!pressed || !resting) throw new Error("expected one pressed and one resting segment");
      return {
        pressed: getComputedStyle(pressed).borderTopWidth,
        resting: getComputedStyle(resting).borderTopWidth,
        restingColour: getComputedStyle(resting).borderTopColor,
      };
    }, OPTION);
    expect(border.resting).toBe(border.pressed);
    expect(border.resting).toBe("1px");
    // Reserved, not drawn: the space is taken, the edge is not visible.
    expect(border.restingColour).toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  });

  it("does not resize a segment when it becomes the pressed one", async () => {
    const before = await widths();
    expect(before.length, "expected the layout switcher's segments").toBeGreaterThan(1);
    await page.evaluate((selector) => {
      const options = [...document.querySelectorAll(selector)];
      const resting = options.find(
        (el) => el.getAttribute("aria-pressed") !== "true",
      ) as HTMLElement;
      resting.click();
    }, OPTION);
    // Same elements, same widths: the press changed colour and nothing else.
    expect(await widths()).toEqual(before);
  });
});

describe("the concentric radius survives the build", () => {
  /*
   * 8px outside, 2px of padding, 6px inside.
   *
   * These were 6/4/2 when the control was first landed. "One selected state
   * for a segmented control" re-cut it against the reference — which measures
   * an 8px frame with 2px of padding around 6px segments — and DESIGN.md §7.8
   * now states those numbers as the geometry. The test kept asserting the old
   * ladder, so it has been failing on a control that is drawn correctly.
   *
   * What the assertion is actually for survives the change untouched: the
   * inner radius must be the outer minus the padding, not equal to it.
   */
  it("draws a segment on the 2px step inside an 8px shell", async () => {
    const radii = await page.evaluate((selector) => {
      const shell = document.querySelector(selector)!;
      const option = shell.querySelector(".segmented-option")!;
      return {
        shell: getComputedStyle(shell).borderTopLeftRadius,
        option: getComputedStyle(option).borderTopLeftRadius,
        padding: getComputedStyle(shell).paddingLeft,
      };
    }, CONTROL);
    expect(radii.shell).toBe("8px");
    expect(radii.padding).toBe("2px");
    // 8 - 2 = 6. Equal radii would pinch at the corner.
    expect(radii.option).toBe("6px");
  });
});
