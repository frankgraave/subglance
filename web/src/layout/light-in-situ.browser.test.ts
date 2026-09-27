/**
 * SUB-119: the light theme measured on the real screens, not only as tokens.
 *
 * DESIGN.md §2.2 says the status colours are darker in light mode "because the
 * dark originals are unreadable on white", and every token has a documented
 * value. What nothing measured is whether a whole screen holds: every mark
 * that carries status, on the surface it is actually drawn on, after the
 * translucent layers are composited. axe covers the text; it does not look at
 * a 20x7 lamp, an SVG heartbeat bar or a 3px rail, which are exactly the
 * marks this product is read by.
 *
 * So this walks seven screens in both themes and holds every status mark to
 * the WCAG 1.4.11 non-text floor of 3:1 against its own backdrop. Dark runs
 * too, because the question "does light hold" is only answerable next to the
 * theme the product opens in, and because the dark surfaces are the
 * translucent ones.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { BACKDROP, OVER_BACKDROP, LUMINANCE } from "./harness/contrast";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { THEME_STORAGE_KEY } from "../theme/theme";
import type { ApiMonitor } from "../monitors/types";

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

/*
 * The shared fixture has a down, an up and a paused monitor, but nothing
 * amber and nothing without a reading, so two of the five lamp states and
 * the warning heartbeat would go unmeasured. The fixture is widened here, on
 * the wire, instead of in the harness, because the layout suites that share
 * the harness pin row counts and names.
 */
function widen(monitors: ApiMonitor[]): ApiMonitor[] {
  const out = monitors.map((monitor): ApiMonitor => {
    if (monitor.name !== "cdn") return monitor;
    return {
      ...monitor,
      status: "warning",
      heartbeats: (monitor.heartbeats ?? []).map((beat, i) =>
        i % 6 === 5 ? { ...beat, ok: false, assessment: "warning" } : beat,
      ),
    };
  });
  // A push monitor nobody has wired up yet: the API calls it pending with no
  // last check, and the client renders it as "waiting" on the unlit lamp.
  const template = monitors[monitors.length - 1];
  out.push({
    ...template,
    id: 99,
    name: "queue-worker",
    type: "push",
    target: "",
    status: "pending",
    last_check: null,
    latency_ms: null,
    uptime_24h: null,
    heartbeats: [],
    push_interval_s: 300,
    push_grace_s: 60,
    push_token_prefix: "abcd",
  });
  return out;
}

type Screen = { name: string; path: string; layout: string; ready: string };

const SCREENS: Screen[] = [
  { name: "dashboard rows", path: "/", layout: "rows", ready: "[data-testid^='monitor-row-']" },
  { name: "dashboard cards", path: "/", layout: "cards", ready: "[data-testid^='monitor-card-']" },
  { name: "dashboard compact", path: "/", layout: "compact", ready: "[data-testid^='monitor-line-']" },
  { name: "status wall", path: "/", layout: "wall", ready: ".wall-card" },
  { name: "monitor detail", path: "/monitors/1", layout: "rows", ready: ".mon-detail-windows" },
  { name: "monitors", path: "/monitors", layout: "rows", ready: ".inv-list > li" },
  { name: "incidents", path: "/incidents", layout: "rows", ready: ".inc-line" },
];

async function open(screen: Screen, theme: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.evaluateOnNewDocument((layoutKey, layout, themeKey, chosen) => {
    localStorage.setItem(layoutKey, layout);
    localStorage.setItem(themeKey, chosen);
  }, LAYOUT_STORAGE_KEY, screen.layout, THEME_STORAGE_KEY, theme);
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      void request.abort("blockedbyclient");
    } else if (url.pathname === "/api/v1/monitors") {
      const response = await fetch(request.url());
      const body = (await response.json()) as { monitors: ApiMonitor[] };
      await request.respond({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ monitors: widen(body.monitors) }),
      });
    } else {
      void request.continue();
    }
  });
  await page.goto(server.url + screen.path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(screen.ready, { visible: true, timeout: 15_000 });
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations()
      .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map((animation) => animation.finished.catch(() => undefined)));
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  });
  return page;
}

const MEASURE = `(() => {
  const luminance = ${LUMINANCE};
  const backdrop = ${BACKDROP};
  const over = ${OVER_BACKDROP};
  const ratio = (a, b) => {
    const x = luminance(a);
    const y = luminance(b);
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  };
  const worst = {};
  const note = (key, value) => {
    const r = Math.round(value * 100) / 100;
    if (!(key in worst) || r < worst[key]) worst[key] = r;
  };
  const drawn = (el) => { const r = el.getBoundingClientRect(); return r.width >= 1 && r.height >= 1; };
  for (const el of document.querySelectorAll(".led")) {
    if (!drawn(el)) continue;
    const s = getComputedStyle(el);
    const state = el.getAttribute("data-state");
    if (state === "off") {
      const m = s.boxShadow.match(/^((?:rgba?|oklch|color)[(][^)]*[)])/);
      const ring = m && over(el, m[1]);
      if (ring) note("lamp off ring", ratio(ring, backdrop(el)));
      continue;
    }
    const fill = over(el, s.backgroundColor);
    if (fill) note("lamp " + state, ratio(fill, backdrop(el)));
  }
  for (const el of document.querySelectorAll(".hb-bar")) {
    const fill = over(el, getComputedStyle(el).fill);
    if (fill) note("bar " + el.getAttribute("data-status"), ratio(fill, backdrop(el)));
  }
  for (const el of document.querySelectorAll("[data-status]")) {
    for (const edge of [el, el.firstElementChild]) {
      if (!edge || edge.tagName === "rect") continue;
      const s = getComputedStyle(edge);
      if (parseFloat(s.borderLeftWidth) < 2) continue;
      const colour = over(edge, s.borderLeftColor);
      if (!colour) continue;
      note("rail " + el.getAttribute("data-status") + " " + s.borderLeftStyle, ratio(colour, backdrop(edge)));
    }
  }
  return { theme: document.documentElement.getAttribute("data-theme"), worst };
})()`;

type Measured = { theme: string | null; worst: Record<string, number> };

/*
 * Every mark that says something about a monitor's state. The key is the
 * measurement name MEASURE produces: kind, status, and for rails the border
 * style, because the paused rail is told apart by being dotted.
 *
 * The neutral rail on a healthy row ("rail up solid", 1.2:1) is left out on
 * purpose: it is the row's resting edge, not a signal, and the lamp beside it
 * carries the state. The empty heartbeat stub is left out for the same
 * reason; it says "no data here" by being almost nothing.
 */
const STATUS_MARKS = [
  "lamp up", "lamp warn", "lamp down", "lamp off ring",
  "bar up", "bar warning", "bar down",
  "rail down solid", "rail warning solid", "rail paused dotted",
];

/*
 * Known shortfall, pinned rather than hidden. The unlit lamp and the waiting
 * rail are both `--idle`, which measures 1.6-2.1:1 in both themes: the grey is
 * chosen to read as "not lit", and at 3:1 it would read as a fourth status
 * colour. Whether that trade is right is an open question (SUB-159), so
 * the value is held *below* the floor here: the day a change
 * lifts it past 3:1, this fails and the key moves into STATUS_MARKS instead of
 * the improvement going unrecorded.
 */
const BELOW_FLOOR = ["lamp idle", "rail waiting solid"];

async function measureAll(theme: string): Promise<Record<string, Measured>> {
  const out: Record<string, Measured> = {};
  for (const screen of SCREENS) {
    const page = await open(screen, theme);
    try {
      out[screen.name] = (await page.evaluate(MEASURE)) as Measured;
    } finally {
      await page.close();
    }
  }
  return out;
}

describe.each(["light", "dark"])("status marks in situ, %s theme", (theme) => {
  let measured: Record<string, Measured>;

  beforeAll(async () => {
    measured = await measureAll(theme);
  }, 180_000);

  it("reaches the theme it asked for on every screen", () => {
    for (const [name, result] of Object.entries(measured)) {
      expect(result.theme, name).toBe(theme);
    }
  });

  it("finds every status mark somewhere, so no floor below is vacuous", () => {
    const seen = new Set(Object.values(measured).flatMap((result) => Object.keys(result.worst)));
    for (const key of [...STATUS_MARKS, ...BELOW_FLOOR]) {
      expect(seen.has(key), `${key} was never drawn`).toBe(true);
    }
  });

  it("holds every status mark to 3:1 against its own backdrop (WCAG 1.4.11)", () => {
    const failures: string[] = [];
    for (const [name, result] of Object.entries(measured)) {
      for (const key of STATUS_MARKS) {
        const ratio = result.worst[key];
        if (ratio !== undefined && ratio < 3) failures.push(`${name}: ${key} ${ratio}:1`);
      }
    }
    expect(failures).toEqual([]);
  });

  it("still draws the unlit state below the floor, until that is decided", () => {
    const lifted: string[] = [];
    for (const [name, result] of Object.entries(measured)) {
      for (const key of BELOW_FLOOR) {
        const ratio = result.worst[key];
        if (ratio !== undefined && ratio >= 3) lifted.push(`${name}: ${key} ${ratio}:1`);
      }
    }
    expect(lifted, "the unlit state now clears 3:1: move it into STATUS_MARKS").toEqual([]);
  });
});
