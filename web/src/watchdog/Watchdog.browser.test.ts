import { afterAll, beforeAll, expect, it, vi } from "vitest";
import axe from "axe-core";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import type { WatchdogState } from "./api";

const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string, browser: Browser, harness: Server;
let receiverStatus = 204;
const received: { event: string; body: string }[] = [];
const children: ChildProcess[] = [];
const receiver = createServer(async (req, res) => {
  let body = ""; for await (const chunk of req) body += chunk;
  received.push({ event: String(req.headers["x-subglance-event"]), body });
  res.writeHead(receiverStatus); res.end("PRIVATE_RESPONSE_BODY");
});
type Instance = { url: string; cookie: string };
let disabled: Instance, configured: Instance, waiting: Instance;

async function freePort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address(); if (!addr || typeof addr === "string") throw new Error("no port");
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return addr.port;
}
async function start(binary: string, name: string, watchdogURL?: string, interval = "1s"): Promise<Instance> {
  const port = await freePort();
  const args = ["--addr", `127.0.0.1:${port}`, "--data-dir", join(dir, name)];
  if (watchdogURL) args.push("--watchdog-url", watchdogURL, "--watchdog-interval", interval);
  const child = spawn(binary, args, { stdio: "ignore" }); children.push(child);
  const url = `http://127.0.0.1:${port}`;
  await vi.waitFor(async () => expect((await fetch(`${url}/health`)).status).toBe(200), { timeout: 15_000 });
  const setup = await fetch(`${url}/api/v1/setup`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: "browser@example.test", password: "local-browser-test-only-123" }) });
  expect(setup.status).toBe(201);
  const cookie = setup.headers.get("set-cookie")!.split(";")[0];
  return { url, cookie };
}
beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "watchdog-browser-"));
  const binary = join(dir, "subglance");
  await promisify(execFile)("go", ["build", "-p", "1", "-o", binary, "./cmd/subglance"], { cwd: root });
  await new Promise<void>((resolve) => receiver.listen(0, "127.0.0.1", resolve));
  const addr = receiver.address(); if (!addr || typeof addr === "string") throw new Error("no receiver");
  const target = `http://127.0.0.1:${addr.port}/PRIVATE_PATH?key=PRIVATE_TOKEN`;
  disabled = await start(binary, "disabled");
  configured = await start(binary, "configured", target);
  waiting = await start(binary, "waiting", target, "1h");
  harness = await serveBuild();
  browser = await chromium();
}, 120_000);
afterAll(async () => {
  await browser?.close(); await harness?.close();
  for (const child of children) {
    if (child.exitCode !== null) continue;
    const stopped = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.kill("SIGINT");
    const kill = setTimeout(() => child.kill("SIGKILL"), 12_000);
    await stopped; clearTimeout(kill);
  }
  receiver.closeAllConnections();
  await new Promise<void>((resolve) => receiver.close(() => resolve()));
  if (dir) await rm(dir, { recursive: true, force: true });
});
async function pageAt(instance: Instance, path: string, theme: string, width: number) {
  const context = await browser.createBrowserContext();
  const [name, value] = instance.cookie.split("=");
  await context.setCookie({ name, value, domain: "127.0.0.1", path: "/", httpOnly: true, sameSite: "Strict" });
  const page = await context.newPage();
  await page.setViewport({ width, height: 1000 });
  await page.evaluateOnNewDocument((key, value) => {
    localStorage.setItem(key, value);
    // Speed only the real polling callback, never its data source.
    const win: Window = window;
    const interval = win.setInterval.bind(win);
    win.setInterval = (handler, delay, ...args) => interval(handler, delay === 15_000 ? 200 : delay, ...args);
  }, THEME_STORAGE_KEY, theme);
  await page.goto(instance.url + path, { waitUntil: "domcontentloaded" });
  return { context, page };
}
async function proof(page: Page, selector: string, name: string) {
  await page.evaluate(() => document.fonts.ready);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  // Evaluate the audit via DevTools: the real server correctly blocks inline
  // script tags with CSP. Do not weaken that production policy for the test.
  await page.evaluate(axe.source);
  const audit = await page.evaluate(async (selector) => {
    const engine = (window as unknown as { axe: typeof axe }).axe;
    return await engine.run({ include: [selector] }, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] } });
  }, selector);
  expect(audit.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.html) }))).toEqual([]);
  const output = process.env.SUBGLANCE_EVIDENCE_DIR;
  if (output) { await mkdir(output, { recursive: true }); await page.screenshot({ path: join(output, `${name}.png`), fullPage: true }); await writeFile(join(output, `${name}.axe.json`), JSON.stringify(audit, null, 2)); }
}

it.each([["dark", 390], ["light", 390], ["dark", 1440], ["light", 1440]] as const)("real command, outbound receiver, API and UI in %s at %ipx", async (theme, width) => {
  expect((await fetch(`${configured.url}/api/v1/watchdog`)).status).toBe(401);
  receiverStatus = 204;
  const { page, context } = await pageAt(configured, "/settings", theme, width);
  try {
    await page.waitForFunction(() => document.querySelector("#self-monitoring")?.textContent?.includes("Last ping succeeded (HTTP 204)."));
    expect(await page.$eval(".watchdog-card .panel", (el) => getComputedStyle(el).display)).toBe("grid");
    const state = await page.evaluate(async () => {
      const response = await fetch("/api/v1/watchdog");
      return { cache: response.headers.get("Cache-Control"), data: await response.json() as WatchdogState };
    });
    expect(state.cache).toBe("private, no-store");
    expect(state.data.configured).toBe(true);
    expect(state.data.last_success_at).not.toBeNull();
    expect(await page.$eval("#self-monitoring", (el) => el.textContent)).toContain("Configured");
    await proof(page, "#self-monitoring", `watchdog-success-${theme}-${width}`);
    receiverStatus = 403;
    // Freeze the comparison after any already-accepted request has completed.
    await expect.poll(async () => (await (await fetch(`${configured.url}/api/v1/watchdog`, { headers: { Cookie: configured.cookie } })).json() as WatchdogState).last_result).toBe("rejected");
    const rejected = await (await fetch(`${configured.url}/api/v1/watchdog`, { headers: { Cookie: configured.cookie } })).json() as WatchdogState;
    await page.waitForFunction(() => document.querySelector("#self-monitoring")?.textContent?.includes("Last ping rejected (HTTP 403)."));
    expect(await page.$$eval("#self-monitoring time", (els) => els.map((el) => el.dateTime))).toContain(rejected.last_success_at);
    expect(await page.$eval("#self-monitoring", (el) => el.textContent)).not.toMatch(/PRIVATE|http:\/\//);
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).not.toMatch(/PRIVATE|last_success_at|watchdog/);
    await proof(page, "#self-monitoring", `watchdog-rejected-${theme}-${width}`);
    // Filtering never unmounts the existing dirty account form.
    await page.type('input[name="current_password"]', "local-unsaved-draft");
    await page.type('input[aria-label="Search settings"]', "watchdog");
    expect(await page.$eval("#account", (el) => (el as HTMLElement).hidden)).toBe(true);
    expect(await page.$eval("#self-monitoring", (el) => (el as HTMLElement).hidden)).toBe(false);
    await page.focus('input[aria-label="Search settings"]'); await page.keyboard.down("Control"); await page.keyboard.press("A"); await page.keyboard.up("Control"); await page.keyboard.press("Backspace");
    expect(await page.$eval('input[name="current_password"]', (el) => el.value)).toBe("local-unsaved-draft");
  } finally { await context.close(); }

  const off = await pageAt(disabled, "/", theme, width);
  try {
    await off.page.waitForSelector(".watchdog-notice");
    expect(await off.page.$$eval(".watchdog-notice", (els) => els.length)).toBe(1);
    expect(await off.page.$eval(".watchdog-notice", (el) => el.textContent)).toContain("SubGlance cannot report its own outage");
    await off.page.focus(".watchdog-notice a");
    expect(await off.page.$eval(".watchdog-notice a", (el) => document.activeElement === el && getComputedStyle(el).outlineStyle !== "none")).toBe(true);
    await proof(off.page, ".watchdog-notice", `watchdog-disabled-${theme}-${width}`);
  } finally { await off.context.close(); }
  const pending = await pageAt(waiting, "/settings", theme, width);
  try {
    await pending.page.waitForFunction(() => document.querySelector("#self-monitoring")?.textContent?.includes("Waiting for the first ping"));
    expect(await pending.page.$$("#self-monitoring time")).toHaveLength(0);
    await proof(pending.page, "#self-monitoring", `watchdog-waiting-${theme}-${width}`);
  } finally { await pending.context.close(); }
  expect(received.some((r) => r.body === "alive; 0 monitors scheduled, 0 checks completed" && r.event === "alive")).toBe(true);
  expect(received.every((r) => r.body === "alive; 0 monitors scheduled, 0 checks completed" || r.body === "stopped; shutting down cleanly")).toBe(true);
});

// Error/malformed snapshots cannot be produced by the real command without
// breaking it; these are explicitly layout/error-boundary fixtures only.
it.each([["dark", 390], ["light", 390], ["dark", 1440], ["light", 1440]] as const)("unknown API state is not disabled in %s at %ipx", async (theme, width) => {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 1000 });
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.setRequestInterception(true);
    page.on("request", (req) => { if (new URL(req.url()).pathname === "/api/v1/watchdog") void req.respond({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "PRIVATE_URL" }) }); else void req.continue(); });
    await page.goto(harness.url + "/settings", { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => document.querySelector("#self-monitoring")?.textContent?.includes("Watchdog state unavailable."));
    expect(await page.$eval("#self-monitoring", (el) => el.textContent)).not.toMatch(/Not configured|PRIVATE/);
    await proof(page, "#self-monitoring", `watchdog-unknown-${theme}-${width}`);
  } finally { await page.close(); }
});
