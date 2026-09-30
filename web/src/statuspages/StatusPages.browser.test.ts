/**
 * The status page editor, measured in a real browser (SUB-153).
 *
 * The settings screens elsewhere wait for the password field, which renders
 * before this card's lazily loaded chunk arrives, so they never see it. This
 * file waits for the card's own rows, then opens both drawers and checks what
 * jsdom cannot: no sideways scroll on a phone, 24px targets, and axe in both
 * themes, scoped to the card and the open drawer.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let server: Server;
let browser: Browser;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

const settle = (page: Page) => page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations().map((animation) => animation.finished.catch(() => undefined)));
  await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
});

async function open(width: number, theme: string): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1, isMobile: width < 640 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.goto(server.url + "/settings#status-pages", { waitUntil: "domcontentloaded" });
    await page.waitForSelector('#status-pages ul[aria-label="Status pages"] li', { timeout: 15_000 });
    await settle(page);
  } catch (err) {
    await page.close();
    throw err;
  }
  return page;
}

type Drawer = "none" | "settings" | "services";

async function openDrawer(page: Page, drawer: Drawer): Promise<void> {
  if (drawer === "none") return;
  const name = drawer === "settings" ? "Settings for Acme for customers" : "Services on Acme for customers";
  await page.click(`#status-pages button[aria-label="${name}"]`);
  await page.waitForSelector(`.drawer-panel form[aria-label="${name}"]`, { visible: true });
  await settle(page);
}

/** Axe over the card and whichever drawer is open; the rest of /settings has its own audit. */
async function audit(page: Page) {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async () => {
    const include = ["#status-pages", ".drawer-panel"].filter((selector) => document.querySelector(selector));
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include: include.map((s) => [s]) }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target.join(" ")) }));
  });
}

describe.each(["light", "dark"])("%s theme", (theme) => {
  it.each<Drawer>(["none", "settings", "services"])("passes axe with drawer: %s", async (drawer) => {
    const page = await open(1440, theme);
    try {
      await openDrawer(page, drawer);
      expect(await audit(page)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});

describe.each([320, 375, 414])("at %ipx", (width) => {
  it.each<Drawer>(["none", "settings", "services"])("fits the phone with drawer: %s", async (drawer) => {
    const page = await open(width, "dark");
    try {
      await openDrawer(page, drawer);
      const seen = await page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        const scope = [...document.querySelectorAll<HTMLElement>("#status-pages, .drawer-panel")];
        const controls = scope.flatMap((root) => [...root.querySelectorAll<HTMLElement>("button, a[href], input, select, textarea, [role='button']")]);
        const outside = scope.flatMap((root) => [...root.querySelectorAll<HTMLElement>("*")])
          .filter((el) => { const r = el.getBoundingClientRect(); return (r.width > 0 || r.height > 0) && (r.left < -1 || r.right > vw + 1); })
          .map((el) => `${el.tagName.toLowerCase()}.${el.className}`);
        const small = controls.filter((el) => {
          if (el.hasAttribute("disabled")) return false;
          const r = el.getBoundingClientRect();
          return (r.width > 0 || r.height > 0) && (r.width < 24 || r.height < 24);
        }).map((el) => (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40));
        return { scrollWidth: document.documentElement.scrollWidth, clientWidth: vw, outside, small };
      });
      expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth, outside: [], small: [] });
    } finally {
      await page.close();
    }
  });
});
