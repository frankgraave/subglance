/**
 * The import & export card, measured in a real browser (SUB-163).
 *
 * jsdom proves what the card says; this proves what the reader gets: a real
 * file input that takes a file, a report that fits a phone without sideways
 * scroll, 24px targets, and axe in both themes on each step of the import:
 * the empty card, the dry-run report with a field that needs a secret, a
 * refusal that points into the file, and the push URL shown after an apply.
 *
 * The importer is answered by request interception, because the harness
 * server serves layout fixtures and has no configuration to import into.
 *
 * @vitest-environment node
 */
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { appliedReport, dryRunReport } from "./fixtures";

let server: Server;
let browser: Browser;
let file: string;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  file = join(mkdtempSync(join(tmpdir(), "subglance-config-")), "subglance.yaml");
  writeFileSync(file, "version: 1\nmonitors:\n  - key: shop\n    name: Webshop\n");
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

type Step = "empty" | "report" | "refused" | "reveal";

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
      const url = new URL(req.url());
      if (url.pathname !== "/api/v1/config/import") { void req.continue(); return; }
      const body = step === "refused"
        ? { status: 400, body: { error: "interval_s must be between 20 and 86400", field: "monitors[2].interval_s" } }
        : { status: 200, body: url.searchParams.get("dry_run") === "true" ? dryRunReport : appliedReport };
      void req.respond({ status: body.status, contentType: "application/json", body: JSON.stringify(body.body) });
    });
    await page.goto(server.url + "/settings#configuration", { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#configuration input[type=file]", { timeout: 15_000 });
    if (step !== "empty") {
      const input = await page.$("#configuration input[type=file]");
      await input!.uploadFile(file);
      await page.waitForSelector(step === "refused" ? "#configuration [role=alert]" : "#configuration [role=group]", { timeout: 10_000 });
    }
    if (step === "reveal") {
      const [confirm] = await page.$$("xpath/.//button[normalize-space()='Import subglance.yaml']");
      await confirm.click();
      await page.waitForSelector("#configuration .push-reveal", { timeout: 10_000 });
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
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include: [["#configuration"]] }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target.join(" ")) }));
  });
}

const steps: Step[] = ["empty", "report", "refused", "reveal"];

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
        const root = document.querySelector<HTMLElement>("#configuration")!;
        const outside = [...root.querySelectorAll<HTMLElement>("*")]
          .filter((el) => { const r = el.getBoundingClientRect(); return (r.width > 0 || r.height > 0) && (r.left < -1 || r.right > vw + 1); })
          .map((el) => `${el.tagName.toLowerCase()}.${el.className}`);
        const small = [...root.querySelectorAll<HTMLElement>("button, a[href], input, select, textarea")].filter((el) => {
          if (el.hasAttribute("disabled")) return false;
          const r = el.getBoundingClientRect();
          return (r.width > 0 || r.height > 0) && (r.width < 24 || r.height < 24);
        }).map((el) => (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40));
        return { scrollWidth: document.documentElement.scrollWidth, clientWidth: vw, outside, small };
      });
      expect(seen).toEqual({ scrollWidth: seen.clientWidth, clientWidth: seen.clientWidth, outside: [], small: [] });
    } finally {
      await page.close();
    }
  });
});

it("sends the chosen file's bytes to the dry run, then to the apply", async () => {
  const page = await browser.newPage();
  try {
    const bodies: string[] = [];
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      const url = new URL(req.url());
      if (url.pathname !== "/api/v1/config/import") { void req.continue(); return; }
      bodies.push(`${url.search} ${req.headers()["content-type"]} ${req.postData() ?? ""}`);
      void req.respond({ status: 200, contentType: "application/json",
        body: JSON.stringify(url.searchParams.get("dry_run") === "true" ? dryRunReport : appliedReport) });
    });
    await page.goto(server.url + "/settings#configuration", { waitUntil: "domcontentloaded" });
    const input = await page.waitForSelector("#configuration input[type=file]", { timeout: 15_000 });
    await input!.uploadFile(file);
    const confirm = await page.waitForSelector("xpath/.//button[normalize-space()='Import subglance.yaml']", { timeout: 10_000 });
    await confirm!.click();
    await page.waitForSelector("#configuration .push-reveal");
    const yaml = "version: 1\nmonitors:\n  - key: shop\n    name: Webshop\n";
    expect(bodies).toEqual([`?dry_run=true application/yaml ${yaml}`, ` application/yaml ${yaml}`]);
  } finally {
    await page.close();
  }
});
