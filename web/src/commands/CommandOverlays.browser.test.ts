import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";

// Real compiled App and Chromium; only HTTP data is a fixture.
let browser: Browser, server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });
const monitor = { id: 1, name: "auth", type: "http", target: "https://auth.example.com", interval_s: 60, timeout_s: 10, enabled: true, status: "up", tags: {}, channels: [] };
async function open() {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.setRequestInterception(true);
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/monitors" || path === "/api/v1/monitors/1") {
      void request.respond({ status: 200, contentType: "application/json", headers: { ETag: 'W/"1757606400"' }, body: JSON.stringify(path.endsWith("/1") ? monitor : { monitors: [monitor] }) });
    } else void request.continue();
  });
  await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
  await page.waitForSelector('[aria-label="Edit auth"]');
  return page;
}
async function command(page: Page, text: string) {
  await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
  await page.waitForSelector('.command-menu[open] [role="combobox"]');
  await page.type('.command-menu [role="combobox"]', text);
  await page.keyboard.press("Enter");
  await page.waitForSelector(".command-menu", { hidden: true });
}
async function titles(page: Page) {
  return page.$$eval(".drawer-title", (els) => els.map((el) => el.textContent));
}

it("repeating Add on its current route keeps the draft guarded without a discard", async () => {
  const page = await open();
  try {
    await page.click('[aria-label="Add monitor"]');
    await page.waitForSelector('.drawer-panel input[id$="-name"]');
    await page.type('.drawer-panel input[id$="-name"]', 'retain add');
    let prompts = 0;
    page.on('dialog', async (dialog) => { prompts++; await dialog.accept(); });
    await command(page, 'Add monitor');
    expect(prompts).toBe(0);
    expect(await page.$eval('.drawer-panel input[id$="-name"]', (el) => (el as HTMLInputElement).value)).toBe('retain add');
    await command(page, 'Go to Monitors');
    expect(prompts).toBe(1);
    expect(await titles(page)).toEqual([]);
  } finally { await page.close(); }
});

it("same-route Monitors command dismisses the clean edit owner", async () => {
  const page = await open();
  try {
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector('.drawer-panel');
    await command(page, 'Go to Monitors');
    expect(new URL(page.url()).pathname).toBe('/monitors');
    expect(await titles(page)).toEqual([]);
  } finally { await page.close(); }
});

it("ordinary Add preserves the inventory filters and DOM mount", async () => {
  const page = await open();
  try {
    await page.type('.shell-search-input', 'auth');
    await page.select('.shell-toolbar select', 'http');
    const inventory = await page.$('.inv-screen');
    await page.click('[aria-label="Add monitor"]');
    await page.waitForSelector('.drawer-panel');
    expect(await inventory!.evaluate((el) => el.isConnected)).toBe(true);
    expect(await page.$eval('.shell-search-input', (el) => (el as HTMLInputElement).value)).toBe('auth');
    expect(await page.$eval('.shell-toolbar select', (el) => (el as HTMLSelectElement).value)).toBe('http');
    await page.click('.drawer-close');
    expect(await inventory!.evaluate((el) => el.isConnected)).toBe(true);
  } finally { await page.close(); }
});

it("Add command replaces the clean edit owner instead of stacking drawers", async () => {
  const page = await open();
  try {
    await page.type('.shell-search-input', 'auth');
    await page.select('.shell-toolbar select', 'http');
    await page.click('[aria-label="Edit auth"]');
    await page.waitForFunction(() => document.querySelector('.drawer-title')?.textContent === 'Edit auth');
    await command(page, "Add monitor");
    expect(await titles(page)).toEqual(["Add monitor"]);
    await page.click('.drawer-close');
    expect(await titles(page)).toEqual([]);
    expect(await page.$eval('.shell-search-input', (el) => (el as HTMLInputElement).value)).toBe('auth');
    expect(await page.$eval('.shell-toolbar select', (el) => (el as HTMLSelectElement).value)).toBe('http');
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector('.drawer-panel input[id$="-name"]');
    expect(await page.$eval('.drawer-panel input[id$="-name"]', (el) => (el as HTMLInputElement).value)).toBe('auth');
  } finally { await page.close(); }
});
