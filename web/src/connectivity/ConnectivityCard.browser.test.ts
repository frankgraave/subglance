/**
 * The connectivity check's editor, measured in a real browser (SUB-168).
 *
 * jsdom proves what the card sends; this proves what the reader gets: axe in
 * both themes, no sideways scroll on a phone, 24px targets, and a field that
 * does not make iOS zoom, in the states the harness fixture never reaches:
 * both settings pinned, and a refusal standing under the address field.
 *
 * The settings are answered by request interception, because the harness
 * server serves one fixed read.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { defaultConnectivitySettings, pinnedConnectivitySettings } from "./fixtures";

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

type Step = "editable" | "pinned" | "refused";

const settle = (page: Page) => page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations().map((animation) => animation.finished.catch(() => undefined)));
  await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
});

async function open(width: number, theme: string, step: Step): Promise<Page> {
  const page = await browser.newPage();
  try {
    await page.setViewport({ width, height: 900, deviceScaleFactor: 1, isMobile: width < 640 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (new URL(req.url()).pathname !== "/api/v1/settings/connectivity") { void req.continue(); return; }
      if (req.method() === "PUT") {
        void req.respond({ status: 400, contentType: "application/json",
          body: JSON.stringify({ error: "connectivity target \"gateway\": want host:port", field: "targets" }) });
        return;
      }
      void req.respond({ status: 200, contentType: "application/json", headers: { etag: 'W/"1"' },
        body: JSON.stringify(step === "pinned" ? pinnedConnectivitySettings : defaultConnectivitySettings) });
    });
    await page.goto(server.url + "/settings#connectivity", { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#connectivity textarea", { timeout: 15_000 });
    if (step === "refused") {
      // Replaced in place, as typing would leave it: the field's own value
      // setter, then the input event React listens for.
      await page.$eval("#connectivity textarea", (el) => {
        const field = el as HTMLTextAreaElement;
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(field, "gateway");
        field.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await page.click("#connectivity button[type='submit']");
      await page.waitForSelector("#connectivity .field-error", { timeout: 10_000 });
    }
    await settle(page);
  } catch (err) {
    await page.close();
    throw err;
  }
  return page;
}

async function audit(page: Page) {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async () => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include: [["#connectivity"]] }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target.join(" ")) }));
  });
}

const steps: Step[] = ["editable", "pinned", "refused"];

describe.each(["light", "dark"])("%s theme", (theme) => {
  it.each(steps)("passes axe at step: %s", async (step) => {
    const page = await open(1440, theme, step);
    try {
      expect(await audit(page)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});

describe.each([320, 375])("at %ipx", (width) => {
  it.each(steps)("fits the phone at step: %s", async (step) => {
    const page = await open(width, "dark", step);
    try {
      const seen = await page.evaluate(() => {
        const vw = document.documentElement.clientWidth;
        const root = document.querySelector<HTMLElement>("#connectivity")!;
        const outside = [...root.querySelectorAll<HTMLElement>("*")]
          .filter((el) => { const r = el.getBoundingClientRect(); return (r.width > 0 || r.height > 0) && (r.left < -1 || r.right > vw + 1); })
          .map((el) => `${el.tagName.toLowerCase()}.${el.className}`);
        const small = [...root.querySelectorAll<HTMLElement>("button, a[href], input, textarea")].filter((el) => {
          if (el.hasAttribute("disabled")) return false;
          // A checkbox's target is its whole label line, not the drawn box.
          const target = el.matches("input[type='checkbox']") ? el.closest("label") ?? el : el;
          const r = target.getBoundingClientRect();
          return (r.width > 0 || r.height > 0) && (r.width < 24 || r.height < 24);
        }).map((el) => (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40));
        // Under 16px, iOS Safari zooms the page on focus and leaves it scrolled sideways.
        const zooms = [...root.querySelectorAll<HTMLElement>("textarea")]
          .filter((el) => parseFloat(getComputedStyle(el).fontSize) < 16).length;
        return { scrollWidth: document.documentElement.scrollWidth, clientWidth: vw, outside, small, zooms };
      });
      expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth, outside: [], small: [], zooms: 0 });
    } finally {
      await page.close();
    }
  });
});
