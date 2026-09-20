// @vitest-environment node
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let server: Server;
let browser: Browser;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); }, 120_000);
afterAll(async () => { await browser?.close(); await server?.close(); });

const monitor = (push: boolean, paused: boolean) => ({
  id: 1, name: "api", type: push ? "push" : "http", target: push ? "" : "https://example.com",
  interval_s: 60, timeout_s: 10, enabled: !paused, status: "up", latency_ms: 30,
  last_check: new Date().toISOString(), created_at: new Date().toISOString(),
  ...(push ? { push_interval_s: 3600, push_grace_s: 60 } : {}),
});

describe("detail Check now in Chromium", () => {
  for (const theme of ["dark", "light"] as const) {
    for (const width of [390, 1440]) {
      it(`${theme} ${width}: posts once, reports rejection, then an unrecorded result`, async () => {
        const page = await browser.newPage();
        const requests: string[] = [];
        let complete!: () => void;
        try {
          await page.setViewport({ width, height: 900 });
          await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
          await page.setRequestInterception(true);
          page.on("request", (request) => {
            const path = new URL(request.url()).pathname;
            if (path === "/api/v1/monitors") {
              void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: [monitor(false, true)] }) });
            } else if (path === "/api/v1/monitors/1/check") {
              requests.push(request.method());
              complete = () => { void request.respond({ status: requests.length === 1 ? 429 : 200, contentType: "application/json", body: JSON.stringify(requests.length === 1
                ? { error: "please wait five seconds" }
                : { ok: true, latency_ms: 12, status_code: 204, recorded: false }) }); };
            } else void request.continue();
          });
          const historyResponse = page.waitForResponse((response) =>
            new URL(response.url()).pathname === "/api/v1/monitors/1/heartbeats");
          await page.goto(server.url + "/monitors/1", { waitUntil: "domcontentloaded" });
          expect((await historyResponse).status()).toBe(200);
          await page.waitForFunction(() => document.querySelector(".response-history")?.textContent?.includes("No failed checks in the recent history."));
          await page.waitForSelector(".mon-detail-name");
          const button = await page.$(".card-head-action .mon-check-now");
          expect(button, "a probed monitor must offer Check now").not.toBeNull();
          expect(await button?.evaluate((node) => node.textContent)).toBe("Check now");
          await button?.click();
          await page.waitForFunction(() => document.querySelector<HTMLButtonElement>(".card-head-action .mon-check-now")?.disabled === true);
          // Programmatic repeated clicks exercise the handler while disabled.
          await button?.evaluate((node) => { (node as HTMLButtonElement).click(); (node as HTMLButtonElement).click(); });
          await page.waitForFunction(() => document.querySelector(".card-head-action .mon-check-now")?.textContent === "Checking…");
          await expect.poll(() => requests.length).toBe(1);
          expect(requests).toEqual(["POST"]);
          complete();
          await page.waitForFunction(() => document.querySelector(".mon-detail [role=alert]")?.textContent?.includes("please wait five seconds"));
          const icon = await page.$eval('.mon-detail [role=alert] svg', (svg) => ({
            hidden: svg.getAttribute("aria-hidden"),
            width: svg.getBoundingClientRect().width,
            rung: parseFloat(getComputedStyle(svg).getPropertyValue("--size-icon-sm")),
          }));
          expect(icon.hidden).toBe("true");
          expect(icon.width).toBe(icon.rung);
          await button?.click();
          await page.waitForFunction(() => document.querySelector<HTMLButtonElement>(".card-head-action .mon-check-now")?.disabled === true);
          // Wait for the second intercepted POST rather than its visual start.
          await expect.poll(() => requests.length).toBe(2);
          complete();
          await page.waitForFunction(() => document.querySelector(".mon-detail [role=status]")?.textContent?.includes("Not recorded"));
          const state = await page.evaluate(() => ({
            result: document.querySelector(".mon-detail [role=status]")?.textContent,
            status: document.querySelector(".mon-detail")?.getAttribute("data-status"),
            alert: document.querySelector(".mon-detail [role=alert]"),
            fits: document.documentElement.scrollWidth <= window.innerWidth,
          }));
          expect(state.result).toContain("Check passed · 12 ms · HTTP 204");
          expect(state.status).toBe("paused");
          expect(state.alert).toBeNull();
          expect(state.fits).toBe(true);
          expect(requests).toEqual(["POST", "POST"]);
        } finally { await page.close(); }
      });
    }
  }

  it("never offers Check now for a push window", async () => {
    const page = await browser.newPage();
    try {
      await page.setRequestInterception(true);
      page.on("request", (request) => {
        if (new URL(request.url()).pathname === "/api/v1/monitors") {
          void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: [monitor(true, false)] }) });
        } else void request.continue();
      });
      await page.goto(server.url + "/monitors/1", { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".mon-detail-name");
      expect(await page.$(".card-head-action .mon-check-now")).toBeNull();
    } finally { await page.close(); }
  });
});
