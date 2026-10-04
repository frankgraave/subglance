/**
 * The small things a reader has to tell apart on the settings and delete
 * screens, measured in the real build (DESIGN.md §8.12).
 *
 * jsdom applies no stylesheet, so a unit test can prove the phrase is wrapped
 * and the field is disabled, but not that the phrase still reads as the label
 * around it, or that a switched-off field still looks like an empty one that
 * failed to load. Those are decided by the cascade: the label's mono caps
 * reach into anything inside it, uppercase included.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
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

async function open(path: string, ready: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(ready, { visible: true, timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  return page;
}

/** How a phrase is drawn against the label it sits in. */
async function phraseInLabel(page: Page, label: string) {
  return page.$eval(label, (el) => {
    const phrase = el.querySelector<HTMLElement>(".phrase");
    if (!phrase) return null;
    const own = getComputedStyle(phrase);
    const around = getComputedStyle(el);
    return {
      // innerText is what is painted, case transforms included.
      painted: phrase.innerText,
      typed: phrase.textContent,
      boxed: own.borderTopStyle === "solid" && parseFloat(own.borderTopWidth) >= 1,
      inkDiffers: own.color !== around.color,
    };
  });
}

describe("the phrase to retype", () => {
  it("stands apart from the reset card's label, in the case it has to be typed", async () => {
    const page = await open("/settings#reset", "#reset .field-label");
    try {
      const drawn = await phraseInLabel(page, "#reset .field-label");
      expect(drawn).toEqual({ painted: "DELETE ALL DATA", typed: "DELETE ALL DATA", boxed: true, inkDiffers: true });
    } finally {
      await page.close();
    }
  }, 60_000);

  it("shows a lowercase monitor name in lowercase, inside a caps label", async () => {
    const page = await open("/monitors", ".inv-list > li");
    try {
      const button = await page.waitForSelector('button[aria-label^="Delete "]', { visible: true });
      const name = (await button!.evaluate((el) => el.getAttribute("aria-label") ?? "")).slice("Delete ".length);
      await button!.click();
      const label = '[role="dialog"] .field-label';
      await page.waitForSelector(label, { visible: true });
      const drawn = await phraseInLabel(page, label);
      // The seed names are lowercase ("api"); the label's uppercase showed
      // "API" while the check wants exactly "api".
      expect(name).not.toBe(name.toUpperCase());
      expect(drawn).toEqual({ painted: name, typed: name, boxed: true, inkDiffers: true });
    } finally {
      await page.close();
    }
  }, 60_000);
});

describe("a retention amount that is switched off", () => {
  it("is dimmed and shows a dash, not an empty box", async () => {
    const page = await open("/settings#retention", "#retention input[type=number]");
    try {
      const fields = await page.$$eval("#retention input[type=number]", (els) => els.map((el) => {
        const input = el as HTMLInputElement;
        const style = getComputedStyle(input);
        return {
          name: input.getAttribute("aria-label"),
          disabled: input.disabled,
          value: input.value,
          placeholder: input.placeholder,
          opacity: Number(style.opacity),
          cursor: style.cursor,
        };
      }));
      // The fixture keeps hourly summaries forever and sets no size limit.
      const off = fields.filter((field) => field.disabled);
      expect(off.map((field) => field.name)).toEqual([
        "Keep hourly summaries and resolved incidents, in days",
        "Limit the database to, in MB",
      ]);
      for (const field of off) {
        expect(field, field.name ?? "").toMatchObject({ value: "", placeholder: "—", opacity: 0.5, cursor: "not-allowed" });
      }
      for (const field of fields.filter((item) => !item.disabled)) {
        expect(field, field.name ?? "").toMatchObject({ placeholder: "", opacity: 1 });
      }
    } finally {
      await page.close();
    }
  }, 60_000);
});
