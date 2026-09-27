/**
 * SUB-119: the motion ladder, measured in the browser rather than read from
 * the source.
 *
 * `tokens.test.ts` proves no stylesheet spells out a duration. It cannot see
 * what the page actually computes: a Tailwind utility, an inline style or a
 * library default would all arrive without passing through a stylesheet it
 * reads. This test opens the six screens an operator uses and reads every
 * element's computed timings, so the claim in DESIGN.md §2.6 — two durations
 * at rest, one curve, nothing moving by itself — is a measurement.
 *
 * The withdrawal rung (600ms) is deliberately absent at rest: it only applies
 * once the live stream drops, which the harness does not do.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

/**
 * `rests` names the timings a screen must compute, as `element duration`.
 * Without it the ladder check below passes on a page that computes nothing
 * at all: every screen carries the hover rung, and the dashboard's lamp is
 * the one element DESIGN.md §2.6 ties to the attention rung.
 */
const SCREENS = [
  { name: "dashboard", path: "/", ready: ".led", rests: ["* 0.15s", "led 0.42s"] },
  { name: "monitor detail", path: "/monitors/1", ready: ".mon-detail-windows", rests: ["* 0.15s"] },
  { name: "monitors", path: "/monitors", ready: ".inv-list > li", rests: ["* 0.15s"] },
  { name: "incidents", path: "/incidents", ready: ".inc-line", rests: ["* 0.15s"] },
  { name: "notifications", path: "/notifications", ready: ".inv-row", rests: ["* 0.15s"] },
  { name: "settings", path: "/settings", ready: 'input[name="current_password"]', rests: ["* 0.15s"] },
];

/** The rungs a resting screen may compute, in seconds: hover/panel and attention. */
const AT_REST = ["0.15s", "0.42s"];
const EASE = "cubic-bezier(0.4, 0, 0.2, 1)";

type Timing = { el: string; kind: string; duration: string; curve: string; name: string };

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

async function open(path: string, ready: string, reduce: boolean): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  if (reduce) await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { timeout: 15_000 });
  return page;
}

/**
 * Every non-zero transition and animation timing on the page, one entry per
 * comma-separated part, labelled with the element's first class so a failure
 * names where to look.
 */
function timings(page: Page): Promise<Timing[]> {
  return page.evaluate(() => {
    const out: Timing[] = [];
    for (const el of [document.documentElement, ...document.querySelectorAll("*")]) {
      const cs = getComputedStyle(el);
      const label =
        typeof el.className === "string" && el.className ? el.className.split(" ")[0] : el.tagName.toLowerCase();
      const read = (kind: string, durations: string, curves: string, names: string) => {
        const d = durations.split(", ");
        const c = curves.split(/,\s*(?![^()]*\))/);
        const n = names.split(", ");
        d.forEach((duration, i) => {
          if (parseFloat(duration) === 0) return;
          out.push({ el: label, kind, duration, curve: c[i % c.length], name: n[i % n.length] });
        });
      };
      read("transition", cs.transitionDuration, cs.transitionTimingFunction, cs.transitionProperty);
      if (cs.animationName !== "none") {
        read("animation", cs.animationDuration, cs.animationTimingFunction, cs.animationName);
      }
    }
    return out;
  });
}

describe.each(SCREENS)("$name", (screen) => {
  it("moves only on the ladder's resting rungs, on the one curve", async () => {
    const page = await open(screen.path, screen.ready, false);
    try {
      const seen = await timings(page);
      // Printed in full on failure: which element, which property, what value.
      expect(seen.filter((t) => !AT_REST.includes(t.duration) || t.curve !== EASE)).toEqual([]);
      // And the rungs are really there: an empty page must not pass.
      const missing = screen.rests.filter((want) => {
        const [el, duration] = want.split(" ");
        return !seen.some((t) => (el === "*" || t.el === el) && t.duration === duration);
      });
      expect(missing).toEqual([]);
    } finally {
      await page.close();
    }
  });

  it("has nothing animating by itself at rest", async () => {
    const page = await open(screen.path, screen.ready, false);
    try {
      const running = await page.evaluate(() =>
        document
          .getAnimations()
          .filter((a) => a.playState === "running" && a.effect?.getTiming().iterations === Infinity)
          .map((a) => (a as CSSAnimation).animationName ?? "unnamed"),
      );
      expect(running).toEqual([]);
    } finally {
      await page.close();
    }
  });

  it("collapses every duration under prefers-reduced-motion", async () => {
    const page = await open(screen.path, screen.ready, true);
    try {
      const seen = await timings(page);
      expect(seen.filter((t) => parseFloat(t.duration) > 0.001)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});
