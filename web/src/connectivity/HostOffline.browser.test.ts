// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { offlineConnectivity } from "./fixtures";

/*
 * The host-offline line in real Chromium (SUB-151): legible in both themes,
 * no horizontal scroll at phone width, and above the monitors it explains.
 * The harness answers "online" for every other suite; this one intercepts the
 * one request that says otherwise.
 */

let browser: Browser;
let harness: Server;

beforeAll(async () => {
  harness = await serveBuild();
  browser = await chromium();
}, 60_000);

afterAll(async () => {
  await browser?.close();
  await harness?.close();
});

const SENTENCE = "No outbound connection since";

it.each([
  ["dark", 375], ["light", 375], ["dark", 1440], ["light", 1440],
] as const)("reads as one line of explanation in %s at %ipx", async (theme, width) => {
  const page = await browser.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await page.setViewport({ width, height: 900 });
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (new URL(req.url()).pathname === "/api/v1/connectivity") {
        void req.respond({ status: 200, contentType: "application/json", body: JSON.stringify(offlineConnectivity) });
      } else {
        void req.continue();
      }
    });
    await page.goto(harness.url + "/", { waitUntil: "domcontentloaded" });
    await page.waitForFunction((text) => document.querySelector('[data-state="host-offline"]')?.textContent?.includes(text), {}, SENTENCE);
    await page.evaluate(() => document.fonts.ready);

    const layout = await page.evaluate(() => {
      const line = document.querySelector<HTMLElement>('[data-state="host-offline"]')!;
      // The first monitor on screen, whichever layout the viewport chose.
      const first = document.querySelector<HTMLElement>(".mon-dashboard .mon-row, .mon-dashboard .mon-card, .mon-dashboard .mon-line");
      const box = line.getBoundingClientRect();
      return {
        scrolls: document.documentElement.scrollWidth > innerWidth,
        inside: box.left >= 0 && box.right <= innerWidth,
        above: first !== null && box.bottom <= first.getBoundingClientRect().top,
        role: line.getAttribute("role"),
        // Up for as long as the host is offline, so the dot must not breathe.
        dotAnimation: getComputedStyle(line.querySelector(".conn-badge-dot")!).animationName,
      };
    });
    expect(layout).toEqual({ scrolls: false, inside: true, above: true, role: "status", dotAnimation: "none" });

    await page.evaluate(axe.source);
    const audit = await page.evaluate(async () => {
      const engine = (window as unknown as { axe: typeof axe }).axe;
      return await engine.run({ include: ['[data-state="host-offline"]'] }, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } });
    });
    expect(audit.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.html) }))).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    await page.close();
  }
});

it("rides the status wall's header line instead of a banner", async () => {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width: 1440, height: 900 });
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), LAYOUT_STORAGE_KEY, "wall");
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (new URL(req.url()).pathname === "/api/v1/connectivity") {
        void req.respond({ status: 200, contentType: "application/json", body: JSON.stringify(offlineConnectivity) });
      } else {
        void req.continue();
      }
    });
    await page.goto(harness.url + "/", { waitUntil: "domcontentloaded" });
    await page.waitForFunction((text) => document.querySelector(".wall-meta")?.textContent?.includes(text), {}, SENTENCE);
    expect(await page.$('[data-state="host-offline"]')).toBeNull();
  } finally {
    await page.close();
  }
});
