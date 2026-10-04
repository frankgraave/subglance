import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";
const root = fileURLToPath(new URL("../../../", import.meta.url));
const read = (path: string) => readFileSync(join(root, path), "utf8");

it("documents 4px as the small annotation and inline-focus rung, not a deprecated token", () => {
  const tokens = read("web/src/styles/tokens.css");
  const design = read("docs/DESIGN.md");
  expect(tokens).toContain("4px for small annotations and inline focus");
  expect(tokens).not.toContain("kept for compatibility");
  expect(design).toContain("**The ladder in use is 2 / 4 / 6 / 8 / 12.**");
  expect(design).not.toContain("legacy step, not in the ladder");
  expect(design).toContain("Panel and PanelList share `--surface-panel`");
});

it("requires an explicit role review for every new 4px caller", () => {
  const callers: string[] = [];
  function walk(path: string) {
    for (const entry of readdirSync(join(root, path), { withFileTypes: true })) {
      const file = `${path}/${entry.name}`;
      if (entry.isDirectory()) walk(file);
      else if (file.endsWith(".css") && !file.endsWith("tokens.css")) {
        const css = read(file).replace(/\/\*[\s\S]*?\*\//g, "");
        for (const match of css.matchAll(/([^{}]+)\{([^{}]+)\}/g)) {
          if (match[2].includes("var(--r-xs)")) callers.push(`${file}: ${match[1].trim()}`);
        }
      }
    }
  }
  walk("web/src");
  expect(callers.sort()).toEqual([
    // The menu's own keycaps: the masthead keycap's role, inside the menu it opens.
    "web/src/commands/commands.css: .command-menu kbd",
    "web/src/components/choice.css: .choice::before, .choice::after",
    // The phrase to retype before a delete: drawn as the masthead keycap is,
    // a small boxed annotation inside a label (DESIGN.md §8.12).
    "web/src/components/controls.css: .phrase",
    "web/src/components/panellist.css: .panel-row :is(a, button):focus-visible",
    // The masthead's breadcrumb link: a word-sized link, so its focus ring is
    // the inline-focus corner rather than a control's.
    "web/src/shell/shell.css: .shell-crumb",
    "web/src/shell/shell.css: .shell-nav-soon",
    "web/src/shell/shell.css: .shell-search-kbd",
  ]);
});
