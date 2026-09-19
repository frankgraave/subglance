// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface } from "node:readline";
import { chromium, type Browser } from "../layout/harness/browser";
import { THEME_STORAGE_KEY } from "../theme/theme";

const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string;
let child: ChildProcess;
let browser: Browser;
let fixture: { url: string; session: string; monitor_id: number; identity_monitor_id: number };

beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "snapshot-browser-"));
  const binary = join(dir, "snapshot-fixture");
  await promisify(execFile)("go", ["build", "-p", "1", "-o", binary, "./internal/api/testdata/snapshot-browser"], { cwd: root });
  child = spawn(binary, [join(dir, "history.db")], { stdio: ["pipe", "pipe", "pipe"] });
  fixture = await new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout! });
    const timer = setTimeout(() => reject(new Error("snapshot API fixture did not start")), 30_000);
    child.once("error", reject);
    child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`snapshot API fixture exited ${code}`)); });
    lines.once("line", (line) => {
      clearTimeout(timer);
      lines.close();
      try { resolve(JSON.parse(line)); } catch { reject(new Error("invalid fixture startup record")); }
    });
  });
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  if (child && child.exitCode === null) {
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.stdin?.end();
    await exited;
  }
  if (dir) await rm(dir, { recursive: true, force: true });
});

it.each(["dark", "light"] as const)("polling preserves disclosed identity, body focus and scroll in %s", async (theme) => {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  try {
    await context.setCookie({ name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname, path: "/", httpOnly: true, sameSite: "Strict" });
    await page.setViewport({ width: 390, height: 1000 });
    await page.evaluateOnNewDocument((key, value) => {
      localStorage.setItem(key, value);
      // Exercise React Query's real polling callback without a minute's wait.
      // Network, API, persisted records, reconciliation and layout stay real.
      const browserWindow: Window = window;
      const interval = browserWindow.setInterval.bind(browserWindow);
      browserWindow.setInterval = (handler, delay, ...args) => interval(handler, delay === 60_000 ? 1000 : delay, ...args);
    }, THEME_STORAGE_KEY, theme);
    await page.goto(`${fixture.url}/monitors/${fixture.identity_monitor_id}`, { waitUntil: "domcontentloaded" });
    const summary = await page.waitForSelector(".response-history summary");
    await page.evaluate(() => document.fonts.ready);
    const readHistory = () => page.evaluate(async (id) => {
      const result = await fetch(`/api/v1/monitors/${id}/heartbeats?limit=100`);
      return await result.json() as { heartbeats: { id: string; ts: string; response: { body: string } }[] };
    }, fixture.identity_monitor_id);
    const initial = (await readHistory()).heartbeats;
    expect(initial.length).toBeGreaterThanOrEqual(2);
    expect(new Set(initial.map((hb) => hb.id)).size).toBe(initial.length);
    await summary!.focus();
    await page.keyboard.press("Enter");
    await page.keyboard.press("Tab");
    const body = await page.$(".response-history pre");
    expect(await body!.evaluate((el) => document.activeElement === el)).toBe(true);
    await page.keyboard.press("End");
    await page.waitForFunction(() => {
      const el = document.querySelector(".response-history pre")!;
      return el.scrollTop > 0 && el.scrollTop + el.clientHeight >= el.scrollHeight;
    });
    const scrollTop = await body!.evaluate((el) => el.scrollTop);
    expect(scrollTop).toBeGreaterThan(0);
    for (const [index, advance] of [0, 1000].entries()) {
      const ts = new Date(Date.parse(initial[0].ts) + advance).toISOString();
      child.stdin!.write(`${JSON.stringify({ ts })}\n`);
      await page.waitForFunction((count) => document.querySelectorAll(".response-history-beat").length === count, {}, initial.length + index + 1);
      const state = await body!.evaluate((el, position) => {
        const disclosures = Array.from(document.querySelectorAll<HTMLDetailsElement>(".response-history details"));
        return {
          connected: el.isConnected,
          sameBody: disclosures[position]?.querySelector("pre") === el,
          open: disclosures[position]?.open,
          onlyOriginalOpen: disclosures.filter((d) => d.open).length === 1 && !disclosures[0].open,
          focused: document.activeElement === el,
          visible: el.checkVisibility(),
          scrollTop: el.scrollTop,
        };
      }, index + 1);
      expect(state).toEqual({ connected: true, sameBody: true, open: true, onlyOriginalOpen: true, focused: true, visible: true, scrollTop });
      const refreshed = (await readHistory()).heartbeats;
      expect(refreshed[index + 1].id).toBe(initial[0].id);
      expect(refreshed[0].id).not.toBe(initial[0].id);
      expect(Date.parse(refreshed[0].ts)).toBe(Date.parse(ts));
      // Same timestamp and body cannot distinguish distinct persisted checks.
      expect(refreshed[0].response.body).toBe(initial[0].response.body);
    }
  } finally { await context.close(); }
});

it.each([
  ["dark", 390], ["light", 390], ["dark", 1280], ["light", 1280],
] as const)("real captured history is safe and keyboard-readable in %s at %ipx", async (theme, width) => {
  // This is the real Go auth handler, not a mocked frontend response.
  const endpoint = `${fixture.url}/api/v1/monitors/${fixture.monitor_id}/heartbeats?limit=100`;
  expect((await fetch(endpoint)).status).toBe(401);
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await context.setCookie({ name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname, path: "/", httpOnly: true, sameSite: "Strict" });
    await page.setViewport({ width, height: 1000, deviceScaleFactor: 1 });
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.goto(`${fixture.url}/monitors/${fixture.monitor_id}`, { waitUntil: "domcontentloaded" });
    const summary = await page.waitForSelector(".response-history summary");
    await page.evaluate(() => document.fonts.ready);
    expect(await page.$eval(".response-history details", (el) => (el as HTMLDetailsElement).open)).toBe(false);
    // Chromium retains a measurable box under closed ::details-content;
    // visibility, not its rectangle, is the user-visible contract.
    expect(await page.$eval(".response-history pre", (el) => el.checkVisibility())).toBe(false);
    const text = await page.$eval(".response-history", (el) => el.textContent ?? "");
    expect(text).toContain("Capture stopped while this monitor was flapping");
    expect(text).toContain("Capture was switched off for this check.");
    expect(text).toContain("No captured response. Reason not recorded.");
    expect(text).toContain("Truncated — only the beginning");
    expect(text).not.toContain("must-not-store");
    await summary!.focus();
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector<HTMLDetailsElement>(".response-history details")?.open);
    expect(await page.$eval(".response-history pre", (el) => el.checkVisibility())).toBe(true);
    const bodySize = await page.$eval(".response-history pre", (el) => ({
      height: el.getBoundingClientRect().height,
      limit: Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--size-pane-scroll")),
    }));
    expect(bodySize.height).toBeLessThanOrEqual(bodySize.limit);
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => document.activeElement?.getAttribute("aria-label"))).toBe("Captured response body");
    await page.keyboard.press("End");
    await page.waitForFunction(() => (document.querySelector(".response-history pre")?.scrollTop ?? 0) > 0);
    const raw = await page.evaluate(async (url) => {
      const response = await fetch(url);
      return { status: response.status, data: await response.json() };
    }, endpoint);
    expect(raw.status).toBe(200);
    const captured = raw.data.heartbeats.find((hb: { response?: { body: string } }) => hb.response)?.response;
    expect(captured?.body).toContain("<script>");
    expect(await page.$eval(".response-history pre", (el) => el.textContent)).toBe(captured.body);
    expect(await page.$(".response-history script, .response-history img, .response-history a, .response-history b")).toBeNull();
    expect(await page.evaluate(() => (window as unknown as { snapshotExecuted?: boolean }).snapshotExecuted)).toBeUndefined();
    const metrics = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      css: getComputedStyle(document.querySelector(".response-history pre")!).whiteSpace,
      headings: Array.from(document.querySelectorAll(".mon-detail h2"), (el) => el.textContent),
    }));
    expect(metrics.overflow).toBeLessThanOrEqual(1);
    expect(metrics.css).toBe("pre-wrap");
    expect(metrics.headings.slice(0, 3)).toEqual(["Recent checks", "Failure responses", "Uptime"]);
    expect(errors).toEqual([]);
    if (process.env.SNAPSHOT_BROWSER_PROOF_DIR) {
      await page.$eval(".response-history pre", (el) => { el.scrollTop = 0; });
      await mkdir(process.env.SNAPSHOT_BROWSER_PROOF_DIR, { recursive: true });
      await page.screenshot({ path: join(process.env.SNAPSHOT_BROWSER_PROOF_DIR, `response-${theme}-${width}.png`), fullPage: true });
    }
  } finally { await context.close(); }
});
