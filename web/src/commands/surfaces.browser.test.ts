import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterAll, beforeAll, expect, it } from "vitest";
import { mkdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Card, Panel } from "../components/Card";
import { PanelList, PanelRow } from "../components/PanelList";
import { chromium, type Browser, type Page } from "../layout/harness/browser";
import { serveBuild, type Server } from "../layout/harness/server";

let browser: Browser, server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

async function measure(page: Page, selectors: string[]) {
  return page.evaluate((selectors) => {
    // Ask Chromium to resolve oklch as sRGB, then composite every ancestor.
    const ctx = document.createElement("canvas").getContext("2d")!;
    function rgba(value: string) {
      ctx.clearRect(0, 0, 1, 1); ctx.fillStyle = value; ctx.fillRect(0, 0, 1, 1);
      return [...ctx.getImageData(0, 0, 1, 1).data];
    }
    return Object.fromEntries(selectors.map((selector) => {
      const el = document.querySelector(selector)!;
      const css = getComputedStyle(el);
      const chain: Element[] = [];
      for (let node: Element | null = el; node; node = node.parentElement) chain.unshift(node);
      let color = [255, 255, 255];
      for (const node of chain) {
        const [r, g, b, a] = rgba(getComputedStyle(node).backgroundColor);
        color = [r, g, b].map((v, i) => v * a / 255 + color[i] * (1 - a / 255));
      }
      return [selector, { background: css.backgroundColor, composite: color.map(Math.round), radius: css.borderRadius, padding: css.padding, shadow: css.boxShadow }];
    }));
  }, selectors);
}

it.each(["dark", "light"])("measures real Panel and PanelList on a Card alongside the nesting reference (%s)", async (theme) => {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 1000 });
  try {
    await page.goto(server.url, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".shell-topbar");
    const markup = renderToStaticMarkup(h("main", { style: { padding: "var(--space-8)", display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--space-8)" } },
      h(Card, { title: "Panel on a Card", children: h(Panel, { children: "Panel content" }) }),
      h(Card, { title: "PanelList on a Card", children: h(PanelList, { label: "Panels", children: h(PanelRow, { children: h("a", { href: "#" }, "Panel row link") }) }) }),
      h("span", { className: "shell-nav-soon" }, "Soon"),
      h("span", { className: "shell-search-kbd" }, "⌘K"),
      h("div", { className: "segmented" }, h("button", { className: "segmented-option" }, "Segment")),
    ));
    await page.evaluate((markup, theme) => { document.body.innerHTML = markup; document.documentElement.dataset.theme = theme; }, markup, theme);
    await page.addStyleTag({ content: "*, *::before, *::after { transition: none !important; animation: none !important; }" });
    await page.evaluate(() => document.fonts.ready);
    await page.focus(".panel-row a");
    const actual = await measure(page, [".card", ".panel", ".panel-row", ".panel-row a", ".shell-nav-soon", ".shell-search-kbd", ".segmented", ".segmented-option"]);
    expect(actual[".panel"].radius).toBe("8px");
    const out = process.env.SUBGLANCE_EVIDENCE_DIR;
    if (out) { await mkdir(out, { recursive: true }); await page.screenshot({ path: `${out}/surfaces-${theme}.png` }); }
    await page.goto(fileURLToPath(new URL("../../../docs/mockups/components.html", import.meta.url)).replace(/^/, "file://"));
    await page.evaluate((theme) => { document.documentElement.dataset.theme = theme; }, theme);
    await page.addStyleTag({ content: "*, *::before, *::after { transition: none !important; animation: none !important; }" });
    await page.evaluate(() => document.fonts.ready);
    const reference = await measure(page, [".frame", ".frame-panel"]);
    if (out) {
      await (await page.$(".frame"))!.screenshot({ path: `${out}/nesting-reference-${theme}.png` });
      await writeFile(`${out}/surfaces-${theme}.json`, JSON.stringify({ actual, reference }, null, 2));
    }
    for (const selector of [".panel", ".panel-row"]) {
      expect(actual[selector].background).toBe(reference[".frame-panel"].background);
      expect(actual[selector].composite).toEqual(theme === "dark" ? [35, 35, 35] : [250, 250, 249]);
      expect(actual[selector].shadow).toBe(reference[".frame-panel"].shadow);
      expect(actual[selector].radius).toBe("8px");
    }
    for (const selector of [".panel-row a", ".shell-nav-soon", ".shell-search-kbd"]) expect(actual[selector].radius).toBe("4px");
    expect(parseFloat(actual[".segmented"].radius) - parseFloat(actual[".segmented"].padding)).toBe(parseFloat(actual[".segmented-option"].radius));
  } finally { await page.close(); }
});
