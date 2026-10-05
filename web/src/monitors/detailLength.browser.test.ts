/**
 * A down monitor's detail page stays readable when its failures repeat
 * (SUB-184).
 *
 * A monitor that had been down for forty minutes answered forty checks with
 * the same 502, and the page drew forty rows of it above the uptime, the
 * latency trend and the incidents: over 6,100px at 1440. The failure card is
 * now last and folds consecutive identical failures into one row, and this
 * measures what that buys in a real browser rather than counting elements:
 * the page is at most about two screen heights, the summaries come before the
 * evidence, and the forty failures are one row.
 *
 * The harness serves no failures of its own, so the history request is
 * answered here with forty identical ones — the shape the seed's down
 * monitor produces.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";

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

const NOW = Date.now();
const FAILURES = Array.from({ length: 40 }, (_, i) => ({
  id: String(5000 - i),
  ts: new Date(NOW - i * 60_000).toISOString(),
  ok: false,
  assessment: "down",
  failure_kind: "status",
  status_code: 502,
  error: "unexpected status code 502",
}));

it.each([1440, 390])("folds forty identical failures and keeps the page short at %ipx", async (width) => {
  const page = await browser.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (new URL(req.url()).pathname === "/api/v1/monitors/1/heartbeats") {
        void req.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ heartbeats: FAILURES }) });
      } else {
        void req.continue();
      }
    });
    await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".response-history-beat", { timeout: 15_000 });
    await page.waitForSelector("[data-testid='lat-plot'] path", { timeout: 15_000 });
    await page.waitForSelector(".mon-detail-windows", { timeout: 15_000 });
    await page.evaluate(() => document.fonts.ready);

    const measured = await page.evaluate(() => ({
      height: document.documentElement.scrollHeight,
      rows: Array.from(document.querySelectorAll(".response-history-beat"), (row) => row.textContent ?? ""),
      headings: Array.from(document.querySelectorAll(".mon-detail h2"), (h) => h.textContent),
      noteOpen: document.querySelector<HTMLDetailsElement>(".mon-detail-uptime-note")?.open,
      gridLabels: Array.from(document.querySelectorAll(".lat-figure .chart-gridlabel"), (l) => l.textContent),
      overflow: document.documentElement.scrollWidth - window.innerWidth,
    }));
    expect(measured.rows).toHaveLength(1);
    expect(measured.rows[0]).toContain("40 checks in a row");
    expect(measured.headings).toEqual(["Recent checks", "Uptime", "Latency", "Incidents", "Failure responses"]);
    expect(measured.noteOpen).toBe(false);
    expect(measured.gridLabels).toHaveLength(3);
    expect(measured.overflow).toBeLessThanOrEqual(1);
    // The bar is "about two screen heights" at 1440: measured at 1,751px
    // here, against 6,002 with every failure on its own row. A phone stacks
    // every card, so it gets three (measured 2,053, against 8,468).
    expect(measured.height).toBeLessThanOrEqual(width >= 1024 ? 2 * 900 : 3 * 900);

    // A gridline label must not collide with the corner time below the plot
    // or run off the plot's left edge.
    const boxes = await page.$$eval(".lat-figure .chart-gridlabel", (labels) => labels.map((label) => {
      const box = label.getBoundingClientRect();
      const plot = label.closest(".chart-plot")!.getBoundingClientRect();
      return { left: box.left - plot.left, top: box.top - plot.top, bottom: plot.bottom - box.bottom };
    }));
    for (const box of boxes) {
      expect(box.left).toBeGreaterThanOrEqual(0);
      expect(box.top).toBeGreaterThanOrEqual(0);
      expect(box.bottom).toBeGreaterThanOrEqual(0);
    }
    expect(errors).toEqual([]);
  } finally {
    await page.close();
  }
});
