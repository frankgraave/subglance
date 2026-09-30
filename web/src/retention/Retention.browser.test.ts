/**
 * The retention card's maintenance half, measured in a real browser (SUB-162).
 *
 * jsdom proves what the card says; this proves what the reader gets: axe in
 * both themes, no sideways scroll on a phone, and 24px targets, in the
 * states the harness fixture never reaches: a pass the size limit shortened
 * and could not bring under, "Run now" asking for its count, a file that
 * compacting would help on a disk too small for the copy, and a pass running.
 *
 * The settings are answered by request interception, because the harness
 * server serves one fixed retention read.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { defaultRetention } from "./fixtures";
import type { Retention } from "./api";

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

const GB = 1_000_000_000;
const MiB = 1_048_576;

// A small disk with a limit that bit: the fuller the card, the more there is to measure.
const pressed: Retention = {
  ...defaultRetention,
  max_database_size: { ...defaultRetention.max_database_size, bytes: 2 * GB, source: "database" },
  last_pass: { ...defaultRetention.last_pass!, size_cap: {
    limit_bytes: 2 * GB, before_bytes: 2.4 * GB, after_bytes: 2.1 * GB, heartbeats: 900_000, hourly_buckets: 1_200,
    raw_since: "2026-09-18T03:30:00Z", hourly_since: "2025-01-01T00:00:00Z", at_floor: true } },
  compact: { size_bytes: 2600 * MiB, free_bytes: 700 * MiB, auto_vacuum: "none", recommended: true, estimate_seconds: 140,
    disk_shortfall: { need_bytes: 5200 * MiB, free_bytes: 900 * MiB }, running: false,
    last: { finished_at: "2026-09-29T10:00:00Z", duration_ms: 0, before_bytes: 1, after_bytes: 1, shrink_pending: false, error: "database or disk is full" } },
};

type Step = "limited" | "asking" | "running";

const settle = (page: Page) => page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations().map((animation) => animation.finished.catch(() => undefined)));
  await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
});

async function open(width: number, theme: string, step: Step): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1, isMobile: width < 640 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.setRequestInterception(true);
    const state = step === "running" ? { ...pressed, running: true, compact: { ...pressed.compact!, running: true } } : pressed;
    page.on("request", (req) => {
      const path = new URL(req.url()).pathname;
      const body = path === "/api/v1/settings/retention" ? state
        : path === "/api/v1/settings/retention/preview" ? { heartbeats: 1234, hourly_buckets: 5, incidents: 0 } : null;
      if (!body) { void req.continue(); return; }
      void req.respond({ status: 200, contentType: "application/json", headers: { etag: 'W/"4"' }, body: JSON.stringify(body) });
    });
    await page.goto(server.url + "/settings#retention", { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => document.querySelector("#retention")?.textContent?.includes("Shortened to"), { timeout: 15_000 });
    if (step === "asking") {
      const [run] = await page.$$("xpath/.//button[normalize-space()='Run now…']");
      await run.click();
      await page.waitForFunction(() => document.querySelector("#retention")?.textContent?.includes("A pass now would fold"), { timeout: 10_000 });
    }
    await settle(page);
  } catch (err) {
    await page.close();
    throw err;
  }
  return page;
}

async function audit(page: Page) {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async () => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include: [["#retention"]] }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target.join(" ")) }));
  });
}

const steps: Step[] = ["limited", "asking", "running"];

describe.each(["light", "dark"])("%s theme", (theme) => {
  it.each(steps)("passes axe at step: %s", async (step) => {
    const page = await open(1440, theme, step);
    try {
      expect(await audit(page)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});

describe.each([320, 375])("at %ipx", (width) => {
  it.each(steps)("fits the phone at step: %s", async (step) => {
    const page = await open(width, "dark", step);
    try {
      const seen = await page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        const root = document.querySelector<HTMLElement>("#retention")!;
        const outside = [...root.querySelectorAll<HTMLElement>("*")]
          .filter((el) => { const r = el.getBoundingClientRect(); return (r.width > 0 || r.height > 0) && (r.left < -1 || r.right > vw + 1); })
          .map((el) => `${el.tagName.toLowerCase()}.${el.className}`);
        const small = [...root.querySelectorAll<HTMLElement>("button, a[href], input, select, textarea")].filter((el) => {
          if (el.hasAttribute("disabled") || el.closest("fieldset:disabled")) return false;
          const r = el.getBoundingClientRect();
          return (r.width > 0 || r.height > 0) && (r.width < 24 || r.height < 24);
        }).map((el) => (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40));
        return { scrollWidth: document.documentElement.scrollWidth, clientWidth: vw, outside, small };
      });
      expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth, outside: [], small: [] });
    } finally {
      await page.close();
    }
  });
});
