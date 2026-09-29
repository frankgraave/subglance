/**
 * SUB-165: the workbench is fetched when it opens, not with the app.
 *
 * The fixture galleries are a developer tool behind one button, and shipping
 * them in the entry chunk spent about 3 kB gzip of the entry budget on every
 * visitor. They now load through `lazy()`. Two claims only a real browser on
 * the real build can check:
 *
 *   1. Loading the dashboard does not request the galleries' chunk. A static
 *      import anywhere would fold the module back into the entry and the
 *      chunk would never be requested at all, which fails claim 2 instead.
 *   2. Opening the workbench requests it and renders what it holds, so the
 *      split did not leave the button pointing at a fallback forever.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

let server: Server;
let browser: Browser;
let page: Page;
const requested: string[] = [];

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  page = await browser.newPage();
  page.on("request", (req) => requested.push(new URL(req.url()).pathname));
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  // Not networkidle: the dashboard holds its event stream open, so the
  // network is never idle. The rendered dashboard is the settled state.
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector('[aria-label="Component workbench"]', {
    timeout: 15_000,
  });
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

const galleryChunks = () =>
  requested.filter((path) => /^\/assets\/WorkbenchGalleries-[^/]+\.js$/.test(path));

it("does not fetch the workbench galleries on first load", () => {
  // Proof the listener saw the load at all: the entry chunk is there.
  expect(requested.some((path) => /^\/assets\/index-[^/]+\.js$/.test(path))).toBe(true);
  expect(galleryChunks()).toEqual([]);
});

it("fetches and renders them when the workbench opens", async () => {
  await page.click('[aria-label="Component workbench"]');
  await page.waitForFunction(
    () =>
      Array.from(document.querySelectorAll("h3")).some(
        (h) => h.textContent?.trim() === "Sizes",
      ),
    { timeout: 15_000 },
  );
  expect(galleryChunks()).toHaveLength(1);
}, 30_000);
