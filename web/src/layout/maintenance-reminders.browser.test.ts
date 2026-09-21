import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let browser: Browser;
let server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

// The HTTP fixture proves the built client/parser/summary seam. Real SQLite
// endpoint semantics and read failures are covered by incident_maintenance_*.
for (const theme of ["dark", "light"]) for (const width of [390, 1440]) {
  it(`shows active and pending maintenance without an eligibility time (${theme} ${width})`, async () => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      await page.setViewport({ width, height: 1000 });
      await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
      let status = "maintenance";
      await page.setRequestInterception(true);
      page.on("request", async (request) => {
        const url = new URL(request.url());
        if (url.origin === server.url && url.pathname === "/api/v1/monitors/1/incidents") {
          const data = await fetch(request.url()).then((response) => response.json());
          data.incidents[0] = { ...data.incidents[0], reminder_count: 2, reminder_status: status, next_reminder_at: status === "scheduled" ? "2026-09-20T12:37:17Z" : null };
          await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
          return;
        }
        await request.continue();
      });
      for (const [state, words] of [
        ["maintenance", "a maintenance window is active"],
        ["maintenance_pending", "the initial alert is pending after maintenance"],
      ]) {
        status = state;
        await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" });
        await page.waitForFunction((words) => document.querySelector(".inc-reminders")?.textContent?.includes(words), {}, words);
        const text = await page.$eval(".inc-reminders", (node) => node.textContent);
        expect(text).toContain("2 reminders issued.");
        expect(text).toContain(`Reminders suspended — ${words}.`);
        expect(text).not.toMatch(/Next reminder due|eligibility time|unavailable/);
        expect(await page.$(".inc-reminders time")).toBeNull();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        const cdp = await page.createCDPSession();
        const tree = await cdp.send("Accessibility.getFullAXTree");
        expect(tree.nodes.some((node) => node.name?.value?.includes(words))).toBe(true);
        await cdp.detach();
      }
      status = "scheduled";
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".inc-reminders time");
      expect(await page.$eval(".inc-reminders time", (node) => node.getAttribute("datetime"))).toBe("2026-09-20T12:37:17Z");
      expect(await page.$eval(".inc-reminders", (node) => node.textContent)).not.toMatch(/Reminders suspended/);
    } finally { await context.close(); }
  });
}
