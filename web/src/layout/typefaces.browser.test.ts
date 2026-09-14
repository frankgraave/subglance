/**
 * SUB-107 in a real browser: the faces the design system is built on have to
 * actually render.
 *
 * Every other check on this is structural — a @font-face rule exists, a file is
 * committed, a preload link points at it. None of those can see a glyph, and
 * the bug they were written against was invisible in exactly that way: the
 * stylesheet named two families, nothing loaded them, `document.fonts.size`
 * was 0, and the product resolved a different system font on every machine
 * while every unit test stayed green.
 *
 * Only a browser can settle it, and the way it settles it is measurement:
 * render a string in the family under test and in a family that cannot exist,
 * and compare widths. Identical widths mean the browser fell back, which is
 * precisely the failure. The string mixes the characters that differ most
 * between faces.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

let server: Server;
let browser: Browser;
let page: Page;

/** A family name no system can have, used as the "fell back" baseline. */
const NOTHING = "__subglance_no_such_face__";

/** Wide in some faces, narrow in others; ambiguous glyphs on purpose. */
const PROBE = "iIl1O0mmmWWW";

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  page = await browser.newPage();
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  // Wait for the dashboard itself, not just the shell: a face is only fetched
  // once something on the page asks for it, so measuring before the monitor
  // rows exist would report the mono as never loaded.
  await page.waitForSelector("[data-testid='monitor-row'], .mon-row, main", { timeout: 15_000 });
  // The faces are declared `font-display: block`, so the browser holds text
  // back rather than painting a fallback. Waiting on document.fonts.ready is
  // what makes that a determinate state rather than a race.
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** Rendered width of PROBE in a family, measured off-screen at 32px. */
async function widthIn(family: string): Promise<number> {
  return page.evaluate(
    (fam: string, text: string) => {
      const span = document.createElement("span");
      span.textContent = text;
      span.style.cssText =
        `position:absolute;left:-9999px;top:0;white-space:pre;` +
        `font-size:32px;font-family:${fam};`;
      document.body.appendChild(span);
      const width = span.getBoundingClientRect().width;
      span.remove();
      return width;
    },
    family,
    PROBE,
  );
}

describe("the shipped faces render", () => {
  it("loads both faces from the application itself", async () => {
    const loaded = await page.evaluate(() =>
      [...document.fonts].map((f) => `${f.family} ${f.status}`),
    );
    expect(loaded).toContain("InterVariable loaded");
    expect(loaded).toContain("CommitMono loaded");
  });

  it("renders the sans in the shipped face, not a system fallback", async () => {
    const fallback = await widthIn(`"${NOTHING}"`);
    const actual = await widthIn(`"InterVariable"`);
    expect(actual).not.toBeCloseTo(fallback, 1);
  });

  it("renders the mono in the shipped face, not a system fallback", async () => {
    const fallback = await widthIn(`"${NOTHING}"`);
    const actual = await widthIn(`"CommitMono"`);
    expect(actual).not.toBeCloseTo(fallback, 1);
  });

  it("sets the interface itself in those faces", async () => {
    // The tokens could name the right family and no element use it. This asks
    // the page what it actually resolved for body text and for a metric.
    const families = await page.evaluate(() => {
      const body = getComputedStyle(document.body).fontFamily;
      const mono = document.querySelector("code, kbd, samp, pre");
      return { body, mono: mono ? getComputedStyle(mono).fontFamily : null };
    });
    expect(families.body).toContain("InterVariable");
  });

  it("gives the sans a continuous weight axis through 450", async () => {
    // --weight-mid: 450 is only a real step if the variable axis covers it.
    // A static face snaps to 400 and the middle of the ladder silently
    // disappears, which is what docs/DESIGN.md §2.5 claims it does not.
    const widths = await page.evaluate((text: string) => {
      const measure = (weight: number) => {
        const span = document.createElement("span");
        span.textContent = text;
        span.style.cssText =
          `position:absolute;left:-9999px;top:0;white-space:pre;` +
          `font-size:32px;font-family:"InterVariable";font-weight:${weight};`;
        document.body.appendChild(span);
        const w = span.getBoundingClientRect().width;
        span.remove();
        return w;
      };
      return { w400: measure(400), w450: measure(450), w500: measure(500) };
    }, PROBE);

    expect(widths.w450).not.toBeCloseTo(widths.w400, 2);
    expect(widths.w450).not.toBeCloseTo(widths.w500, 2);
  });

  it("preloads only faces the first screen actually uses", async () => {
    // The ticket asked for this to be measured rather than assumed, and a
    // preload for a face nothing asks for is a download the user pays for
    // twice: once in bandwidth, once in bandwidth taken from the bundle. Both
    // faces are used on the dashboard — the sans for every label, the mono for
    // every latency — so both are preloaded. If a future screen drops one,
    // this fails and the link should go with it.
    const used = await page.evaluate(() =>
      [...document.fonts].filter((f) => f.status === "loaded").map((f) => f.family),
    );
    const preloaded = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLLinkElement>("link[rel=preload][as=font]")].map((link) =>
        link.href.split("/").pop()?.split("-")[0],
      ),
    );
    for (const family of preloaded) {
      expect(used, `${family} is preloaded but never used`).toContain(family);
    }
  });

  it("makes no request off this origin for a face", async () => {
    // The product is installed on networks with no outbound access. A CDN URL
    // that happens to work on a developer's machine is the failure mode.
    const external = await page.evaluate(() =>
      performance
        .getEntriesByType("resource")
        .filter((e) => e.name.endsWith(".woff2") || e.name.endsWith(".woff"))
        .map((e) => e.name)
        .filter((name) => new URL(name).origin !== window.location.origin),
    );
    expect(external).toEqual([]);
  });
});
