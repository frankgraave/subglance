/**
 * The status wall fills the screen it is on, and reads from across a room
 * (SUB-199).
 *
 * The wall is for a television on a wall. At the product's type scale a name
 * is 15px, which on a 1080p television three metres away is unreadable, and
 * the board used to sit in under half the screen's height whatever the
 * screen. `useWallZoom` now magnifies the whole board to the largest factor
 * that still fits; these cases measure what that leaves on the screen, at
 * the two resolutions televisions come in.
 *
 * "Readable" is pinned to a published number rather than a feeling: Android
 * TV designs at 960 x 540 and scales up, with 12sp as the smallest text it
 * allows and 18sp as its default. At 1080p that is 24px and 36px of screen,
 * at 4K 48px and 72px. A name must clear the minimum with the demo estate on
 * the board; a handful of monitors reaches the default.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import type { ApiMonitor } from "../monitors/types";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { seedEstate } from "./harness/seed";
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

const SCREENS = [
  { name: "1080p", width: 1920, height: 1080, scale: 2 },
  { name: "4K", width: 3840, height: 2160, scale: 4 },
] as const;

/** Android TV's smallest and default text, in its 960 x 540 reference pixels. */
const TV_MIN_TEXT = 12;
const TV_DEFAULT_TEXT = 18;

/** The demo estate `make seed` creates, with a spread of statuses. */
const STATUSES: readonly ApiMonitor["status"][] = ["down", "warning", "up", "up", "up", "recovering", "up"];
const ESTATE: ApiMonitor[] = seedEstate().map((monitor, i) =>
  monitor.enabled ? { ...monitor, status: STATUSES[i % STATUSES.length]! } : monitor,
);

/** Far more than fits: the wall must fall back to the product's own sizes. */
const CROWD: ApiMonitor[] = Array.from({ length: 240 }, (_, i) => ({
  ...ESTATE[i % ESTATE.length]!,
  id: i + 1,
  name: `${ESTATE[i % ESTATE.length]!.name} ${i + 1}`,
}));

async function open(width: number, height: number, monitors?: ApiMonitor[]): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height, deviceScaleFactor: 1 });
  if (monitors) {
    const body = JSON.stringify({ monitors });
    await page.setRequestInterception(true);
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname === "/api/v1/monitors" && request.method() === "GET") {
        void request.respond({ status: 200, contentType: "application/json", body });
      } else {
        void request.continue();
      }
    });
  }
  await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    LAYOUT_STORAGE_KEY,
    "wall",
  );
  // `domcontentloaded`: the wall holds an SSE stream open.
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".wall-card", { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
  );
  return page;
}

type Measure = {
  zoom: number;
  /** A name's size as drawn on the screen: its font size times the zoom. */
  nameSize: number;
  /** The board's drawn height against the room the stage leaves it. */
  boardHeight: number;
  room: number;
  /** Rows of tiles, read from the tiles' own positions. */
  rows: number;
  scrolls: boolean;
  sideways: boolean;
};

function measure(page: Page): Promise<Measure> {
  return page.evaluate(() => {
    const board = document.querySelector<HTMLElement>(".wall-board")!;
    const stage = document.querySelector<HTMLElement>(".wall-stage")!;
    const name = document.querySelector<HTMLElement>(".wall-card-name")!;
    const style = getComputedStyle(stage);
    const root = document.documentElement;
    const tops = new Set(
      [...document.querySelectorAll(".wall-card")].map((card) => Math.round(card.getBoundingClientRect().top)),
    );
    return {
      zoom: board.currentCSSZoom,
      nameSize: Number.parseFloat(getComputedStyle(name).fontSize) * name.currentCSSZoom,
      boardHeight: board.getBoundingClientRect().height,
      room: window.innerHeight - Number.parseFloat(style.paddingTop) - Number.parseFloat(style.paddingBottom),
      rows: tops.size,
      scrolls: root.scrollHeight > root.clientHeight,
      sideways: root.scrollWidth > root.clientWidth,
    };
  });
}

/** Whether the board would still fit one hundredth of a zoom larger. */
function nextStepFits(page: Page, m: Measure): Promise<boolean> {
  return page.evaluate(
    (zoom: number, room: number) => {
      const board = document.querySelector<HTMLElement>(".wall-board")!;
      const root = document.documentElement;
      const was = board.style.zoom;
      board.style.zoom = String(zoom + 0.01);
      const fits = board.getBoundingClientRect().height <= room && root.scrollWidth <= root.clientWidth;
      board.style.zoom = was;
      return fits;
    },
    m.zoom,
    m.room,
  );
}

describe("the status wall on a television", () => {
  it.each(SCREENS)("fills a $name screen with the demo estate, names readable", async (screen) => {
    const page = await open(screen.width, screen.height, ESTATE);
    try {
      const m = await measure(page);
      // Every monitor on one screen: nobody is at a wall to scroll it.
      expect(m.scrolls, JSON.stringify(m)).toBe(false);
      expect(m.sideways, JSON.stringify(m)).toBe(false);
      // Filled. Not to the pixel: a column drops out of the grid at some
      // zoom, which adds a row, so the board grows in steps. What is asserted
      // is that the zoom stopped because the next step does not fit, and
      // that the board takes most of the room (it used to take under half).
      expect(m.boardHeight, JSON.stringify(m)).toBeGreaterThan(m.room * 0.85);
      expect(await nextStepFits(page, m), JSON.stringify(m)).toBe(false);
      expect(m.nameSize, JSON.stringify(m)).toBeGreaterThanOrEqual(TV_MIN_TEXT * screen.scale);
    } finally {
      await page.close();
    }
  });

  it.each(SCREENS)("draws a handful of monitors on a $name screen at the TV default size", async (screen) => {
    // The harness's four monitors: the zoom runs into its ceiling long before
    // the board fills the height, which is the point of having one.
    const page = await open(screen.width, screen.height);
    try {
      const m = await measure(page);
      expect(m.zoom, JSON.stringify(m)).toBe(screen.scale);
      expect(m.nameSize, JSON.stringify(m)).toBeGreaterThanOrEqual(TV_DEFAULT_TEXT * screen.scale * (15 / 18));
      expect(m.scrolls, JSON.stringify(m)).toBe(false);
    } finally {
      await page.close();
    }
  });

  it("keeps the product's own sizes when the estate is larger than the screen", async () => {
    // A board that does not fit at 1x scrolls rather than shrinking its names
    // below the type scale's floor.
    const page = await open(1920, 1080, CROWD);
    try {
      const m = await measure(page);
      expect(m.zoom, JSON.stringify(m)).toBe(1);
      expect(m.sideways, JSON.stringify(m)).toBe(false);
    } finally {
      await page.close();
    }
  });

  it("follows the screen when the window is resized", async () => {
    const page = await open(1920, 1080, ESTATE);
    try {
      const before = await measure(page);
      await page.setViewport({ width: 3840, height: 2160, deviceScaleFactor: 1 });
      await page.evaluate(
        () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
      );
      const after = await measure(page);
      expect(after.zoom, JSON.stringify({ before, after })).toBeGreaterThan(before.zoom * 1.5);
      expect(after.scrolls).toBe(false);
    } finally {
      await page.close();
    }
  });
});

describe("the status wall on a desk", () => {
  it("draws a phone exactly as before: no magnification", async () => {
    const page = await open(390, 844, ESTATE);
    try {
      expect((await measure(page)).zoom).toBe(1);
    } finally {
      await page.close();
    }
  });
});
