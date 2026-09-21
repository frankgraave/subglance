import { afterAll, beforeAll, expect, it } from "vitest";
import { mkdir, writeFile } from "node:fs/promises";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";

// Command navigation with the actual edit/tag forms and explicit fixture HTTP data.
let browser: Browser, server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });
const monitor = { id: 1, name: "auth", type: "http", target: "https://auth.example.com", interval_s: 60, timeout_s: 10, enabled: true, status: "up", tags: {}, channels: [], repeat_after_s: 0 };
async function open(theme: string, width: number) {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((theme) => localStorage.setItem('subglance:theme', theme), theme);
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
const name = '.drawer-panel input[id$="-name"]';

it('opening the current detail route is a no-op that leaves its draft guarded', async () => {
  const page = await open('dark', 1440);
  try {
    await command(page, 'Open auth');
    await page.waitForSelector('[aria-label="Edit monitor"]');
    await page.click('[aria-label="Edit monitor"]');
    await page.waitForSelector(name);
    await page.type(name, ' kept');
    let prompts = 0;
    page.on('dialog', async (dialog) => { prompts++; await dialog.dismiss(); });
    await command(page, 'Open auth');
    expect(prompts).toBe(0);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe('auth kept');
    await command(page, 'Go to Monitors');
    expect(prompts).toBe(1);
    expect(new URL(page.url()).pathname).toBe('/monitors/1');
  } finally { await page.close(); }
});

it.each([['dark', 390], ['light', 390], ['dark', 1440], ['light', 1440]] as const)('dirty edit cancel/accept tears down exactly the old owner (%s/%s)', async (theme, width) => {
  const page = await open(theme, width);
  try {
    await page.type('.shell-search-input', 'auth');
    const inventory = await page.$('.inv-screen');
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector(name);
    const draft = await page.$(name);
    await page.type(name, ' changed');
    let prompts = 0, accept = false;
    page.on('dialog', async (dialog) => { prompts++; if (accept) await dialog.accept(); else await dialog.dismiss(); });
    for (const action of ['Add monitor', 'Go to Monitors']) {
      await command(page, action);
      expect(prompts).toBe(action === 'Add monitor' ? 1 : 2);
      expect(new URL(page.url()).pathname).toBe('/monitors');
      expect(await titles(page)).toEqual(['Edit auth']);
      expect(await draft!.evaluate((el) => ({ connected: el.isConnected, value: (el as HTMLInputElement).value, focused: el === document.activeElement }))).toEqual({ connected: true, value: 'auth changed', focused: true });
    }
    accept = true;
    await command(page, 'Add monitor');
    expect(prompts).toBe(3);
    expect(await titles(page)).toEqual(['Add monitor']);
    expect(await draft!.evaluate((el) => el.isConnected)).toBe(false);
    expect(await inventory!.evaluate((el) => el.isConnected)).toBe(true);
    expect(await page.$eval('.shell-search-input', (el) => (el as HTMLInputElement).value)).toBe('auth');
    await page.type(name, 'new draft');
    accept = false;
    await page.keyboard.press('Escape');
    expect(prompts).toBe(4);
    expect(await titles(page)).toEqual(['Add monitor']);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe('new draft');
    const evidence = process.env.SUBGLANCE_EVIDENCE_DIR;
    if (evidence) {
      await mkdir(evidence, { recursive: true });
      await page.screenshot({ path: `${evidence}/owner-${theme}-${width}.png` });
      await writeFile(`${evidence}/owner-${theme}-${width}.json`, JSON.stringify({ prompts, titles: await titles(page), path: new URL(page.url()).pathname }));
    }
    accept = true;
    await page.keyboard.press('Escape');
    expect(prompts).toBe(5);
    expect(await titles(page)).toEqual([]);
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector(name);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe('auth');
  } finally { await page.close(); }
});

it('accepting same-route Monitors really removes the edit draft', async () => {
  const page = await open('dark', 1440);
  try {
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector(name);
    await page.type(name, 'discard');
    let prompts = 0;
    page.on('dialog', async (dialog) => { prompts++; await dialog.accept(); });
    await command(page, 'Go to Monitors');
    expect(prompts).toBe(1);
    expect(await titles(page)).toEqual([]);
    await page.click('[aria-label="Edit auth"]');
    await page.waitForSelector(name);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe('auth');
  } finally { await page.close(); }
});

it.each(['Add monitor', 'Go to Monitors'])('bulk tag owner unmounts for %s while filters/selection survive', async (action) => {
  const page = await open('dark', 1440);
  try {
    await page.type('.shell-search-input', 'auth');
    await page.click('.inv-list input[type="checkbox"]');
    await page.click('::-p-text(Manage tags)');
    await page.waitForSelector('.bulk-tags-form');
    await page.type('.bulk-tags-form input', 'unsaved-tag');
    const oldForm = await page.$('.bulk-tags-form');
    await command(page, action);
    expect(await titles(page)).toEqual(action === 'Add monitor' ? ['Add monitor'] : []);
    expect(await oldForm!.evaluate((el) => el.isConnected)).toBe(false);
    if (action === 'Add monitor') await page.click('.drawer-close');
    expect(await page.$eval('.shell-search-input', (el) => (el as HTMLInputElement).value)).toBe('auth');
    expect(await page.$eval('.inv-list input[type="checkbox"]', (el) => (el as HTMLInputElement).checked)).toBe(true);
    await page.click('::-p-text(Manage tags)');
    await page.waitForSelector('.bulk-tags-form');
    expect(await page.$eval('.bulk-tags-form input', (el) => (el as HTMLInputElement).value)).toBe('');
  } finally { await page.close(); }
});