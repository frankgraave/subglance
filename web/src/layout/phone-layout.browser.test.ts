/**
 * Layout checks that jsdom cannot perform.
 *
 * The rest of the frontend suite runs in jsdom, which has no layout engine:
 * every element reports zero width, `getBoundingClientRect` returns zeros and
 * media queries never match. An entire class of bug — anything about *size* —
 * is invisible to it by construction. SUB-29's detail view shipped drawing a
 * 720px heartbeat bar inside a 317px panel while 404 passing tests said
 * nothing, because the only width a jsdom test can observe is the one it was
 * handed.
 *
 * The assertions here are deliberately numeric and few. Screenshot diffing was
 * rejected: font rendering differs per platform, so every legitimate design
 * change becomes a pile of blessed images. A horizontal overflow is a number
 * that is either larger than the viewport or is not, and it never needs
 * blessing.
 *
 * These do not run with `npm test`. They need a built bundle and a browser, so
 * they live behind `npm run test:browser`.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { serveBuild, type Server } from "./harness/server";

/*
 * 320 is the narrowest phone still in use (iPhone SE 1st generation), 375 the
 * most common, 414 the widest that still gets the phone layout. Where the
 * layout switches over is a unit test's business; these are the widths where
 * it must hold.
 */
const WIDTHS = [320, 375, 414];

/*
 * What a phone can actually be looking at.
 *
 * `effectiveLayout` collapses rows and compact into cards below 640px, so
 * asking for those three would test the same screen three times. The wall
 * survives narrow viewports. The preference is still seeded per screen,
 * because the point is what a user with that preference sees on a phone.
 *
 * The monitor detail view is deliberately absent, and the reason matters: at
 * the time of writing it fails all three checks on `develop` (a 717px
 * heartbeat SVG pushes a 375px page out to 746px). That is the bug this file
 * was written for, and it is already fixed on the SUB-29 branch. Adding the
 * screen here would make this PR red for a defect it does not own and cannot
 * fix without merging unrelated work. It is added by the SUB-29 PR itself,
 * where the assertion turns red without the fix and green with it — which is
 * the only way to prove these checks bite.
 */
const SCREENS = [
  { name: "dashboard (cards)", layout: "cards", path: "/", ready: "[data-testid^='monitor-card-']" },
  { name: "dashboard (rows preference)", layout: "rows", path: "/", ready: "[data-testid^='monitor-card-']" },
  { name: "status wall", layout: "wall", path: "/", ready: ".wall-card" },
];

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

/**
 * Opens one screen at one width, with the layout preference seeded before the
 * app boots. The preference lives in localStorage, so it has to be written
 * against the right origin and before any script runs — hence the blank
 * navigation first.
 */
async function open(width: number, screen: (typeof SCREENS)[number]): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 800, deviceScaleFactor: 1, isMobile: true });
  await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    LAYOUT_STORAGE_KEY,
    screen.layout,
  );
  await page.goto(server.url + screen.path, { waitUntil: "domcontentloaded" });
  /*
   * `domcontentloaded` rather than `networkidle2`: the dashboard holds an SSE
   * connection open for as long as the page lives, so a wait for network
   * silence never returns. The selector below is the real readiness signal
   * anyway — it proves the screen rendered, which an idle network does not.
   */
  await page.waitForSelector(screen.ready, { timeout: 15_000 });
  /*
   * One animation frame after the content appears, so the measurement sees
   * settled layout rather than a frame mid-paint.
   */
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
  );
  return page;
}

describe.each(WIDTHS)("at %ipx", (width) => {
  describe.each(SCREENS)("$name", (screen) => {
    it("does not scroll sideways", async () => {
      const page = await open(width, screen);
      try {
        const seen = await page.evaluate(() => ({
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
        }));
        /*
         * Compared as an object so a failure prints both numbers: "746 vs 375"
         * says immediately whether something is a little too wide or is
         * drawing at desktop size, which a bare boolean would not.
         */
        expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth });
      } finally {
        await page.close();
      }
    });

    it("keeps every visible element inside the viewport", async () => {
      const page = await open(width, screen);
      try {
        const offenders = await page.evaluate(() => {
          const vw = document.documentElement.clientWidth;
          const out: { tag: string; cls: string; left: number; right: number }[] = [];

          /*
           * An element wider than the viewport is only a bug if the user can
           * reach it. Two cases where they cannot, and both occur here:
           *
           *   - Content inside something that clips or scrolls its overflow.
           *     The heartbeat bar's accessibility table is 1370px wide inside
           *     a clipped container; it is read by screen readers and never
           *     painted, so counting it would make this test fail on every
           *     screen for a thing that is working as designed.
           *   - Anything visually hidden, by the same argument.
           *
           * `document.documentElement.scrollWidth` in the test above is what
           * catches real page-level overflow. This test localises it: it names
           * the element, which a scrollWidth number cannot.
           */
          const isClippedBy = (el: Element): boolean => {
            for (let p = el.parentElement; p; p = p.parentElement) {
              const s = window.getComputedStyle(p);
              if (s.overflowX !== "visible" || s.overflowY !== "visible") return true;
              if (s.clipPath !== "none" || s.visibility === "hidden") return true;
            }
            return false;
          };

          for (const el of Array.from(document.body.querySelectorAll<HTMLElement>("*"))) {
            const r = el.getBoundingClientRect();
            if (r.width === 0 && r.height === 0) continue; // not rendered
            // One pixel of slack for sub-pixel rounding. Both edges matter: a
            // negative `left` hangs off the left side without ever growing
            // `scrollWidth`, so the page-level test above cannot see it.
            if (r.left >= -1 && r.right <= vw + 1) continue;
            if (isClippedBy(el)) continue;
            out.push({
              tag: el.tagName.toLowerCase(),
              cls: typeof el.className === "string" ? el.className.slice(0, 50) : "",
              left: Math.round(r.left),
              right: Math.round(r.right),
            });
          }
          return out;
        });
        expect(offenders).toEqual([]);
      } finally {
        await page.close();
      }
    });

    /*
     * WCAG 2.2 SC 2.5.8 (Target Size, Minimum). Unmeasurable in jsdom, and
     * this is a product used one-handed on a phone just after an alert fired.
     */
    it("gives every visible control a 24x24 target", async () => {
      const page = await open(width, screen);
      try {
        const tooSmall = await page.evaluate(() => {
          const sel = "button, a[href], input, select, [role='button'], [role='tab']";
          const out: { tag: string; label: string; w: number; h: number }[] = [];
          for (const el of Array.from(document.body.querySelectorAll<HTMLElement>(sel))) {
            if (el.hasAttribute("disabled")) continue;
            const style = window.getComputedStyle(el);
            if (style.visibility === "hidden" || style.display === "none") continue;

            /*
             * Visually-hidden inputs are exempt, and deliberately so. The
             * theme control is a fieldset of `sr-only` radios whose visible
             * target is the <label> wrapping each one — the pattern that keeps
             * a native radio's keyboard and screen-reader behaviour instead of
             * reimplementing it on a <div>. The 1x1 box is the input's
             * clipping rectangle, not the thing a finger lands on.
             *
             * All three conditions are required: label ancestry on its own
             * would also wave through a *visible* checkbox or radio that is
             * genuinely too small to hit.
             */
            if (
              el instanceof HTMLInputElement &&
              el.classList.contains("sr-only") &&
              el.closest("label") !== null
            ) {
              continue;
            }

            const r = el.getBoundingClientRect();
            if (r.width === 0 && r.height === 0) continue;
            if (r.width < 24 || r.height < 24) {
              out.push({
                tag: el.tagName.toLowerCase(),
                label: (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 40),
                w: Math.round(r.width),
                h: Math.round(r.height),
              });
            }
          }
          return out;
        });
        expect(tooSmall).toEqual([]);
      } finally {
        await page.close();
      }
    });
  });
});
