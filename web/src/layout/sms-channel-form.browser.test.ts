/**
 * The SMS channel form, in a real layout engine (SUB-164).
 *
 * The SMS type brings three controls no other channel form has: a provider
 * select that swaps the fields beneath it, a textarea for the phone numbers,
 * and a checkbox for recovery messages. jsdom proves they send the right
 * config; it cannot prove they are operable. This file opens the form in the
 * Add channel drawer and holds it to what the rest of the app is held to:
 * no axe violation at WCAG 2 A/AA, and a 24x24 target (SC 2.5.8) for every
 * control, measured on the control's own box as `phone-layout.browser.test.ts`
 * does — a checkbox inside a big label is still a 13px checkbox.
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

/**
 * Picks an option in the drawer's nth select the way React hears it: through
 * the native value setter and a bubbling change event.
 */
async function choose(page: Page, index: number, value: string): Promise<void> {
  await page.evaluate(
    (i, v) => {
      const select = [...document.querySelectorAll<HTMLSelectElement>(".drawer-panel select")][i];
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
      setter?.call(select, v);
      select.dispatchEvent(new Event("change", { bubbles: true }));
    },
    index,
    value,
  );
}

async function openSmsForm(width: number): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/notifications", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".inv-row", { timeout: 15_000 });
  const add = await page.waitForSelector('button[aria-label="Add channel"]', { visible: true });
  if (!add) throw new Error("no Add channel button");
  await add.click();
  await page.waitForSelector(".drawer-panel .form-column", { visible: true });
  await choose(page, 0, "sms");
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

describe("the SMS channel form", () => {
  for (const width of [1440, 390]) {
    it(`passes axe and gives every control a 24px target at ${width}px`, async () => {
      const page = await openSmsForm(width);
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
                what: `${el.tagName.toLowerCase()}[${el.getAttribute("type") ?? ""}] ${el.id || el.className}`,
                w: Math.round(r.width),
                h: Math.round(r.height),
              };
            })
            .filter((t) => (t.w > 0 || t.h > 0) && (t.w < 24 || t.h < 24)),
        );
        expect(small).toEqual([]);

        // The recovery checkbox's whole sentence is its target: the label
        // wraps the box, so a click on the words toggles it.
        const toggled = await page.evaluate(() => {
          const label = document.querySelector<HTMLLabelElement>(".drawer-panel .choice-label");
          const box = label?.querySelector<HTMLInputElement>("input[type=checkbox]");
          if (!label || !box) return null;
          const before = box.checked;
          label.click();
          return before !== box.checked;
        });
        expect(toggled).toBe(true);
      } finally {
        await page.close();
      }
    }, 60_000);
  }

  it("swaps the provider's fields without moving the shared ones", async () => {
    const page = await openSmsForm(1440);
    try {
      const labels = () =>
        page.evaluate(() =>
          [...document.querySelectorAll(".drawer-panel .form-column .field-label")].map(
            (el) => el.textContent?.trim() ?? "",
          ),
        );
      const gateway = await labels();
      expect(gateway).toContain("Gateway address");
      expect(gateway).not.toContain("Auth token");
      await choose(page, 1, "twilio");
      const twilio = await labels();
      expect(twilio).toContain("Auth token");
      expect(twilio).not.toContain("Gateway address");
      // Numbers and country code stay put above the provider's fields.
      expect(twilio.slice(0, 5)).toEqual(gateway.slice(0, 5));
    } finally {
      await page.close();
    }
  }, 60_000);
});
