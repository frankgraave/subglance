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
import type { ApiIncident } from "../monitors/detail";

type Screen = {
  name: string;
  path: string;
  ready: string;
  layout?: string;
  auth?: "setup" | "login";
  drawer?: "add" | "navigation";
  passwordError?: boolean;
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
  { name: "settings", path: "/settings", ready: 'input[name="current_password"]' },
  { name: "settings password error", path: "/settings", ready: 'input[name="current_password"]', passwordError: true },
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

async function openScreen(page: Page, screen: Screen, theme: string, incidentIdOffset = 0): Promise<void> {
  await page.setViewport({ width: screen.drawer === "navigation" ? 375 : 1440, height: 900 });
  await page.evaluateOnNewDocument((layoutKey, layout, themeKey, chosenTheme) => {
    localStorage.setItem(layoutKey, layout);
    localStorage.setItem(themeKey, chosenTheme);
  }, LAYOUT_STORAGE_KEY, screen.layout ?? "rows", THEME_STORAGE_KEY, theme);

  // Auth cases reach the real session gate and real forms without credentials.
  // The identity regression below also renumbers the existing incident fixture.
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) {
      void request.abort("blockedbyclient");
    } else if (screen.passwordError && url.pathname === "/api/v1/auth/password") {
      void request.respond({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "current password is incorrect" }) });
    } else if (screen.auth && url.pathname === "/api/v1/auth/me") {
      void request.respond({ status: 401, contentType: "application/json", body: '{"error":"unauthorized"}' });
    } else if (screen.auth && url.pathname === "/api/v1/setup") {
      void request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ setup_required: screen.auth === "setup" }) });
    } else if (incidentIdOffset && url.pathname === "/api/v1/monitors/1/incidents") {
      // Preserve the harness's wire fixture, changing only persisted identity.
      const response = await fetch(request.url());
      const body = await response.json() as { incidents: ApiIncident[] };
      await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify({
        ...body, incidents: body.incidents.map((incident) => ({ ...incident, id: incident.id + incidentIdOffset })),
      }) });
    } else {
      void request.continue();
    }
  });
  await page.goto(server.url + screen.path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(screen.ready, { visible: true, timeout: 15_000 });
  if (screen.passwordError) {
    await page.type('input[name="current_password"]', "wrong example password");
    await page.type('input[name="new_password"]', "new example passphrase");
    await page.type('input[name="confirmation"]', "new example passphrase");
    await page.click('form[aria-label="Change password"] button[type="submit"]');
    await page.waitForSelector('input[name="current_password"][aria-invalid="true"]');
  }
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
  it("has no remaining waivers for the repaired secondary text roles", () => {
    expect(baseline.entries.filter((entry) =>
      /\.(?:inc-sub|tb-count|mon-detail-note)\b/.test(entry.selector),
    )).toEqual([]);
    // Remaining auth debt is out of this repair's scope. In particular,
    // do not turn those exact selectors into wildcard useId exemptions.
    for (const entry of baseline.entries.filter((entry) => entry.selector.includes("_r_"))) {
      expect(["#_r_0_-strength"]).toContain(entry.selector);
    }
  });
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

  it.each(["light", "dark"])("rejects a new incident violation after persisted IDs and useId shift in %s", async (theme) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      const screen = SCREENS.find((screen) => screen.name === "monitor detail")!;
      const fixture = await (await fetch(`${server.url}/api/v1/monitors/1/incidents`)).json() as { incidents: ApiIncident[] };
      const received = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/monitors/1/incidents")
        .then((response) => response.json() as Promise<{ incidents: ApiIncident[] }>);
      await openScreen(page, screen, theme, 10_000);
      expect((await received).incidents.map((incident) => incident.id)).toEqual(fixture.incidents.map((incident) => incident.id + 10_000));
      await page.waitForSelector(".inc-sub", { visible: true });
      const before = await page.$$eval(".inc-line", (elements) => elements.map((element) => element.getAttribute("aria-controls")));
      expect(before.length).toBe(fixture.incidents.length);
      await audit(page, `${theme}: monitor detail`);

      // Remount through real navigation so React itself allocates different
      // useId values, as inserting another Card does in a combined branch.
      const back = await page.waitForSelector(".mon-detail-back", { visible: true });
      await back!.click();
      const monitor = await page.waitForSelector('[data-testid="monitor-row-1"] .mon-name', { visible: true });
      await monitor!.click();
      await page.waitForSelector(".mon-detail-windows", { visible: true });
      await page.waitForSelector(".inc-sub", { visible: true });
      const after = await page.$$eval(".inc-line", (elements) => elements.map((element) => element.getAttribute("aria-controls")));
      expect(after.length).toBe(before.length);
      for (const id of after) {
        expect(id).toBeTruthy();
        expect(before).not.toContain(id);
      }
      await audit(page, `${theme}: monitor detail`);

      // A NEW sibling with the same text role must not inherit any exemption
      // from the old incident, even when axe chooses a different selector.
      await page.$eval(".inc-sub", (element) => {
        const broken = element.cloneNode(false) as HTMLElement;
        broken.textContent = "New incident contrast regression";
        broken.style.cssText = "color: var(--ink-3); transition: none; animation: none";
        element.after(broken);
      });
      await expect(audit(page, `${theme}: monitor detail`)).rejects.toMatchObject({
        actual: expect.arrayContaining([expect.objectContaining({
          id: "color-contrast", html: expect.stringContaining("New incident contrast regression"),
        })]),
      });
      await page.$eval(".inc-sub + .inc-sub", (element) => element.remove());
      await audit(page, `${theme}: monitor detail`);
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

describe.each(["light", "dark"])("readable secondary text in %s", (theme) => {
  it.each([
    { screenName: "monitor detail", selector: ".inc-sub" },
    { screenName: "monitor detail", selector: ".mon-detail-note" },
    { screenName: "incidents", selector: ".inc-sub" },
    { screenName: "incidents", selector: ".mon-detail-note" },
    { screenName: "monitors", selector: ".tb-count" },
  ])("$selector on $screenName clears AA without a waiver", async ({ screenName, selector }) => {
    const context = await browser.createBrowserContext();
    const page = await context.newPage();
    try {
      await openScreen(page, SCREENS.find((screen) => screen.name === screenName)!, theme);
      await page.waitForSelector(selector, { visible: true });
      await page.addScriptTag({ content: axe.source });
      const result = await page.evaluate(async (target) => {
        const result = await (window as typeof window & { axe: typeof axe }).axe.run(
          { include: [target] }, { runOnly: { type: "rule", values: ["color-contrast"] } },
        );
        return {
          violations: result.violations,
          incomplete: result.incomplete,
          checked: result.passes.flatMap((rule) => rule.nodes).length,
        };
      }, selector);
      // This is deliberately independent of the debt baseline: these roles
      // must be readable even when a new card, row or toolbar changes IDs.
      expect(result.violations, `${theme}: ${screenName} ${selector}`).toEqual([]);
      expect(result.incomplete).toEqual([]);
      expect(result.checked).toBeGreaterThan(0);
      const colors = await page.$$eval(selector, (elements) => {
        const probe = document.createElement("span");
        probe.style.cssText = "color: var(--ink-2); transition: none; animation: none";
        document.body.append(probe);
        const expected = getComputedStyle(probe).color;
        probe.remove();
        return elements.filter((element) => element.checkVisibility()).map((element) => ({
          actual: getComputedStyle(element).color, expected,
        }));
      });
      expect(colors.length).toBeGreaterThan(0);
      for (const { actual, expected } of colors) expect(actual).toBe(expected);
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
