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
async function readyAddForm(page: Page): Promise<void> {
  await page.waitForSelector(".add-form");
  // Finding the form is not enough: the close button starts outside the
  // viewport while its parent drawer slides in. Wait for that real animation,
  // not a fixed delay or a retry of the click after it missed.
  await page.$eval(".drawer-panel", async (panel) => {
    await Promise.all(panel.getAnimations().map((animation) => animation.finished));
  });
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

it.each([[false, false], [true, false], [false, true]])("restores the exact draft entry after multi-entry Back across the native skip link (intervening Forward: %s, draft fragment: %s)", async (race, draftHash) => {
  const page = await pageAt("/");
  try {
    const cdp = await page.createCDPSession();
    const initialLength = await page.evaluate(() => history.length);
    await page.focus('a[href="#shell-main"]');
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => location.hash === "#shell-main");
    expect(await page.evaluate(() => history.length)).toBe(initialLength + 1);
    await (await page.waitForSelector('a[href="/monitors"]'))!.click();
    await (await page.waitForSelector('button[aria-label="Add monitor"]'))!.click();
    await readyAddForm(page);
    const name = 'input[id$="-name"]';
    await page.type(name, "native hash draft");
    if (draftHash) {
      await page.evaluate(() => { location.hash = "draft"; });
      await page.waitForFunction(() => location.hash === "#draft");
    }
    const draftPath = `/monitors/new${draftHash ? "#draft" : ""}`;
    const backSteps = draftHash ? -4 : -3;
    const before = await cdp.send("Page.getNavigationHistory");
    if (race) await page.evaluate(() => {
      const go = history.go.bind(history);
      let held = false;
      history.go = (delta = 0) => {
        if (delta > 0 && !held) {
          // Hold the first reversal and let a real Forward win the race.
          held = true;
          history.forward();
        } else go(delta);
      };
    });

    await confirmAction(page, () => page.evaluate((delta) => history.go(delta), backSteps), false);
    await expect.poll(() => new URL(page.url()).pathname + new URL(page.url()).hash).toBe(draftPath);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("native hash draft");
    expect(await cdp.send("Page.getNavigationHistory")).toEqual(before);
    for (const action of [
      () => page.click(".add-button-quiet"),
      () => page.evaluate(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }))),
    ]) {
      await confirmAction(page, action, false);
      expect(new URL(page.url()).pathname + new URL(page.url()).hash).toBe(draftPath);
      expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("native hash draft");
    }

    await confirmAction(page, () => page.evaluate((delta) => history.go(delta), backSteps), true);
    await page.waitForFunction(() => location.pathname === "/" && !document.querySelector(".add-form"));
    for (const path of ["/#shell-main", "/monitors", "/monitors/new", ...(draftHash ? [draftPath] : [])]) {
      await page.evaluate(() => history.forward());
      await page.waitForFunction((expected) => location.pathname + location.hash === expected, {}, path);
    }
    await readyAddForm(page);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("");
    expect(await cdp.send("Page.getNavigationHistory")).toEqual(before);
  } finally { await page.close(); }
});

it("keeps native fragment entries on a dirty form reversible in both directions", async () => {
  const page = await pageAt("/monitors/new");
  try {
    await readyAddForm(page);
    // Seed a real future route while pristine, then come Back and start a draft.
    await page.$eval('a[href="/settings"]', (link) => (link as HTMLAnchorElement).click());
    await page.waitForSelector('input[name="current_password"]');
    await page.evaluate(() => history.back());
    await readyAddForm(page);
    const name = 'input[id$="-name"]';
    await page.type(name, "forward draft");
    const cdp = await page.createCDPSession();
    const before = await cdp.send("Page.getNavigationHistory");
    await confirmAction(page, () => page.evaluate(() => history.forward()), false);
    await expect.poll(() => new URL(page.url()).pathname).toBe("/monitors/new");
    expect(await cdp.send("Page.getNavigationHistory")).toEqual(before);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("forward draft");

    // Real fragment navigation, not pushState with a copied router index.
    // Adding it intentionally replaces the future Settings entry in the browser.
    let unexpected = 0;
    const reject = (dialog: import("puppeteer-core").Dialog) => { unexpected++; void dialog.dismiss(); };
    page.on("dialog", reject);
    await page.evaluate(() => { location.hash = "draft"; });
    await page.waitForFunction(() => location.hash === "#draft");
    await page.evaluate(() => history.back());
    await page.waitForFunction(() => location.hash === "");
    await page.evaluate(() => history.forward());
    await page.waitForFunction(() => location.hash === "#draft");
    page.off("dialog", reject);
    expect(unexpected).toBe(0);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("forward draft");
    await confirmAction(page, () => page.click(".add-button-quiet"), false);
    expect(new URL(page.url()).pathname + new URL(page.url()).hash).toBe("/monitors/new#draft");
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("forward draft");
  } finally { await page.close(); }
});

it.each(["dashboard", "direct"])("protects Escape, close, Cancel, backdrop and history while keeping keyboard focus (%s entry)", async (entry) => {
  const page = await pageAt(entry === "direct" ? "/monitors/new" : "/");
  try {
    if (entry === "dashboard") {
      await (await page.waitForSelector('a[href="/monitors"]'))!.click();
      await (await page.waitForSelector('button[aria-label="Add monitor"]'))!.click();
    }
    await readyAddForm(page);
    const name = 'input[id$="-name"]';
    await page.type(name, "browser draft");
    for (const [label, action] of [
      ["Escape", () => page.keyboard.press("Escape")],
      ["close", () => page.click('.drawer-close')],
      ["Cancel", () => page.click('.add-button-quiet')],
      ["backdrop", () => page.click('.drawer-scrim', { offset: { x: 2, y: 100 } })],
    ] as const) {
      await page.focus(name);
      try {
        await confirmAction(page, action, false);
      } catch (error) {
        throw new Error(`Discard action failed: ${label}`, { cause: error });
      }
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
    await readyAddForm(page);
    expect(await page.$eval(name, (el) => (el as HTMLInputElement).value)).toBe("");
    let unexpected = 0;
    page.on("dialog", (dialog) => { unexpected++; void dialog.dismiss(); });
    await page.keyboard.press("Escape");
    expect(unexpected).toBe(0);
    expect(await page.$(".add-form")).toBeNull();
  } finally { await page.close(); }
});
