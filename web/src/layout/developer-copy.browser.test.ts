/**
 * No developer material in what a user reads (SUB-193), measured on the real
 * build in a real browser.
 *
 * The UI assessment of 1 October found the Notifications page pointing its
 * readers at an internal ticket number, the channel form explaining itself
 * with a Go package path and the design mockup, and a beaker button on every
 * screen leading to a page of fixture monitors. Each was written by somebody
 * who knew the context and forgot the reader did not. This walks every route,
 * with the drawers that hold the longest help text open, and fails on any of
 * the three markers in the text the user can read: the page's text, the
 * names a screen reader announces, tooltips, placeholders and the tab title.
 *
 * The workbench is the one exception, by design: it is a developer tool at
 * an address nothing links to. The last test pins that it is still reachable
 * and still the only place the masthead's beaker used to lead.
 *
 * Source comments are not checked and may name tickets freely; only what
 * reaches the screen counts.
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

/** Every screen, plus the two forms that open over a list at their own address. */
const ROUTES: { path: string; ready: string }[] = [
  { path: "/", ready: "[data-testid^='monitor-row-']" },
  { path: "/monitors", ready: ".inv-list > li" },
  { path: "/monitors/new", ready: ".drawer-panel form" },
  { path: "/monitors/1", ready: ".mon-detail-windows" },
  { path: "/incidents", ready: ".inc-line" },
  { path: "/notifications", ready: ".inv-row" },
  { path: "/notifications/new", ready: ".drawer-panel form" },
  { path: "/settings", ready: "#users li .segmented" },
];

/** Ticket numbers, source paths, and the design prototype. */
const MARKERS = [/\bSUB-\d+/, /\binternal\//, /mockup/i];

async function open(path: string, ready: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  // `domcontentloaded`: the dashboard holds an SSE stream open for the life
  // of the page, so waiting for network silence never returns.
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { timeout: 15_000 });
  return page;
}

/**
 * What a user can read on the page: rendered text (`innerText`, so text in a
 * closed `<details>` or a hidden settings section is skipped, as the eye
 * skips it), every announced name, tooltip and placeholder, and the tab.
 */
function readable(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out = [document.title, document.body.innerText];
    for (const el of Array.from(document.querySelectorAll("[aria-label], [title], [placeholder], [alt]"))) {
      for (const attr of ["aria-label", "title", "placeholder", "alt"]) {
        const value = el.getAttribute(attr);
        if (value) out.push(value);
      }
    }
    // The text of a `<details>` body is read once opened; count it as read.
    for (const d of Array.from(document.querySelectorAll("details"))) out.push(d.textContent ?? "");
    return out;
  });
}

describe("text a user reads", () => {
  it.each(ROUTES)("names no ticket, source path or mockup on $path", async ({ path, ready }) => {
    const page = await open(path, ready);
    try {
      const found = (await readable(page)).flatMap((text) =>
        MARKERS.flatMap((marker) => {
          const hit = text.match(marker);
          if (hit === null || hit.index === undefined) return [];
          return [`${marker}: …${text.slice(Math.max(0, hit.index - 40), hit.index + 40)}…`];
        }),
      );
      expect(found).toEqual([]);
    } finally {
      await page.close();
    }
  });

  it("has no control in the masthead that leads to the workbench", async () => {
    const page = await open("/", "[data-testid^='monitor-row-']");
    try {
      const names = await page.evaluate(() =>
        Array.from(document.querySelectorAll(".shell-topbar button, .shell-topbar a")).map(
          (el) => `${el.getAttribute("aria-label") ?? ""} ${el.getAttribute("title") ?? ""} ${el.textContent ?? ""}`,
        ),
      );
      expect(names.length).toBeGreaterThan(0);
      expect(names.filter((name) => /workbench/i.test(name))).toEqual([]);
    } finally {
      await page.close();
    }
  });

  it("still serves the workbench at its own address", async () => {
    const page = await open("/workbench", ".chip--status");
    try {
      const title = await page.evaluate(() => document.querySelector("h1")?.textContent?.trim());
      expect(title).toBe("Workbench");
    } finally {
      await page.close();
    }
  });
});
