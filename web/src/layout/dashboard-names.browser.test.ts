/**
 * The dashboard shows the demo estate's names whole, and says why a monitor
 * is down in words rather than in its latency column (SUB-194).
 *
 * The rows layout gave its four fixed columns 440px first and the name what
 * was left. Beside the expanded sidebar at 820px that was an 86px cell, and
 * 25 of the 26 seed names ended in an ellipsis ("Postgr…", "TLS —…"). A down
 * monitor's error stood in for its latency, clipped to "unexpecte…" in a
 * 104px column, while the failure kind that says the same thing in three
 * words never reached the dashboard. Now rows give way to cards until the
 * name cell has its floor, and a down row names its failure kind under the
 * name in the incident row's words.
 *
 * The fixture is the estate `make seed` creates, read out of
 * `cmd/seed/catalogue.go`, with the seed's own failure texts on a third of
 * it. 820 is a tablet, 1024 a small laptop and 1440 the desktop; each beside
 * the expanded sidebar and beside the rail, because the navigation is what
 * decides the column.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { causeWords } from "../incidents/story";
import type { ApiMonitor } from "../monitors/types";
import { chromium, type Browser, type Page } from "./harness/browser";
import { seedEstate, seedFailures } from "./harness/seed";
import { serveBuild, type Server } from "./harness/server";
import { RAIL_VETO_MAX_WIDTH, ROWS_NAME_FLOOR, SIDEBAR_VETO_MAX_WIDTH } from "./useMediaQuery";

const FAILURES = seedFailures();

/*
 * Every third seed monitor is down with one of the seed's failures, so each
 * failure kind the seed knows is on screen at once. The last down monitor
 * carries no kind: a failure the server did not class still has to say
 * something, and what it says is its own message.
 */
const ESTATE: ApiMonitor[] = seedEstate().map((monitor, i) => {
  if (i % 3 !== 1 || !monitor.enabled) return monitor;
  const failure = FAILURES[Math.floor(i / 3) % FAILURES.length]!;
  const classed = i < 24;
  return {
    ...monitor,
    status: "down",
    error: failure.message,
    ...(classed ? { failure_kind: failure.cause } : {}),
    incident_id: String(100 + i),
    incident_since: new Date(Date.now() - 20 * 60_000).toISOString(),
  };
});
const DOWN = ESTATE.filter((monitor) => monitor.status === "down");

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

async function openDashboard(width: number, sidebar: "expanded" | "collapsed"): Promise<Page> {
  const page = await browser.newPage();
  await page.evaluateOnNewDocument((state: string) => {
    localStorage.setItem("subglance:sidebar", state);
    localStorage.setItem("subglance:layout", "rows");
  }, sidebar);
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      await request.abort("blockedbyclient");
    } else if (url.pathname === "/api/v1/monitors" && request.method() === "GET") {
      await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ monitors: ESTATE }) });
    } else {
      await request.continue();
    }
  });
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector("[data-testid^='monitor-row-'], [data-testid^='monitor-card-']", { timeout: 15_000 });
  // The bundled faces are `font-display: block`: measure the shipped face.
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
  );
  return page;
}

type Seen = {
  layout: "rows" | "cards";
  shown: number;
  clippedNames: string[];
  causes: { name: string; text: string; title: string; clipped: boolean }[];
  errorsInNumbers: number;
  rowHeights: number[];
  nameCell: number;
  overflow: number;
};

async function look(page: Page): Promise<Seen> {
  return page.evaluate(() => {
    const rows = [...document.querySelectorAll<HTMLElement>(".mon-row")];
    const cards = [...document.querySelectorAll<HTMLElement>(".mon-card")];
    // The name's own box: an ellipsis means it is narrower than what it holds.
    const names = rows.length > 0
      ? rows.map((row) => row.querySelector<HTMLElement>(".mon-name")!)
      : cards.map((card) => card.querySelector<HTMLElement>(".mon-card-name > a")!);
    const cell = document.querySelector<HTMLElement>(".mon-cell--name");
    const padding = cell
      ? parseFloat(getComputedStyle(cell).paddingLeft) + parseFloat(getComputedStyle(cell).paddingRight)
      : 0;
    return {
      layout: rows.length > 0 ? "rows" as const : "cards" as const,
      shown: rows.length > 0 ? rows.length : cards.length,
      clippedNames: names
        .filter((name) => name.scrollWidth > name.clientWidth)
        .map((name) => `${(name.textContent ?? "").trim()} (${name.clientWidth} of ${name.scrollWidth}px)`),
      causes: rows
        .filter((row) => row.dataset.status === "down")
        .map((row) => {
          const chip = row.querySelector<HTMLElement>(".mon-cell--name .mon-error");
          return {
            name: (row.querySelector(".mon-name")?.textContent ?? "").trim(),
            text: (chip?.textContent ?? "").trim(),
            title: chip?.title ?? "",
            clipped: chip ? chip.scrollWidth > chip.clientWidth : false,
          };
        }),
      errorsInNumbers: document.querySelectorAll(".mon-cell--num .mon-error").length,
      rowHeights: [...new Set(rows.map((row) => Math.round(row.getBoundingClientRect().height)))],
      nameCell: cell ? cell.clientWidth - padding : 0,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    };
  });
}

describe("the seed estate on the dashboard", () => {
  it("is read from the seed catalogue, with failures of every seeded kind", () => {
    expect(ESTATE.length).toBeGreaterThanOrEqual(20);
    expect(new Set(DOWN.map((monitor) => monitor.failure_kind).filter(Boolean)).size).toBeGreaterThanOrEqual(6);
    expect(DOWN.some((monitor) => monitor.failure_kind === undefined)).toBe(true);
  });

  for (const sidebar of ["expanded", "collapsed"] as const) {
    describe.each([820, 1024, 1440])(`at %ipx beside the ${sidebar === "expanded" ? "sidebar" : "rail"}`, (width) => {
      it("shows every name whole and says why a monitor is down in words", async () => {
        const page = await openDashboard(width, sidebar);
        try {
          const seen = await look(page);
          expect(seen.shown, "the estate did not render").toBe(ESTATE.length);
          expect(seen.overflow).toBe(0);
          expect(seen.clippedNames).toEqual([]);
          if (seen.layout === "rows") {
            // Every row one height, down rows included: the why sits on the
            // address's line rather than adding one.
            expect(seen.rowHeights).toHaveLength(1);
            expect(seen.errorsInNumbers, "an error is back in a number column").toBe(0);
            const want = DOWN.map((monitor) => ({
              name: monitor.name,
              text: causeWords(monitor.failure_kind) ?? monitor.error!,
              title: monitor.error!,
            }));
            expect(seen.causes.map(({ name, text, title }) => ({ name, text, title })).sort((a, b) => a.name.localeCompare(b.name)))
              .toEqual(want.sort((a, b) => a.name.localeCompare(b.name)));
            // A classed failure is a few words, and they are never cut.
            const classed = new Set(DOWN.filter((monitor) => monitor.failure_kind).map((monitor) => monitor.name));
            expect(seen.causes.filter((cause) => classed.has(cause.name) && cause.clipped)).toEqual([]);
          }
        } finally {
          await page.close();
        }
      });
    });
  }

  it.each([
    ["expanded", SIDEBAR_VETO_MAX_WIDTH],
    ["collapsed", RAIL_VETO_MAX_WIDTH],
  ] as const)("beside the %s navigation, rows come back exactly where a name has its floor", async (sidebar, veto) => {
    const below = await openDashboard(veto, sidebar);
    try {
      expect((await look(below)).layout).toBe("cards");
    } finally {
      await below.close();
    }
    const above = await openDashboard(veto + 1, sidebar);
    try {
      const seen = await look(above);
      expect(seen.layout).toBe("rows");
      expect(seen.overflow).toBe(0);
      // The floor is the name's content box, inside the cell's padding, and
      // the first width with rows gives it that and not a pixel more.
      expect(seen.nameCell).toBeGreaterThanOrEqual(ROWS_NAME_FLOOR);
      expect(seen.nameCell).toBeLessThan(ROWS_NAME_FLOOR + 2);
    } finally {
      await above.close();
    }
  });
});
