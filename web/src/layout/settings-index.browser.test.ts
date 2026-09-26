/**
 * The settings index, measured in a real browser (SUB-28).
 *
 * jsdom has no layout, so the unit tests can prove which link is current but
 * not the three things the reader sees: the index stays in view while the
 * cards scroll, a followed link lands its card below the sticky masthead
 * rather than under it, and below the tablet rung the index moves above the
 * cards without widening the page.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

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

async function open(width: number, height: number, hash = ""): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.goto(server.url + "/settings" + hash, { waitUntil: "domcontentloaded" });
    await page.waitForSelector('input[name="current_password"]', { timeout: 15_000 });
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  } catch (err) {
    await page.close();
    throw err;
  }
  return page;
}

const box = (page: Page, selector: string) =>
  page.$eval(selector, (el) => {
    const r = el.getBoundingClientRect();
    return { top: Math.round(r.top), left: Math.round(r.left), right: Math.round(r.right), bottom: Math.round(r.bottom) };
  });

describe("at a desktop width", () => {
  it("puts the index left of the cards and keeps it below the masthead while the page scrolls", async () => {
    const page = await open(1280, 600);
    try {
      const index = await box(page, ".settings-index");
      const sections = await box(page, ".settings-sections");
      expect(index.right).toBeLessThanOrEqual(sections.left);
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
      expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
      const bar = await box(page, ".shell-topbar");
      const stuck = await box(page, ".settings-index");
      expect(stuck.top).toBeGreaterThanOrEqual(bar.bottom);
      expect(stuck.top).toBeLessThan(bar.bottom + 40);
    } finally {
      await page.close();
    }
  });

  it("lands a deep link's card below the masthead and marks it current", async () => {
    const page = await open(1280, 600, "#tokens");
    try {
      await page.waitForFunction(() => window.scrollY > 0, { timeout: 5_000 });
      const bar = await box(page, ".shell-topbar");
      const tokens = await box(page, "#tokens");
      expect(tokens.top).toBeGreaterThanOrEqual(bar.bottom);
      expect(await page.$eval('.settings-index [aria-current="true"]', (el) => el.textContent)).toBe("API tokens");
    } finally {
      await page.close();
    }
  });

  it("scrolls to a section when its index link is followed", async () => {
    const page = await open(1280, 600);
    try {
      await page.click('.settings-index a[href="#retention"]');
      await page.waitForFunction(() => location.hash === "#retention" && window.scrollY > 0, { timeout: 5_000 });
      const bar = await box(page, ".shell-topbar");
      const retention = await box(page, "#retention");
      expect(retention.top).toBeGreaterThanOrEqual(bar.bottom);
      expect(retention.top).toBeLessThan(bar.bottom + 40);
    } finally {
      await page.close();
    }
  });
});

it("follows a hand scroll to the last section at the bottom of the page", async () => {
  const page = await open(1280, 600);
  try {
    await page.mouse.move(900, 400);
    for (let i = 0; i < 40; i++) await page.mouse.wheel({ deltaY: 400 });
    // The last link rather than a name: which card is last depends on the
    // session's role and on which settings cards exist.
    await page.waitForFunction(
      () => {
        const links = document.querySelectorAll(".settings-index a");
        return links.length > 0 && links[links.length - 1].getAttribute("aria-current") === "true";
      },
      { timeout: 5_000 },
    );
  } finally {
    await page.close();
  }
});

describe.each([320, 768, 900])("at %ipx", (width) => {
  it("puts the index above the cards without widening the page", async () => {
    const page = await open(width, 800);
    try {
      const index = await box(page, ".settings-index");
      const sections = await box(page, ".settings-sections");
      expect(index.bottom).toBeLessThanOrEqual(sections.top);
      const seen = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
      }));
      expect(seen.scrollWidth).toBe(seen.clientWidth);
    } finally {
      await page.close();
    }
  });
});
