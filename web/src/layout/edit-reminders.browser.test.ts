import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import type { ElementHandle } from "puppeteer-core";
import { mkdir } from "node:fs/promises";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let browser: Browser; let server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

async function fill(page: Page, selector: string, value: string) {
  await page.focus(selector); await page.keyboard.down("Control"); await page.keyboard.press("A"); await page.keyboard.up("Control");
  await page.keyboard.press("Backspace"); if (value) await page.keyboard.type(value);
}
async function button(page: Page, text: string) {
  const handle = await page.waitForFunction((word) => [...document.querySelectorAll("button")].find((node) => node.textContent?.trim() === word), {}, text);
  await (handle.asElement() as ElementHandle<HTMLButtonElement>).click();
}
async function settle(page: Page) {
  await page.evaluate(async () => { await document.fonts.ready; await Promise.all(document.getAnimations().filter((a) => a.effect?.getComputedTiming().iterations !== Infinity).map((a) => a.finished.catch(() => undefined))); });
}
async function auditNewSurface(page: Page, selector: string) {
  await page.addScriptTag({ content: axe.source });
  const findings = await page.evaluate(async (selector) => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run(document.querySelector(selector)!, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] } });
    return result.violations.map(({ id, nodes }) => ({ id, nodes: nodes.map(({ html, target, failureSummary }) => ({ html, target, failureSummary })) }));
  }, selector);
  expect(findings).toEqual([]);
}
async function proof(page: Page, name: string) {
  const path = process.env.SUBGLANCE_PROOF_DIR;
  if (path) { await mkdir(path, { recursive: true }); await page.screenshot({ path: `${path}/${name}.png`, fullPage: name.startsWith("reminders-") }); }
}

it("the shared harness serves a complete, paired settings read and reminder metadata", async () => {
  const response = await fetch(`${server.url}/api/v1/monitors/1`);
  expect(response.status).toBe(200);
  expect(response.headers.get("etag")).toMatch(/W\/".+"/);
  expect(await response.json()).toMatchObject({ repeat_after_s: 900, headers: {}, body: "", min_tls_version: "1.2" });
  const incidents = await fetch(`${server.url}/api/v1/monitors/1/incidents?limit=20`).then((response) => response.json());
  expect(incidents.incidents[0]).toMatchObject({ reminder_count: 2, reminder_status: "scheduled" });
});

/** A stateful HTTP fixture tests the built browser client, not the Go store.
 * Integration against the real conditional PATCH lives in the delivery run. */
async function session(width: number, theme: string, role = "admin") {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const seed = await fetch(`${server.url}/api/v1/monitors/1`).then((r) => r.json());
  let stored = { ...seed }; let version = 1; let acked = false;
  const writes: { body: Record<string, unknown>; version?: string }[] = [];
  const previews: Record<string, unknown>[] = [];
  const creates: Record<string, unknown>[] = [];
  await page.setViewport({ width, height: 1000 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    const respond = (body: unknown, status = 200) => request.respond({ status, contentType: "application/json", headers: { ETag: `W/"${version}"` }, body: JSON.stringify(body) });
    if (url.origin !== server.url) { await request.abort(); return; }
    if (url.pathname === "/api/v1/auth/me") { await respond({ id: 1, email: "operator@example.com", role }); return; }
    if (url.pathname === "/api/v1/monitors/1") {
      if (request.method() === "PATCH") {
        const body = JSON.parse(request.postData()!); const supplied = request.headers()["if-match"];
        writes.push({ body, version: supplied });
        if (supplied !== `W/"${version}"`) { await respond({ error: "monitor changed" }, 412); return; }
        stored = { ...stored, ...body }; version++;
      }
      await respond(stored); return;
    }
    if (url.pathname === "/api/v1/monitors/preview") {
      const body = JSON.parse(request.postData()!); previews.push(body);
      await respond({ ok: false, checked_at: new Date().toISOString(), latency_ms: 10, type: body.type, target: body.target, error: "connection refused" }); return;
    }
    if (url.pathname === "/api/v1/monitors" && request.method() === "POST") {
      const body = JSON.parse(request.postData()!); creates.push(body); stored = { ...stored, ...body }; version++;
      await respond({ id: 1 }); return;
    }
    if (url.pathname === "/api/v1/incidents/42/ack") { acked = true; await request.respond({ status: 204 }); return; }
    if (url.pathname === "/api/v1/monitors/1/incidents") {
      const data = await fetch(request.url()).then((r) => r.json());
      data.incidents[0] = { ...data.incidents[0], acked, ...(acked ? { acked_at: new Date().toISOString() } : {}), reminder_status: acked ? "acknowledged" : stored.repeat_after_s === 0 ? "disabled" : "scheduled", next_reminder_at: acked || stored.repeat_after_s === 0 ? null : "2026-09-20T12:37:17Z" };
      await respond(data); return;
    }
    if (url.pathname === "/api/v1/monitors") { await respond({ monitors: [stored] }); return; }
    await request.continue();
  });
  await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inc-reminders time");
  return { context, page, writes, previews, creates, stored: () => stored, concurrent: () => { stored = { ...stored, name: "Concurrent name", repeat_after_s: 877 }; version++; } };
}

for (const theme of ["dark", "light"]) for (const width of [390, 1440]) describe(`${theme} ${width}`, () => {
  it("detail edit, arbitrary repeats, preview, conflict reload, dirty guard and ack work by keyboard", async () => {
    const f = await session(width, theme, theme === "light" ? "editor" : "admin"); const { page } = f;
    try {
      expect(await page.$eval(".mon-detail-windows", (node) => node.textContent)).not.toContain("undefined");
      expect(await page.$eval(".inc-reminders time", (node) => node.getAttribute("datetime"))).toBe("2026-09-20T12:37:17Z");
      expect(await page.$eval(".inc-reminders", (node) => node.textContent)).toContain("2 reminders issued");
      await auditNewSurface(page, ".inc-reminders");
      await proof(page, `reminders-${theme}-${width}`);
      await page.focus('button[aria-label="Edit monitor"]'); await page.keyboard.press("Enter");
      await page.waitForSelector('input[name="name"]'); await settle(page);
      expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe("900");
      await fill(page, '[data-repeat-input]', "59"); await button(page, "Save changes");
      await page.waitForSelector('[data-repeat-input][aria-invalid="true"]');
      expect(f.writes).toHaveLength(0);
      expect(await page.$eval('[data-repeat-input]', (node) => node === document.activeElement)).toBe(true);
      await auditNewSurface(page, '[role="dialog"]');
      await fill(page, '[data-repeat-input]', "731"); await fill(page, 'input[name="name"]', "Renamed in detail");
      await proof(page, `edit-${theme}-${width}`);
      await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
      expect(f.writes[0]).toEqual({ body: { name: "Renamed in detail", repeat_after_s: 731 }, version: 'W/"1"' });
      await page.reload({ waitUntil: "domcontentloaded" }); await page.waitForSelector(".inc-reminders time");
      await button(page, "Edit monitor"); await page.waitForSelector('[data-repeat-input]');
      expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe("731");
      await fill(page, 'input[name="target"]', "https://changed.example/health"); await button(page, "Save changes");
      expect(f.writes).toHaveLength(1);
      await button(page, "Test it"); await page.waitForFunction(() => document.querySelector(".add-result")?.textContent?.includes("connection refused"));
      expect(f.previews.at(-1)).toMatchObject({ target: "https://changed.example/health", min_tls_version: "1.2", follow_redirects: true });
      await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
      expect(f.stored().target).toBe("https://changed.example/health");
      await button(page, "Edit monitor"); await page.waitForSelector('input[name="name"]');
      await fill(page, 'input[name="name"]', "Unsaved local draft");
      page.once("dialog", (dialog) => { void dialog.dismiss(); }); await page.keyboard.press("Escape");
      expect(await page.$eval('input[name="name"]', (node) => (node as HTMLInputElement).value)).toBe("Unsaved local draft");
      f.concurrent(); await button(page, "Save changes");
      await page.waitForFunction(() => document.querySelector('[role="alert"]')?.textContent?.includes("Nothing was overwritten"));
      expect(f.stored().name).toBe("Concurrent name");
      expect(await page.$eval('button[type="submit"]', (node) => (node as HTMLButtonElement).disabled)).toBe(true);
      await auditNewSurface(page, '[role="dialog"]'); await proof(page, `conflict-${theme}-${width}`);
      await button(page, "Reload latest settings");
      await page.waitForFunction(() => (document.querySelector('input[name="name"]') as HTMLInputElement)?.value === "Concurrent name");
      expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe("877");
      await page.select('.repeat-field select', "off"); await button(page, "Save changes"); await page.waitForSelector('[role="dialog"]', { hidden: true });
      await page.waitForFunction(() => document.querySelector('.inc-reminders')?.textContent?.includes("Do not repeat"));
      await page.reload({ waitUntil: "domcontentloaded" }); await page.waitForSelector('.inc-reminders');
      await button(page, "Edit monitor"); await page.waitForSelector('.repeat-field select');
      expect(await page.$eval('.repeat-field select', (node) => (node as HTMLSelectElement).value)).toBe("off");
      await button(page, "Cancel");
      await page.click('button[aria-label^="Mute repeat alerts for"]');
      await page.waitForFunction(() => document.querySelector('.inc-reminders')?.textContent?.includes("incident acknowledged"));
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    } finally { await f.context.close(); }
  });
});

it.each([0, 60, 731, 86400])("creates repeat base %s and reads the exact persisted value after reload", async (value) => {
  const f = await session(390, "dark"); const { page } = f;
  try {
    await page.goto(`${server.url}/monitors/new`, { waitUntil: "domcontentloaded" }); await page.waitForSelector('.add-form');
    await fill(page, '.add-form input[placeholder^="example.com"]', "https://created.example");
    await page.click('.add-advanced summary');
    await page.select('.add-advanced select', "http");
    if (value === 0) {
      await fill(page, '[data-repeat-input]', "0");
      expect(await page.$eval('.repeat-field select', (node) => node === document.activeElement)).toBe(true);
    } else await fill(page, '[data-repeat-input]', String(value));
    await button(page, "Save monitor"); await page.waitForSelector('[role="dialog"]', { hidden: true });
    expect(f.creates[0].repeat_after_s).toBe(value);
    await page.goto(`${server.url}/monitors/1`, { waitUntil: "domcontentloaded" }); await page.waitForSelector('.inc-reminders');
    await button(page, "Edit monitor"); await page.waitForSelector('.repeat-field select');
    if (value === 0) expect(await page.$eval('.repeat-field select', (node) => (node as HTMLSelectElement).value)).toBe("off");
    else expect(await page.$eval('[data-repeat-input]', (node) => (node as HTMLInputElement).value)).toBe(String(value));
  } finally { await f.context.close(); }
});

it("viewer has no edit or acknowledgement controls", async () => {
  const f = await session(1440, "light", "viewer");
  try { expect(await f.page.$('button[aria-label="Edit monitor"]')).toBeNull(); expect(await f.page.$('button[aria-label^="Mute repeat alerts"]')).toBeNull(); }
  finally { await f.context.close(); }
});
