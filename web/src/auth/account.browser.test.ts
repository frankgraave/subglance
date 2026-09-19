import { afterAll, beforeAll, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

let server: Server;
let browser: Browser;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });
async function pageAt(path: string, theme = "dark", width = 1440) {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 1000 });
  await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".shell-topbar");
  return page;
}
async function fill(page: Page, selector: string, value: string) {
  await page.$eval(selector, (input) => { (input as HTMLInputElement).value = ""; });
  await page.type(selector, value);
}
async function confirmAction(page: Page, action: () => Promise<unknown>, accept: boolean) {
  let dialogs = 0;
  let decided!: () => void;
  const decision = new Promise<void>((resolve) => { decided = resolve; });
  const handler = async (dialog: import("puppeteer-core").Dialog) => {
    dialogs++;
    expect(dialog.type()).toBe("confirm");
    expect(dialog.message()).toContain("Discard this unsaved monitor?");
    if (accept) await dialog.accept(); else await dialog.dismiss();
    decided();
  };
  page.on("dialog", handler);
  try { await Promise.all([action(), decision]); } finally { page.off("dialog", handler); }
  expect(dialogs, "exactly one discard decision per action").toBe(1);
}

it.each(["dark", "light"])("changes a password through the real browser fetcher, correcting the old password without losing the session (%s)", async (theme) => {
  const page = await pageAt("/", theme);
  const requests: unknown[] = [];
  await page.setRequestInterception(true);
  page.on("request", (req) => {
    if (new URL(req.url()).pathname !== "/api/v1/auth/password") { void req.continue(); return; }
    requests.push(JSON.parse(req.postData()!));
    const wrong = requests.length === 1;
    void req.respond({ status: wrong ? 401 : 204, contentType: "application/json",
      ...(wrong ? { body: JSON.stringify({ error: "current password is incorrect" }) } : {}) });
  });
  try {
    const beforeSearch = await page.$eval('input[type="search"]', (el) => ({
      height: el.getBoundingClientRect().height, font: getComputedStyle(el).fontSize,
    }));
    await (await page.waitForSelector('a[href="/settings"]'))!.click();
    await page.waitForSelector('input[name="current_password"]');
    const afterSearch = await page.$eval('input[type="search"]', (el) => ({
      height: el.getBoundingClientRect().height, font: getComputedStyle(el).fontSize,
    }));
    expect(afterSearch).toEqual(beforeSearch);
    await fill(page, 'input[name="current_password"]', "wrong test password");
    await fill(page, 'input[name="new_password"]', "a fresh test passphrase");
    await fill(page, 'input[name="confirmation"]', "a fresh test passphrase");
    await page.click('form[aria-label="Change password"] button[type="submit"]');
    await page.waitForSelector('input[name="current_password"][aria-invalid="true"]');
    expect(await page.$eval('input[name="current_password"]', (el) => el.parentElement?.querySelector('[role="alert"]')?.textContent)).toBe("current password is incorrect");
    expect(await page.$('a[href="/settings"][aria-current="page"]')).toBeTruthy();
    await fill(page, 'input[name="current_password"]', "correct test password");
    await page.click('form[aria-label="Change password"] button[type="submit"]');
    await page.waitForFunction(() => document.querySelector('form[aria-label="Change password"] [role="status"]')?.textContent?.includes("Every other session was signed out"));
    expect(requests).toEqual([
      { current_password: "wrong test password", new_password: "a fresh test passphrase" },
      { current_password: "correct test password", new_password: "a fresh test passphrase" },
    ]);
    expect(await page.$$eval('input[type="password"]', (els) => els.map((el) => (el as HTMLInputElement).value))).toEqual(["", "", ""]);
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).not.toContain("passphrase");
    expect(await page.$('.shell-account')).toBeTruthy();
    if (process.env.SUBGLANCE_EVIDENCE_DIR) await page.screenshot({ path: `${process.env.SUBGLANCE_EVIDENCE_DIR}/account-settings-${theme}.png`, fullPage: true });
  } finally { await page.close(); }
});

it("keeps the password card usable on a phone with no horizontal overflow", async () => {
  const page = await pageAt("/settings", "dark", 390);
  try {
    await page.waitForSelector('input[name="current_password"]');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(await page.$eval('input[name="current_password"]', (el) => getComputedStyle(el).fontSize)).toBe("16px");
    if (process.env.SUBGLANCE_EVIDENCE_DIR) await page.screenshot({ path: `${process.env.SUBGLANCE_EVIDENCE_DIR}/account-settings-phone.png`, fullPage: true });
  } finally { await page.close(); }
});

it.each(["dashboard", "direct"])("protects Escape, close, Cancel, backdrop and history while keeping keyboard focus (%s entry)", async (entry) => {
  const page = await pageAt(entry === "direct" ? "/monitors/new" : "/");
  try {
    if (entry === "dashboard") {
      await (await page.waitForSelector('a[href="/monitors"]'))!.click();
      await (await page.waitForSelector('button[aria-label="Add monitor"]'))!.click();
    }
    await page.waitForSelector(".add-form");
    const name = 'input[id$="-name"]';
    await page.type(name, "browser draft");
    for (const action of [
      () => page.keyboard.press("Escape"),
      () => page.click('.drawer-close'),
      () => page.click('.add-button-quiet'),
      () => page.click('.drawer-scrim', { offset: { x: 2, y: 100 } }),
    ]) {
      await page.focus(name);
      await confirmAction(page, action, false);
      expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("browser draft");
      expect(new URL(page.url()).pathname).toBe("/monitors/new");
    }
    let unloaded = false;
    const unloadDecision = new Promise<void>((resolve) => page.once("dialog", async (dialog) => {
      expect(dialog.type()).toBe("beforeunload");
      await dialog.dismiss(); unloaded = true; resolve();
    }));
    await Promise.all([page.evaluate(() => location.reload()), unloadDecision]);
    expect(unloaded).toBe(true);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("browser draft");
    // The existing focus trap still wraps the last control back to Close.
    await page.focus('.add-button-quiet'); await page.keyboard.press("Tab");
    expect(await page.evaluate(() => document.activeElement?.className)).toBe("drawer-close");
    if (entry === "dashboard") {
      await confirmAction(page, () => page.evaluate(() => history.back()), false);
      await page.waitForFunction(() => location.pathname === "/monitors/new");
      expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("browser draft");
    }
    await confirmAction(page, () => page.keyboard.press("Escape"), true);
    await page.waitForFunction(() => !document.querySelector(".add-form"));
    await (await page.waitForSelector('button[aria-label="Add monitor"]'))!.click();
    await page.waitForSelector(name);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("");
    let unexpected = 0;
    page.on("dialog", (dialog) => { unexpected++; void dialog.dismiss(); });
    await page.keyboard.press("Escape");
    expect(unexpected).toBe(0);
    expect(await page.$(".add-form")).toBeNull();
  } finally { await page.close(); }
});
