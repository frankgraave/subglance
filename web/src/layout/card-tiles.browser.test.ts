/**
 * Every card on every screen carries an icon tile, in the real build (SUB-167).
 *
 * `Card`'s `icon` prop is required, so the type already refuses a card without
 * one. That covers `<Card>` in source. It does not cover what reaches the
 * page: markup that borrows the `card` class without the component, or a
 * header that loses its tile to a CSS rule. This walks the routes in Chromium
 * and counts, per screen, the cards and the tiles in their headers, so a
 * screen that renders an untiled card fails here by name.
 *
 * Every layout of the dashboard is walked, because rows, cards and compact
 * lines each render their own cards, and the settings page is walked as an
 * administrator (the harness session), which is the role that sees all
 * eleven cards.
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

type Census = { cards: number; untiled: string[] };

/**
 * Opens a route and waits until its cards have stopped arriving: several are
 * lazy or wait on a fetch, so the count is taken once two samples 300ms
 * apart agree, not on the first paint.
 */
async function census(path: string, layout?: string): Promise<Census> {
  const page: Page = await browser.newPage();
  try {
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    if (layout !== undefined) {
      await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
      await page.evaluate((value: string) => localStorage.setItem("subglance:layout", value), layout);
    }
    // `domcontentloaded`: the dashboard holds an SSE stream open.
    await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".card", { timeout: 15_000 });
    let last = -1;
    for (let tries = 0; tries < 20; tries += 1) {
      await new Promise((resolve) => setTimeout(resolve, 300));
      const loading = await page.evaluate(() => /Loading [a-z ]+…/.test(document.body.innerText));
      const count = await page.$$eval(".card", (cards) => cards.length);
      if (count === last && !loading) break;
      last = count;
    }
    return await page.$$eval(".card", (cards) => ({
      cards: cards.length,
      untiled: cards
        .filter((card) => card.querySelector(":scope > .card-head .icon-tile svg") === null)
        .map((card) => card.querySelector(".card-title")?.textContent ?? "(untitled)"),
    }));
  } finally {
    await page.close();
  }
}

describe("every card has an icon tile", () => {
  it.each([
    ["/", "rows"],
    ["/", "cards"],
    ["/", "compact"],
    ["/monitors", undefined],
    ["/monitors/1", undefined],
    ["/incidents", undefined],
    ["/notifications", undefined],
    ["/settings", undefined],
  ])("on %s (layout %s)", async (path, layout) => {
    const { cards, untiled } = await census(path, layout);
    expect(cards).toBeGreaterThan(0);
    expect(untiled).toEqual([]);
  });

  it("on settings, all eleven administrator cards", async () => {
    // The screen this ticket was filed against. Eleven, not "more than
    // zero": a lazy card still on its fallback would otherwise pass.
    const { cards, untiled } = await census("/settings");
    expect(cards).toBe(11);
    expect(untiled).toEqual([]);
  });
});
