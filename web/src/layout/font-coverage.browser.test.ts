/** SUB-109: inspect the fonts that painted glyphs, not CSS family names. */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { mkdir } from "node:fs/promises";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import type { ApiMonitor } from "../monitors/types";

let server: Server;
let browser: Browser;
const names = ["API Café Αθήνα Москва", "Αθήνα", "Москва", "Café Łódź"];

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
});
afterAll(async () => {
  await browser?.close();
  await server?.close();
});

async function paintedFonts(page: Page, selector: string) {
  await page.$eval(selector, (el) => el.scrollIntoView({ block: "center" }));
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  });
  const session = await page.createCDPSession();
  try {
    await session.send("DOM.enable");
    await session.send("CSS.enable");
    const { root } = await session.send("DOM.getDocument");
    const { nodeId } = await session.send("DOM.querySelector", { nodeId: root.nodeId, selector });
    expect(nodeId, `missing rendered node: ${selector}`).not.toBe(0);
    const { fonts } = await session.send("CSS.getPlatformFontsForNode", { nodeId });
    expect(fonts.length, `no glyphs painted: ${selector}`).toBeGreaterThan(0);
    return fonts.filter((font) => font.glyphCount > 0);
  } finally {
    await session.detach();
  }
}

for (const theme of ["light", "dark"]) {
  for (const width of [375, 1440]) {
    describe(`${theme}, ${width}`, () => {
      it("keeps mixed-script names usable with explicit Latin and system coverage", async () => {
        const page = await browser.newPage();
        const external: string[] = [];
        try {
          await page.setViewport({ width, height: 900 });
          await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
          await page.setRequestInterception(true);
          page.on("request", async (request) => {
            const url = new URL(request.url());
            if (url.origin !== server.url) {
              external.push(request.url());
              await request.abort("blockedbyclient");
            } else if (url.pathname === "/api/v1/monitors") {
              // Preserve wire shape, status and timings. A short tag keeps
              // this font test independent of long-tag wrapping.
              const response = await fetch(request.url());
              const body = await response.json();
              const rename = (monitor: ApiMonitor) => ({ ...monitor,
                name: names[Number(monitor.id) - 1], target: "https://probe.invalid/Αθήνα/Москва",
                tags: { env: "production" },
              });
              await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify(
                { ...body, monitors: body.monitors.map(rename) },
              ) });
            } else {
              await request.continue();
            }
          });
          await page.goto(server.url + "/monitors", { waitUntil: "domcontentloaded" });
          await page.waitForSelector('.inv-name a[href="/monitors/1"]');
          await page.evaluate(() => document.fonts.ready.then(() => undefined));
          expect(await page.$eval("html", (el) => el.dataset.theme)).toBe(theme);

          for (let id = 1; id <= names.length; id++) {
            const selector = `.inv-name a[href="/monitors/${id}"]`;
            expect(await page.$eval(selector, (el) => el.textContent)).toBe(names[id - 1]);
            const fonts = await paintedFonts(page, selector);
            const custom = fonts.filter((font) => font.isCustomFont);
            if (id === 1 || id === 4) {
              expect(custom.map((font) => font.familyName)).toEqual(["Inter Variable"]);
            }
            // A clipped label can also paint a Latin ellipsis. The untruncated
            // probes below assert exclusive system coverage for pure scripts.
            expect(fonts.some((font) => !font.isCustomFont), names[id - 1]).toBe(id !== 4);
          }
          const mono = await paintedFonts(page, ".inv-sub");
          expect(mono.filter((font) => font.isCustomFont).map((font) => font.familyName)).toEqual(["CommitMono"]);
          expect(mono.some((font) => !font.isCustomFont), "non-Latin target needs fallback too").toBe(true);

          // Independent probes inherit each real role. Precomposed Latin and UI
          // symbols stay self-hosted; Greek/Cyrillic (including extended and
          // decomposed forms) use system glyphs. No network font can mask a gap.
          for (const role of ["sans", "mono"]) {
            for (const [text, custom] of [["Café Łódź € ← −", true], ["Αθήνα", false], ["Москва", false], ["Ἀθήνα", false], ["И\u0306", false]] as const) {
              await page.evaluate((role, text) => {
                document.getElementById("font-probe")?.remove();
                const span = document.createElement("span");
                span.id = "font-probe";
                span.style.fontFamily = `var(--font-${role})`;
                span.textContent = text;
                document.querySelector("main")!.append(span);
              }, role, text);
              const fonts = await paintedFonts(page, "#font-probe");
              expect(fonts.every((font) => font.isCustomFont === custom), `${role}: ${text}`).toBe(true);
            }
          }
          await page.evaluate(() => document.getElementById("font-probe")?.remove());
          await page.evaluate(() => window.scrollTo(0, 0));
          const layout = await page.evaluate(() => ({
            width: innerWidth, scrollWidth: document.documentElement.scrollWidth,
            overflowing: [...document.querySelectorAll("main *")].filter((el) => el.getBoundingClientRect().right > innerWidth)
              .map((el) => ({ tag: el.tagName, class: el.className, right: el.getBoundingClientRect().right })),
          }));
          expect(layout.scrollWidth, JSON.stringify(layout)).toBeLessThanOrEqual(width);
          await page.addScriptTag({ content: axe.source });
          const violations = await page.evaluate(async () => {
            const result = await (window as typeof window & { axe: typeof axe }).axe.run(".inv-name a", {
              runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
            });
            return result.violations;
          });
          expect(violations, "mixed-script link accessibility, including contrast").toEqual([]);
          await page.evaluate(() => window.scrollTo(0, 0));
          if (process.env.SUBGLANCE_FONT_SCREENSHOTS) {
            await mkdir(process.env.SUBGLANCE_FONT_SCREENSHOTS, { recursive: true });
            await page.screenshot({ path: `${process.env.SUBGLANCE_FONT_SCREENSHOTS}/${theme}-${width}.png`, fullPage: true });
          }
          const link = await page.waitForSelector('.inv-name a[href="/monitors/1"]');
          await link!.focus();
          await page.keyboard.press("Enter");
          await page.waitForSelector(".mon-detail-name");
          expect(await page.$eval(".mon-detail-name", (el) => el.textContent)).toBe(names[0]);
          const detail = await paintedFonts(page, ".mon-detail-name");
          expect(detail.some((font) => font.isCustomFont)).toBe(true);
          expect(detail.some((font) => !font.isCustomFont)).toBe(true);
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
          if (process.env.SUBGLANCE_FONT_SCREENSHOTS) {
            await page.screenshot({ path: `${process.env.SUBGLANCE_FONT_SCREENSHOTS}/${theme}-${width}-detail.png`, fullPage: true });
          }
          expect(external).toEqual([]);
        } finally {
          await page.close();
        }
      });
    });
  }
}
