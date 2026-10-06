/**
 * A refusal under a field on every settings card, measured in a real browser.
 *
 * `.field-error` used to draw the whole sentence in the failure red, which at
 * the helper size measured 4.49:1 on a light settings panel and 4.28:1 on a
 * dark one, under the 4.5:1 a sentence needs. The browser gate did not see
 * it, because no audited screen ever rendered a refusal: a card shows one
 * only after the server has answered a save with an error. So this file makes
 * every settings card ask, answers each with a refusal, and then measures:
 *
 * - axe on the refusal and the field it names, in both themes;
 * - the refusal's words against its real backdrop, at least 4.5:1;
 * - its alert glyph, a graphic, at least 3:1 — and that it is there at all,
 *   since the words are no longer the coloured part.
 *
 * A last test walks the settings page and fails on a card with a field that
 * is neither driven to a refusal here nor exempted below with a reason, so a
 * new card cannot ship a refusal nobody has measured.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { BACKDROP, LUMINANCE, OVER_BACKDROP } from "./harness/contrast";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { defaultRetention } from "../retention/fixtures";

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

/** What the server says to each write, keyed `METHOD path`, in its wire shape. */
const REFUSALS: Record<string, { error: string; field?: string }> = {
  "POST /api/v1/auth/password": { error: "current password is incorrect", field: "current_password" },
  "POST /api/v1/users": { error: "an account with this email already exists", field: "email" },
  "POST /api/v1/status-pages": { error: "this address is already used by another page", field: "slug" },
  "PUT /api/v1/settings/connectivity": { error: "connectivity target \"gateway\": want host:port", field: "targets" },
  "PUT /api/v1/settings/retention": { error: "raw_seconds must be at least one day", field: "raw_seconds" },
  "POST /api/v1/tokens": { error: "a token with this name already exists", field: "name" },
  "POST /api/v1/instance/reset": { error: "confirm must be the exact phrase" },
};

/** Replaces a field's value the way typing would leave it, so React sees the change. */
async function fill(page: Page, selector: string, value: string): Promise<void> {
  await page.waitForSelector(selector, { visible: true, timeout: 15_000 });
  await page.$eval(selector, (el, next) => {
    const field = el as HTMLInputElement | HTMLTextAreaElement;
    const proto = field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(field, next);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
}

async function press(page: Page, xpath: string): Promise<void> {
  const [button] = await page.$$(`xpath/${xpath}`);
  if (!button) throw new Error(`no button at ${xpath}`);
  await button.click();
}

type Refused = {
  section: string;
  /** Where the refusal and the form stand: the card, or the drawer it opened. */
  scope: string;
  /** Takes the card from its resting state to a submitted form. */
  submit: (page: Page) => Promise<void>;
};

const CARDS: Refused[] = [
  { section: "account", scope: "#account", submit: async (page) => {
    await fill(page, '#account input[name="current_password"]', "wrong example password");
    await fill(page, '#account input[name="new_password"]', "new example passphrase");
    await fill(page, '#account input[name="confirmation"]', "new example passphrase");
    await page.click('#account form[aria-label="Change password"] button[type="submit"]');
  } },
  { section: "users", scope: "#users", submit: async (page) => {
    await page.waitForSelector("#users li .segmented", { timeout: 15_000 });
    await press(page, "//*[@id='users']//button[normalize-space()='Add user']");
    await fill(page, '#users form[aria-label="Add user"] input[type="email"]', "operator@example.com");
    await fill(page, '#users form[aria-label="Add user"] input[type="password"]', "an example passphrase");
    await page.click('#users form[aria-label="Add user"] button[type="submit"]');
  } },
  { section: "status-pages", scope: ".drawer-panel", submit: async (page) => {
    await page.waitForSelector('#status-pages ul[aria-label="Status pages"] li', { timeout: 15_000 });
    await press(page, "//*[@id='status-pages']//button[normalize-space()='New page']");
    const form = '.drawer-panel form[aria-label="New status page"]';
    await fill(page, `${form} input[maxlength="120"]`, "Acme for customers");
    await page.click(`${form} button[type="submit"]`);
  } },
  { section: "connectivity", scope: "#connectivity", submit: async (page) => {
    await fill(page, "#connectivity textarea", "gateway");
    await page.click("#connectivity button[type='submit']");
  } },
  { section: "retention", scope: "#retention", submit: async (page) => {
    await fill(page, '#retention input[aria-label="Keep raw heartbeats, in days"]', "60");
    await page.click('#retention form[aria-label="Retention"] button[type="submit"]');
  } },
  { section: "tokens", scope: "#tokens", submit: async (page) => {
    await fill(page, '#tokens form[aria-label="Create API token"] input[placeholder="grafana"]', "grafana");
    await page.click('#tokens form[aria-label="Create API token"] button[type="submit"]');
  } },
  { section: "reset", scope: "#reset", submit: async (page) => {
    await fill(page, '#reset form[aria-label="Reset this instance"] input[type="text"]', "DELETE ALL DATA");
    await page.click('#reset form[aria-label="Reset this instance"] button[type="submit"]');
  } },
];

/**
 * Cards with a field that this file does not drive to a `.field-error`, and
 * why. The completeness test fails on a card with a field that is in neither
 * list, and on an entry here that no longer names a card with a field.
 */
const EXEMPT: Record<string, string> = {
  configuration:
    "Its only field is the file chooser, and a refused import is not a sentence under it: it is a caveat box in full ink behind a rail, " +
    "which ConfigFiles.browser.test.ts audits with axe in both themes on the refused step.",
};

const settle = (page: Page) => page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations()
    .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
    .map((animation) => animation.finished.catch(() => undefined)));
  await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
});

async function openSettings(theme: string, hash = ""): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
  await page.setRequestInterception(true);
  page.on("request", (req) => {
    const path = new URL(req.url()).pathname;
    const refusal = REFUSALS[`${req.method()} ${path}`];
    if (refusal) {
      void req.respond({ status: 400, contentType: "application/json", body: JSON.stringify(refusal) });
    } else if (req.method() === "GET" && path === "/api/v1/settings/retention") {
      // The harness serves retention without a version; the card refuses to
      // save without one, before it ever asks the server.
      void req.respond({ status: 200, contentType: "application/json", headers: { etag: 'W/"1"' }, body: JSON.stringify(defaultRetention) });
    } else {
      void req.continue();
    }
  });
  await page.goto(`${server.url}/settings${hash}`, { waitUntil: "domcontentloaded" });
  return page;
}

/**
 * axe on the refusal and the field it names, not the whole card: what this
 * file sets up is a refusal, so what it answers for is the refusal. The rest
 * of each card is held by the accessibility gate and the card's own browser
 * test. Each finding carries axe's summary, so a red run states the ratio.
 */
async function audit(page: Page, scope: string) {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async (within) => {
    const include = [`${within} .field-error`, `${within} [aria-invalid="true"]`]
      .filter((selector) => document.querySelector(selector) !== null)
      .map((selector) => [selector]);
    const result = await (window as typeof window & { axe: typeof axe }).axe.run({ include }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return result.violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => `${node.target.join(" ")}: ${node.failureSummary ?? ""}`) }));
  }, scope);
}

/** Each refusal in the scope: its words and its glyph against what is really behind them. */
const MEASURE = `(scope) => {
  const luminance = ${LUMINANCE};
  const backdrop = ${BACKDROP};
  const over = ${OVER_BACKDROP};
  const ratio = (a, b) => {
    const x = luminance(a);
    const y = luminance(b);
    return Math.round(((Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)) * 100) / 100;
  };
  return [...document.querySelectorAll(scope + " .field-error")].map((el) => {
    // A glyph that is not drawn marks nothing, whatever colour it computes to.
    const svg = el.querySelector(":scope > svg");
    const box = svg && svg.getBoundingClientRect();
    const glyph = box && box.width >= 1 && box.height >= 1 ? svg : null;
    const words = over(el, getComputedStyle(el).color);
    const mark = glyph && over(glyph, getComputedStyle(glyph).stroke === "none" ? getComputedStyle(glyph).color : getComputedStyle(glyph).stroke);
    return {
      text: el.textContent.trim().slice(0, 40),
      words: words ? ratio(words, backdrop(el)) : 0,
      glyph: mark ? ratio(mark, backdrop(glyph)) : 0,
    };
  });
}`;

describe.each(["light", "dark"])("%s theme", (theme) => {
  it.each(CARDS.map((card) => [card.section, card] as const))("draws a legible refusal on %s", async (_, card) => {
    const page = await openSettings(theme, `#${card.section}`);
    try {
      await card.submit(page);
      await page.waitForSelector(`${card.scope} .field-error`, { visible: true, timeout: 10_000 });
      await settle(page);
      const seen = await page.evaluate(`(${MEASURE})(${JSON.stringify(card.scope)})`) as
        { text: string; words: number; glyph: number }[];
      expect(seen.length).toBeGreaterThan(0);
      for (const refusal of seen) {
        // The sentence is text at the helper size: 4.5:1. The glyph is a
        // graphic that marks the line as a refusal: 3:1, and present.
        expect(refusal.words, `${refusal.text}: words`).toBeGreaterThanOrEqual(4.5);
        expect(refusal.glyph, `${refusal.text}: glyph`).toBeGreaterThanOrEqual(3);
      }
      expect(await audit(page, card.scope)).toEqual([]);
    } finally {
      await page.close();
    }
  });
});

it("drives every settings card with a field to a refusal, or says why not", async () => {
  const page = await openSettings("dark");
  try {
    await page.waitForSelector("#users li .segmented", { timeout: 15_000 });
    // Every lazily loaded card has arrived and left its loading line.
    await page.waitForFunction(() => [...document.querySelectorAll(".settings-section")]
      .every((section) => !/Loading/.test(section.textContent ?? "")), { timeout: 15_000 });
    // A card whose fields stand on the page at rest. Users and status pages
    // keep theirs behind a button (a form, a drawer), so they are not found
    // here; both are driven above all the same.
    const withFields = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>(".settings-section")]
      .filter((section) => section.querySelector("input, textarea, select") !== null)
      .map((section) => section.id));
    const covered = CARDS.map((card) => card.section);
    expect(withFields.filter((id) => !covered.includes(id) && !(id in EXEMPT))).toEqual([]);
    expect(Object.keys(EXEMPT).filter((id) => !withFields.includes(id))).toEqual([]);
  } finally {
    await page.close();
  }
});
