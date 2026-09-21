// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface, type Interface } from "node:readline";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";

const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string;
let child: ChildProcess;
let lines: Interface;
let browser: Browser;
let fixture: { url: string; session: string; ids: number[] };
const readLine = () => new Promise<string>((resolve) => lines.once("line", resolve));

beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "warning-browser-"));
  const binary = join(dir, "fixture");
  await promisify(execFile)("go", ["build", "-p", "1", "-o", binary, "./internal/api/testdata/warning-browser"], { cwd: root });
  child = spawn(binary, [join(dir, "history.db")], { stdio: ["pipe", "pipe", "pipe"] });
  lines = createInterface({ input: child.stdout! });
  fixture = JSON.parse(await Promise.race([readLine(), new Promise<string>((_, reject) => {
    child.once("error", reject);
    child.once("exit", (code) => reject(new Error(`fixture exited ${code}`)));
  })]));
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  lines?.close();
  if (child && child.exitCode === null) {
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.stdin?.end();
    await exited;
  }
  if (dir) await rm(dir, { recursive: true, force: true });
});

it.each([
  ["dark", 375, 0], ["light", 375, 1], ["dark", 1440, 2], ["light", 1440, 3],
] as const)("real warnings, confirmation, history and silent recovery: %s %ipx", async (theme, width, index) => {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  page.setDefaultTimeout(5_000);
  const id = fixture.ids[index];
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await context.setCookie({ name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname, path: "/", httpOnly: true, sameSite: "Strict" });
    await page.setViewport({ width, height: 1000 });
    await page.evaluateOnNewDocument((themeKey, themeValue) => localStorage.setItem(themeKey, themeValue), THEME_STORAGE_KEY, theme);
    for (const layout of ["rows", "cards", "compact", "wall"]) {
      await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), LAYOUT_STORAGE_KEY, layout);
      await page.goto(fixture.url, { waitUntil: "domcontentloaded" });
      await page.waitForSelector('[data-status="warning"] .led, .led[data-status="warning"]');
      const ax = await page.accessibility.snapshot();
      expect(JSON.stringify(ax)).toMatch(/"name":"(?:Warning|WARNING)"/);
      if (layout === "wall") {
        const borders = await page.$eval('.wall-card[data-status="warning"]', (node) => {
          const card = node as HTMLElement;
          card.style.transition = "none";
          const live = getComputedStyle(card).borderColor;
          const wall = card.closest<HTMLElement>(".wall")!;
          wall.dataset.stale = "true";
          const stale = getComputedStyle(card).borderColor;
          wall.dataset.stale = "false";
          return { live, stale };
        });
        expect(borders.live).not.toBe(borders.stale);
      }
      if (layout !== "wall") {
        const filter = await page.waitForSelector('.mon-count:has([data-state="warn"])');
        await filter!.click();
        expect(await filter!.evaluate((el) => el.getAttribute("aria-pressed"))).toBe("true");
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    }
    await page.goto(`${fixture.url}/monitors/${id}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector('.mon-detail[data-status="warning"] .hb-bar--warning');
    await page.focus('.hb-track');
    await page.keyboard.press("End");
    expect(await page.$eval('.hb-sr-only[aria-live="polite"]', el=>el.textContent)).toContain("Warning — unconfirmed, no alert");
    const legend=await page.$$eval('.legend-item',items=>items.map(item=>({label:item.querySelector('dt')?.textContent,value:item.querySelector('dd')?.textContent})));
    expect(legend).toContainEqual({label:"Warnings (unconfirmed)",value:"1"});
    expect(legend).toContainEqual({label:"Failed",value:"0"});
    await page.keyboard.press("Escape");
    expect(new URL(page.url()).pathname).toBe(`/monitors/${id}`);
    await page.waitForFunction(() => document.querySelector(".response-history")?.textContent?.includes("unconfirmed failure; no alert; excluded from uptime"));
    await page.waitForSelector('.inc-row[data-state="warning"]');
    expect(await page.$eval('.inc-row', (el) => el.textContent)).not.toMatch(/Down since|repeat alerts are still escalating/);
    expect(await page.$('.inc-row .led[data-status="down"]')).toBeNull();
    const summary = await page.waitForSelector('.response-history summary');
    await summary!.focus();
    await page.keyboard.press("Enter");
    expect(await page.$eval('.response-history pre', (el) => el.checkVisibility())).toBe(true);
    expect(await page.$eval('.response-history pre', (el) => el.textContent)).toBe("temporary upstream failure");
    const read = () => page.evaluate(async (monitorID) => {
      const [monitor, beats, uptime] = await Promise.all([
        fetch(`/api/v1/monitors/${monitorID}`).then((r) => r.json()),
        fetch(`/api/v1/monitors/${monitorID}/heartbeats`).then((r) => r.json()),
        fetch(`/api/v1/monitors/${monitorID}/uptime?window=24h`).then((r) => r.json()),
      ]);
      return { monitor, beats, uptime };
    }, id);
    const before = await read();
    expect(before.monitor.status).toBe("warning");
    expect(before.monitor.uptime_24h).toBeNull();
    expect(before.beats.heartbeats[0]).toMatchObject({ assessment: "warning", failure_kind: "status", ok: false });
    expect(before.uptime.windows[0]).toMatchObject({ total: 0, warning: 1, uptime: null });
    // Audit the new status, history and uptime surfaces without contrast waivers.
    await page.evaluate(axe.source);
    const audit = await page.evaluate(async () => {
      const a = (window as unknown as { axe: typeof axe }).axe;
      return a.run({ include: [".mon-detail-status", ".response-history", ".mon-detail-windows", ".mon-detail-uptime-note", ".legend"] }, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] } });
    });
    expect(audit.violations).toEqual([]);
    if (process.env.WARNING_BROWSER_PROOF_DIR) {
      await mkdir(process.env.WARNING_BROWSER_PROOF_DIR, { recursive: true });
      await page.screenshot({ path: join(process.env.WARNING_BROWSER_PROOF_DIR, `warning-${theme}-${width}.png`), fullPage: true });
    }
    // A real caller confirms the outage. The already-recorded warning remains immutable.
    const checkNow = await page.waitForSelector('.card-head-action .mon-check-now');
    expect(await checkNow!.evaluate((el) => el.textContent?.trim())).toBe("Check now");
    await checkNow!.click();
    await page.waitForSelector('.mon-detail[data-status="down"]');
    const confirmed = await read();
    expect(confirmed.uptime.windows[0]).toMatchObject({ total: 1, down: 1, warning: 1, uptime: 0 });
    expect(confirmed.beats.heartbeats.map((b: { assessment: string }) => b.assessment)).toEqual(["down", "warning"]);
    // Recover another unconfirmed monitor first; it must produce zero additional alerts.
    let response = readLine();
    child.stdin!.write(`${JSON.stringify({ healthy: index + 4 })}\n`);
    expect(JSON.parse(await response)).toMatchObject({ added_alerts: 0 });
    // Recovery of the confirmed monitor produces exactly one recovery alert and arrives over SSE.
    response = readLine();
    child.stdin!.write(`${JSON.stringify({ healthy: index })}\n`);
    expect(JSON.parse(await response)).toMatchObject({ added_alerts: 1 });
    await page.waitForSelector('.mon-detail[data-status="up"]');
    const recovered = await read();
    expect(recovered.uptime.windows[0]).toMatchObject({ total: 2, up: 1, down: 1, warning: 1, uptime: 50 });
    expect(errors).toEqual([]);
  } finally { await context.close(); }
});
