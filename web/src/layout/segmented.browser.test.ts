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
  await page.waitForSelector(".segmented .segmented-option", { timeout: 15_000 });
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** Widths of every segment in the first control, in order. */
function widths(): Promise<number[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll(".segmented .segmented-option")].map(
      (el) => el.getBoundingClientRect().width,
    ),
  );
}

describe("the segmented control holds its size when the selection moves", () => {
  it("gives an inactive segment the same border box as the pressed one", async () => {
    const border = await page.evaluate(() => {
      const options = [...document.querySelectorAll(".segmented .segmented-option")];
      const pressed = options.find((el) => el.getAttribute("aria-pressed") === "true");
      const resting = options.find((el) => el.getAttribute("aria-pressed") !== "true");
      if (!pressed || !resting) throw new Error("expected one pressed and one resting segment");
      return {
        pressed: getComputedStyle(pressed).borderTopWidth,
        resting: getComputedStyle(resting).borderTopWidth,
        restingColour: getComputedStyle(resting).borderTopColor,
      };
    });
    expect(border.resting).toBe(border.pressed);
    expect(border.resting).toBe("1px");
    // Reserved, not drawn: the space is taken, the edge is not visible.
    expect(border.restingColour).toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  });

  it("does not resize a segment when it becomes the pressed one", async () => {
    const before = await widths();
    await page.evaluate(() => {
      const options = [...document.querySelectorAll(".segmented .segmented-option")];
      const resting = options.find(
        (el) => el.getAttribute("aria-pressed") !== "true",
      ) as HTMLElement;
      resting.click();
    });
    // Same elements, same widths: the press changed colour and nothing else.
    expect(await widths()).toEqual(before);
  });
});

describe("the concentric radius survives the build", () => {
  it("draws a segment on the 2px step inside a 6px shell", async () => {
    const radii = await page.evaluate(() => {
      const shell = document.querySelector(".segmented")!;
      const option = document.querySelector(".segmented .segmented-option")!;
      return {
        shell: getComputedStyle(shell).borderTopLeftRadius,
        option: getComputedStyle(option).borderTopLeftRadius,
        padding: getComputedStyle(shell).paddingLeft,
      };
    });
    expect(radii.shell).toBe("6px");
    expect(radii.padding).toBe("4px");
    // 6 - 4 = 2. Equal radii would pinch at the corner.
    expect(radii.option).toBe("2px");
  });
});
