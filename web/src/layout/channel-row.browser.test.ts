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

async function openChannels(options: { channels?: string } = {}): Promise<Page> {
  /*
   * The fixture shape is an env var the harness reads per request, so it is
   * set around the navigation and restored afterwards rather than for the
   * whole file — the other cases in here want the full five-channel set, and
   * a leaked variable would silently give them two.
   */
  const previous = process.env.SUBGLANCE_HARNESS_CHANNELS;
  if (options.channels !== undefined) {
    process.env.SUBGLANCE_HARNESS_CHANNELS = options.channels;
  }
  try {
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
  } finally {
    if (previous === undefined) {
      delete process.env.SUBGLANCE_HARNESS_CHANNELS;
    } else {
      process.env.SUBGLANCE_HARNESS_CHANNELS = previous;
    }
  }
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
  it("holds the strings a type this build does not know produces", async () => {
    /*
     * The fixture's fifth channel names a type outside `CHANNEL_TYPES`, so
     * `typeLabel` falls back to "Unknown type" and `describeDestination` to
     * "this build does not know this channel type". Both are strings the
     * layout has to hold and neither is produced by any other row.
     *
     * Measured rather than merely asserted present, which is the only part of
     * this jsdom cannot do: "Unknown type" is 4ch wider than the widest real
     * label, in a type column sized for the real ones, and a fallback that
     * silently clips or wraps its row to two lines is exactly the defect this
     * file exists for. So the cell is required to render its whole string on
     * one line, and the row is required to be no taller than its neighbours.
     */
    const page = await openChannels();
    try {
      const unknown = await page.evaluate(() => {
        const rows = [...document.querySelectorAll<HTMLElement>(".inv-row")];
        const row = rows.find((r) =>
          /Unknown type/.test(r.querySelector(".inv-type")?.textContent ?? ""),
        );
        if (row === undefined) return null;
        const type = row.querySelector<HTMLElement>(".inv-type")!;
        const sub = row.querySelector<HTMLElement>(".inv-sub")!;
        const others = rows
          .filter((r) => r !== row)
          /*
           * Compared against the rows carrying no chip only. One fixture
           * channel is disabled and wears a `Disabled` chip, which makes its
           * name line 23px rather than 20 and its row 57px rather than 54 —
           * a legitimate 3px that has nothing to do with the type label.
           * Measuring against the tallest row would fold that in and make
           * this assertion a test of the chip.
           */
          .filter((r) => r.querySelector(".inv-paused-chip") === null)
          .map((r) => Math.round(r.getBoundingClientRect().height));
        return {
          typeText: (type.textContent ?? "").trim(),
          subText: (sub.textContent ?? "").trim(),
          typeClipped: type.scrollWidth > Math.ceil(type.clientWidth) + 1,
          typeLines: Math.round(
            type.getBoundingClientRect().height /
              parseFloat(getComputedStyle(type).lineHeight),
          ),
          rowHeight: Math.round(row.getBoundingClientRect().height),
          otherRowHeights: others,
        };
      });
      expect(unknown, "no row rendered the unknown-type fallback").not.toBeNull();
      expect(unknown!.typeText).toBe("Unknown type");
      expect(unknown!.subText).toBe("this build does not know this channel type");
      expect(unknown!.typeClipped, "the Unknown type label is clipped").toBe(
        false,
      );
      expect(unknown!.typeLines, "the Unknown type label wrapped").toBe(1);
      expect(
        Math.max(...unknown!.otherRowHeights),
        "the unknown-type row is taller than the rows around it",
      ).toBe(unknown!.rowHeight);
    } finally {
      await page.close();
    }
  });

  it("gives the delivery caveat less height than the list it qualifies", async () => {
    /*
     * The rejection, measured in a real layout engine.
     *
     * On the owner's instance the caveat was a six-line block above two rows:
     * you read an explanation of the Delivery column before you ever reached
     * the Delivery column. `NotificationsView.test.tsx` pins the amount of
     * prose, which is what *produced* the height, but jsdom reports every box
     * as zero — so the thing actually rejected has never been checked. The
     * summary is bounded to a 62ch measure, and unchanged text that gains a
     * word, or a narrower viewport, puts it onto a second line with no unit
     * test able to see it.
     *
     * Two channels, matching the instance the rejection was written against.
     * A relationship rather than a number: the closed caveat must be shorter
     * than the list below it, which is the shape that was rejected and not a
     * particular pixel count.
     */
    const page = await openChannels({ channels: "two" });
    try {
      const measured = await page.evaluate(() => {
        const legend = document.querySelector<HTMLElement>(".nt-legend");
        const list = document.querySelector<HTMLElement>(".inv-list");
        if (legend === null || list === null) return null;
        const summary = legend.querySelector<HTMLElement>(".nt-legend-summary")!;
        return {
          open: (legend as HTMLDetailsElement).open,
          rows: list.querySelectorAll(".inv-row").length,
          legendHeight: Math.round(legend.getBoundingClientRect().height),
          listHeight: Math.round(list.getBoundingClientRect().height),
          summaryLines: Math.round(
            summary.getBoundingClientRect().height /
              parseFloat(getComputedStyle(summary).lineHeight),
          ),
        };
      });
      expect(measured, "the notifications page did not render a legend").not.toBeNull();
      expect(measured!.open, "the caveat is open by default").toBe(false);
      expect(measured!.rows).toBe(2);
      expect(
        measured!.summaryLines,
        "the closed caveat wrapped onto more than one line",
      ).toBe(1);
      expect(
        measured!.legendHeight,
        "the closed caveat is taller than the list it qualifies",
      ).toBeLessThan(measured!.listHeight);
    } finally {
      await page.close();
    }
  });

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
