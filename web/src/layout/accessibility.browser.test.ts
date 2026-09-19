/**
 * SUB-105: the WCAG 2 A/AA floor against the shipped bundle, not jsdom.
 * Picked up by the existing browser job; axe is injected only by this test.
 * No disabled rules. Existing contrast debt is listed by exact rule, selector,
 * screen and theme in accessibility-waivers.json; stale waivers fail too.
 * Findings name the rule and offending markup so a red CI run is actionable.
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import baseline from "./accessibility-waivers.json";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { THEME_STORAGE_KEY } from "../theme/theme";

type Screen = {
  name: string;
  path: string;
  ready: string;
  layout?: string;
  auth?: "setup" | "login";
  drawer?: "add" | "navigation";
};

const SCREENS: Screen[] = [
  { name: "dashboard rows", path: "/", layout: "rows", ready: "[data-testid^='monitor-row-']" },
  { name: "dashboard cards", path: "/", layout: "cards", ready: "[data-testid^='monitor-card-']" },
  { name: "dashboard compact", path: "/", layout: "compact", ready: "[data-testid^='monitor-line-']" },
  { name: "status wall", path: "/", layout: "wall", ready: ".wall-card" },
  { name: "monitor detail", path: "/monitors/1", ready: ".mon-detail-windows" },
  { name: "monitors", path: "/monitors", ready: ".inv-list > li" },
  { name: "incidents", path: "/incidents", ready: ".inc-line" },
  { name: "notifications", path: "/notifications", ready: ".inv-row" },
  { name: "setup", path: "/", auth: "setup", ready: ".auth-card input[type='password']" },
  { name: "login", path: "/", auth: "login", ready: ".auth-card input[type='password']" },
  { name: "add monitor", path: "/monitors", ready: ".inv-list > li", drawer: "add" },
  { name: "navigation drawer", path: "/", ready: "[data-testid^='monitor-card-']", drawer: "navigation" },
];

let server: Server;
let browser: Browser;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
});

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

async function openScreen(page: Page, screen: Screen, theme: string): Promise<void> {
  await page.setViewport({ width: screen.drawer === "navigation" ? 375 : 1440, height: 900 });
  await page.evaluateOnNewDocument((layoutKey, layout, themeKey, chosenTheme) => {
    localStorage.setItem(layoutKey, layout);
    localStorage.setItem(themeKey, chosenTheme);
  }, LAYOUT_STORAGE_KEY, screen.layout ?? "rows", THEME_STORAGE_KEY, theme);

  // Only these two responses differ from the signed-in harness. This reaches
  // the real session gate and real auth form without a backend or credentials.
  await page.setRequestInterception(true);
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      void request.abort("blockedbyclient");
    } else if (screen.auth && url.pathname === "/api/v1/auth/me") {
      void request.respond({ status: 401, contentType: "application/json", body: '{"error":"unauthorized"}' });
    } else if (screen.auth && url.pathname === "/api/v1/setup") {
      void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ setup_required: screen.auth === "setup" }) });
    } else {
      void request.continue();
    }
  });
  await page.goto(server.url + screen.path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(screen.ready, { visible: true, timeout: 15_000 });
  if (screen.auth) {
    expect(await page.$eval(".auth-title", (el) => el.textContent)).toBe(
      screen.auth === "setup" ? "Set up this instance" : "Sign in",
    );
  }
  if (screen.drawer) {
    const selector = screen.drawer === "add"
      ? 'button[aria-label="Add monitor"]'
      : 'button[aria-label="Open navigation"]';
    const button = await page.waitForSelector(selector, { visible: true });
    if (!button) throw new Error(`Missing ${selector}`);
    await button.click();
    await page.waitForSelector(screen.drawer === "add" ? ".drawer-panel .add-form" : ".shell-drawer", { visible: true });
  }
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations()
      .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map((animation) => animation.finished.catch(() => undefined)));
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  });
  expect(await page.$eval("html", (el) => el.getAttribute("data-theme"))).toBe(theme);
}

async function audit(page: Page, scope = "light: login"): Promise<void> {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
    });
    return result.violations.map(({ id, help, helpUrl, nodes }) => ({
      id, help, helpUrl,
      nodes: nodes.map(({ target, html, failureSummary }) => ({ target, html, failureSummary })),
    }));
  });
  const expected = baseline.entries.filter((entry) => entry.scopes.includes(scope));
  const key = (rule: string, target: unknown) => JSON.stringify([rule, target]);
  const waived = new Set(expected.map((entry) => key(entry.rule, [entry.selector])));
  const observed = violations.flatMap(({ id, help, helpUrl, nodes }) =>
    nodes.map((node) => ({ id, help, helpUrl, ...node })),
  );
  expect(observed.filter((node) => !waived.has(key(node.id, node.target))), scope).toEqual([]);
  const found = new Set(observed.map((node) => key(node.id, node.target)));
  expect(expected.filter((entry) => !found.has(key(entry.rule, [entry.selector]))),
    `${scope}: remove obsolete contrast waivers after remediation`).toEqual([]);
}

describe("the accessibility gate itself", () => {
  it("keeps every waiver unique, reasoned and scoped to an audited screen/theme", () => {
    const scopes = new Set(SCREENS.flatMap((screen) => ["light", "dark"].map((theme) => `${theme}: ${screen.name}`)));
    const keys = baseline.entries.map((entry) => `${entry.rule}: ${entry.selector}`);
    expect(new Set(keys).size).toBe(keys.length);
    for (const entry of baseline.entries) {
      expect(entry.rule).toBe("color-contrast");
      expect(entry.reason.length).toBeGreaterThan(40);
      expect(entry.scopes.length).toBeGreaterThan(0);
      expect(new Set(entry.scopes).size).toBe(entry.scopes.length);
      for (const scope of entry.scopes) expect(scopes.has(scope), scope).toBe(true);
    }
  });
  it("rejects a now-obsolete waiver after a real rendered element is repaired", async () => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      await openScreen(page, SCREENS.find((screen) => screen.name === "setup")!, "light");
      await audit(page, "light: setup");
      await page.$eval(".auth-note", (element) => {
        (element as HTMLElement).style.color = "var(--ink)";
      });
      await expect(audit(page, "light: setup")).rejects.toThrow("remove obsolete contrast waivers");
      await page.$eval(".auth-note", (element) => (element as HTMLElement).style.removeProperty("color"));
      await audit(page, "light: setup");
    } finally {
      await context.close();
    }
  });

  it.each([
    { tag: "input", rule: "label" },
    { tag: "button", rule: "button-name" },
    { tag: "p", rule: "color-contrast" },
  ])("rejects a deliberately broken $tag ($rule), then passes when restored", async ({ tag, rule }) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      await openScreen(page, SCREENS.find((screen) => screen.name === "login")!, "light");
      await audit(page);
      await page.evaluate((elementTag) => {
        const broken = document.createElement(elementTag);
        broken.id = "deliberate-accessibility-regression";
        if (elementTag === "p") {
          broken.textContent = "Deliberately unreadable text";
          broken.style.color = "#aaaaaa";
          broken.style.backgroundColor = "#ffffff";
        }
        document.querySelector(".auth-card")!.append(broken);
      }, tag);
      await expect(audit(page)).rejects.toThrow(rule);
      await page.evaluate(() => document.getElementById("deliberate-accessibility-regression")!.remove());
      await audit(page);
    } finally {
      await context.close();
    }
  });
});

describe.each(["light", "dark"])("WCAG 2 A/AA in %s", (theme) => {
  it.each(SCREENS)("$name", async (screen) => {
    // Isolate storage, cookies and service workers between cases, not just tabs.
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      await openScreen(page, screen, theme);
      await audit(page, `${theme}: ${screen.name}`);
    } finally {
      await context.close();
    }
  });
});
