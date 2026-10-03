/**
 * The add and edit monitor forms are fetched when a drawer asks for them.
 *
 * They used to load with the entry bundle although nobody needs them until
 * Add or Edit is pressed. They now come from `monitors/LazyMonitorForms.tsx`
 * through `lazy()`. Three claims only a real browser on the real build can
 * check:
 *
 *   1. Loading the monitors screen requests neither form's chunk. A static
 *      import anywhere would fold the module back into the entry, and the
 *      chunk would then never be requested at all, which fails claim 2.
 *   2. Opening each drawer requests its chunk and renders the form, so the
 *      split did not leave a drawer showing its fallback forever.
 *   3. The drawer does not move when the form arrives. The chunk is held
 *      back on purpose, so the loading line is measured on screen, and the
 *      drawer and its title are the same boxes before and after.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, expect, it } from "vitest";
import type { HTTPRequest } from "puppeteer-core";
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

const ADD_CHUNK = /^\/assets\/AddMonitor-[^/]+\.js$/;
const EDIT_CHUNK = /^\/assets\/EditMonitorForm-[^/]+\.js$/;

/**
 * A page that records every path it requests, and holds back any request
 * whose path matches `hold` until `release` is called.
 */
async function recordingPage(hold?: RegExp) {
  const page = await browser.newPage();
  const requested: string[] = [];
  const held: HTTPRequest[] = [];
  let released = false;
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.setRequestInterception(true);
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    requested.push(path);
    if (hold !== undefined && !released && hold.test(path)) {
      held.push(request);
      return;
    }
    void request.continue();
  });
  const release = async () => {
    released = true;
    await Promise.all(held.splice(0).map((request) => request.continue()));
  };
  return { page, requested, held, release };
}

async function openMonitors(page: Page) {
  // Not networkidle: the screens hold an event stream open, so the network is
  // never idle. A rendered inventory row is the settled state.
  await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
  await page.waitForSelector('button[aria-label="Add monitor"]', { timeout: 15_000 });
}

/** The drawer's panel and title, as boxes. */
function frame(page: Page) {
  return page.evaluate(() => {
    const box = (selector: string) => {
      const rect = document.querySelector(selector)!.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    };
    return { panel: box(".drawer-panel"), title: box(".drawer-title"), head: box(".drawer-head") };
  });
}

it("requests neither form's chunk until a drawer opens", async () => {
  const { page, requested } = await recordingPage();
  try {
    await openMonitors(page);
    // Proof the listener saw the load at all: the entry chunk is there.
    expect(requested.some((path) => /^\/assets\/index-[^/]+\.js$/.test(path))).toBe(true);
    expect(requested.filter((path) => ADD_CHUNK.test(path) || EDIT_CHUNK.test(path))).toEqual([]);
  } finally {
    await page.close();
  }
}, 30_000);

it("fetches the add form when Add monitor is pressed, without moving the drawer", async () => {
  const { page, requested, held, release } = await recordingPage(ADD_CHUNK);
  try {
    await openMonitors(page);
    await page.click('button[aria-label="Add monitor"]');
    await page.waitForFunction(
      () => document.querySelector(".drawer-panel")?.textContent?.includes("Loading the form"),
      { timeout: 15_000 },
    );
    expect(held).toHaveLength(1);
    expect(await page.$eval(".drawer-title", (node) => node.textContent)).toBe("Add monitor");
    const loading = await frame(page);

    await release();
    await page.waitForSelector('.drawer-panel input[id$="-name"]', { timeout: 15_000 });
    expect(await frame(page)).toEqual(loading);
    expect(requested.filter((path) => ADD_CHUNK.test(path))).toHaveLength(1);
    expect(requested.filter((path) => EDIT_CHUNK.test(path))).toEqual([]);
  } finally {
    await page.close();
  }
}, 30_000);

it("fetches the edit form when a monitor's Edit is pressed, without moving the drawer", async () => {
  const { page, requested, held, release } = await recordingPage(EDIT_CHUNK);
  try {
    await page.goto(server.url + "/monitors/1", { waitUntil: "domcontentloaded" });
    const edit = await page.waitForSelector('button[aria-label="Edit monitor"]', { timeout: 15_000 });
    await edit!.click();
    await page.waitForFunction(
      () => document.querySelector(".drawer-panel")?.textContent?.includes("Loading the form"),
      { timeout: 15_000 },
    );
    expect(held).toHaveLength(1);
    const loading = await frame(page);

    await release();
    await page.waitForSelector('.drawer-panel input[name="name"]', { timeout: 15_000 });
    expect(await frame(page)).toEqual(loading);
    expect(requested.filter((path) => EDIT_CHUNK.test(path))).toHaveLength(1);
    expect(requested.filter((path) => ADD_CHUNK.test(path))).toEqual([]);
  } finally {
    await page.close();
  }
}, 30_000);
