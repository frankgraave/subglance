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
import { execFile } from "node:child_process";
import { createServer, type Server } from "node:http";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { chromium, type Browser, type Page } from "../layout/harness/browser";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const fonts = fileURLToPath(new URL("../../public/fonts/", import.meta.url));
const PHONE = [320, 375, 414];
const WIDER = [768, 1440];
const SCENARIOS = ["outage", "allup", "maintenance"];

let dir: string, browser: Browser, server: Server, base: string, csp: string;

beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "status-page-"));
  await promisify(execFile)("go", ["run", "./internal/statuspage/preview", "-out", dir], { cwd: root });
  csp = await readFile(join(dir, "csp.txt"), "utf8");
  server = createServer(async (req, res) => {
    const path = new URL(req.url ?? "/", "http://localhost").pathname;
    try {
      if (path.startsWith("/status/fonts/")) {
        const body = await readFile(join(fonts, basename(path)));
        res.writeHead(200, { "content-type": "font/woff2" }).end(body);
        return;
      }
      const name = /^\/status\/([a-z]+)$/.exec(path)?.[1];
      if (name) {
        const body = await readFile(join(dir, `${name}.html`));
        res.writeHead(200, { "content-type": "text/html; charset=utf-8", "content-security-policy": csp }).end(body);
        return;
      }
    } catch { /* fall through to 404 */ }
    res.writeHead(404).end();
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address();
  if (!addr || typeof addr === "string") throw new Error("no port");
  base = `http://127.0.0.1:${addr.port}`;
  browser = await chromium();
}, 180_000);

afterAll(async () => {
  await browser?.close();
  await new Promise<void>((resolve) => (server ? server.close(() => resolve()) : resolve()));
  if (dir) await rm(dir, { recursive: true, force: true });
});

async function open(width: number, theme: string, scenario: string): Promise<{ page: Page; blocked: string[] }> {
  const page = await browser.newPage();
  const blocked: string[] = [];
  page.on("console", (msg) => { if (/Content Security Policy|Refused to/i.test(msg.text())) blocked.push(msg.text()); });
  page.on("requestfailed", (req) => blocked.push(`failed: ${req.url()}`));
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: theme }]);
  await page.goto(`${base}/status/${scenario}`, { waitUntil: "load" });
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
          expect(m.narrowestBar).toBeGreaterThanOrEqual(2);
          expectDistinctHeights(m.heights);
          expect(m.historyDays).toEqual([90, 90, 90, 90, 90]);
          if (width === 1440) expect(await audit(page)).toEqual([]);
        } finally { await page.close(); }
      });
    }
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
