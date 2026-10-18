import { afterAll, beforeAll, expect, it } from "vitest";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

/*
 * A new push URL from the monitor's page, in a real browser: More actions,
 * New push URL, the question, then the reveal with its Copy buttons. Escape
 * at the question sends nothing. The shared harness has no push monitor, so
 * this file answers the monitor list and the replace request itself.
 */

let browser: Browser;
let server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

const NEW_URL = "https://status.example.com/api/v1/push/sgu_Ej4k2NewTokenForTheBrowserTest";
const PUSH_MONITOR = {
  id: 1, name: "Nightly database backup", type: "push", target: "", interval_s: 60, timeout_s: 10,
  enabled: true, status: "up", push_interval_s: 86_400, push_grace_s: 3_600, push_token_prefix: "sgu_old123",
  last_check: new Date(Date.now() - 3_600_000).toISOString(), latency_ms: null, tags: {},
  created_at: new Date(Date.now() - 30 * 86_400_000).toISOString(),
};

it.each([["dark", 390], ["dark", 1440], ["light", 1440]] as const)(
  "replaces the push URL from the detail page: %s %ipx", async (theme, width) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    page.setDefaultTimeout(8_000);
    let replaced = 0;
    try {
      await page.setViewport({ width, height: 900 });
      await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
      await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
      await page.setRequestInterception(true);
      page.on("request", (request) => {
        const url = new URL(request.url());
        if (url.pathname === "/api/v1/monitors/1/push-token" && request.method() === "POST") {
          replaced += 1;
          void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ ...PUSH_MONITOR, push_url: NEW_URL }) });
        } else if (url.pathname === "/api/v1/monitors") {
          void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: [PUSH_MONITOR] }) });
        } else void request.continue();
      });
      await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector('button[aria-label="More actions"]', { visible: true });
      await page.evaluate(() => document.fonts.ready);

      const open = async () => {
        await page.click('button[aria-label="More actions"]');
        const items = await page.$$('[role="menu"] [role="menuitem"]');
        let found = false;
        for (const item of items) {
          if ((await item.evaluate((node) => node.textContent ?? "")).includes("New push URL")) { await item.click(); found = true; break; }
        }
        expect(found, "New push URL is in the menu").toBe(true);
        await page.waitForSelector('[role="dialog"]', { visible: true });
      };

      // Escape at the question changes nothing.
      await open();
      await page.keyboard.press("Escape");
      await page.waitForSelector('[role="dialog"]', { hidden: true });
      expect(replaced).toBe(0);

      await open();
      const question = await page.$eval('[role="dialog"]', (node) => node.textContent ?? "");
      expect(question).toContain("stops working immediately");
      if (process.env.SUBGLANCE_PROOF_DIR) {
        await mkdir(process.env.SUBGLANCE_PROOF_DIR, { recursive: true });
        await page.screenshot({ path: join(process.env.SUBGLANCE_PROOF_DIR, `push-url-question-${theme}-${width}.png`) });
      }
      const buttons = await page.$$('[role="dialog"] button');
      for (const button of buttons) {
        if ((await button.evaluate((node) => node.textContent ?? "")) === "Replace the URL") { await button.click(); break; }
      }
      const field = await page.waitForSelector("#push-reveal-url", { visible: true });
      expect(await field!.evaluate((node) => (node as HTMLInputElement).value)).toBe(NEW_URL);
      expect(replaced).toBe(1);
      const layout = await page.evaluate(() => {
        const panel = document.querySelector('[role="dialog"]')!.getBoundingClientRect();
        const copies = [...document.querySelectorAll('[role="dialog"] .copy-row button')].map((node) => node.getBoundingClientRect());
        return { panelRight: panel.right, width: innerWidth, copies: copies.map((r) => ({ right: r.right, height: r.height })),
          scrolls: document.documentElement.scrollWidth > innerWidth };
      });
      expect(layout.copies.length).toBe(3);
      for (const copy of layout.copies) expect(copy.right).toBeLessThanOrEqual(layout.panelRight);
      expect(layout.scrolls).toBe(false);
      if (process.env.SUBGLANCE_PROOF_DIR) {
        await page.screenshot({ path: join(process.env.SUBGLANCE_PROOF_DIR, `push-url-reveal-${theme}-${width}.png`) });
      }
    } finally { await context.close(); }
  },
);
