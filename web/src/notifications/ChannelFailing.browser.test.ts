// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

/*
 * The dashboard's line for a channel that stopped delivering (SUB-212), in
 * real Chromium: legible in both themes, inside the viewport at phone width,
 * above the monitors whose alerts may have gone nowhere, and a link that
 * meets the target floor. The harness serves channels that are all healthy
 * for every other suite; this one intercepts the list to make one fail.
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

const failing = {
  id: 1,
  name: "platform-oncall-primary-escalation",
  type: "slack",
  config: { url: "****0f3a" },
  enabled: true,
  delivery: {
    state: "failed", window_days: 30, last_delivered_at: null,
    last_failed_at: "2026-10-05T03:00:00Z", failed: 3, pending: 0, retrying: 0,
    last_error: "endpoint rejected the alert (404)",
    failing_since: "2026-10-05T03:00:00Z", notice: "sent",
    notice_sent_at: "2026-10-05T03:01:00Z", notice_channel_id: 2,
  },
};
const carrier = { id: 2, name: "Ops mailing list", type: "email", config: { to: "ops@example.com" }, enabled: true };

it.each([
  ["dark", 375], ["light", 375], ["dark", 1440], ["light", 1440],
] as const)("names the failing channel above the monitors in %s at %ipx", async (theme, width) => {
  const page = await browser.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await page.setViewport({ width, height: 900 });
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (new URL(req.url()).pathname === "/api/v1/channels") {
        void req.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ channels: [failing, carrier] }) });
      } else {
        void req.continue();
      }
    });
    await page.goto(harness.url + "/", { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".nt-failing");
    await page.evaluate(() => document.fonts.ready);

    const layout = await page.evaluate(() => {
      const line = document.querySelector<HTMLElement>(".nt-failing")!;
      const link = line.querySelector<HTMLElement>("a")!;
      const first = document.querySelector<HTMLElement>(".mon-dashboard .mon-row, .mon-dashboard .mon-card, .mon-dashboard .mon-line");
      const box = line.getBoundingClientRect();
      return {
        scrolls: document.documentElement.scrollWidth > innerWidth,
        inside: box.left >= 0 && box.right <= innerWidth,
        above: first !== null && box.bottom <= first.getBoundingClientRect().top,
        role: line.getAttribute("role"),
        names: line.textContent?.includes("platform-oncall-primary-escalation") === true &&
          line.textContent.includes("Reported through Ops mailing list"),
        linkTall: link.getBoundingClientRect().height >= 24,
      };
    });
    expect(layout).toEqual({ scrolls: false, inside: true, above: true, role: "status", names: true, linkTall: true });

    await page.evaluate(axe.source);
    const audit = await page.evaluate(async () => {
      const engine = (window as unknown as { axe: typeof axe }).axe;
      return await engine.run({ include: [".nt-failing"] }, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } });
    });
    expect(audit.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.html) }))).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    await page.close();
  }
});
