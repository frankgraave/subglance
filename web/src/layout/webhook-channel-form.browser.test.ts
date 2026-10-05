/**
 * The webhook channel form, in a real layout engine (SUB-211).
 *
 * The webhook type brings a method select and a body template: a monospaced
 * textarea a person types JSON into. jsdom proves they send the right config;
 * this file proves the form is operable. It opens the form in the Add channel
 * drawer at desktop and phone widths and holds it to no axe violation at WCAG 2
 * A/AA, a 24x24 target (SC 2.5.8) for every control, a body field in the mono
 * face that stays inside the drawer, and no horizontal scroll in the drawer.
 *
 * Does not run with `npm test`: needs a built bundle and a browser.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";

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

async function openWebhookForm(width: number): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/notifications", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inv-row", { timeout: 15_000 });
  const add = await page.waitForSelector('button[aria-label="Add channel"]', { visible: true });
  if (!add) throw new Error("no Add channel button");
  await add.click();
  await page.waitForSelector(".drawer-panel .form-column", { visible: true });
  await page.evaluate(() => {
    const select = document.querySelector<HTMLSelectElement>(".drawer-panel select");
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
    setter?.call(select, "webhook");
    select?.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await page.waitForSelector(".drawer-panel textarea", { visible: true });
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => undefined)),
    );
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
    );
  });
  return page;
}

describe("the webhook channel form", () => {
  for (const width of [1440, 390]) {
    it(`passes axe, sizes every control and keeps the body in the drawer at ${width}px`, async () => {
      const page = await openWebhookForm(width);
      try {
        await page.addScriptTag({ content: axe.source });
        const violations = await page.evaluate(async () => {
          const result = await (window as typeof window & { axe: typeof axe }).axe.run(
            ".drawer-panel",
            { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] } },
          );
          return result.violations.map(({ id, nodes }) => ({
            id,
            targets: nodes.map((n) => n.target),
          }));
        });
        expect(violations).toEqual([]);

        const small = await page.evaluate(() =>
          [
            ...document.querySelectorAll<HTMLElement>(
              ".drawer-panel input, .drawer-panel select, .drawer-panel textarea, .drawer-panel button",
            ),
          ]
            .map((el) => {
              const r = el.getBoundingClientRect();
              return {
                what: `${el.tagName.toLowerCase()} ${el.id || el.className}`,
                w: Math.round(r.width),
                h: Math.round(r.height),
              };
            })
            .filter((t) => (t.w > 0 || t.h > 0) && (t.w < 24 || t.h < 24)),
        );
        expect(small).toEqual([]);

        const body = await page.evaluate(() => {
          const panel = document.querySelector<HTMLElement>(".drawer-panel");
          const label = [...document.querySelectorAll<HTMLLabelElement>(".drawer-panel label")].find(
            (l) => l.textContent?.startsWith("Body"),
          );
          const field = label ? document.getElementById(label.htmlFor) : null;
          if (!panel || !field) return null;
          const p = panel.getBoundingClientRect();
          const f = field.getBoundingClientRect();
          // The first family named by the mono token, compared without
          // quotes: Chrome serialises the computed list in its own style.
          const first = (list: string) => list.split(",")[0]?.replace(/["']/g, "").trim();
          const mono = first(getComputedStyle(document.documentElement).getPropertyValue("--font-mono"));
          return {
            tag: field.tagName.toLowerCase(),
            mono: mono !== "" && first(getComputedStyle(field).fontFamily) === mono,
            inside: f.left >= p.left - 0.5 && f.right <= p.right + 0.5,
            tall: f.height > 100,
            scrolls: panel.scrollWidth > panel.clientWidth,
          };
        });
        expect(body).toEqual({ tag: "textarea", mono: true, inside: true, tall: true, scrolls: false });
      } finally {
        await page.close();
      }
    }, 60_000);
  }
});
