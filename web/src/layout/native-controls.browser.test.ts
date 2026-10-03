/**
 * The dropdown's arrow and the file chooser's button as they are drawn, in
 * the real build (SUB-190, DESIGN.md §8.11).
 *
 * `tokens.test.ts` proves no file writes a `<select>` or a file input past
 * `Select` and `FileInput`. It cannot see the picture: `appearance: none`
 * takes the platform's arrow away, and a later `background` shorthand in any
 * field's class wipes the painted one, which leaves a select that reads as a
 * text box and still passes every DOM assertion. So this opens every screen
 * the harness can show a select on, in both themes, and asks the browser
 * what it computed and the compositor what it painted.
 *
 * @vitest-environment node
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";
import { colourAt } from "./harness/pixel";

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

/** `scope` holds the selects measured: with a drawer open, the page behind it is under the scrim. */
type Screen = { name: string; path: string; ready: string; scope: string; drawer?: boolean; minSelects: number };

/*
 * The floor per screen is what the harness shows today, so a screen that
 * stops rendering its selects fails here instead of passing on nothing.
 */
const SCREENS: Screen[] = [
  { name: "monitors toolbar", path: "/monitors", ready: ".shell-toolbar .tb-select", scope: ".shell-toolbar", minSelects: 2 },
  { name: "add monitor drawer", path: "/monitors", ready: ".inv-list > li", scope: ".drawer-panel", drawer: true, minSelects: 2 },
  { name: "notifications", path: "/notifications", ready: ".inv-row", scope: "main", minSelects: 1 },
  { name: "settings", path: "/settings", ready: "#tokens select", scope: "main", minSelects: 1 },
];

async function open(screen: Screen, theme: string): Promise<Page> {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.evaluateOnNewDocument(
    (key: string, value: string) => window.localStorage.setItem(key, value),
    THEME_STORAGE_KEY,
    theme,
  );
  await page.goto(server.url + screen.path, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(screen.ready, { visible: true, timeout: 15_000 });
  if (screen.drawer) {
    await (await page.waitForSelector('button[aria-label="Add monitor"]', { visible: true }))!.click();
    await page.waitForSelector(".drawer-panel .form-column", { visible: true });
  }
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations()
      .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map((animation) => animation.finished.catch(() => undefined)));
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
    // Settings mounts its cards lazily, and a card arriving between the
    // measurement and the screenshot moves the select out from under the
    // sample. Wait until the page has held one height for 300ms.
    let height = -1;
    for (let still = 0; still < 300; ) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      const now = document.documentElement.scrollHeight;
      still = now === height ? still + 50 : 0;
      height = now;
    }
  });
  return page;
}

function distance(a: string, b: string): number {
  const x = a.split(",").map(Number);
  const y = b.split(",").map(Number);
  return Math.max(...x.map((v, i) => Math.abs(v - y[i])));
}

type Handle = NonNullable<Awaited<ReturnType<Page["$"]>>>;

/** Instant, then two frames: a smooth scroll would still be moving when the compositor is asked what it drew. */
async function centre(select: Handle): Promise<void> {
  await select.evaluate(async (el) => {
    el.scrollIntoView({ block: "center", behavior: "instant" });
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  });
}

/** A state's border and ring are transitions: let them end before the pixels are read. */
async function settle(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations()
      .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
      .map((animation) => animation.finished.catch(() => undefined)));
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  });
}

/** What is wrong with the select's painted caret where it stands now, or null when it is drawn. */
async function caretFault(page: Page, select: Handle): Promise<object | null> {
  const drawn = await select.evaluate((el) => {
    const s = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    const border = parseFloat(s.borderRightWidth);
    // The second gradient's right edge, read back from what the
    // browser computed: `right 12px` comes back as
    // `calc(100% - 12px)`, `right 0` as `100%`.
    const second = s.backgroundPositionX.split(",")[1]?.trim() ?? "";
    const offset = /^calc\(100% - ([\d.]+)px\)$/.exec(second);
    const inset = offset ? parseFloat(offset[1]) : second === "100%" ? 0 : NaN;
    return {
      name: el.getAttribute("aria-label") || el.id || el.className,
      cls: el.className,
      appearance: s.appearance,
      image: s.backgroundImage,
      position: s.backgroundPositionX,
      // Where the two squares meet, one square (`background-size`)
      // in from the second one's edge: the caret's middle column.
      caretX: r.right - border - inset - parseFloat(s.backgroundSize),
      innerRight: r.right - border,
      top: r.top,
      cy: r.top + r.height / 2,
      padRight: parseFloat(s.paddingRight),
    };
  });
  const gradients = (drawn.image.match(/linear-gradient/g) ?? []).length;
  // A `background` shorthand resets the size and position too, which leaves
  // no caret to look for: that is the fault, not a point to sample.
  if (gradients !== 2 || !Number.isFinite(drawn.caretX)) return drawn;
  // The painted caret, against the field's own fill two pixels in
  // from its edge, where nothing is drawn. The caret is 4px tall and
  // centred, so a short column through its middle crosses it
  // wherever subpixel rounding puts it.
  const fill = await colourAt(page, drawn.innerRight - 2, drawn.top + 3);
  let contrast = 0;
  let caret = fill;
  for (const dx of [-1, 0]) {
    for (const dy of [-3, -2, -1, 0, 1]) {
      const sample = await colourAt(page, drawn.caretX + dx, drawn.cy + dy);
      if (distance(sample, fill) > contrast) { contrast = distance(sample, fill); caret = sample; }
    }
  }
  if (!drawn.cls.split(" ").includes("select") || drawn.appearance !== "none" || gradients !== 2
      || contrast < 40 || drawn.padRight < 16) {
    return { ...drawn, caret, fill, contrast };
  }
  return null;
}

/*
 * The states a stylesheet gives a select a rule of its own for. Any of them
 * can clear what the resting rule painted: a `background` shorthand on
 * `:hover` or `:disabled` wipes the caret only while the select is in that
 * state, which a pass at rest never sees.
 */
const STATES = ["hover", "focus-visible", "disabled"] as const;

for (const theme of ["dark", "light"]) {
  describe(`the dropdown arrow in ${theme}`, () => {
    it.each(SCREENS)("$name: every select draws the painted caret, not the platform's", async (screen) => {
      const page = await open(screen, theme);
      try {
        const selects = await page.$$(`${screen.scope} select`);
        const faults: object[] = [];
        let seen = 0;
        for (const select of selects) {
          if (!(await select.evaluate((el) => el.checkVisibility()))) continue;
          await centre(select);
          seen++;
          const fault = await caretFault(page, select);
          if (fault) faults.push(fault);
        }
        expect(seen, `${screen.name}: visible selects`).toBeGreaterThanOrEqual(screen.minSelects);
        expect(faults).toEqual([]);
      } finally {
        await page.close();
      }
    }, 60_000);

    // One select of each kind: the toolbar's own class and a form field's `.input`.
    it.each(SCREENS.slice(0, 2))("$name: the caret survives hover, keyboard focus and disabled", async (screen) => {
      const page = await open(screen, theme);
      try {
        let select: Handle | undefined;
        for (const candidate of await page.$$(`${screen.scope} select`)) {
          if (await candidate.evaluate((el) => el.checkVisibility())) { select = candidate; break; }
        }
        expect(select, `${screen.name}: a visible select`).toBeDefined();
        if (!select) return;
        await centre(select);
        const faults: object[] = [];
        for (const state of STATES) {
          await select.evaluate((el) => {
            (el as HTMLSelectElement).disabled = false;
            (el as HTMLSelectElement).blur();
          });
          await page.mouse.move(0, 0);
          if (state === "hover") await page.mouse.move(...(await select.evaluate((el) => {
            const r = el.getBoundingClientRect();
            return [r.left + r.width / 2, r.top + r.height / 2] as [number, number];
          })));
          // A key first: script focus after a pointer interaction is not a keyboard focus.
          if (state === "focus-visible") {
            await page.keyboard.press("Shift");
            await select.evaluate((el) => (el as HTMLSelectElement).focus());
          }
          if (state === "disabled") await select.evaluate((el) => { (el as HTMLSelectElement).disabled = true; });
          await settle(page);
          const style = await select.evaluate((el, s) => ({
            reached: el.matches(`:${s}`),
            cursor: getComputedStyle(el).cursor,
            opacity: Number(getComputedStyle(el).opacity),
          }), state);
          expect(style.reached, `${screen.name}: the select is in :${state}`).toBe(true);
          // Disabled is dimmed and refuses the pointer, as a disabled `.button` does.
          if (state === "disabled") expect(style).toMatchObject({ cursor: "not-allowed", opacity: 0.5 });
          const fault = await caretFault(page, select);
          if (fault) faults.push({ state, ...fault });
        }
        expect(faults).toEqual([]);
      } finally {
        await page.close();
      }
    }, 60_000);
  });

  describe(`the file chooser in ${theme}`, () => {
    it("draws its button as the product's button, in the product's type", async () => {
      const page = await open({ name: "import", path: "/settings#configuration", ready: "#configuration input[type=file]", scope: "main", minSelects: 0 }, theme);
      try {
        const drawn = await page.$eval("#configuration input[type=file]", (el) => {
          const part = getComputedStyle(el, "::file-selector-button");
          const button = document.querySelector("#configuration .button") ?? document.querySelector(".button");
          const reference = getComputedStyle(button!);
          const root = getComputedStyle(document.documentElement);
          return {
            cls: el.className,
            height: el.getBoundingClientRect().height,
            font: part.fontFamily === getComputedStyle(document.body).fontFamily,
            size: part.fontSize === root.getPropertyValue("--type-body").trim(),
            radius: part.borderTopLeftRadius === reference.borderTopLeftRadius,
            border: part.borderTopColor === reference.borderTopColor,
            fill: part.backgroundColor === reference.backgroundColor,
            ink: part.color === reference.color,
            padding: part.paddingLeft === reference.paddingLeft,
          };
        });
        expect(drawn).toMatchObject({
          cls: "file-input", font: true, size: true, radius: true, border: true, fill: true, ink: true, padding: true,
        });
        // WCAG 2.2 SC 2.5.8: the control's own box clears the 24px floor.
        expect(drawn.height).toBeGreaterThanOrEqual(24);
      } finally {
        await page.close();
      }
    }, 60_000);
  });
}
