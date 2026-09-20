// @vitest-environment node
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium, type Browser, type Page } from "./harness/browser";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const dir = join(root, "docs/mockups");
function entries(path: string): string[] {
  return readdirSync(path, { withFileTypes: true }).flatMap(e =>
    e.isDirectory() ? entries(join(path, e.name)) : e.name.endsWith(".html") ? [join(path, e.name)] : [],
  ).sort();
}
const pages = entries(dir);
const tokens = readFileSync(join(root, "web/src/styles/tokens.css"), "utf8").split("\n@theme inline {")[0];
const names = [...new Set([...tokens.replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/(--[\w-]+)\s*:/g)].map(m => m[1]))];
const evidence = process.env.MOCKUP_PROOF_DIR;
let browser: Browser;
const proof: object[] = [];

beforeAll(async () => { browser = await chromium(); });
afterAll(async () => {
  await browser?.close();
  if (evidence) {
    mkdirSync(evidence, { recursive: true });
    writeFileSync(join(evidence, "computed.json"), JSON.stringify(proof, null, 2) + "\n");
  }
});

async function open(path: string, theme: string) {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 1000, deviceScaleFactor: 1 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  const errors: string[] = [];
  page.on("pageerror", e => errors.push(String(e)));
  page.on("requestfailed", req => errors.push(`${req.url()}: ${req.failure()?.errorText}`));
  await page.evaluateOnNewDocument((t: string) => {
    localStorage.setItem("sg-theme", t);
    localStorage.setItem("subglance:mockup-theme", t);
    localStorage.setItem("sg-variant", "a");
    localStorage.setItem("sg-pulse", "off");
  }, theme);
  await page.goto(pathToFileURL(path).href, { waitUntil: "load" });
  await page.evaluate(async (t: string) => {
    document.documentElement.dataset.theme = t;
    await Promise.all([document.fonts.load('14px "InterVariable"'), document.fonts.load('14px "CommitMono"')]);
    await document.fonts.ready;
    // Theme restoration can start CSS transitions after load. Wait for their
    // actual completion, not a fixed delay; infinite skeleton animations are
    // deliberately excluded because they never settle.
    await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
    await Promise.all(document.getAnimations().filter(a => a instanceof CSSTransition).map(a => a.finished.catch(() => undefined)));
  }, theme);
  return { page, errors };
}

async function capture(page: Page, name: string) {
  if (!evidence) return;
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
}

async function typography(page: Page) {
  return page.evaluate(() => {
    const rootStyle = getComputedStyle(document.documentElement);
    const roles = ["page", "card", "row", "body", "helper", "section"];
    const pairs = roles.map(r => [rootStyle.getPropertyValue(`--type-${r}`).trim(), rootStyle.getPropertyValue(`--lead-${r}`).trim()]);
    pairs.push([rootStyle.getPropertyValue("--type-helper").trim(), rootStyle.getPropertyValue("--lead-prose").trim()]);
    return Array.from(document.body.querySelectorAll<HTMLElement>("*"))
      .filter(el => !["SCRIPT", "STYLE", "SVG", "PATH"].includes(el.tagName) &&
        (Array.from(el.childNodes).some(n => n.nodeType === Node.TEXT_NODE && n.textContent?.trim()) || el.matches("input,textarea,select")))
      .flatMap(el => {
        const s = getComputedStyle(el);
        const weights = ["plain", "mid", "strong", "heavy"].map(w => rootStyle.getPropertyValue(`--weight-${w}`).trim());
        const face = s.fontFamily.startsWith("CommitMono") ? "mono" : "sans";
        const family = rootStyle.getPropertyValue(`--font-${face}`).trim().split(",")[0].replaceAll('"', "");
        const typeMatches = pairs.some(([size, lead]) => size === s.fontSize && lead === s.lineHeight);
        return typeMatches && weights.includes(s.fontWeight) && s.fontFamily.replaceAll('"', "").startsWith(family) ? [] :
          [{ element: el.tagName + "." + el.className, text: el.textContent?.trim().slice(0, 40), size: s.fontSize, leading: s.lineHeight, weight: s.fontWeight, family: s.fontFamily }];
      });
  });
}

async function statusAlternatives(page: Page) {
  return page.evaluate(() => Array.from(document.querySelectorAll<HTMLElement>(".led")).flatMap(led => {
    const wrap = led.parentElement!;
    const label = led.nextElementSibling as HTMLElement | null;
    const state = led.dataset.state || "idle";
    const words: Record<string, string[]> = {
      up: ["Up", "Recovered", "Connected", "Healthy"], down: ["Down"],
      warn: ["Warning", "Degraded", "Pending", "Acknowledged"],
      idle: ["Waiting", "No data yet"], off: ["Paused", "Not monitored"],
    };
    const text = label?.textContent?.trim() ?? "";
    const base = text.replace(/^Was /, "");
    const s = label ? getComputedStyle(label) : null;
    const valid = led.getAttribute("aria-hidden") === "true" && wrap.classList.contains("led-wrap") &&
      label?.matches(".sr-only, .led-label") && !label.closest('[aria-hidden="true"], [hidden]') &&
      s?.display !== "none" && s?.visibility !== "hidden" &&
      words[state]?.some(word => word.toLowerCase() === base.toLowerCase());
    return valid ? [] : [{ state, text, html: wrap.outerHTML.slice(0, 260) }];
  }));
}

it("keeps resting LEDs finite with default motion and pulse preferences", async () => {
  // Isolate storage from the reduced-motion/pulse-off helpers above. Do not
  // override either preference: this is the first-visit, natural default path.
  const context = await browser.createBrowserContext();
  try {
    for (const path of pages) {
      const page = await context.newPage();
      try {
        await page.goto(pathToFileURL(path).href, { waitUntil: "load" });
        const result = await page.evaluate(() => {
          const lamps = Array.from(document.querySelectorAll(".led"));
          const infinite = lamps.flatMap((led, index) =>
            led.getAnimations({ subtree: true })
              .filter(a => a.effect?.getComputedTiming().iterations === Infinity)
              .map(() => index));
          return { count: lamps.length, infinite, reduced: matchMedia("(prefers-reduced-motion: reduce)").matches, pulse: document.body.dataset.pulse };
        });
        expect(result.reduced, relative(dir, path)).toBe(false);
        if (path.endsWith("dashboard-directions.html")) expect(result.pulse).toBe("on");
        expect(result.count).toBeGreaterThan(0);
        expect(result.infinite, `${relative(dir, path)}: resting LEDs must not animate forever`).toEqual([]);
      } finally { await page.close(); }
    }
  } finally { await context.close(); }
});

for (const theme of ["dark", "light"]) {
  describe(`file:// mockup contract in ${theme}`, () => {
    it.each(pages)("%s resolves the shared system with readable paired type", async path => {
      const { page, errors } = await open(path, theme);
      try {
        const actual = await page.evaluate((keys: string[]) => Object.fromEntries(keys.map(key => [key, getComputedStyle(document.documentElement).getPropertyValue(key).trim()])), names);
        // A separate document with ONLY the real source. Comparing mockup tokens
        // against themselves would bless an overriding palette as correct.
        const ref = await browser.newPage();
        let expected: Record<string, string>;
        try {
          await ref.setContent(`<html data-theme="${theme}"><style>${tokens}</style></html>`);
          expected = await ref.evaluate((keys: string[]) => Object.fromEntries(keys.map(key => [key, getComputedStyle(document.documentElement).getPropertyValue(key).trim()])), names);
        } finally { await ref.close(); }
        expect(actual, "resolved live tokens").toEqual(expected);
        expect(await typography(page), "whole-pixel size/leading on every text element").toEqual([]);
        const fonts = await page.evaluate(() => Array.from(document.fonts).map(f => ({ family: f.family, status: f.status })));
        expect(fonts).toEqual(expect.arrayContaining([{ family: "InterVariable", status: "loaded" }, { family: "CommitMono", status: "loaded" }]));
        const ledger = { file: relative(dir, path), theme, url: page.url(), fonts, tokens: actual, lamps: await page.$$eval(".led", els => els.length) };
        proof.push(ledger);
        expect(page.url()).toMatch(/^file:\/\//);
        expect(errors, "disk resources and scripts").toEqual([]);
        await capture(page, `${relative(dir, path).replaceAll("/", "-").replace(".html", "")}-${theme}`);
      } finally { await page.close(); }
    });

    it.each(pages)("%s uses current surfaces, density and segmented geometry", async path => {
      const { page } = await open(path, theme);
      try {
        const faults = await page.evaluate(() => {
          const errors: string[] = [];
          const probe = document.createElement("div");
          probe.style.setProperty("transition", "none", "important");
          probe.style.setProperty("animation", "none", "important");
          document.body.append(probe);
          const resolved = (prop: string, value: string) => {
            probe.style.setProperty(prop, value);
            return getComputedStyle(probe).getPropertyValue(prop);
          };
          const check = (el: Element, prop: string, value: string) => {
            const actual = getComputedStyle(el).getPropertyValue(prop);
            const expected = resolved(prop, value);
            if (actual !== expected) errors.push(`${el.tagName}.${el.className} ${prop}: ${actual} != ${expected}`);
          };
          check(document.body, "background-color", "var(--canvas)");
          for (const card of document.querySelectorAll(".card, .frame, .board, a.proposal")) {
            check(card, "border-radius", "var(--r-lg)");
            check(card, "background-color", "var(--surface)");
            check(card, "box-shadow", "var(--shadow-flat)");
            if (!card.matches(".empty, .frame, .board")) check(card, "padding-top", "var(--space-4)");
          }
          for (const panel of document.querySelectorAll(".frame-panel, .panel:not(.secret), .row, .demo-row, .skel-row")) {
            check(panel, "border-radius", "var(--r-md)");
            check(panel, "background-color", "var(--surface-panel)");
            check(panel, "box-shadow", "var(--shadow-raised)");
            check(panel, "padding-top", "var(--space-3)");
            check(panel, "padding-right", "var(--space-4)");
          }
          for (const frame of document.querySelectorAll(".frame, .board")) {
            check(frame, "padding-top", "var(--space-1h)");
            check(frame, "gap", "var(--space-1h)");
          }
          for (const seg of document.querySelectorAll(".seg, .segmented, .switcher")) {
            check(seg, "border-radius", "var(--r-md)");
            check(seg, "padding-top", "var(--space-0h)");
            const selected = seg.querySelectorAll('button[aria-pressed="true"]');
            if (selected.length !== 1) errors.push(`${seg.className}: needs exactly one selection`);
            for (const button of seg.querySelectorAll("button")) {
              check(button, "border-radius", "var(--r-sm)");
              check(button, "min-height", "var(--size-square-compact)");
              if (button.getAttribute("aria-pressed") === "true") {
                check(button, "background-color", "var(--accent)");
                check(button, "color", "var(--accent-ink)");
                check(button, "border-color", "var(--accent-border)");
              }
            }
          }
          // Structural surface depth, not DOM depth. Neutral layout wrappers
          // may group rows; a wrapper painting a third panel may not.
          const surface = ".card, .frame, .board, .frame-panel, .panel, .row, .demo-row, .skel-row, .inner";
          for (const leaf of document.querySelectorAll(surface)) {
            let depth = 0;
            for (let el: Element | null = leaf; el && el !== document.body; el = el.parentElement) {
              if (el.matches(surface) && getComputedStyle(el).backgroundColor !== "rgba(0, 0, 0, 0)") depth++;
            }
            if (depth > 2) errors.push(`${leaf.className}: ${depth} painted structural surfaces; maximum two`);
          }
          probe.remove();
          return errors;
        });
        expect(faults, "current palette/radius/depth/density/nesting and control accent").toEqual([]);
      } finally { await page.close(); }
    });

    it("keeps dashboard labels honest through every layout, outage, drawer and disconnect", async () => {
      const { page } = await open(join(dir, "dashboard-directions.html"), theme);
      try {
        for (const variant of ["a", "b", "c", "d"]) {
          await page.click(`.switcher [data-v="${variant}"]`);
          expect(await statusAlternatives(page), `layout ${variant}`).toEqual([]);
          await capture(page, `dashboard-${variant}-${theme}`);
        }
        await page.click('.switcher [data-v="a"]');
        await page.click('.seg button:nth-child(2)');
        expect(await page.$eval('.seg button:nth-child(2)', el => el.getAttribute("aria-pressed"))).toBe("true");
        await page.click('#simulate');
        for (const [state, word] of [["warn", "Warning"], ["down", "Down"], ["up", "Up"]]) {
          await expect(page.waitForFunction((s: string, w: string) => {
            const led = document.querySelector('.row[data-id="4"] .led');
            return led?.getAttribute("data-state") === s && led.nextElementSibling?.textContent === w;
          }, { timeout: 5000 }, state, word), `outage ${state}: lamp and word must agree in one observation`).resolves.toBeTruthy();
          expect(await statusAlternatives(page), `outage ${state}`).toEqual([]);
        }
        await page.click('.row[data-id="3"]');
        // The click updates text synchronously but the drawer's sliding animation only
        // settles at the next frame, even with reduced-motion's short duration.
        await page.waitForFunction(() => {
          const drawer = document.querySelector('#drawer')!;
          return drawer.classList.contains('open') && drawer.getBoundingClientRect().right <= innerWidth + 0.5;
        });
        expect(await page.$eval('#dLed + span', el => el.textContent)).toBe("Warning");
        expect(await statusAlternatives(page), "drawer and generated timeline").toEqual([]);
        await page.click('#dClose');
        await page.click('#simulateConn');
        expect(await page.$eval('.row[data-id="4"] .led + span', el => el.textContent)).toBe("Was up");
        expect(await statusAlternatives(page), "stale claims stay textual").toEqual([]);
        await page.click('#simulateConn');
        expect(await page.$eval('.row[data-id="4"] .led + span', el => el.textContent)).toBe("Up");
      } finally { await page.close(); }
    });

    it("covers generated component toasts and inline edit typography", async () => {
      const { page } = await open(join(dir, "components.html"), theme);
      try {
        await page.click('#toastBtn');
        await page.waitForSelector('.toast .led');
        expect(await statusAlternatives(page)).toEqual([]);
        await page.click('[data-editable]');
        expect(await typography(page), "editing input").toEqual([]);
        await page.keyboard.press('Enter');
        expect(await typography(page), "saved name").toEqual([]);
      } finally { await page.close(); }
    });

    it.each(pages)("%s gives every LED an exposed word, not a disclaimer", async path => {
      const { page } = await open(path, theme);
      try {
        expect(await page.$$eval(".led", els => els.length)).toBeGreaterThan(0);
        expect(await statusAlternatives(page), "every LED has its matching accessible text alternative").toEqual([]);
      } finally { await page.close(); }
    });
  });
}
