/**
 * The dashboard served under a path prefix, the way a reverse proxy that
 * maps https://example.com/sg/ to SubGlance and strips the prefix serves it.
 *
 * `--base-url https://example.com/sg` is documented, and every alert links
 * to `<base>/incidents#…` and `<base>/monitors/<id>`, so the dashboard has to
 * open at those addresses. Before this it could not: the shell asked for
 * `/assets/…` and `/fonts/…`, every request went to `/api/…` and the router
 * read `/sg/monitors` as a page it does not have. Each of those is a request
 * outside the prefix, which a proxy like that never sees, so the harness
 * answers anything outside `/sg/` with a 404 and this file fails on the first
 * request that leaves it, as well as on the screens it should reach.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

const PREFIX = "/sg";

let server: Server;
let browser: Browser;

beforeAll(async () => {
  server = await serveBuild({ prefix: PREFIX });
  browser = await chromium();
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

/** A page that records every request it makes and every one that failed. */
async function watched(): Promise<{ page: Page; requests: string[]; failed: string[] }> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  const requests: string[] = [];
  const failed: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.protocol === "http:") requests.push(url.pathname);
  });
  page.on("response", (response) => {
    const path = new URL(response.url()).pathname;
    if (response.status() >= 400 && path !== "/favicon.ico") failed.push(`${response.status()} ${path}`);
  });
  page.on("requestfailed", (request) => {
    /*
     * A stream the dashboard closes itself — on a reload, or when the screen
     * that holds it unmounts — ends as an aborted request. That is the client
     * hanging up, not a request that could not reach the server.
     */
    if (request.failure()?.errorText === "net::ERR_ABORTED") return;
    failed.push(`failed ${new URL(request.url()).pathname} ${request.failure()?.errorText}`);
  });
  return { page, requests, failed };
}

/*
 * Requests that leave the prefix. `/favicon.ico` is not one the dashboard
 * makes: the browser asks the root of the host for it by itself, because the
 * shell names no icon.
 */
const outside = (requests: string[]) =>
  requests.filter((path) => !path.startsWith(`${PREFIX}/`) && path !== "/favicon.ico");

it("opens an alert's monitor link two levels deep, with its bundle, fonts, API and stream under the prefix", async () => {
  const { page, requests, failed } = await watched();
  try {
    await page.goto(`${server.url}${PREFIX}/monitors/1`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".mon-detail-windows", { timeout: 15_000 });
    expect(await page.$eval("h1", (h) => h.textContent)).toBe(
      "checkout-api-eu-west-1.internal.acme-corporation.example",
    );
    // The faces arrived: a fallback face would pass every other assertion.
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    expect(await page.evaluate(() => document.fonts.check("15px InterVariable"))).toBe(true);
    expect(requests).toContain(`${PREFIX}/api/v1/stream`);
    expect(requests.some((path) => path.startsWith(`${PREFIX}/assets/`))).toBe(true);
    expect(requests.some((path) => path.startsWith(`${PREFIX}/fonts/`))).toBe(true);
    expect(outside(requests)).toEqual([]);
    expect(failed).toEqual([]);
  } finally {
    await page.close();
  }
});

it("links, navigates and goes back inside the prefix", async () => {
  const { page, requests, failed } = await watched();
  try {
    await page.goto(`${server.url}${PREFIX}/`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("[data-testid^='monitor-row-']", { timeout: 15_000 });

    const hrefs = await page.$$eval("nav[aria-label='Primary'] a", (links) => links.map((a) => a.getAttribute("href")));
    expect(hrefs.length).toBeGreaterThan(0);
    for (const href of hrefs) expect(href, `${href} leaves the prefix`).toMatch(/^\/sg\//);
    const monitorLink = await page.$eval("[data-testid='monitor-row-1'] a[href]", (a) => a.getAttribute("href"));
    expect(monitorLink).toBe(`${PREFIX}/monitors/1`);

    await page.click(`nav[aria-label='Primary'] a[href='${PREFIX}/incidents']`);
    await page.waitForSelector(".inc-line", { timeout: 15_000 });
    expect(await page.evaluate(() => location.pathname)).toBe(`${PREFIX}/incidents`);
    expect(await page.$eval("h1", (h) => h.textContent)).toBe("Incidents");

    await page.goBack();
    await page.waitForSelector("[data-testid^='monitor-row-']", { timeout: 15_000 });
    expect(await page.evaluate(() => location.pathname)).toBe(`${PREFIX}/`);
    expect(await page.$eval("h1", (h) => h.textContent)).toBe("Dashboard");

    // A reload of a route the router pushed, which the server has to answer
    // with a shell whose references still lead back under the prefix.
    await page.click(`nav[aria-label='Primary'] a[href='${PREFIX}/notifications']`);
    await page.waitForSelector(".inv-row", { timeout: 15_000 });
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector(".inv-row", { timeout: 15_000 });

    expect(outside(requests)).toEqual([]);
    expect(failed).toEqual([]);
  } finally {
    await page.close();
  }
});

it("names an unknown address by its path inside the prefix and links back to the dashboard there", async () => {
  const { page } = await watched();
  try {
    await page.goto(`${server.url}${PREFIX}/nowhere/at/all`, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => document.querySelector("main code")?.textContent === "/nowhere/at/all", { timeout: 15_000 });
    const back = await page.$$eval("main a.button", (links) => links.map((a) => a.getAttribute("href")));
    expect(back).toContain(`${PREFIX}/`);
  } finally {
    await page.close();
  }
});
