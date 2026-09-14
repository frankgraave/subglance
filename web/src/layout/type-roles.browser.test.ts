/**
 * SUB-106 in a real browser: the face roles reach the pixels.
 *
 * `tokens.test.ts` proves the roles are declared and applied in the source. It
 * cannot prove they survive the build — `@utility` plus `@apply` is resolved by
 * Tailwind, and a role that fails to compile leaves `font-variant-numeric` at
 * its initial value with no error anywhere. jsdom cannot see it either: it
 * applies no stylesheet, so every computed style it reports is the default.
 *
 * What is measured here is the computed style of real elements on the rendered
 * dashboard, which is the only place the question can actually be settled.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { LAYOUT_STORAGE_KEY } from "../shell/preferences";
import { serveBuild, type Server } from "./harness/server";

let server: Server;
let browser: Browser;
let page: Page;

beforeAll(async () => {
  server = await serveBuild();
  browser = await chromium();
  page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 900, deviceScaleFactor: 1 });
  await page.goto(server.url + "/blank-for-storage", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    LAYOUT_STORAGE_KEY,
    "rows",
  );
  await page.goto(server.url + "/", { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".mon-cell--num", { timeout: 15_000 });
}, 120_000);

afterAll(async () => {
  await browser?.close();
  await server?.close();
});

function computed(selector: string, property: string): Promise<string> {
  return page.evaluate(
    (sel: string, prop: string) => {
      const el = document.querySelector(sel);
      if (!el) throw new Error(`no element matched ${sel}`);
      return getComputedStyle(el).getPropertyValue(prop);
    },
    selector,
    property,
  );
}

/**
 * The rgb() a token resolves to in the theme the page is actually in.
 *
 * Comparing against a literal would pin one theme: the headless browser runs
 * in light, where `--ink-2` is #5c6165, while the dark value is #9ba1a6. The
 * decision under test is the token, not either of its two values, so the token
 * is what gets resolved — through a probe element, because a custom property
 * reads back as its declared text rather than as a computed colour.
 */
function resolved(token: string): Promise<string> {
  return page.evaluate((name: string) => {
    const probe = document.createElement("span");
    probe.style.color = `var(${name})`;
    document.body.append(probe);
    const value = getComputedStyle(probe).color;
    probe.remove();
    return value;
  }, token);
}

describe("the mono role reaches the pixels", () => {
  it("gives a latency cell lined-up figures and a slashed zero", async () => {
    // The whole point of the role: this cell asked for the mono family and got
    // the numeric configuration with it, without naming either.
    const numeric = await computed(".mon-cell--num", "font-variant-numeric");
    expect(numeric).toContain("tabular-nums");
    expect(numeric).toContain("slashed-zero");
  });

  it("turns ligatures off on mono, so a URL keeps its characters", async () => {
    // `text-rendering: optimizeLegibility` on body enables ligatures and is
    // inherited. A mono ligature fuses `->` or `!=` into a glyph that is no
    // longer the characters it stands for, in the one place on screen where a
    // character has to be exactly itself.
    expect(await computed(".mon-target", "font-variant-ligatures")).toBe("none");
  });

  it("leaves the sans face proportional", async () => {
    // The inverse error: a tabular sans would space out every label on screen
    // to the width of a digit.
    const numeric = await computed(".mon-name", "font-variant-numeric");
    expect(numeric).toBe("normal");
  });
});

describe("the caps legend reaches the pixels", () => {
  // `.mon-head` is a column header in the rows layout, which the harness
  // already loads. It is one of the two rules that faded the label to
  // `--ink-4`, so it is the exact element the role was meant to fix.
  it("sets the legend in mono, not the sans it inherited", async () => {
    const family = await computed(".mon-head", "font-family");
    // Resolved family, not the token: this is what the engine actually chose.
    expect(family.toLowerCase()).toContain("mono");
  });

  it("keeps it uppercase at the section size and leading", async () => {
    expect(await computed(".mon-head", "text-transform")).toBe("uppercase");
    expect(await computed(".mon-head", "font-size")).toBe("12px");
    expect(await computed(".mon-head", "line-height")).toBe("12px");
  });

  it("renders it at the one tone that clears the text floor", async () => {
    // Asserted positively, because the decision was a specific tone and not
    // merely "not the old one": `--ink-4` (1.90:1) is what comes back if the
    // role fails to apply, but `--ink-3` measures 3.37:1 and is also below the
    // 4.5:1 that 12px text owes. `--ink-2` (7.31:1 dark, 6.26:1 light) is the
    // first rung that clears it, so that is what this pins.
    const colour = await computed(".mon-head", "color");
    expect(colour).toBe(await resolved("--ink-2"));
    expect(colour).not.toBe(await resolved("--ink-3"));
  });
});

describe("the shape and weight steps reach the pixels", () => {
  it("draws the lamp on the 2px step, not a fractional radius", async () => {
    // A half-pixel radius renders differently per device pixel ratio, so the
    // brand mark was not the same shape on every screen.
    expect(await computed(".led", "border-top-left-radius")).toBe("2px");
  });
});
