/**
 * What the drawer is, measured rather than assumed.
 *
 * This file exists because of a bug jsdom cannot see and reading the CSS did
 * not reveal either. `.drawer-panel` sets `background: var(--surface)` and
 * `z-index: 51`, and `.drawer-scrim` sets `background: var(--scrim)` at
 * `z-index: 50` — by the stylesheet the drawer is opaque and the page behind
 * it is dimmed. On screen the monitor list read straight through both.
 *
 * The cause is stacking context, not colour: an ancestor with its own
 * `z-index` confines a descendant's `z-index` to that context, so 51 stops
 * meaning "above everything" and starts meaning "above its siblings". No
 * assertion about a CSS declaration can catch that, because every declaration
 * involved is correct. Only a real browser knows what is actually on top.
 *
 * So these assertions are about *observed* paint, in the spirit of the phone
 * layout checks next door: what does the browser say is at this point, and is
 * the thing behind it hidden. Both are numbers or element identities, never
 * screenshots, so a legitimate visual change does not need blessing.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

/** The phone navigation toggle, by the name a screen reader announces. */
const NAV_BUTTON = 'button[aria-label="Open navigation"]';

let server: Server;
let browser: Browser;

/**
 * A page pinned to the dark theme, which is the product's default and the
 * theme these defects live in.
 *
 * Not a detail, and it is the reason an earlier version of this file could not
 * fail: headless Chromium reports `prefers-color-scheme: light`, so every page
 * here rendered the light palette — where `--surface`, `--surface-2` and
 * `--border` are already opaque hex values. A tooltip filled with `--surface-2`
 * is fully opaque in light and 5% white in dark, so an assertion about
 * transparency taken on a light page is an assertion that can only ever pass.
 * Two of the checks below were green against the very defect they were written
 * for until this line existed.
 *
 * Seeded through the same storage key the product reads, rather than through
 * an emulated media query, because the preference is what a user actually sets
 * and it is what the app consults first.
 */
async function darkPage(): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.evaluateOnNewDocument(
    (key: string) => window.localStorage.setItem(key, "dark"),
    THEME_STORAGE_KEY,
  );
  return page;
}

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/**
 * Splits an "r,g,b" reading into numbers, so two colours can be compared with
 * a tolerance instead of as strings.
 */
function parseChannels(reading: string): number[] {
  const parts = reading.split(",").map((n) => Number(n.trim()));
  if (parts.length !== 3 || parts.some(Number.isNaN)) {
    throw new Error(`not a colour reading: ${reading}`);
  }
  return parts;
}

/**
 * Opens a screen and reaches the add drawer, which is the path a user takes
 * and therefore the one worth testing.
 *
 * The button moved in SUB-138. It used to sit in the masthead on every
 * screen; it now sits in the header of the monitors card it adds to, because
 * a control in chrome that is present on Notifications while meaning
 * something about monitors is chrome that has to be re-read per screen. So
 * from anywhere else the path is: navigate to Monitors, then press it.
 *
 * What these tests assert is unchanged and is not about the button: the
 * drawer is one surface, opaque, painted and hit-tested over the page it was
 * opened from, whichever screen that was.
 */
async function openAddDrawer(path: string): Promise<Page> {
  const page = await darkPage();
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".shell-topbar", { timeout: 15_000 });

  if (path !== "/monitors") {
    await page.click('a[href="/monitors"]');
    await page.waitForSelector('button[aria-label="Add monitor"]', {
      timeout: 15_000,
    });
  }

  /*
   * Found by its accessible name rather than a test id, so the selector is
   * the same string a screen-reader user hears. A rename that breaks this
   * test is a rename that broke the button's name.
   */
  await page.click('button[aria-label="Add monitor"]');
  await page.waitForSelector(".drawer-panel", { timeout: 15_000 });
  /*
   * Wait for the entrance to *finish*, not for two frames to pass.
   *
   * The drawer slides in from `translateX(100%)` over `--dur-panel`, so for
   * the first 150ms its box is partly or wholly outside the viewport and
   * `elementFromPoint` at its centre answers `null` — which is the same
   * "nothing of the drawer is here" the trapped-stacking-context bug produced,
   * arrived at for a completely different reason. Two animation frames is
   * roughly 32ms, so the measurement landed mid-slide and the test would have
   * reported a defect the product does not have.
   *
   * `getAnimations().finished` asks the engine when the movement is over
   * rather than guessing a duration here, so changing `--dur-panel` cannot
   * silently reintroduce the race. The fallback keeps a browser without the
   * API from hanging the suite; the tests below then measure whatever is on
   * screen, which is the honest thing for them to do.
   */
  await page.evaluate(async () => {
    const panel = document.querySelector(".drawer-panel");
    if (panel === null) return;
    const animations = panel.getAnimations?.() ?? [];
    await Promise.all(animations.map((a) => a.finished.catch(() => undefined)));
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
    );
  });
  return page;
}

const SCREENS = [
  { name: "dashboard", path: "/" },
  { name: "monitors", path: "/monitors" },
  { name: "incidents", path: "/incidents" },
];

describe.each(SCREENS)("the add drawer on $name", (screen) => {
  it("is what the pointer actually hits over its own area", async () => {
    /*
     * `elementFromPoint` answers the question the screenshot raised: at a
     * point inside the drawer, what is on top? If a stacking context has
     * trapped the panel, the answer is a row of the list behind it.
     */
    const page = await openAddDrawer(screen.path);
    try {
      const hit = await page.evaluate(() => {
        const panel = document.querySelector(".drawer-panel");
        if (panel === null) return { found: false, inside: false, tag: "" };
        const box = panel.getBoundingClientRect();
        const el = document.elementFromPoint(
          box.left + box.width / 2,
          box.top + box.height / 2,
        );
        return {
          found: true,
          inside: el !== null && panel.contains(el),
          tag: el === null ? "null" : `${el.tagName}.${el.className}`,
        };
      });
      // The tag rides along so a failure names what was on top instead.
      expect(hit).toMatchObject({ found: true, inside: true });
    } finally {
      await page.close();
    }
  });

  it("paints an opaque surface, not a tint over the page", async () => {
    /*
     * A computed `background-color` with any alpha below 1 means the list
     * behind shows through. Read off the element rather than the stylesheet,
     * so a token change that introduces transparency is caught too.
     *
     * The page is pinned to dark for a reason that cost this file an entire
     * round of mutation testing: in the light theme every surface token is
     * already an opaque hex, so this assertion is unfalsifiable there. See
     * `darkPage`.
     */
    const page = await openAddDrawer(screen.path);
    try {
      const alpha = await page.evaluate(`(() => { ${RESOLVE_COLOUR}
        const panel = document.querySelector(".drawer-panel");
        if (panel === null) return null;
        const rgba = toRgba(getComputedStyle(panel).backgroundColor);
        return rgba === null ? "unreadable" : rgba.a;
      })()`);
      expect(alpha).toBe(1);
    } finally {
      await page.close();
    }
  });

  it("is painted over the page, not merely hit-tested over it", async () => {
    /*
     * The pointer check above and this one are not the same assertion.
     *
     * `elementFromPoint` answers about hit testing; a screenshot answers about
     * paint, and the two can disagree — an element can win the hit test and
     * still be drawn under something, which is precisely the shape of a
     * stacking-context bug. The drawer covering the list is a claim about
     * pixels, so it is measured in pixels.
     */
    const page = await openAddDrawer(screen.path);
    try {
      const seen = await page.evaluate(() => {
        const panel = document.querySelector(".drawer-panel");
        if (panel === null) return null;
        const box = panel.getBoundingClientRect();
        return {
          // A strip near the leading edge, where the list behind is densest.
          x: box.left + 12,
          y: box.top + box.height / 2,
          fill: getComputedStyle(panel).backgroundColor,
        };
      });
      if (seen === null) throw new Error("no drawer on screen");

      /*
       * Compared within one unit per channel rather than exactly.
       *
       * `--surface-float` is authored in oklch, and Chromium converts it to
       * sRGB twice by two different routes: once for `getComputedStyle`, once
       * for the compositor that fills the pixel. Those two round
       * independently, and here they land a single unit apart — 37 painted
       * against 38 computed. Asserting equality made the test fail on a
       * correct drawer, which is a false alarm about the most expensive kind
       * of bug to chase.
       *
       * The tolerance costs nothing this test was buying. The defect it
       * exists for is the drawer painting as the page showing through it —
       * `--surface` at 3% alpha over a dark canvas, tens of units away from
       * the opaque fill, not one. `mutations-ui-feedback.sh` reverts exactly
       * that and this assertion still fails, which is what keeps the number
       * below honest.
       */
      const painted = parseChannels(await colourAt(page, seen.x, seen.y));
      const declared = parseChannels(await resolveColour(page, seen.fill));
      for (const [i, channel] of painted.entries()) {
        expect(
          Math.abs(channel - declared[i]),
          `channel ${i}: painted ${painted.join(",")} against declared ${declared.join(",")}`,
        ).toBeLessThanOrEqual(1);
      }
    } finally {
      await page.close();
    }
  });

  it("covers the page behind it with a scrim", async () => {
    // The scrim is what makes the drawer read as modal. Without it the page
    // behind stays at full contrast and competes with the form.
    const page = await openAddDrawer(screen.path);
    try {
      const scrim = await page.evaluate(() => {
        const el = document.querySelector(".drawer-scrim");
        if (el === null) return null;
        const box = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        return {
          coversViewport:
            box.width >= window.innerWidth && box.height >= window.innerHeight,
          visible: style.visibility !== "hidden" && style.display !== "none",
          onTopOfContent: document.elementFromPoint(40, window.innerHeight - 40) === el,
        };
      });
      expect(scrim).toEqual({
        coversViewport: true,
        visible: true,
        onTopOfContent: true,
      });
    } finally {
      await page.close();
    }
  });
});

describe("the add button", () => {
  it("opens the same drawer from every screen", async () => {
    /*
     * The reported inconsistency: the same control replaced the whole screen
     * on the dashboard and opened a drawer on monitors. Asserted as "a drawer
     * exists and the page behind it is still there", because the failure mode
     * was not a missing form — it was the list disappearing.
     */
    for (const screen of SCREENS) {
      const page = await openAddDrawer(screen.path);
      try {
        const seen = await page.evaluate(() => ({
          drawer: document.querySelectorAll(".drawer-panel").length,
          // The shell survives: a full-page form unmounted the content column.
          shell: document.querySelectorAll(".shell-topbar").length,
        }));
        expect({ screen: screen.name, ...seen }).toEqual({
          screen: screen.name,
          drawer: 1,
          shell: 1,
        });
      } finally {
        await page.close();
      }
    }
  });
});

/**
 * The heartbeat readout, measured the same way and for the same reason.
 *
 * Frank: *"the tooltip is cut off at the top here and it is transparent, so
 * you cannot read it properly."* Two defects that look like one.
 *
 * Transparent: `.hb-tooltip` filled itself with `--surface-2`, which in the
 * dark theme is 5% white — so 95% of the row behind it came through, and the
 * same element rendered a different colour depending on whether it opened over
 * the page or over a card. Nothing in the stylesheet was wrong; the token was
 * built for surfaces that *rest* on each other and the tooltip *floats*.
 *
 * Cut off: the readout grows upward out of its row, so on the first row of a
 * list it rises past the sticky topbar. It carried `z-index: 10` and the bar
 * carries 20 — and no amount of reading either rule says which wins, because
 * the answer depends on which ancestors created stacking contexts three files
 * away. Only a browser knows.
 *
 * Both are asserted against what the engine reports rather than against a
 * declaration, so a token change that reintroduces transparency, or a new
 * sticky ancestor that re-traps the readout, is caught by the same two checks.
 */
/**
 * Opens the readout on the top row, with that row scrolled under the bar.
 *
 * The position is the test. The readout grows *upward* out of its row, so it
 * can only be clipped by the topbar when the row it belongs to sits close
 * under it — hovering a row in the middle of the list produces a tooltip with
 * nothing above it, which passes whether the bug is present or not. 130px puts
 * the track far enough down to hover and near enough that the 96px readout
 * above it reaches into the bar's 57px band.
 */
async function openReadoutUnderTheBar(): Promise<Page> {
  const page = await darkPage();
  // Short on purpose: the fixture is four monitors, and the page has to be
  // taller than the viewport before anything can be scrolled under the bar.
  await page.setViewport({ width: 1440, height: 400, deviceScaleFactor: 1 });
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".hb-track", { timeout: 15_000 });

  await page.evaluate(() => {
    const track = document.querySelector(".hb-track");
    if (track === null) return;
    window.scrollBy(0, track.getBoundingClientRect().top - 130);
  });

  const point = await page.evaluate(() => {
    const track = document.querySelector(".hb-track");
    if (track === null) return null;
    const box = track.getBoundingClientRect();
    // Left of centre, so the readout is wide enough to extend past its column.
    return { x: box.left + box.width * 0.3, y: box.top + box.height / 2 };
  });
  if (point === null) throw new Error("no heartbeat track on the dashboard");
  await page.mouse.move(point.x, point.y);
  await page.waitForSelector(".hb-tooltip", { timeout: 15_000 });
  return page;
}


/**
 * Any CSS colour as `{ r, g, b, a }`, resolved by the engine rather than parsed.
 *
 * Written as a string of source injected into the page, because it has to run
 * in three separate `page.evaluate` calls and Puppeteer serialises each one
 * independently.
 *
 * Parsing the computed value by hand is what this replaces, and the reason is
 * instructive: `--surface-float` is declared in `oklch()`, and Chrome now
 * reports `oklch(0.269 0 0)` from `getComputedStyle` rather than converting to
 * `rgb()`. A regex for `rgba?(...)` therefore matched nothing and the check
 * read "cannot tell" as "fine". Compositing the colour over black and over
 * white and comparing lets the browser do the conversion for every notation
 * it supports, including ones that do not exist yet.
 */
const RESOLVE_COLOUR = `
function toRgba(colour) {
  const read = (backdrop) => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const ctx = canvas.getContext("2d");
    ctx.fillStyle = backdrop;
    ctx.fillRect(0, 0, 1, 1);
    ctx.fillStyle = colour;
    // An unparseable colour leaves fillStyle at the previous value, which
    // would silently report the backdrop as the answer.
    if (ctx.fillStyle === backdrop && colour !== backdrop) return null;
    ctx.fillRect(0, 0, 1, 1);
    return ctx.getImageData(0, 0, 1, 1).data;
  };
  const onBlack = read("#000000");
  const onWhite = read("#ffffff");
  if (onBlack === null || onWhite === null) return null;
  // Over white a transparent colour lands higher than over black by exactly
  // 255 * (1 - alpha) per channel.
  const alpha = 1 - (onWhite[0] - onBlack[0]) / 255;
  return {
    r: onBlack[0],
    g: onBlack[1],
    b: onBlack[2],
    // Rounded to three places: the two reads go through 8-bit channels, so an
    // exact 1 would otherwise depend on rounding luck.
    a: Math.round(alpha * 1000) / 1000,
  };
}
`;

/**
 * The colour actually painted at one viewport point, as `r,g,b`.
 *
 * `elementFromPoint` is the wrong instrument for this element and that is not
 * a detail: `.hb-tooltip` sets `pointer-events: none`, so hit testing skips it
 * and answers with whatever is behind — the same answer it gives when the
 * readout really is buried, which would make the check pass in both
 * directions. A screenshot asks the compositor what it drew, which is the
 * question Frank's report is about.
 *
 * The clip is in page coordinates while every rect above is in viewport
 * coordinates, hence the scroll offset: getting that wrong samples a point
 * 130px away and reports a confident answer about the wrong pixel.
 */
async function colourAt(page: Page, x: number, y: number): Promise<string> {
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

/** A CSS colour as the `r,g,b` string `colourAt` returns, for comparison. */
async function resolveColour(page: Page, colour: string): Promise<string> {
  const rgba = (await page.evaluate(`(() => { ${RESOLVE_COLOUR}
    return toRgba(${JSON.stringify(colour)});
  })()`)) as { r: number; g: number; b: number } | null;
  if (rgba === null) throw new Error(`could not resolve the colour ${colour}`);
  return `${rgba.r},${rgba.g},${rgba.b}`;
}

describe("the heartbeat readout", () => {
  it("paints an opaque surface, not a tint over the row behind it", async () => {
    const page = await openReadoutUnderTheBar();
    try {
      const alpha = await page.evaluate(`(() => { ${RESOLVE_COLOUR}
        const tip = document.querySelector(".hb-tooltip");
        if (tip === null) return null;
        const rgba = toRgba(getComputedStyle(tip).backgroundColor);
        return rgba === null ? "unreadable" : rgba.a;
      })()`);
      expect(alpha).toBe(1);
    } finally {
      await page.close();
    }
  });

  it("is the same colour whatever it opens over", async () => {
    /*
     * The half an alpha fill fails that a single measurement cannot see.
     *
     * `--surface-2` is 5% white, so the readout composited against whatever
     * was beneath it: over the page it landed at #232323, over a card at
     * #292929. One element, two colours, decided by which row you happened to
     * hover. Opacity alone does not catch this — a 50% fill would be "not
     * transparent enough to read through" and still be two colours — so it is
     * asserted separately and against painted pixels rather than against the
     * declaration, because the declaration is identical in both cases and it
     * is the compositing that differs.
     *
     * Two probes on one page: one on the canvas, one inside a card. Same
     * element, same class, two backgrounds.
     */
    const page = await darkPage();
    await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".card, .mon-board", { timeout: 15_000 });
    try {
      const spots = await page.evaluate(() => {
        const place = (parent: Element, id: string) => {
          const probe = document.createElement("div");
          probe.className = "hb-tooltip";
          probe.id = id;
          // Pinned into view and sized, so both samples are taken from a
          // painted rectangle rather than from a zero-height element.
          probe.style.position = "fixed";
          probe.style.top = id === "probe-canvas" ? "200px" : "320px";
          probe.style.left = "300px";
          probe.style.width = "160px";
          probe.style.height = "60px";
          parent.append(probe);
        };
        // Straight onto the page.
        place(document.body, "probe-canvas");
        // And onto a card, which is the surface that shifted the old fill.
        const card = document.createElement("div");
        card.className = "card";
        document.body.append(card);
        place(card, "probe-card");
        const rect = (id: string) => {
          const box = document.getElementById(id)!.getBoundingClientRect();
          return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
        };
        return { canvas: rect("probe-canvas"), card: rect("probe-card") };
      });

      const onCanvas = await colourAt(page, spots.canvas.x, spots.canvas.y);
      const onCard = await colourAt(page, spots.card.x, spots.card.y);
      expect({ onCanvas, onCard }).toEqual({ onCanvas, onCard: onCanvas });
    } finally {
      await page.close();
    }
  });

  it("is painted over the sticky bar it grows up into, not under it", async () => {
    const page = await openReadoutUnderTheBar();
    try {
      const geometry = await page.evaluate(() => {
        const tip = document.querySelector(".hb-tooltip");
        const bar = document.querySelector(".shell-topbar");
        if (tip === null || bar === null) return null;
        const box = tip.getBoundingClientRect();
        return {
          x: box.left + box.width / 2,
          y: box.top + 6,
          // The whole test is void unless the readout genuinely reaches into
          // the bar's band, so that is asserted rather than assumed.
          overlapsTheBar: box.top < bar.getBoundingClientRect().bottom,
          fill: getComputedStyle(tip).backgroundColor,
        };
      });
      if (geometry === null) throw new Error("no readout on screen");
      expect(geometry.overlapsTheBar).toBe(true);

      /*
       * The pixel inside the bar's band is the readout's own fill.
       *
       * Compared against what the element computes rather than against a
       * literal colour, so a change to `--surface-float` — or a theme — is a
       * change this test follows instead of one it blocks. What it refuses is
       * the pixel being anything *else*, which is what "cut off at the top"
       * looks like to the compositor.
       */
      expect(await colourAt(page, geometry.x, geometry.y)).toBe(
        await resolveColour(page, geometry.fill),
      );
    } finally {
      await page.close();
    }
  });
});

describe("the phone navigation drawer", () => {
  /*
   * The same defect as the add drawer, on a viewport nobody screenshotted.
   *
   * It survived the first pass precisely because the reported bug arrived
   * with a desktop screenshot: `.shell-drawer` kept a layered `--surface`,
   * which composites whatever is behind it, so the page read through the
   * navigation. Found by a mutation surviving — the fix was already written
   * and nothing held it down, which is the same as not having made it.
   */
  it("paints an opaque surface at phone width", async () => {
    const page = await browser.newPage();
    await page.setViewport({
      width: 375,
      height: 800,
      deviceScaleFactor: 1,
      isMobile: true,
    });
    await page.goto(server.url + "/blank-for-storage", {
      waitUntil: "domcontentloaded",
    });
    await page.evaluate(() =>
      window.localStorage.setItem("subglance.theme", "dark"),
    );
    await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".shell-topbar", { timeout: 15_000 });

    try {
      /*
       * By accessible name only. A fallback such as
       * `.shell-topbar .shell-icon-btn` matches the first icon button in the
       * bar, which on this viewport is a different control — the test then
       * opens nothing and reports a colour it never measured.
       */
      await page.waitForSelector(NAV_BUTTON, { timeout: 15_000 });
      await page.click(NAV_BUTTON);
      await page.waitForSelector(".shell-drawer", { timeout: 15_000 });
      await page.evaluate(() =>
        Promise.all(
          document
            .getAnimations()
            .map((animation) => animation.finished.catch(() => undefined)),
        ),
      );

      const alpha = await page.evaluate(() => {
        const panel = document.querySelector(".shell-drawer");
        if (panel === null) return "no .shell-drawer in the document";
        const colour = getComputedStyle(panel).backgroundColor;
        /*
         * Chrome reports oklch() straight through from getComputedStyle, so
         * an rgb regex matches nothing and the check passes while measuring
         * nothing at all. Painting the colour and reading the pixel back
         * resolves any notation to concrete channels, alpha included.
         */
        const probe = document
          .createElement("canvas")
          .getContext("2d", { willReadFrequently: true });
        if (probe === null) return "no canvas 2d context";
        probe.clearRect(0, 0, 1, 1);
        probe.fillStyle = colour;
        probe.fillRect(0, 0, 1, 1);
        return probe.getImageData(0, 0, 1, 1).data[3] / 255;
      });
      expect(alpha).toBe(1);
    } finally {
      await page.close();
    }
  });
});
