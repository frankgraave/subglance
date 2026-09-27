/**
 * TO_RGBA against a real canvas.
 *
 * The canvas silently keeps its previous fillStyle when handed a value it
 * cannot parse, so a typo in a computed colour used to be measured as the
 * sentinel the converter set first. It must come back as null instead, while
 * black and white themselves stay measurable.
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { chromium, type Browser, type Page } from "./browser";
import { TO_RGBA } from "./contrast";

let browser: Browser;
let page: Page;

beforeAll(async () => {
  browser = await chromium();
  page = await browser.newPage();
});

afterAll(async () => {
  await browser?.close();
});

async function convert(colour: string): Promise<unknown> {
  return page.evaluate(`(${TO_RGBA})(${JSON.stringify(colour)})`);
}

describe("TO_RGBA", () => {
  it("returns null for a colour the canvas cannot parse", async () => {
    expect(await convert("not-a-colour")).toBeNull();
    expect(await convert("rgb(nope)")).toBeNull();
  });

  it("keeps black and white measurable", async () => {
    expect(await convert("black")).toEqual([0, 0, 0, 1]);
    expect(await convert("#fff")).toEqual([255, 255, 255, 1]);
  });

  it("converts a modern colour syntax to sRGB bytes", async () => {
    expect(await convert("oklch(0.628 0.2577 29.23)")).toEqual([255, 0, 0, 1]);
    // Alpha comes back as a byte, so 0.5 reads as 128/255.
    const [r, g, b, a] = (await convert("rgb(0 128 0 / 0.5)")) as number[];
    expect([r, g, b]).toEqual([0, 128, 0]);
    expect(a).toBeCloseTo(0.5, 2);
  });
});
