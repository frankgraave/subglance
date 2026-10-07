/**
 * The public status page as the server renders it, measured in Chromium.
 *
 * status-page-mockup.browser.test.ts holds the prototype to the layout claims
 * of docs/design/status-page.md §2. This file holds the real page to the same
 * claims: `go run ./internal/statuspage/preview` renders the prototype's
 * scenarios with the stylesheet from this build, and each document is served
 * with the Content-Security-Policy the renderer states, at /status/<name>, so
 * its relative font URLs resolve the way they do behind the product.
 *
 * The server lives in layout/harness/statusPages.ts, shared with the axe gate
 * in layout/accessibility.browser.test.ts.
 *
 * Beyond the prototype's checks it asserts what only the real page can get
 * wrong: that the inline stylesheet and theme script survive the policy (a
 * hash mismatch fails silently, as an unstyled page), that both faces load
 * from the relative path, that the theme follows prefers-color-scheme with no
 * control for it, and that axe finds nothing in either theme.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveStatusPages, type StatusPages } from "../layout/harness/statusPages";

const PHONE = [320, 375, 414];
const WIDER = [768, 1440];
const SCENARIOS = ["outage", "allup", "maintenance"];

let browser: Browser, pages: StatusPages;

beforeAll(async () => {
  pages = await serveStatusPages();
  browser = await chromium();
}, 180_000);

afterAll(async () => {
  await browser?.close();
  await pages?.close();
});

async function open(width: number, theme: string, scenario: string): Promise<{ page: Page; blocked: string[] }> {
  const page = await browser.newPage();
  const blocked: string[] = [];
  page.on("console", (msg) => { if (/Content Security Policy|Refused to/i.test(msg.text())) blocked.push(msg.text()); });
  page.on("requestfailed", (req) => blocked.push(`failed: ${req.url()}`));
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: theme }]);
  await page.goto(`${pages.url}/status/${scenario}`, { waitUntil: "load" });
  await page.evaluate(() => document.fonts.ready);
  return { page, blocked };
}

async function measure(page: Page) {
  return page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll(".sp-service"));
    const bars = rows.map(row => Array.from(row.querySelectorAll<HTMLElement>(".sp-days i"))
      .filter(bar => bar.getBoundingClientRect().width > 0));
    const axis = rows.map(row => Array.from(row.querySelectorAll<HTMLElement>(".sp-axis > span"))
      .filter(span => span.checkVisibility()).map(span => span.textContent));
    return {
      theme: document.documentElement.dataset.theme,
      styled: getComputedStyle(document.body).backgroundColor !== "rgba(0, 0, 0, 0)",
      faces: Array.from(document.fonts).filter(face => face.status === "loaded").map(face => face.family).sort(),
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      rows: rows.length,
      visibleBars: [...new Set(bars.map(list => list.length))],
      narrowestBar: Math.min(...bars.flat().map(bar => bar.getBoundingClientRect().width)),
      oldestLabel: [...new Set(axis.map(labels => labels[0]))],
      // The figure under the bar names its period; it has to be the bar's.
      uptimePeriod: [...new Set(axis.map(labels => /, (\d+) days$/.exec(labels[1] ?? "")?.[1] ?? labels[1]))],
      // Screen-reader text drawn on screen: the summary's status word beside
      // the sentence that already says it, or the 90-day history sentence
      // under a 30-day bar. Clipped to a pixel when the rule is shipped.
      exposedSrOnly: Array.from(document.querySelectorAll<HTMLElement>(".sr-only"))
        .filter(el => { const r = el.getBoundingClientRect(); return r.width > 1 || r.height > 1; })
        .map(el => el.textContent?.slice(0, 40)),
      wordless: Array.from(document.querySelectorAll(".led")).filter(led =>
        !led.nextElementSibling?.textContent?.trim()).length,
      heights: Object.fromEntries(["up", "warn", "down", "none"].map(state => [state,
        [...new Set(bars.flat().filter(bar => bar.dataset.s === state)
          .map(bar => bar.getBoundingClientRect().height))]])),
      historyDays: rows.map(row => (row.querySelector(".sp-history")?.textContent ?? "")
        .match(/^Last 90 days: (\d+) up, (\d+) degraded, (\d+) down, (\d+) no data\./)
        ?.slice(1).reduce((sum, n) => sum + Number(n), 0) ?? 0),
      controls: document.querySelectorAll("button, input, select, a[href]").length,
    };
  });
}

/** Each state present on the page draws at one height, and no two states share it. */
function expectDistinctHeights(heights: Record<string, number[]>) {
  const present = Object.entries(heights).filter(([, list]) => list.length > 0);
  for (const [state, list] of present) expect(list, `${state} bars at one height`).toHaveLength(1);
  const drawn = present.map(([, list]) => list[0]);
  expect(new Set(drawn).size, `heights per state: ${JSON.stringify(heights)}`).toBe(drawn.length);
}

async function audit(page: Page) {
  // Injected over DevTools: the page's policy rightly blocks an injected
  // <script>, and the test must not loosen the policy it is testing.
  await page.evaluate(axe.source);
  const result = await page.evaluate(async () => {
    const engine = (window as unknown as { axe: typeof axe }).axe;
    return await engine.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } });
  });
  return result.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.html) }));
}

function expectRenderedAsDesigned(m: Awaited<ReturnType<typeof measure>>, theme: string, blocked: string[]) {
  expect(blocked, "nothing blocked by the page's own policy").toEqual([]);
  expect(m.theme, "theme from prefers-color-scheme").toBe(theme);
  expect(m.styled, "inline stylesheet applied").toBe(true);
  expect(m.faces, "both faces loaded from the relative path").toEqual(["CommitMono", "InterVariable"]);
  expect(m.controls, "nothing on the page to operate, not even a theme toggle").toBe(0);
  expect(m.overflow, "page-level sideways scroll").toBeLessThanOrEqual(0);
  expect(m.wordless).toBe(0);
  expect(m.exposedSrOnly, "screen-reader text is not drawn").toEqual([]);
}

for (const theme of ["dark", "light"]) {
  describe(`rendered status page in ${theme}`, () => {
    for (const scenario of SCENARIOS) {
      it.each(PHONE)(`${scenario}: %ipx shows 30 days and does not scroll sideways`, async width => {
        const { page, blocked } = await open(width, theme, scenario);
        try {
          const m = await measure(page);
          expectRenderedAsDesigned(m, theme, blocked);
          expect(m.rows).toBe(5);
          expect(m.visibleBars).toEqual([30]);
          expect(m.oldestLabel).toEqual(["30 days ago"]);
          expect(m.uptimePeriod, "uptime over the days the bar draws").toEqual(["30"]);
          expect(m.narrowestBar, "a bar under 2px is no longer a bar").toBeGreaterThanOrEqual(2);
          expectDistinctHeights(m.heights);
          expect(m.historyDays).toEqual([90, 90, 90, 90, 90]);
          if (width === 375) expect(await audit(page)).toEqual([]);
        } finally { await page.close(); }
      });
      it.each(WIDER)(`${scenario}: %ipx shows 90 days`, async width => {
        const { page, blocked } = await open(width, theme, scenario);
        try {
          const m = await measure(page);
          expectRenderedAsDesigned(m, theme, blocked);
          expect(m.visibleBars).toEqual([90]);
          expect(m.oldestLabel).toEqual(["90 days ago"]);
          expect(m.uptimePeriod, "uptime over the days the bar draws").toEqual(["90"]);
          expect(m.narrowestBar).toBeGreaterThanOrEqual(2);
          expectDistinctHeights(m.heights);
          expect(m.historyDays).toEqual([90, 90, 90, 90, 90]);
          if (width === 1440) expect(await audit(page)).toEqual([]);
        } finally { await page.close(); }
      });
    }
    // The operator's own page: in Dutch, with a logo from beside the page and
    // an accent title, both allowed by the page's own policy and nothing more.
    it.each([375, 1440])("branded: %ipx loads its logo, colours its title and passes axe", async (width) => {
      const { page, blocked } = await open(width, theme, "branded");
      try {
        const m = await measure(page);
        expectRenderedAsDesigned(m, theme, blocked);
        expect(m.rows).toBe(5);
        const seen = await page.evaluate(() => {
          const logo = document.querySelector<HTMLImageElement>(".sp-logo");
          const title = document.querySelector<HTMLElement>(".sp-head h1");
          const box = logo?.getBoundingClientRect();
          return {
            lang: document.documentElement.lang,
            loaded: logo ? logo.complete && logo.naturalWidth > 0 : false,
            alt: logo?.getAttribute("alt"),
            height: box?.height ?? 0,
            beforeTitle: !!(logo && title && box && box.bottom <= title.getBoundingClientRect().top + 1),
            title: title ? getComputedStyle(title).color : "",
            footer: document.querySelector(".sp-foot")?.textContent,
          };
        });
        expect(seen).toEqual({
          lang: "nl", loaded: true, alt: "", height: 40, beforeTitle: true,
          title: "rgb(59, 130, 246)", footer: "Tijden in Europe/Amsterdam",
        });
        expect(await audit(page)).toEqual([]);
      } finally { await page.close(); }
    });
    it("empty: says so, and passes axe", async () => {
      const { page, blocked } = await open(375, theme, "empty");
      try {
        const m = await measure(page);
        expectRenderedAsDesigned(m, theme, blocked);
        expect(m.rows).toBe(0);
        expect(await page.$eval("main", (el) => el.textContent)).toContain("No services on this page yet.");
        expect(await audit(page)).toEqual([]);
      } finally { await page.close(); }
    });
  });
}
