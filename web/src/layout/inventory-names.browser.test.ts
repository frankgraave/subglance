/**
 * The monitors inventory shows the demo estate's names and addresses whole
 * (SUB-194).
 *
 * The inventory is read to find a monitor, and the name and address are the
 * only way to tell one row from another. At 1440px the row used to give every
 * fixed settings column its width first and the name what was left: 86px
 * for someone who can edit, so "API — health" and "API — checkout" both
 * ended in "API — …" and every address was about ten characters. Now the
 * name and address come first, and this file holds them there.
 *
 * The fixture is the estate `make seed` creates, read out of
 * `cmd/seed/catalogue.go` rather than copied here, so a longer name added to
 * the seed later is measured too. Those names are what a first look at the
 * product shows; `inventory-rows.browser.test.ts` keeps the deliberately
 * over-long harness names and checks they clip cleanly instead.
 *
 * 820 is a tablet beside the expanded sidebar, where the rows wrap; 1024 is
 * a small laptop, still wrapped; 1440 is the desktop, on one line. Both roles,
 * because someone who can edit has a selection box and four actions that come
 * out of the same row.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { seedEstate } from "./harness/seed";
import { serveBuild, type Server } from "./harness/server";

const ESTATE = seedEstate();

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

async function openInventory(width: number, role: "admin" | "viewer"): Promise<Page> {
  const page = await browser.newPage();
  await page.evaluateOnNewDocument(() => localStorage.setItem("subglance:sidebar", "expanded"));
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      await request.abort("blockedbyclient");
    } else if (url.pathname === "/api/v1/auth/me") {
      await request.respond({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: 1, email: "operator@example.com", role, created_at: new Date().toISOString() }),
      });
    } else if (url.pathname === "/api/v1/monitors" && request.method() === "GET") {
      await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: ESTATE }) });
    } else {
      await request.continue();
    }
  });
  await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inv-row", { timeout: 15_000 });
  // The bundled faces are `font-display: block`: measure the shipped face,
  // not the fallback that happened to be on screen first.
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
  );
  return page;
}

describe("the seed estate in the monitors inventory", () => {
  it("is read from the seed catalogue", () => {
    // Twenty-six monitors, one of them paused and three of them push: if the
    // parser stops finding them, every check below passes on nothing.
    expect(ESTATE.length).toBeGreaterThanOrEqual(20);
    expect(ESTATE.some((monitor) => !monitor.enabled)).toBe(true);
    expect(ESTATE.some((monitor) => monitor.type === "push")).toBe(true);
  });

  for (const role of ["admin", "viewer"] as const) {
    describe.each([820, 1024, 1440])(`at %ipx for ${role === "admin" ? "an admin" : "a viewer"}`, (width) => {
      it("shows every name and address whole", async () => {
        const page = await openInventory(width, role);
        try {
          const clipped = await page.evaluate(() =>
            [...document.querySelectorAll<HTMLElement>(".inv-name > a, .inv-sub")]
              // The link is the name's box; the address is its own. An
              // ellipsis means the box is narrower than what it holds.
              .filter((element) => element.scrollWidth > element.clientWidth)
              .map((element) => `${(element.textContent ?? "").trim()} (${element.clientWidth} of ${element.scrollWidth}px)`),
          );
          const rows = await page.evaluate(() => document.querySelectorAll(".inv-row").length);
          expect(rows, "the estate did not render").toBe(ESTATE.length);
          expect(clipped).toEqual([]);
        } finally {
          await page.close();
        }
      });
    });
  }
});
