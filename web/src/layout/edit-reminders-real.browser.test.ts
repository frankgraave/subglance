import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import type { ElementHandle } from "puppeteer-core";
import { chromium, type Browser, type Page } from "./harness/browser";

let browser: Browser;
let directory: string;
let binary: string;
beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "subglance-edit-browser-"));
  binary = join(directory, "fixture");
  await promisify(execFile)("go", ["build", "-o", binary, "./internal/api/testdata/edit-reminder-browser"], { cwd: resolve(".."), timeout: 110_000 });
  browser = await chromium();
});
afterAll(async () => { await browser?.close(); if (directory) await rm(directory, { recursive: true, force: true }); });

type Fixture = { url: string; token: string; target: string; monitor_id: number; incident_id: number };
async function startFixture() {
  const child = spawn(binary, [], { stdio: ["pipe", "pipe", "pipe"] });
  const lines = createInterface({ input: child.stdout });
  let errors = "";
  child.stderr.on("data", (data: Buffer) => { errors += data.toString(); });
  const data = await new Promise<Fixture>((accept, reject) => {
    const timer = setTimeout(() => { child.kill(); reject(new Error("Go fixture startup timed out")); }, 15_000);
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("exit", () => { clearTimeout(timer); reject(new Error(`Go fixture exited: ${errors}`)); });
    lines.once("line", (line) => { clearTimeout(timer); try { accept(JSON.parse(line) as Fixture); } catch { reject(new Error("invalid Go fixture bootstrap")); } });
  });
  const api = (path: string, init: RequestInit = {}) => fetch(`${data.url}/api/v1${path}`, { ...init, headers: { Authorization: `Bearer ${data.token}`, "Content-Type": "application/json", ...init.headers } });
  return { ...data, api, close: async () => {
    lines.close();
    if (child.exitCode !== null) return;
    const exited = once(child, "exit");
    child.stdin.end("done\n");
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    await exited; clearTimeout(timer);
  } };
}
async function fill(page: Page, selector: string, value: string) {
  await page.focus(selector); await page.keyboard.down("Control"); await page.keyboard.press("A"); await page.keyboard.up("Control");
  await page.keyboard.press("Backspace"); if (value) await page.keyboard.type(value);
}
async function button(page: Page, label: string) {
  const node = await page.waitForFunction((text) => [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === text), {}, label);
  await (node.asElement() as ElementHandle<HTMLButtonElement>).click();
}
async function edit(page: Page) {
  await button(page, "Edit monitor"); await page.waitForSelector('input[name="name"]');
  await page.evaluate(async () => { await Promise.all(document.getAnimations().filter((a) => a.effect?.getComputedTiming().iterations !== Infinity).map((a) => a.finished.catch(() => undefined))); });
}

it("real Go API persists the edit, previews without history, rejects stale ETags and stops due reminders", async () => {
  const f = await startFixture(); const context = await browser.createBrowserContext(); const page = await context.newPage();
  const path = `/monitors/${f.monitor_id}`;
  try {
    await page.setExtraHTTPHeaders({ Authorization: `Bearer ${f.token}` });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.goto(`${f.url}${path}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".inc-reminders time");
    // Editing and reminders coexist with the independently shipped history
    // query; an unhandled endpoint must not hide behind the ready edit button.
    await page.waitForFunction(() => document.querySelector(".response-history")?.textContent?.includes("Capture was switched off for this check."));
    expect(await page.$('.response-history [role="alert"]')).toBeNull();
    const initial = await f.api(`${path}/incidents`).then((r) => r.json());
    expect(initial.incidents[0].reminder_count).toBe(2);
    expect(await page.$eval(".inc-reminders time", (node) => node.getAttribute("datetime"))).toBe(initial.incidents[0].next_reminder_at);
    expect(await page.$eval(".inc-reminders", (node) => node.textContent)).toContain("2 reminders issued");
    await edit(page);
    await fill(page, '[data-repeat-input]', "59"); await button(page, "Save changes");
    await page.waitForSelector('[data-repeat-input][aria-invalid="true"]');
    expect((await f.api(path).then((r) => r.json())).repeat_after_s).toBe(900);
    await fill(page, '[data-repeat-input]', "731"); await fill(page, 'input[name="name"]', "Real persisted rename");
    await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
    const saved = await f.api(path).then((r) => r.json());
    expect(saved).toMatchObject({ name: "Real persisted rename", repeat_after_s: 731, headers: { "X-Check": "retain-me" }, body: "retained-body", follow_redirects: false });
    await page.reload({ waitUntil: "domcontentloaded" }); await page.waitForSelector(".inc-reminders time"); await edit(page);
    expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe("731");
    const before = await f.api(`${path}/heartbeats`).then((r) => r.json());
    const probes = await fetch(`${f.target}/stats`).then((r) => r.json());
    await fill(page, 'input[name="target"]', `${f.target}/changed`);
    await button(page, "Save changes");
    expect((await f.api(path).then((r) => r.json())).target).toBe(`${f.target}/old`);
    await button(page, "Test it"); await page.waitForSelector(".add-result-bad");
    expect((await fetch(`${f.target}/stats`).then((r) => r.json())).probes).toBe(probes.probes + 1);
    expect(await f.api(`${path}/heartbeats`).then((r) => r.json())).toEqual(before);
    expect((await f.api(path).then((r) => r.json())).target).toBe(`${f.target}/old`);
    await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
    expect((await f.api(path).then((r) => r.json())).target).toBe(`${f.target}/changed`);
    await edit(page); await fill(page, 'input[name="name"]', "Must not overwrite");
    const concurrent = await f.api(path);
    const changed = await f.api(path, { method: "PATCH", headers: { "If-Match": concurrent.headers.get("etag")! }, body: JSON.stringify({ name: "Concurrent real write", repeat_after_s: 877 }) });
    expect(changed.status).toBe(200);
    const conflict = page.waitForResponse((r) => r.request().method() === "PATCH" && r.status() === 412);
    await button(page, "Save changes"); await conflict;
    await page.waitForFunction(() => document.querySelector('[role="alert"]')?.textContent?.includes("Nothing was overwritten"));
    expect(await page.$eval('input[name="name"]', (node) => (node as HTMLInputElement).value)).toBe("Must not overwrite");
    expect((await f.api(path).then((r) => r.json())).name).toBe("Concurrent real write");
    await button(page, "Reload latest settings");
    await page.waitForFunction(() => (document.querySelector('input[name="name"]') as HTMLInputElement)?.value === "Concurrent real write");
    expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe("877");
    await page.select('.repeat-field select', "off"); await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
    await page.waitForFunction(() => document.querySelector('.inc-reminders')?.textContent?.includes("Do not repeat"));
    expect((await f.api(`${path}/incidents`).then((r) => r.json())).incidents[0]).toMatchObject({ reminder_count: 2, reminder_status: "disabled", next_reminder_at: null });
    await page.reload({ waitUntil: "domcontentloaded" }); await page.waitForSelector('.inc-reminders'); await edit(page);
    expect(await page.$eval('.repeat-field select', (node) => (node as HTMLSelectElement).value)).toBe("off");
    await button(page, "Cancel"); await page.click('button[aria-label^="Mute repeat alerts for"]');
    await page.waitForFunction(() => document.querySelector('.inc-reminders')?.textContent?.includes("incident acknowledged"));
    expect((await f.api(`${path}/incidents`).then((r) => r.json())).incidents[0]).toMatchObject({ reminder_count: 2, reminder_status: "acknowledged", next_reminder_at: null });
  } finally { await context.close(); await f.close(); }
});

it.each([0, 60, 731, 86400])("real Go create persists repeat base %s across reload", async (value) => {
  const f = await startFixture(); const context = await browser.createBrowserContext(); const page = await context.newPage();
  try {
    await page.setExtraHTTPHeaders({ Authorization: `Bearer ${f.token}` });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.goto(`${f.url}/monitors/new`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector('.add-form');
    await fill(page, '.add-form input[placeholder^="example.com"]', `${f.target}/created`);
    await page.click('.add-advanced summary');
    await page.select('.add-advanced select', "http");
    await fill(page, '[data-repeat-input]', String(value));
    const created = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/api/v1/monitors");
    await button(page, "Save monitor"); const response = await created;
    expect(response.status()).toBe(201); const { id } = await response.json();
    const path = `/monitors/${id}`;
    expect((await f.api(path).then((r) => r.json())).repeat_after_s).toBe(value);
    await page.goto(`${f.url}${path}`, { waitUntil: "domcontentloaded" }); await edit(page);
    if (value === 0) expect(await page.$eval('.repeat-field select', (node) => (node as HTMLSelectElement).value)).toBe("off");
    else expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe(String(value));
  } finally { await context.close(); await f.close(); }
});
