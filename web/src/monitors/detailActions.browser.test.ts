import { afterAll, beforeAll, expect, it } from "vitest";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let browser: Browser;
let server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

it.each([["dark", 375], ["light", 375], ["dark", 1440], ["light", 1440]] as const)(
  "keeps the detail title clear of both actions: %s %ipx", async (theme, width) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    page.setDefaultTimeout(5_000);
    try {
      await page.setViewport({ width, height: 1000 });
      await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
      await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
      await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" });
      const edit = await page.waitForSelector('button[aria-label="Edit monitor"]');
      await page.evaluate(() => document.fonts.ready);
      const bounds = await page.evaluate(() => {
        const button = document.querySelector('button[aria-label="Edit monitor"]')!;
        const header = button.closest(".card-head")!;
        const title = header.querySelector(".card-title")!.getBoundingClientRect();
        return [...header.querySelectorAll("button")].map((node) => {
          const r = node.getBoundingClientRect();
          return {
            label: node.textContent?.trim(),
            overlapsTitle: r.left < title.right && r.right > title.left && r.top < title.bottom && r.bottom > title.top,
            insideViewport: r.left >= 0 && r.right <= innerWidth,
          };
        });
      });
      expect(bounds.map((b) => b.label)).toEqual(["Edit monitor", "Check now"]);
      for (const bound of bounds) {
        expect(bound.overlapsTitle, `${bound.label} overlaps the card title`).toBe(false);
        expect(bound.insideViewport).toBe(true);
      }
      if (process.env.SUBGLANCE_PROOF_DIR) {
        await mkdir(process.env.SUBGLANCE_PROOF_DIR, { recursive: true });
        await page.screenshot({ path: join(process.env.SUBGLANCE_PROOF_DIR, `detail-actions-${theme}-${width}.png`), fullPage: true });
      }
      await edit!.click();
      await page.waitForSelector('[role="dialog"] input[name="name"]');
      await page.keyboard.press("Escape");
      await page.waitForSelector('[role="dialog"]', { hidden: true });
    } finally { await context.close(); }
  },
);
