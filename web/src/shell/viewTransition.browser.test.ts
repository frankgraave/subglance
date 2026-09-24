/**
 * Opening a monitor morphs in a real browser (SUB-23).
 *
 * jsdom has no View Transitions API, so the unit tests stub it. This checks
 * the part only Chromium can: that the click actually starts a transition,
 * that the monitor's name is a shared element in it (the browser skips the
 * whole transition when two elements hold one name, and would do so without
 * an error), that the name is gone afterwards, and that reduced motion gets
 * the instant swap.
 *
 * Assertions are about animations and elements, never screenshots.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";

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

/** `groups` holds every view-transition pseudo-element that animated. */
type Seen = { started: number; groups: string[]; skipped: number };

/** Records every transition the page starts and the groups it animates. */
async function watchTransitions(page: Page) {
  await page.evaluate(() => {
    const seen = { started: 0, groups: [] as string[], skipped: 0 };
    (window as unknown as { __seen: typeof seen }).__seen = seen;
    const original = document.startViewTransition.bind(document);
    document.startViewTransition = ((update: () => void) => {
      const transition = original(update);
      seen.started += 1;
      transition.ready.then(
        () => {
          for (const animation of document.documentElement.getAnimations({ subtree: true })) {
            const pseudo = (animation.effect as KeyframeEffect | null)?.pseudoElement ?? "";
            if (pseudo.startsWith("::view-transition-")) seen.groups.push(pseudo);
          }
        },
        () => {
          seen.skipped += 1;
        },
      );
      return transition;
    }) as typeof document.startViewTransition;
  });
}

async function settle(page: Page): Promise<Seen> {
  await page.waitForFunction(() => document.getAnimations().length === 0, { timeout: 5_000 });
  return page.evaluate(() => (window as unknown as { __seen: Seen }).__seen);
}

async function namedElements(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      [...document.querySelectorAll<HTMLElement>("*")].filter(
        (el) => el.style.getPropertyValue("view-transition-name") !== "",
      ).length,
  );
}

it.each([375, 1440])("morphs the monitor's name into the page title at %ipx", async (width) => {
  const page = await browser.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "no-preference" }]);
    await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
    const link = "main a[href='/monitors/1']";
    await page.waitForSelector(link, { visible: true, timeout: 15_000 });
    await watchTransitions(page);

    await page.click(link);
    await page.waitForSelector(".mon-detail-name", { timeout: 15_000 });
    const opened = await settle(page);
    expect(opened.started).toBe(1);
    expect(opened.skipped).toBe(0);
    // Both halves of the pair. An old snapshot alone is the row fading out
    // with nothing to land on: what happens when the new screen has not been
    // rendered yet at the moment the browser captures it.
    expect(opened.groups).toContain("::view-transition-old(monitor-title)");
    expect(opened.groups).toContain("::view-transition-new(monitor-title)");
    expect(await namedElements(page)).toBe(0);

    // Back swaps instantly, and the list is clickable at once: the next
    // monitor opens (and morphs) on a click made right after the list shows.
    await page.click(".mon-detail-back");
    await page.waitForSelector(link, { visible: true, timeout: 15_000 });
    await page.click(link);
    await page.waitForSelector(".mon-detail-name", { timeout: 15_000 });
    const again = await settle(page);
    expect(again.started).toBe(2);
    expect(again.skipped).toBe(0);
    expect(await namedElements(page)).toBe(0);
    expect(errors).toEqual([]);
  } finally {
    await page.close();
  }
});

it("keeps the new screen clickable while the title is still moving", async () => {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "no-preference" }]);
    await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
    const link = "main a[href='/monitors/1']";
    await page.waitForSelector(link, { visible: true, timeout: 15_000 });
    await watchTransitions(page);

    // Hold the animation still, so the click below lands mid-transition on
    // every machine rather than only on a slow one.
    await page.click(link);
    await page.waitForFunction(
      () => document.getAnimations().some((a) => a.playState === "running"),
      { timeout: 5_000 },
    );
    await page.evaluate(() => {
      for (const animation of document.getAnimations()) animation.pause();
    });
    await page.click(".mon-detail-back");
    await page.waitForSelector(link, { visible: true, timeout: 5_000 });
    expect(await page.evaluate(() => location.pathname)).toBe("/");
  } finally {
    await page.close();
  }
});

it("swaps instantly under reduced motion", async () => {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
    const link = "main a[href='/monitors/1']";
    await page.waitForSelector(link, { visible: true, timeout: 15_000 });
    await watchTransitions(page);

    await page.click(link);
    await page.waitForSelector(".mon-detail-name", { timeout: 15_000 });
    const seen = await settle(page);
    expect(seen.started).toBe(0);
    expect(await namedElements(page)).toBe(0);
  } finally {
    await page.close();
  }
});
