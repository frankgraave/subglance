/**
 * WCAG contrast helpers for the browser suite, shared as source strings.
 *
 * Each constant is a JavaScript expression that `page.evaluate` compiles
 * inside Chromium, so the colour conversion and the compositing are the
 * browser's own rather than a reimplementation in Node. They are strings, not
 * functions, because a function passed to `page.evaluate` cannot close over
 * another one: interpolating the strings is how they compose.
 *
 * Moved here from status-legibility.browser.test.ts once a second file needed
 * the same measurement; the reasoning stays attached to each helper.
 */

/** sRGB relative luminance, per WCAG 2.x. */
export const LUMINANCE = `(rgb) => {
  const channel = (v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  const [r, g, b] = rgb;
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}`;

/**
 * Any CSS colour, as `[r, g, b, a]` in sRGB.
 *
 * Every parse in this file used to be `match(/\d+(\.\d+)?/g).slice(0, 3)`,
 * which silently assumes the string is in rgb notation. The ink scale is authored in
 * `oklch()`, so `oklch(0.708 0 0)` — a mid grey — was read as the numbers
 * 0.708, 0 and 0 and treated as near-black RGB. That bug and the
 * alpha-dropping backdrop cancelled each other out: a nonsense dark
 * foreground measured against a wrongly-white backdrop produced a large
 * ratio, and the assertion passed. Fixing either one alone makes this file
 * fail, which is what happened, and is why both are fixed together.
 *
 * The canvas normalises whatever the computed style hands over — `oklch()`,
 * `color()`, a keyword, a hex — to sRGB bytes, using the browser's own
 * conversion rather than a reimplementation of it here.
 */
export const TO_RGBA = `(() => {
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  return (colour) => {
    if (!colour) return null;
    ctx.clearRect(0, 0, 1, 1);
    // An unparseable value leaves fillStyle at whatever it was before, so a
    // single sentinel cannot tell a typo from that sentinel's own colour.
    // Two different sentinels can: a real colour normalises to the same
    // string after both, a rejected one reads back as each sentinel in turn.
    ctx.fillStyle = "black";
    ctx.fillStyle = colour;
    const afterBlack = ctx.fillStyle;
    ctx.fillStyle = "white";
    ctx.fillStyle = colour;
    if (ctx.fillStyle !== afterBlack) return null;
    ctx.globalCompositeOperation = "copy";
    ctx.fillRect(0, 0, 1, 1);
    ctx.globalCompositeOperation = "source-over";
    const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
    return [r, g, b, a / 255];
  };
})()`;

/**
 * The real backdrop of an element: every ancestor background composited down
 * to an opaque colour.
 *
 * This existed as "walk up, take the first background that is not fully
 * transparent, drop its alpha". In the dark theme that is measurably wrong,
 * and wrong in the dangerous direction. `--surface` is
 * a 3% white veil over a near-black page — so
 * discarding the alpha reported the backdrop as pure white. Every ratio on
 * this page was then computed against white rather than against the almost
 * black composite the eye actually sees, which flatters a light lamp and
 * penalises a dark one: the check could pass a lamp that fails and fail a
 * lamp that passes.
 *
 * So the walk collects the layers instead of stopping at the first, and
 * composites them back to front with the standard source-over formula,
 * ending on the page's own opaque colour. Only an alpha of exactly 0 is
 * skipped, because a 3% veil is a layer.
 */
export const BACKDROP = `(() => {
  const toRgba = ${TO_RGBA};
  return (el) => {
    const layers = [];
    for (let p = el.parentElement; p; p = p.parentElement) {
      const parsed = toRgba(window.getComputedStyle(p).backgroundColor);
      if (parsed === null || parsed[3] === 0) continue;
      layers.push(parsed);
      if (parsed[3] >= 1) break;
    }
    // Bottom-most first, so each layer paints over the one below it.
    let out = [255, 255, 255];
    for (let i = layers.length - 1; i >= 0; i -= 1) {
      const layer = layers[i];
      const a = layer[3];
      out = [0, 1, 2].map((k) => layer[k] * a + out[k] * (1 - a));
    }
    return out;
  };
})()`;

/**
 * The element's own painted colour, composited over its backdrop.
 *
 * A lamp or a label can itself be translucent, and the same alpha-dropping
 * bug applies to the foreground. Returns null when there is nothing painted.
 */
export const OVER_BACKDROP = `(() => {
  const toRgba = ${TO_RGBA};
  const backdrop = ${BACKDROP};
  return (el, colour) => {
    const parsed = toRgba(colour);
    if (parsed === null || parsed[3] === 0) return null;
    const a = parsed[3];
    const back = backdrop(el);
    return [0, 1, 2].map((k) => parsed[k] * a + back[k] * (1 - a));
  };
})()`;
