// @vitest-environment node
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium, type Browser, type Page } from "./harness/browser";

const dir = fileURLToPath(new URL("../../../docs/mockups/", import.meta.url));
function entrypoints(path: string): string[] {
  return readdirSync(path, { withFileTypes: true }).flatMap(entry =>
    entry.isDirectory() ? entrypoints(join(path, entry.name)).map(file => `${entry.name}/${file}`) :
      entry.name.endsWith(".html") ? [entry.name] : [],
  ).sort();
}
const pages = entrypoints(dir);
const selectCounts: Record<string, number> = {
  "components.html": 1, "dashboard-directions.html": 0, "index.html": 0,
  "pages/incidents.html": 1, "pages/index.html": 0, "pages/monitors.html": 4,
  "pages/notifications.html": 0, "pages/settings.html": 3,
};
const evidence = process.env.MOCKUP_REGRESSION_PROOF_DIR;
const proof: object[] = [];
let browser: Browser;

beforeAll(async () => { browser = await chromium(); });
afterAll(async () => {
  await browser?.close();
  if (evidence) {
    mkdirSync(evidence, { recursive: true });
    writeFileSync(join(evidence, "regressions.json"), JSON.stringify(proof, null, 2) + "\n");
  }
});

async function open(file: string, theme: string) {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 1000 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((value: string) => {
    localStorage.setItem("sg-theme", value);
    localStorage.setItem("subglance:mockup-theme", value);
    localStorage.setItem("sg-pulse", "off");
  }, theme);
  await page.goto(pathToFileURL(join(dir, file)).href, { waitUntil: "load" });
  await page.evaluate(async (value: string) => {
    document.documentElement.dataset.theme = value;
    await document.fonts.ready;
  }, theme);
  return page;
}

async function expectStatus(page: Page, selector: string, state: string, word: string) {
  const actual = await page.$eval(selector, row => ({
    state: row.querySelector<HTMLElement>(".led")!.dataset.state,
    word: row.querySelector("[data-statusword]")!.textContent?.trim(),
    hiddenLamp: row.querySelector(".led")!.getAttribute("aria-hidden"),
  }));
  expect(actual, `${selector}: lamp and accessible status word`).toEqual({ state, word, hiddenLamp: "true" });
  // Inspect Chromium's accessibility tree, not just text that might be hidden.
  const label = await page.$(`${selector} [data-statusword]`);
  expect(label).not.toBeNull();
  const ax = await page.accessibility.snapshot({ root: label!, interestingOnly: false });
  expect(JSON.stringify(ax), `${selector}: exposed status text`).toContain(`"name":"${word}"`);
  proof.push({ theme: await page.$eval("html", el => el.getAttribute("data-theme")), selector, ...actual, ax });
}

for (const theme of ["dark", "light"]) {
  describe(`mockup interaction regressions in ${theme}`, () => {
    it.each(pages)("%s retains every dropdown indicator and paired type at rest and focus", async file => {
      expect(pages, "all eight disk entrypoints are accounted for").toEqual(Object.keys(selectCounts).sort());
      const page = await open(file, theme);
      try {
        const selects = await page.$$("select");
        expect(selects.length, `${file}: all selects, including drawer fields`).toBe(selectCounts[file]);
        const faults: object[] = [];
        for (const select of selects) {
          if (await select.evaluate(el => !!el.closest('.drawer:not([data-open="true"])'))) {
            await page.click('.card-actions button[onclick="openDrawer()"]');
            await page.waitForFunction(() => {
              const drawer = document.querySelector(".drawer")!;
              return drawer.getAttribute("data-open") === "true" && drawer.getBoundingClientRect().right <= innerWidth + 0.5;
            });
          }
          for (const state of ["rest", "focus"]) {
            await select.evaluate((el, active) => active ? el.focus() : el.blur(), state === "focus");
            await page.evaluate(async () => {
              await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
              await Promise.all(document.getAnimations().filter(a => a instanceof CSSTransition).map(a => a.finished.catch(() => undefined)));
            });
            const actual = await select.evaluate(el => {
              const s = getComputedStyle(el);
              const root = getComputedStyle(document.documentElement);
              return {
                label: el.getAttribute("aria-label"), visible: el.checkVisibility(),
                focused: document.activeElement === el,
                appearance: s.appearance, image: s.backgroundImage,
                size: s.fontSize, leading: s.lineHeight,
                expectedSize: root.getPropertyValue("--type-body").trim(),
                expectedLeading: root.getPropertyValue("--lead-body").trim(),
              };
            });
            proof.push({ file, theme, state, ...actual });
            if (!actual.visible || actual.focused !== (state === "focus") ||
                (actual.appearance === "none" && actual.image === "none") ||
                actual.size !== actual.expectedSize || actual.leading !== actual.expectedLeading) {
              faults.push({ state, ...actual });
            }
          }
        }
        expect(faults, "every rendered select needs a native or painted arrow and paired type in both states").toEqual([]);
      } finally { await page.close(); }
    });

    it("reveals every row action reached by keyboard without hover", async () => {
      const page = await open("components.html", theme);
      try {
        await page.mouse.move(0, 0);
        const count = await page.$$eval(".demo-row .row-actions button", els => els.length);
        expect(count).toBe(6);
        const visited = new Set<string>();
        for (let tab = 0; tab < 100 && visited.size < count; tab++) {
          await page.keyboard.press("Tab");
          const action = await page.evaluate(async () => {
            const button = document.activeElement;
            if (!button?.matches(".demo-row .row-actions button")) return null;
            const actions = button.closest(".row-actions")!;
            await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
            await Promise.all(actions.getAnimations().map(a => a.finished));
            return {
              index: Array.from(document.querySelectorAll(".demo-row .row-actions button")).indexOf(button),
              title: button.getAttribute("title"),
              hovered: button.closest(".demo-row")!.matches(":hover"),
              focused: button.matches(":focus-visible"),
              opacity: getComputedStyle(actions).opacity,
            };
          });
          if (!action) continue;
          expect(action.hovered, "keyboard path must not be rescued by hover").toBe(false);
          expect(action.focused).toBe(true);
          expect(action.opacity, `focused row action ${action.index}: ${action.title}`).toBe("1");
          visited.add(String(action.index));
        }
        expect(visited.size, "Tab reaches all six actions in both rows").toBe(count);
      } finally { await page.close(); }
    });

    it.each(["Enter", "Escape", "Tab"] as const)("inline edit preserves the identical name role before/during/after %s", async exit => {
      const page = await open("components.html", theme);
      try {
        const editors = await page.$$("[data-editable]");
        expect(editors.length).toBe(2);
        for (const editor of editors) {
          const measure = () => editor.$eval("span, input", el => {
            const s = getComputedStyle(el);
            return { size: s.fontSize, leading: s.lineHeight, family: s.fontFamily };
          });
          const original = await editor.$eval("span", el => el.textContent);
          const before = await measure();
          const role = await page.evaluate(() => {
            const s = getComputedStyle(document.documentElement);
            return { size: s.getPropertyValue("--type-row").trim(), leading: s.getPropertyValue("--lead-row").trim() };
          });
          expect(before).toMatchObject(role);
          await editor.click();
          const input = await editor.$("input");
          expect(input).not.toBeNull();
          const during = await measure();
          await page.keyboard.type("Renamed monitor");
          await page.keyboard.press(exit);
          expect(await editor.$("input")).toBeNull();
          expect(await editor.$eval("span", el => el.textContent)).toBe(exit === "Escape" ? original : "Renamed monitor");
          const after = await measure();
          proof.push({ file: "components.html", theme, exit, original, before, during, after });
          expect(during, "editing must keep the original row role, not another valid role").toEqual(before);
          expect(after, "saved or cancelled name must keep its original row role").toEqual(before);
        }
      } finally { await page.close(); }
    });

    it("pauses a down monitor with an exposed word and restores Down on resume", async () => {
      const page = await open("pages/monitors.html", theme);
      try {
        const row = '#list .row:nth-child(2)';
        const toggle = `${row} button[onclick="togglePause(this)"]`;
        await expectStatus(page, row, "down", "Down");
        await page.click(toggle);
        await expectStatus(page, row, "off", "Paused");
        expect(await page.$eval(toggle, el => el.textContent)).toBe("Resume");
        await page.click(toggle);
        await expectStatus(page, row, "down", "Down");
        expect(await page.$eval(toggle, el => el.textContent)).toBe("Pause");
      } finally { await page.close(); }
    });

    it("resumes a monitor without a prior check as Waiting, not Paused", async () => {
      const page = await open("pages/monitors.html", theme);
      try {
        const row = '#list .row[data-type="ping"]';
        await expectStatus(page, row, "off", "Paused");
        await page.click(`${row} button[onclick="togglePause(this)"]`);
        await expectStatus(page, row, "idle", "Waiting");
      } finally { await page.close(); }
    });

    it("bulk pause exposes Paused for every selected row and resume restores each prior status", async () => {
      const page = await open("pages/monitors.html", theme);
      try {
        const initial = [["up", "Up"], ["down", "Down"], ["up", "Up"], ["warn", "Warning"], ["up", "Up"], ["up", "Up"], ["off", "Paused"]];
        expect(await page.$$eval("#list .row", els => els.length)).toBe(initial.length);
        for (const [i, [state, word]] of initial.entries()) {
          await expectStatus(page, `#list .row:nth-child(${i + 1})`, state, word);
        }
        await page.click("#selall");
        await page.click('#bulkbar button[onclick="bulk(\'pause\')"]');
        for (const i of initial.keys()) {
          await expectStatus(page, `#list .row:nth-child(${i + 1})`, "off", "Paused");
        }
        await page.click("#selall");
        await page.click('#bulkbar button[onclick="bulk(\'resume\')"]');
        for (const [i, [state, word]] of initial.entries()) {
          await expectStatus(page, `#list .row:nth-child(${i + 1})`, state === "off" ? "idle" : state, state === "off" ? "Waiting" : word);
        }
      } finally { await page.close(); }
    });
  });
}
