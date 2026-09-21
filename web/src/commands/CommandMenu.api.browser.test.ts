// Real embedded artifact + Go API + SQLite. No HTTP interception.
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface } from "node:readline";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
let dir: string, child: ChildProcess, browser: Browser;
let fixture: { url: string; session: string; monitor_id: number };
beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "command-api-"));
  const binary = join(dir, "fixture");
  await promisify(execFile)("go", ["build", "-p", "1", "-o", binary, "./internal/api/testdata/snapshot-browser"], { cwd: fileURLToPath(new URL("../../../", import.meta.url)) });
  child = spawn(binary, [join(dir, "commands.db")], { stdio: ["pipe", "pipe", "pipe"] });
  fixture = await new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout! });
    const timer = setTimeout(() => reject(new Error("API fixture startup timed out")), 30_000);
    child.once("error", reject);
    child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`API fixture exited ${code}`)); });
    lines.once("line", (line) => { clearTimeout(timer); lines.close(); resolve(JSON.parse(line)); });
  });
  browser = await chromium();
}, 120_000);
afterAll(async () => {
  await browser?.close();
  if (child && child.exitCode === null) { const ended = new Promise<void>((resolve) => child.once("exit", () => resolve())); child.stdin!.end(); await ended; }
  if (dir) await rm(dir, { recursive: true, force: true });
});
async function command(page: Page, query: string) {
  if (!await page.$(".command-menu[open]")) {
    await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
    await page.waitForSelector(".command-menu[open]");
  }
  await page.focus(".command-menu input");
  await page.keyboard.down("Control"); await page.keyboard.press("a"); await page.keyboard.up("Control");
  await page.keyboard.type(query);
  await page.waitForFunction((query) => [...document.querySelectorAll('.command-menu [role="option"]')].some((el) => el.textContent === query), {}, query);
  await page.keyboard.press("Enter");
}
it("pauses/resumes persisted monitors, opens the create form and expires the open menu through real auth", async () => {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  try {
    await context.setCookie({ name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname, path: "/", httpOnly: true, sameSite: "Strict" });
    await page.goto(fixture.url + "/monitors", { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".shell-command-launcher");
    const read = () => page.evaluate(async (id) => { const r = await fetch(`/api/v1/monitors/${id}`); return { status: r.status, body: await r.json() }; }, fixture.monitor_id);
    expect((await read()).body.enabled).toBe(true);
    await command(page, "Pause Response diagnostics");
    await expect.poll(async () => (await read()).body.enabled).toBe(false);
    await page.waitForFunction(() => !document.querySelector('.command-menu [aria-disabled="true"]'));
    await command(page, "Resume Response diagnostics");
    await expect.poll(async () => (await read()).body.enabled).toBe(true);
    await page.waitForFunction(() => !document.querySelector('.command-menu [aria-disabled="true"]'));
    await command(page, "Add monitor");
    await page.waitForSelector(".add-form");
    expect(new URL(page.url()).pathname).toBe("/monitors/new");
    await page.keyboard.press("Escape");
    await page.waitForSelector(".add-form", { hidden: true });
    // Invalidate this cookie on the actual server, then discover 401 via a
    // palette request. The menu must disappear instead of exposing cached data.
    expect(await page.evaluate(async () => (await fetch("/api/v1/auth/logout", { method: "POST" })).status)).toBe(204);
    await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
    await page.waitForSelector(".auth-screen");
    expect(await page.$(".command-menu")).toBeNull();
    await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
    expect(await page.$(".command-menu")).toBeNull();
    expect((await read()).status).toBe(401);
  } finally { await context.close(); }
}, 30_000);
