import { afterAll, beforeAll, expect, it } from "vitest";
import axe from "axe-core";
import { mkdir, writeFile } from "node:fs/promises";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
let browser: Browser, server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });
async function open(theme: string, width: number, path = "/", deferWrite?: (complete: () => void) => void) {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 1000 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((theme) => localStorage.setItem("subglance:theme", theme), theme);
  await page.setRequestInterception(true);
  const paused = new Set<number>();
  page.on("request", (request) => {
    const url = new URL(request.url());
    const write = url.pathname.match(/^\/api\/v1\/monitors\/(\d+)\/(pause|resume)$/);
    if (write) {
      if (write[2] === "pause") paused.add(Number(write[1])); else paused.delete(Number(write[1]));
      const complete = () => { void request.respond({ status: 204 }); };
      if (deferWrite) deferWrite(complete); else complete();
      return;
    }
    if (url.pathname === "/api/v1/monitors") {
      void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: Array.from({ length: 200 }, (_, i) => ({ id: i + 1, name: `Service ${String(i + 1).padStart(3, "0")}`, type: "http", target: `https://service-${i + 1}.example`, enabled: !paused.has(i + 1), status: "up", interval_s: 60, timeout_s: 10, tags: {} })) }) });
    } else void request.continue();
  });
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".shell-topbar");
  return page;
}
async function launch(page: Page) {
  await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
  await page.waitForSelector(".command-menu[open]");
  await page.waitForSelector('.command-menu [role="option"]');
}
async function search(page: Page, value: string) {
  await page.focus('.command-menu [role="combobox"]');
  await page.keyboard.down("Control"); await page.keyboard.press("a"); await page.keyboard.up("Control");
  await page.keyboard.type(value);
}

it("rejects native modified button activation and restores input focus after pointer writes", async () => {
  const page = await open("dark", 390, "/monitors/new");
  try {
    await page.waitForSelector(".add-form");
    await page.type('input[id$="-name"]', "button draft");
    await launch(page);
    await page.focus(".command-menu > button");
    await page.keyboard.down("Shift"); await page.keyboard.press("Enter"); await page.keyboard.up("Shift");
    expect(await page.$(".command-menu[open]")).toBeTruthy();
    await search(page, "Pause Service 001");
    await page.click('.command-menu [role="option"]');
    await page.waitForFunction(() => !document.querySelector('.command-menu [aria-disabled="true"]'));
    expect(await page.$eval(".command-menu input", (el) => el === document.activeElement)).toBe(true);
    await search(page, "theme");
    await page.keyboard.press("ArrowDown");
    expect(await page.$eval('.command-menu [aria-selected="true"]', (el) => el.textContent)).toBe("Use dark theme");
    await page.keyboard.press("Escape");
    expect(await page.$eval('input[id$="-name"]', (el) => (el as HTMLInputElement).value)).toBe("button draft");
  } finally { await page.close(); }
});

it("navigates every destination by keyboard and System follows live OS theme changes", async () => {
  const page = await open("light", 1440);
  try {
    for (const [name, path] of [["Monitors", "/monitors"], ["Notifications", "/notifications"], ["Incidents", "/incidents"], ["Settings", "/settings"], ["Dashboard", "/"]]) {
      await launch(page); await search(page, `Go to ${name}`); await page.keyboard.press("Enter");
      await page.waitForFunction((path) => location.pathname === path, {}, path);
    }
    await launch(page); await search(page, "Use system theme"); await page.keyboard.press("Enter");
    expect(await page.evaluate(() => localStorage.getItem("subglance:theme"))).toBe("system");
    for (const theme of ["dark", "light"]) {
      await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: theme }]);
      await page.waitForFunction((theme) => document.documentElement.dataset.theme === theme, {}, theme);
    }
  } finally { await page.close(); }
});

it("dismissal during a committed write still refreshes the dashboard after the response", async () => {
  let complete!: () => void;
  const page = await open("dark", 1440, "/", (done) => { complete = done; });
  try {
    await page.waitForSelector('.shell-search-input');
    await launch(page); await search(page, "Pause Service 001"); await page.keyboard.press("Enter");
    await expect.poll(() => typeof complete).toBe("function");
    await page.keyboard.press("Escape");
    await page.waitForSelector(".command-menu", { hidden: true });
    complete();
    await page.waitForFunction(() => [...document.querySelectorAll(".mon-count")].some((el) => el.textContent?.includes("1 paused")));
  } finally { await page.close(); }
});

it.each([ ["dark", 390], ["light", 390], ["dark", 1440], ["light", 1440] ] as const)("keyboard search reaches offscreen monitor 200, fits and passes waiver-free axe (%s/%s)", async (theme, width) => {
  const page = await open(theme, width);
  try {
    await page.focus('.shell-command-launcher');
    await page.keyboard.press("Enter");
    await page.waitForSelector('.command-menu [role="option"]');
    await search(page, "Open Service");
    expect(await page.$$eval('.command-menu [role="option"]', (els) => els.length)).toBe(200);
    for (let i = 0; i < 199; i++) await page.keyboard.press("ArrowDown");
    const shape = await page.evaluate(() => {
      const menu = document.querySelector<HTMLDialogElement>(".command-menu")!;
      const input = menu.querySelector<HTMLInputElement>("input")!;
      const active = document.getElementById(input.getAttribute("aria-activedescendant")!)!;
      const list = menu.querySelector('[role="listbox"]')!;
      const r = menu.getBoundingClientRect(), a = active.getBoundingClientRect(), l = list.getBoundingClientRect();
      const probe = document.createElement("div"); probe.style.backgroundColor = "var(--surface-float)"; probe.style.transition = "none"; menu.append(probe);
      const opaque = getComputedStyle(probe).backgroundColor; probe.remove();
      return { active: active.textContent, focused: document.activeElement === input, visible: a.top >= l.top && a.bottom <= l.bottom + 1, fits: r.left >= 0 && r.right <= innerWidth && r.bottom <= innerHeight, height: r.height, fill: getComputedStyle(menu).backgroundColor, opaque, overflow: document.documentElement.scrollWidth > innerWidth };
    });
    expect(shape.active).toContain("Open Service 200");
    expect(shape.focused).toBe(true);
    expect(shape.visible).toBe(true);
    expect(shape.fits).toBe(true);
    expect(shape.overflow).toBe(false);
    expect(shape.fill).toBe(shape.opaque);
    expect(shape.height).toBeLessThan(800);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => (await (window as unknown as { axe: typeof axe }).axe.run(document.querySelector(".command-menu")!)).violations);
    expect(violations).toEqual([]);
    const out = process.env.SUBGLANCE_EVIDENCE_DIR;
    if (out) { await mkdir(out, { recursive: true }); await page.screenshot({ path: `${out}/commands-${theme}-${width}.png` }); await writeFile(`${out}/commands-${theme}-${width}.json`, JSON.stringify(shape, null, 2)); }
    await page.keyboard.press("Tab");
    expect(await page.$eval('.command-menu > button', (el) => el === document.activeElement)).toBe(true);
    await page.keyboard.press("Tab");
    expect(await page.$eval('.command-menu input', (el) => el === document.activeElement)).toBe(true);
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => location.pathname === "/monitors/200");
    expect(await page.$(".command-menu")).toBeNull();
  } finally { await page.close(); }
}, 30_000);

it.each(["dark", "light"])("Escape only closes the top menu over a dirty drawer; navigation asks exactly once (%s)", async (theme) => {
  const page = await open(theme, 390, "/monitors/new");
  try {
    await page.waitForSelector(".add-form");
    await page.type('input[id$="-name"]', "keep this draft");
    let prompts = 0, accept = false;
    page.on("dialog", async (dialog) => { prompts++; expect(dialog.type()).toBe("confirm"); if (accept) await dialog.accept(); else await dialog.dismiss(); });
    await launch(page);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".command-menu", { hidden: true });
    expect(prompts).toBe(0);
    expect(await page.$eval('input[id$="-name"]', (el) => ({ value: (el as HTMLInputElement).value, focused: document.activeElement === el }))).toEqual({ value: "keep this draft", focused: true });
    await launch(page); await search(page, "Go to Settings"); await page.keyboard.press("Enter");
    await expect.poll(() => prompts).toBe(1);
    expect(new URL(page.url()).pathname).toBe("/monitors/new");
    expect(await page.$eval('input[id$="-name"]', (el) => (el as HTMLInputElement).value)).toBe("keep this draft");
    await launch(page); await search(page, "Open Service 200"); await page.keyboard.press("Enter");
    await expect.poll(() => prompts).toBe(2);
    expect(new URL(page.url()).pathname).toBe("/monitors/new");
    accept = true;
    await launch(page); await search(page, "Go to Settings"); await page.keyboard.press("Enter");
    await page.waitForFunction(() => location.pathname === "/settings");
    expect(prompts).toBe(3);
    expect(await page.$(".add-form")).toBeNull();
  } finally { await page.close(); }
});
