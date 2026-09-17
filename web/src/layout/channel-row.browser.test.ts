/**
 * The channel rows, measured in a real layout engine.
 *
 * jsdom reports every width as zero, so the whole class of defect this file
 * exists for is invisible to the rest of the suite by construction. Two of
 * them shipped on the notifications screen and were found only by measuring a
 * populated page in Chromium (SUB-138):
 *
 * 1. The enable/disable toggle is labelled with the state it *moves to*, so it
 *    renders "Enable" (60.1px) on a disabled channel and "Disable" (64.8px) on
 *    an enabled one. The action cluster is right-aligned, so those 4.7px push
 *    every column in that row out of line with the rows above it. The one
 *    disabled channel in the harness fixture sat visibly askew for no reason a
 *    reader could account for.
 *
 * 2. Pressing Send test swaps the label for "Sending test…" — 76.1px to
 *    103.8px — so the entire cluster jumped 28px sideways at the exact moment
 *    the pointer was over it, and the Edit/Delete buttons moved out from under
 *    the cursor mid-click.
 *
 * Both are asserted as relationships between rows rather than against the
 * measured numbers above. The numbers are what a font renders today; the rule
 * is that columns line up and controls hold still, and that is what must not
 * regress.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
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

async function openChannels(): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/notifications", {
    waitUntil: "domcontentloaded",
  });
  await page.waitForSelector(".inv-row", { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  return page;
}

/** The left edge of each row's action cluster, one number per row. */
function actionEdges(page: Page): Promise<number[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll(".inv-row .inv-acts")].map(
      (el) => Math.round(el.getBoundingClientRect().left * 10) / 10,
    ),
  );
}

describe("the channel rows", () => {
  it("lines the columns up across rows whatever the toggle says", async () => {
    const page = await openChannels();
    try {
      /*
       * The fixture is deliberately mixed — one of its channels is disabled,
       * so one toggle means "Enable" and the rest mean "Disable". If the test
       * ran against a uniform list it would pass with the widths unreserved
       * and prove nothing.
       *
       * Read from the accessible name rather than from `textContent`, which
       * is empty by design: the toggle is a glyph now (SUB-138) and the verb
       * lives in `aria-label`. That change is also *why* the geometry below
       * holds without a reserved width — the two states are the same square —
       * so the assertion is kept exactly as it was rather than relaxed. It
       * would still catch a regression that put words back without reserving
       * room for them.
       */
      const labels = await page.evaluate(() =>
        [...document.querySelectorAll(".inv-row .nt-act-toggle")].map((el) =>
          (el.getAttribute("aria-label") ?? "").split(" ")[0],
        ),
      );
      expect(new Set(labels).size).toBeGreaterThan(1);

      const edges = await actionEdges(page);
      expect(edges.length).toBeGreaterThan(1);
      expect(new Set(edges).size).toBe(1);
    } finally {
      await page.close();
    }
  });

  it("does not move the row's controls when a test starts", async () => {
    const page = await openChannels();
    try {
      const before = await actionEdges(page);
      await page.evaluate(() => {
        const button = document.querySelector(
          ".inv-row .nt-act-test",
        ) as HTMLElement | null;
        button?.click();
      });
      /*
       * Waits for the label to actually change rather than for a fixed delay:
       * a sleep that is too short measures the pre-click layout and passes
       * against a regression.
       */
      await page.waitForFunction(
        () =>
          /Sending test/.test(
            document.querySelector(".inv-row .nt-act-test")?.textContent ?? "",
          ),
        { timeout: 5_000 },
      );
      const during = await actionEdges(page);
      expect(during).toEqual(before);
    } finally {
      await page.close();
    }
  });
});
