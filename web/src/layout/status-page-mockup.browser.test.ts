/**
 * The public status page prototype (docs/mockups/pages/status.html), measured.
 *
 * The shared mockup contract already holds this page to the tokens, the type
 * roles and the lamp-plus-word rule. What it does not know is the one layout
 * claim the design document makes for this page: 90 days of history on a
 * tablet or desktop, 30 on a phone, and never a bar so thin it stops being one
 * (docs/design/status-page.md §2). Those are numbers, so they are asserted as
 * numbers, at the three phone widths the product is held to and two wider ones.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium, type Browser } from "./harness/browser";

const file = pathToFileURL(fileURLToPath(new URL("../../../docs/mockups/pages/status.html", import.meta.url))).href;
const PHONE = [320, 375, 414];
const WIDER = [768, 1440];
const SCENARIOS = ["outage", "allup", "maintenance"];

let browser: Browser;
beforeAll(async () => { browser = await chromium(); });
afterAll(async () => { await browser?.close(); });

async function measure(width: number, theme: string, scenario: string) {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    await page.evaluateOnNewDocument((t: string) => localStorage.setItem("subglance:mockup-theme", t), theme);
    await page.goto(file, { waitUntil: "load" });
    await page.click(`[data-scenario="${scenario}"]`);
    return await page.evaluate(() => {
      const rows = Array.from(document.querySelectorAll("#services .sp-row"));
      const bars = rows.map(row => Array.from(row.querySelectorAll<HTMLElement>(".sp-days i"))
        .filter(bar => bar.getBoundingClientRect().width > 0));
      const axis = rows.map(row => Array.from(row.querySelectorAll<HTMLElement>(".sp-axis > span"))
        .filter(span => span.checkVisibility()).map(span => span.textContent));
      return {
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        rows: rows.length,
        visibleBars: [...new Set(bars.map(list => list.length))],
        narrowestBar: Math.min(...bars.flat().map(bar => bar.getBoundingClientRect().width)),
        oldestLabel: [...new Set(axis.map(labels => labels[0]))],
        // Every lamp on the page carries a visible word, never colour alone.
        wordless: Array.from(document.querySelectorAll(".led")).filter(led =>
          !led.nextElementSibling?.textContent?.trim()).length,
        // Each daily state draws at its own height, so no two share a colour-only difference.
        heights: Object.fromEntries(["up", "warn", "down", "none"].map(state => [state,
          [...new Set(bars.flat().filter(bar => bar.dataset.s === state)
            .map(bar => bar.getBoundingClientRect().height))]])),
        // The bars are aria-hidden; the text alternative must account for all 90 days.
        historyDays: rows.map(row => (row.querySelector(".sp-history")?.textContent ?? "")
          .match(/^Last 90 days: (\d+) up, (\d+) degraded, (\d+) down, (\d+) no data\./)
          ?.slice(1).reduce((sum, n) => sum + Number(n), 0) ?? 0),
        // The review controls, measured against the 24 by 24 CSS-pixel target floor.
        smallTargets: Array.from(document.querySelectorAll<HTMLElement>(".sp-review button"))
          .filter(button => button.checkVisibility())
          .map(button => button.getBoundingClientRect())
          .filter(box => box.width < 24 || box.height < 24).length,
      };
    });
  } finally { await page.close(); }
}

/** Each state present on the page draws at one height, and no two states share it. */
function expectDistinctHeights(heights: Record<string, number[]>) {
  const present = Object.entries(heights).filter(([, list]) => list.length > 0);
  for (const [state, list] of present) expect(list, `${state} bars at one height`).toHaveLength(1);
  const drawn = present.map(([, list]) => list[0]);
  expect(new Set(drawn).size, `heights per state: ${JSON.stringify(heights)}`).toBe(drawn.length);
}

for (const theme of ["dark", "light"]) {
  describe(`status page prototype in ${theme}`, () => {
    for (const scenario of SCENARIOS) {
      it.each(PHONE)(`${scenario}: %ipx shows 30 days and does not scroll sideways`, async width => {
        const m = await measure(width, theme, scenario);
        expect(m.overflow, "page-level sideways scroll").toBeLessThanOrEqual(0);
        expect(m.rows).toBe(5);
        expect(m.visibleBars).toEqual([30]);
        expect(m.oldestLabel).toEqual(["30 days ago"]);
        expect(m.narrowestBar, "a bar under 2px is no longer a bar").toBeGreaterThanOrEqual(2);
        expect(m.wordless).toBe(0);
        expect(m.smallTargets, "controls under 24x24 CSS px").toBe(0);
        expectDistinctHeights(m.heights);
        expect(m.historyDays).toEqual([90, 90, 90, 90, 90]);
      });
      it.each(WIDER)(`${scenario}: %ipx shows 90 days`, async width => {
        const m = await measure(width, theme, scenario);
        expect(m.overflow).toBeLessThanOrEqual(0);
        expect(m.visibleBars).toEqual([90]);
        expect(m.oldestLabel).toEqual(["90 days ago"]);
        expect(m.narrowestBar).toBeGreaterThanOrEqual(2);
        expect(m.wordless).toBe(0);
        expectDistinctHeights(m.heights);
        expect(m.historyDays).toEqual([90, 90, 90, 90, 90]);
      });
    }
  });
}
