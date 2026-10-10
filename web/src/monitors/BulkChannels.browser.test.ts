// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface } from "node:readline";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { THEME_STORAGE_KEY } from "../theme/theme";

/*
 * Channels for a selection, against the production API and SQLite: the
 * drawer opens from the selection bar, the add and the remove are written,
 * the inventory's channel column follows without a reload, the preview names
 * the monitors a remove leaves without a channel of their own, and the
 * drawer passes axe and fits a phone in both themes.
 */

const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string;
let child: ChildProcess;
let browser: Browser;
let fixture: { url: string; session: string };
beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "channels-browser-"));
  const binary = join(dir, "tags-fixture");
  await promisify(execFile)("go", ["build", "-p", "1", "-o", binary, "./internal/api/testdata/tags-browser"], { cwd: root });
  child = spawn(binary, [join(dir, "channels.db")], { stdio: ["pipe", "pipe", "pipe"] });
  fixture = await new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout! });
    const timer = setTimeout(() => reject(new Error("channel API fixture did not start")), 30_000);
    child.once("error", reject);
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`channel fixture exited ${code}`));
    });
    lines.once("line", (line) => {
      clearTimeout(timer);
      lines.close();
      resolve(JSON.parse(line));
    });
  });
  browser = await chromium();
}, 120_000);
afterAll(async () => {
  await browser?.close();
  if (child && child.exitCode === null) {
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.stdin?.end();
    await exited;
  }
  if (dir) await rm(dir, { recursive: true, force: true });
});

async function press(page: Page, name: string) {
  await (await page.waitForSelector(`::-p-aria(${name}[role="button"])`))!.click();
}
async function tick(page: Page, name: string) {
  await (await page.waitForSelector(`input[aria-label="Select ${name}"]`))!.click();
}
async function formText(page: Page, text: string) {
  await page.waitForFunction((t) => document.querySelector(".bulk-channels-form")?.textContent?.includes(t), {}, text);
}
/** Picks the action and the channel, then previews. */
async function preview(page: Page, action: "add" | "remove") {
  // The channel list loads with the form; both fields are there once it has.
  await page.waitForFunction(() => document.querySelectorAll(".bulk-channels-form select").length === 2);
  const [actionSelect, channelSelect] = await page.$$(".bulk-channels-form select");
  await actionSelect.select(action);
  const value = await channelSelect.$eval("option:not([disabled])", (o) => (o as HTMLOptionElement).value);
  await channelSelect.select(value);
  await press(page, "Preview change");
  await page.waitForSelector(".bulk-channels-form .bulk-tags-preview");
}
async function audit(page: Page, include: string[]) {
  await page.addScriptTag({ content: axe.source });
  const result = await page.evaluate(
    async (selectors) =>
      (window as typeof window & { axe: typeof axe }).axe.run(
        { include: selectors },
        { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"] } },
      ),
    include,
  );
  return result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}
const rowText = (page: Page, name: string) =>
  page.$$eval(".inv-row", (rows, n) => rows.find((r) => r.textContent?.includes(n as string))?.textContent ?? "", name);

it.each([
  ["dark", 390, ["Tag monitor 101", "Tag monitor 102"]],
  ["light", 1440, ["Tag monitor 103", "Tag monitor 104"]],
] as const)("adds and removes a channel on a real selection in %s at %ipx", async (theme, width, names) => {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  try {
    await context.setCookie({
      name: "subglance_session", value: fixture.session, domain: new URL(fixture.url).hostname,
      path: "/", httpOnly: true, sameSite: "Strict",
    });
    // The production CSP blocks injected inline scripts; bypass only to load axe.
    await page.setBypassCSP(true);
    await page.setViewport({ width, height: 1000 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
    await page.goto(`${fixture.url}/monitors`, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => document.querySelectorAll(".inv-row").length === 205);
    expect(await page.$('::-p-aria(Channels for 0 selected[role="button"])')).toBeNull();
    for (const name of names) await tick(page, name);
    expect(await audit(page, [".bulk-tags-selection"])).toEqual([]);

    await press(page, "Channels for 2 selected");
    await formText(page, "2 monitors selected, including any hidden by filters.");
    await preview(page, "add");
    await formText(page, "2 monitors will change; 0 already have it.");
    expect(await audit(page, [".bulk-channels-drawer"])).toEqual([]);
    const fits = await page.$eval(".bulk-channels-drawer", (el) => ({
      sideways: document.documentElement.scrollWidth > document.documentElement.clientWidth,
      right: el.getBoundingClientRect().right <= window.innerWidth + 0.5,
    }));
    expect(fits).toEqual({ sideways: false, right: true });
    await press(page, "Confirm channel change");
    await formText(page, "Added to 2 monitors; 0 unchanged.");
    await press(page, "Close");
    await page.waitForFunction(() => !document.querySelector(".bulk-channels-drawer"));
    // The column follows the write without a reload.
    await page.waitForFunction(
      (n) => [...document.querySelectorAll(".inv-row")].find((r) => r.textContent?.includes(n))?.textContent?.includes("Ops pager"),
      {}, names[0],
    );

    // Remove from one: it is left with no channel of its own.
    await press(page, "Clear selection");
    await tick(page, names[0]);
    await press(page, "Channels for 1 selected");
    await preview(page, "remove");
    await formText(page, "1 monitor will change; 0 already do not have it.");
    await formText(page, "1 monitor will have no channel of its own left");
    await press(page, "Confirm channel change");
    await formText(page, "Removed from 1 monitor; 0 unchanged.");
    const links = await page.evaluate(async (n) => {
      const all = (await (await fetch("/api/v1/monitors")).json()).monitors as { id: number; name: string }[];
      const out: Record<string, string[]> = {};
      for (const name of n) {
        const id = all.find((m) => m.name === name)!.id;
        const body = await (await fetch(`/api/v1/monitors/${id}/channels`)).json();
        out[name] = body.channels.map((c: { name: string }) => c.name);
      }
      return out;
    }, names as unknown as string[]);
    expect(links).toEqual({ [names[0]]: [], [names[1]]: ["Ops pager"] });
    expect(await rowText(page, names[1])).toContain("Ops pager");
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
}, 120_000);
