import { afterAll, beforeAll, expect, it } from "vitest";
import axe from "axe-core";
import { mkdir, writeFile } from "node:fs/promises";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
let browser: Browser, server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });
/*
 * The menu's navigation and theme commands are there the moment it opens, but
 * its monitor commands arrive with the inventory, which is fetched when the
 * menu opens. Under the load of the full browser suite that gap was once wide
 * enough for a count read straight after typing to find 0 of 200 options
 * (SUB-169). So every helper waits for the state it depends on, and the
 * fixture holds the inventory back: a helper that samples instead of waiting
 * then fails on every run, not once in several hundred.
 */
const inventoryDelayMs = 250;
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
      setTimeout(() => void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: Array.from({ length: 200 }, (_, i) => ({ id: i + 1, name: `Service ${String(i + 1).padStart(3, "0")}`, type: "http", target: `https://service-${i + 1}.example`, enabled: !paused.has(i + 1), status: "up", interval_s: 60, timeout_s: 10, tags: {} })) }) }), inventoryDelayMs);
    } else void request.continue();
  });
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".shell-topbar");
  return page;
}
async function launch(page: Page) {
  await page.keyboard.down("Control"); await page.keyboard.press("k"); await page.keyboard.up("Control");
  await page.waitForSelector(".command-menu[open]");
  await inventoryListed(page);
}
// An option is not enough: the static commands render before the inventory.
// Only monitor commands start with "Open ".
async function inventoryListed(page: Page) {
  await page.waitForFunction(() => [...document.querySelectorAll('.command-menu [role="option"]')].some((el) => el.textContent?.startsWith("Open ")));
}
async function search(page: Page, value: string) {
  await page.focus('.command-menu [role="combobox"]');
  await page.keyboard.down("Control"); await page.keyboard.press("a"); await page.keyboard.up("Control");
  await page.keyboard.type(value);
}

it.each(["dark", "light"])("uses the surface-specific focus ring in %s mode", async (theme) => {
  const page = await open(theme, 1440);
  try {
    await launch(page);
    for (const selector of [".command-menu input", ".command-foot > button"]) {
      if (selector.endsWith("button")) await page.keyboard.press("Tab");
      await page.waitForFunction((selector) => !document.querySelector(selector)!.getAnimations().some((animation) => animation.playState === "running"), {}, selector);
      const ring = await page.$eval(selector, (el, theme) => {
        const probe = document.createElement("span");
        probe.style.color = `var(${theme === "dark" ? "--accent-ring-dark" : "--accent-ring"})`;
        el.parentElement!.append(probe);
        const expectedColor = getComputedStyle(probe).color;
        probe.remove();
        const style = getComputedStyle(el);
        return { focused: el === document.activeElement && el.matches(":focus-visible"),
          color: style.outlineColor, expectedColor, width: style.outlineWidth,
          expectedWidth: style.getPropertyValue("--outline-ring-w").trim(), style: style.outlineStyle };
      }, theme);
      expect(ring.focused).toBe(true);
      expect(ring.color).toBe(ring.expectedColor);
      expect(ring.width).toBe(ring.expectedWidth);
      expect(ring.style).toBe("solid");
    }
  } finally { await page.close(); }
});

it("rejects native modified button activation and restores input focus after pointer writes", async () => {
  const page = await open("dark", 390, "/monitors/new");
  try {
    await page.waitForSelector(".form-column");
    await page.type('input[id$="-name"]', "button draft");
    await launch(page);
    await page.focus(".command-foot > button");
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
    await inventoryListed(page);
    await search(page, "Open Service");
    await expect.poll(() => page.$$eval('.command-menu [role="option"]', (els) => els.length), { timeout: 5_000 }).toBe(200);
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
    expect(await page.$eval('.command-foot > button', (el) => el === document.activeElement)).toBe(true);
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
    await page.waitForSelector(".form-column");
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
    expect(await page.$(".form-column")).toBeNull();
  } finally { await page.close(); }
});

it.each([390, 1440])("draws groups on the section role, centres each lamp and shows key hints only with a keyboard (%s)", async (width) => {
  const page = await open("dark", width);
  try {
    await launch(page);
    const shape = await page.evaluate(() => {
      const menu = document.querySelector(".command-menu")!;
      const label = menu.querySelector(".command-group")!;
      const probe = document.createElement("span"); probe.style.fontSize = "var(--type-section)"; menu.append(probe);
      const section = getComputedStyle(probe).fontSize; probe.remove();
      const option = menu.querySelector(".command-monitor")!;
      const lamp = option.querySelector(".led")!.getBoundingClientRect(), row = option.getBoundingClientRect();
      const keys = menu.querySelector(".command-keys")!;
      const foot = menu.querySelector(".command-foot")!.getBoundingClientRect();
      return { label: label.textContent, labelSize: getComputedStyle(label).fontSize, section,
        lampOffset: Math.abs((lamp.top + lamp.bottom) / 2 - (row.top + row.bottom) / 2),
        keys: getComputedStyle(keys).display, footFits: foot.left >= 0 && foot.right <= innerWidth,
      };
    });
    expect(shape.label).toBe("Monitors");
    expect(shape.labelSize).toBe(shape.section);
    expect(shape.lampOffset).toBeLessThanOrEqual(1);
    expect(shape.keys).toBe(width < 640 ? "none" : "flex");
    expect(shape.footFits).toBe(true);
  } finally { await page.close(); }
});
