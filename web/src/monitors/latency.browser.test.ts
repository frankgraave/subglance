/**
 * The latency chart on the detail page, measured in a real browser (SUB-23).
 *
 * jsdom has no layout, so the unit tests can prove the chart draws the right
 * paths but not that it fits: that the plot takes its rung's height, spans
 * its panel, leaves the page without a horizontal scrollbar at 375px, and
 * that the keyboard readout lands inside the viewport at both ends of the
 * line. The harness serves a day with a gap and an outage step in it, so a
 * broken line and a down tick are on screen rather than only the easy case.
 *
 * Assertions are about boxes and element counts, never screenshots.
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

it.each([375, 1440])("fits, breaks at gaps and reads out by keyboard at %ipx", async (width) => {
  const page = await browser.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    await page.goto(server.url + "/monitors/1", { waitUntil: "domcontentloaded" });
    await page.waitForSelector("[data-testid='lat-plot'] path", { timeout: 15_000 });

    const box = await page.$eval("[data-testid='lat-plot']", (plot) => {
      const rect = plot.getBoundingClientRect();
      const panel = plot.closest(".panel")!.getBoundingClientRect();
      const rung = getComputedStyle(document.documentElement).getPropertyValue("--size-plot").trim();
      return {
        height: rect.height,
        rung,
        insidePanel: rect.left >= panel.left - 0.5 && rect.right <= panel.right + 0.5,
        width: rect.width,
        runs: plot.querySelectorAll("[data-testid='lat-run']").length,
        down: plot.querySelectorAll("[data-testid='lat-down']").length,
        overflow: document.documentElement.scrollWidth - window.innerWidth,
      };
    });
    expect(`${box.height}px`).toBe(box.rung);
    expect(box.insidePanel).toBe(true);
    expect(box.width).toBeGreaterThan(width < 640 ? 250 : 500);
    // The fixture has one gap and one outage step: three measured runs.
    expect(box.runs).toBe(3);
    expect(box.down).toBe(1);
    expect(box.overflow).toBeLessThanOrEqual(1);

    await page.focus("[data-testid='lat-plot']");
    for (const key of ["Home", "End"] as const) {
      await page.keyboard.press(key);
      const tip = await page.$eval("[data-testid='lat-tooltip']", (el) => {
        const r = el.getBoundingClientRect();
        return { left: r.left, right: r.right, text: el.textContent ?? "" };
      });
      expect(tip.left).toBeGreaterThanOrEqual(0);
      expect(tip.right).toBeLessThanOrEqual(width);
      expect(tip.text).toMatch(/ms/);
    }
    await page.keyboard.press("Escape");
    expect(await page.$("[data-testid='lat-tooltip']")).toBeNull();
    expect(errors).toEqual([]);
  } finally {
    await page.close();
  }
});
