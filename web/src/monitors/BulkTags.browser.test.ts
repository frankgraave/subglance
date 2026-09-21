// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface } from "node:readline";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { THEME_STORAGE_KEY } from "../theme/theme";

const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string;
let child: ChildProcess;
let browser: Browser;
let fixture: { url: string; session: string; viewer_session: string };
beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "tags-browser-"));
  const binary = join(dir, "tags-fixture");
  await promisify(execFile)(
    "go",
    ["build", "-p", "1", "-o", binary, "./internal/api/testdata/tags-browser"],
    { cwd: root },
  );
  child = spawn(binary, [join(dir, "tags.db")], {
    stdio: ["pipe", "pipe", "pipe"],
  });
  fixture = await new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout! });
    const timer = setTimeout(
      () => reject(new Error("tag API fixture did not start")),
      30_000,
    );
    child.once("error", reject);
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`tag fixture exited ${code}`));
    });
    lines.once("line", (line) => {
      clearTimeout(timer);
      lines.close();
      try {
        resolve(JSON.parse(line));
      } catch {
        reject(new Error("invalid fixture startup record"));
      }
    });
  });
  browser = await chromium();
}, 120_000);
afterAll(async () => {
  await browser?.close();
  if (child && child.exitCode === null) {
    const exited = new Promise<void>((resolve) =>
      child.once("exit", () => resolve()),
    );
    child.stdin?.end();
    await exited;
  }
  if (dir) await rm(dir, { recursive: true, force: true });
});

async function press(page: Page, name: string) {
  const button = await page.waitForSelector(
    `::-p-aria(${name}[role="button"])`,
  );
  await button!.click();
}
async function fill(page: Page, name: string, value: string) {
  await page.waitForSelector(".bulk-tags-form");
  // Chromium derives these names from the rendered uppercase field labels.
  const input = await page.waitForSelector(
    `::-p-aria(${name.toUpperCase()}[role="textbox"])`,
  );
  await input!.focus();
  await page.keyboard.down("Control");
  await page.keyboard.press("a");
  await page.keyboard.up("Control");
  await page.keyboard.press("Backspace");
  await input!.type(value);
}
async function commit(page: Page, changed: number) {
  await press(page, "Preview change");
  await press(page, "Confirm tag change");
  await page.waitForFunction(
    (count) =>
      document
        .querySelector(".bulk-tags-form")
        ?.textContent?.includes(`Changed ${count} monitors;`),
    {},
    changed,
  );
}

it.each([
  ["dark", 390],
  ["light", 390],
  ["dark", 1440],
  ["light", 1440],
] as const)(
  "real SQLite selection, global merge and live facets in %s at %ipx",
  async (theme, width) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    const suffix = `${theme}-${width}`,
      oldKey = `old-${suffix}`,
      newKey = `env-${suffix}`;
    try {
      await context.setCookie({
        name: "subglance_session",
        value: fixture.session,
        domain: new URL(fixture.url).hostname,
        path: "/",
        httpOnly: true,
        sameSite: "Strict",
      });
      // The production CSP blocks injected inline scripts; bypass only to load axe.
      await page.setBypassCSP(true);
      await page.setViewport({ width, height: 1000 });
      await page.emulateMediaFeatures([
        { name: "prefers-reduced-motion", value: "reduce" },
      ]);
      await page.evaluateOnNewDocument(
        (key, value) => localStorage.setItem(key, value),
        THEME_STORAGE_KEY,
        theme,
      );
      await page.goto(fixture.url, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`[data-facet-key="${oldKey}"]`);
      // Seed the infinite-stale dashboard query BEFORE editing and keep this same
      // document alive throughout. Navigation is not a reload or an HTTP fixture.
      await page.evaluate(() => {
        document.documentElement.dataset.tagProof = "same-document";
      });
      if (width === 390) await press(page, "Open navigation");
      await (await page.waitForSelector('a[href="/monitors"]'))!.click();
      await page.waitForSelector('input[aria-label="Select Tag monitor 001"]');
      await page.waitForFunction(
        () => document.querySelectorAll(".inv-row").length === 205,
      );
      const before = await page.evaluate(async () => {
        const r = await fetch("/api/v1/monitors/1");
        return { etag: r.headers.get("ETag"), body: await r.json() };
      });
      await (await page.$(
        'input[aria-label="Select Tag monitor 001"]',
      ))!.click();
      await (await page.$(
        'input[aria-label="Select Tag monitor 002"]',
      ))!.click();
      expect(
        await page.$eval(".bulk-tags-selection", (el) => el.textContent),
      ).toContain("2 selected");
      await page.addScriptTag({ content: axe.source });
      const selectionAudit = await page.evaluate(async () =>
        (window as typeof window & { axe: typeof axe }).axe.run(
          {
            include: [
              ".bulk-tags-actions",
              ".bulk-tags-selection",
              ".bulk-tags-select",
            ],
          },
          {
            runOnly: {
              type: "tag",
              values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
            },
          },
        ),
      );
      expect(selectionAudit.violations).toEqual([]);
      expect(
        await page.$eval(
          ".bulk-tags-selection",
          (el) => getComputedStyle(el).display,
        ),
      ).toBe("flex");
      await press(page, "Manage tags");
      await fill(page, "Tag key", `added-${suffix}`);
      await fill(page, "Tag value", "value:with,delimiters");
      await commit(page, 2);
      const selected = await page.evaluate(async (key) => {
        const data = await (await fetch("/api/v1/monitors")).json();
        return data.monitors
          .filter((m: { tags?: Record<string, string> }) => m.tags?.[key])
          .map((m: { id: number }) => m.id);
      }, `added-${suffix}`);
      expect(selected).toEqual([1, 2]);
      // The same management surface can rename every matching monitor, regardless
      // of the two selected above. One pre-existing destination must be retained.
      await page.select(".bulk-tags-form select", "rename_key");
      await fill(page, "Tag key", oldKey);
      await fill(page, "New key", newKey);
      await press(page, "Preview change");
      await page.waitForSelector(".bulk-tags-preview");
      expect(
        await page.$eval(".bulk-tags-preview", (el) => el.textContent),
      ).toContain("205 monitors will change");
      expect(
        await page.$eval(".bulk-tags-preview", (el) => el.textContent),
      ).toContain(
        "1 monitors already have the destination key; their existing value will be kept",
      );
      await page.addScriptTag({ content: axe.source });
      const audit = await page.evaluate(async () =>
        (window as typeof window & { axe: typeof axe }).axe.run(
          { include: [".bulk-tags-drawer"] },
          {
            runOnly: {
              type: "tag",
              values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
            },
          },
        ),
      );
      expect(audit.violations).toEqual([]);
      const layout = await page.$eval(".bulk-tags-drawer", (el) => ({
        width: el.getBoundingClientRect().width,
        overflow: el.scrollWidth > el.clientWidth,
        background: getComputedStyle(el).backgroundColor,
        fields: Array.from(el.querySelectorAll("input,select")).map(
          (input) => ({
            width: input.getBoundingClientRect().width,
            font: getComputedStyle(input).fontSize,
          }),
        ),
      }));
      expect(layout.width).toBeLessThanOrEqual(width);
      expect(layout.overflow).toBe(false);
      expect(layout.background).not.toBe("rgba(0, 0, 0, 0)");
      expect(layout.fields.every((field) => field.width > 0)).toBe(true);
      if (process.env.SUBGLANCE_PROOF_DIR) {
        await mkdir(process.env.SUBGLANCE_PROOF_DIR, { recursive: true });
        await page.screenshot({
          path: join(process.env.SUBGLANCE_PROOF_DIR, `tags-${suffix}.png`),
        });
      }
      await press(page, "Confirm tag change");
      await page.waitForFunction(() =>
        document
          .querySelector(".bulk-tags-form")
          ?.textContent?.includes("Changed 205 monitors; 0 unchanged."),
      );
      const persisted = await page.evaluate(
        async ({ oldKey, newKey, etag }) => {
          const data = await (await fetch("/api/v1/monitors")).json();
          const detail = await (await fetch("/api/v1/monitors/1")).json();
          const stale = await fetch("/api/v1/monitors/1", {
            method: "PATCH",
            headers: { "Content-Type": "application/json", "If-Match": etag! },
            body: '{"name":"stale overwrite"}',
          });
          return {
            old: data.monitors.filter(
              (m: { tags?: Record<string, string> }) => m.tags?.[oldKey],
            ).length,
            current: data.monitors.filter(
              (m: { tags?: Record<string, string> }) => m.tags?.[newKey],
            ).length,
            destination: detail.tags[newKey],
            headers: detail.headers,
            body: detail.body,
            stale: stale.status,
          };
        },
        { oldKey, newKey, etag: before.etag },
      );
      expect(persisted).toEqual({
        old: 0,
        current: 205,
        destination: "staging",
        headers: { "X-Preserve": "yes" },
        body: "preserve",
        stale: 412,
      });
      await page.keyboard.press("Escape");
      await page.waitForFunction(
        () => !document.querySelector(".bulk-tags-drawer"),
      );
      if (width === 390) await press(page, "Open navigation");
      await (await page.waitForSelector('a[href="/"]'))!.click();
      await page.waitForFunction(
        (key) =>
          !document.querySelector(`[data-facet-key="${key}"]`) &&
          !!document.querySelector(".mon-group-select"),
        {},
        oldKey,
      );
      expect(await page.$(`[data-facet-key="${newKey}"]`)).not.toBeNull();
      const groupOptions = await page.$$eval(
        ".mon-group-select option",
        (options) =>
          options.map((option) => (option as HTMLOptionElement).value),
      );
      expect(groupOptions).toContain(newKey);
      expect(groupOptions).not.toContain(oldKey);
      expect(
        await page.evaluate(() => document.documentElement.dataset.tagProof),
      ).toBe("same-document");
      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  },
  90_000,
);

it("accepts the store's Unicode value length and keeps keyboard focus after a no-op", async () => {
  const context = await browser.createBrowserContext();
  try {
    await context.setCookie({
      name: "subglance_session",
      value: fixture.session,
      domain: new URL(fixture.url).hostname,
      path: "/",
      httpOnly: true,
      sameSite: "Strict",
    });
    const page = await context.newPage();
    await page.goto(`${fixture.url}/monitors`, {
      waitUntil: "domcontentloaded",
    });
    await (await page.waitForSelector(
      'input[aria-label="Select Tag monitor 001"]',
    ))!.click();
    await press(page, "Manage tags");
    await fill(page, "Tag key", "unicode");
    const unicode = "😀".repeat(40);
    await fill(page, "Tag value", unicode);
    expect(
      await page.$eval(
        '::-p-aria(TAG VALUE[role="textbox"])',
        (el) => (el as HTMLInputElement).value,
      ),
    ).toBe(unicode);
    await commit(page, 1);
    await page.select(".bulk-tags-form select", "remove");
    await commit(page, 1);
    // Exact-pair removal is now a no-op. Focus cannot go to disabled Confirm.
    await press(page, "Preview change");
    await page.waitForFunction(() =>
      document
        .querySelector(".bulk-tags-preview")
        ?.textContent?.includes("0 monitors will change"),
    );
    expect(
      await page.evaluate(() => document.activeElement?.textContent),
    ).toContain("0 monitors will change");
    await page.keyboard.press("Escape");
    await page.waitForFunction(
      () => !document.querySelector(".bulk-tags-drawer"),
    );
  } finally {
    await context.close();
  }
});

it("keeps Tab and Shift+Tab inside the drawer while the real preview waits", async () => {
  const context = await browser.createBrowserContext();
  let release: (() => void) | undefined;
  try {
    await context.setCookie({ name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname, path: "/", httpOnly: true, sameSite: "Strict" });
    const page = await context.newPage();
    await page.goto(`${fixture.url}/monitors`, { waitUntil: "domcontentloaded" });
    await (await page.waitForSelector('input[aria-label="Select Tag monitor 001"]'))!.click();
    await press(page, "Manage tags");
    await fill(page, "Tag key", "pending");
    await fill(page, "Tag value", "yes");
    // Delay transport only; the real Go endpoint still supplies the response.
    const gate = new Promise<void>((resolve) => { release = resolve; });
    await page.setRequestInterception(true);
    page.on("request", (request) => {
      if (request.url().endsWith("/monitors/tags/preview")) void gate.then(() => request.continue());
      else void request.continue();
    });
    await press(page, "Preview change");
    await page.waitForFunction(() => document.activeElement?.textContent === "Working…");
    for (const backwards of [false, true]) {
      for (let i = 0; i < 4; i++) {
        if (backwards) await page.keyboard.down("Shift");
        await page.keyboard.press("Tab");
        if (backwards) await page.keyboard.up("Shift");
        expect(await page.evaluate(() => !!document.activeElement?.closest(".bulk-tags-drawer"))).toBe(true);
      }
    }
    release!();
    await page.waitForSelector(".bulk-tags-preview");
  } finally { release?.(); await context.close(); }
});

it("the real viewer session gets no selection or management actions", async () => {
  const context = await browser.createBrowserContext();
  try {
    await context.setCookie({
      name: "subglance_session",
      value: fixture.viewer_session,
      domain: new URL(fixture.url).hostname,
      path: "/",
      httpOnly: true,
      sameSite: "Strict",
    });
    const page = await context.newPage();
    await page.goto(`${fixture.url}/monitors`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector(".inv-row");
    expect(await page.$(".bulk-tags-select")).toBeNull();
    expect(await page.$('::-p-aria(Manage tags[role="button"])')).toBeNull();
    const status = await page.evaluate(
      async () =>
        (
          await fetch("/api/v1/monitors/tags/preview", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: '{"action":"rename_key","key":"x","new_key":"y"}',
          })
        ).status,
    );
    expect(status).toBe(403);
  } finally {
    await context.close();
  }
});
