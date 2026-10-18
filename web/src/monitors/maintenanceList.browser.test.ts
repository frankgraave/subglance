// @vitest-environment node
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

/*
 * The maintenance list with every state on screen at once: an active window,
 * an upcoming one, a weekly one and two that have ended, then the question
 * that removes the ended ones. The schedule is stubbed so the states do not
 * depend on the hour the suite runs at; maintenance.browser.test.ts drives
 * the real API and checker.
 *
 * A reader in Amsterdam, so a one-off window reads on a clock that is not
 * UTC: 18:00Z is written 20:00 in October.
 */

let server: Server;
let browser: Browser;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); }, 120_000);
afterAll(async () => { await browser?.close(); await server?.close(); });

const hour = 3_600_000;
function schedule(now: number) {
  const at = (ms: number) => new Date(ms).toISOString();
  return [
    { id: 1, name: "Database migration", monitor_id: 1, starts_at: at(now - hour), ends_at: at(now + hour), active: true },
    { id: 2, name: "Release 4.2 deploy", tag_key: "env", tag_value: "prod", starts_at: "2099-10-20T18:00:00Z", ends_at: "2099-10-20T19:00:00Z", active: false },
    { id: 3, name: "Patch night", tag_key: "env", tag_value: "prod", timezone: "Europe/Amsterdam", weekdays: [0, 3], local_time: "02:00", duration_minutes: 90, active: false },
    { id: 4, name: "Last week's deploy", monitor_id: 1, starts_at: "2026-10-01T18:00:00Z", ends_at: "2026-10-01T19:00:00Z", active: false },
    { id: 5, name: "Data centre move", monitor_id: 1, starts_at: "2026-09-01T18:00:00Z", ends_at: "2026-09-02T06:00:00Z", active: false },
  ];
}

describe("the maintenance list in Chromium", () => {
  for (const theme of ["dark", "light"] as const) {
    for (const width of [390, 1440]) {
      it(`${theme} ${width}: states, local times and the question that removes ended windows`, async () => {
        const page = await browser.newPage();
        const deleted: string[] = [];
        try {
          await page.setViewport({ width, height: 1000 });
          await page.emulateTimezone("Europe/Amsterdam");
          await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
          await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
          const now = Date.now();
          await page.setRequestInterception(true);
          page.on("request", (request) => {
            const path = new URL(request.url()).pathname;
            if (path === "/api/v1/maintenance") {
              void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ maintenance: schedule(now).filter((w) => !deleted.includes(String(w.id))) }) });
            } else if (path.startsWith("/api/v1/maintenance/") && request.method() === "DELETE") {
              deleted.push(path.split("/").pop()!);
              void request.respond({ status: 204, body: "" });
            } else void request.continue();
          });
          await page.goto(`${server.url}/monitors`, { waitUntil: "domcontentloaded" });
          await (await page.waitForSelector('::-p-aria(Maintenance[role="button"])'))!.click();
          await page.waitForSelector(".maintenance-list li");
          const rows = await page.$$eval(".maintenance-list li", (items) => items.map((li) => li.querySelectorAll("p")[1]?.textContent));
          expect(rows).toEqual([
            expect.stringMatching(/^\d+ \w{3} \d{4}, \d\d:\d\d → /),
            "20 Oct 2099, 20:00 → 21:00",
            "Sunday, Wednesday at 02:00 (Europe/Amsterdam), for 1 h 30 min",
            "1 Oct 2026, 20:00 → 21:00",
            "1 Sep 2026, 20:00 → 2 Sep 2026, 08:00",
          ]);
          const states = await page.$$eval(".maintenance-list li", (items) => items.map((li) => li.getAttribute("data-state")));
          expect(states).toEqual(["active", "upcoming", "upcoming", "ended", "ended"]);
          const proof = process.env.MAINTENANCE_BROWSER_PROOF_DIR;
          const shoot = async (name: string) => { if (proof) { await mkdir(proof, { recursive: true }); await page.screenshot({ path: join(proof, `${name}-${theme}-${width}.png`), fullPage: true }); } };
          await page.$eval(".maintenance-clear", (el) => el.scrollIntoView({ block: "center" }));
          await shoot("maintenance-list");
          // Nothing in the drawer is wider than the screen, the list included.
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
          await (await page.waitForSelector('::-p-aria(Remove ended windows[role="button"])'))!.click();
          await page.waitForFunction(() => document.activeElement?.textContent === "Keep them");
          expect(await page.$eval('.maintenance [role="group"] p', (el) => el.textContent)).toBe("Remove 2 ended windows? They no longer apply. Checks they excluded from uptime stay excluded.");
          // The question stands clear of the list above it and the form below.
          const gaps = await page.$eval('.maintenance [role="group"]', (el) => {
            const box = el.getBoundingClientRect();
            return [box.top - el.previousElementSibling!.getBoundingClientRect().bottom, el.nextElementSibling!.getBoundingClientRect().top - box.bottom];
          });
          for (const gap of gaps) expect(gap).toBeGreaterThanOrEqual(12);
          await shoot("maintenance-remove-question");
          await page.evaluate(axe.source);
          const audit = await page.evaluate(async () => (window as unknown as { axe: typeof axe }).axe.run({ include: [".maintenance"] }, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } }));
          expect(audit.violations).toEqual([]);
          await (await page.waitForSelector('::-p-aria(Remove 2 ended windows[role="button"])'))!.click();
          await page.waitForFunction(() => document.querySelector(".maintenance")?.textContent?.includes("Removed 2 ended windows. Recorded history is unchanged."));
          expect(deleted).toEqual(["4", "5"]);
          await page.waitForFunction(() => document.querySelectorAll(".maintenance-list li").length === 3);
          expect(await page.$('::-p-aria(Remove ended windows[role="button"])')).toBeNull();
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        } finally { await page.close(); }
      });
    }
  }
});
