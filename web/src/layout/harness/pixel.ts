/**
 * What the compositor painted, read back from a screenshot.
 *
 * Moved here from drawer-stacking.browser.test.ts once a second file needed
 * it (the checkbox's drawn states, SUB-167). The reasoning stays attached.
 */
import type { Page } from "./browser";

/**
 * The colour actually painted at one viewport point, as `r,g,b`.
 *
 * `elementFromPoint` is the wrong instrument for this element and that is not
 * a detail: `.hb-tooltip` sets `pointer-events: none`, so hit testing skips it
 * and answers with whatever is behind — the same answer it gives when the
 * readout really is buried, which would make the check pass in both
 * directions. A screenshot asks the compositor what it drew, which is the
 * question the heartbeat readout's report was about.
 *
 * The clip is in page coordinates while every rect above is in viewport
 * coordinates, hence the scroll offset: getting that wrong samples a point
 * 130px away and reports a confident answer about the wrong pixel.
 */
export async function colourAt(page: Page, x: number, y: number): Promise<string> {
  const scrollY = (await page.evaluate(() => window.scrollY)) as number;
  const shot = (await page.screenshot({
    captureBeyondViewport: false,
    clip: {
      x: Math.round(x),
      y: Math.round(y + scrollY),
      width: 1,
      height: 1,
    },
    encoding: "base64",
  })) as string;
  // A 1x1 PNG: walk the chunks, inflate IDAT, and read the one pixel past its
  // filter byte. Decoding here rather than pulling in an image library for
  // three bytes.
  const png = Buffer.from(shot, "base64");
  let idat = Buffer.alloc(0);
  for (let at = 8; at + 8 <= png.length; ) {
    const length = png.readUInt32BE(at);
    const type = png.toString("ascii", at + 4, at + 8);
    if (type === "IDAT") {
      idat = Buffer.concat([idat, png.subarray(at + 8, at + 8 + length)]);
    }
    at += 12 + length;
  }
  const { inflateSync } = await import("node:zlib");
  const raw = inflateSync(idat);
  return `${raw[1]},${raw[2]},${raw[3]}`;
}
