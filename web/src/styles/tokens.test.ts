import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Guards the two halves of SUB-20's acceptance criterion:
 *
 *   1. tokens.css agrees with docs/DESIGN.md §2 — the design document is the
 *      source of truth, and a drifting stylesheet makes it a lie.
 *   2. tokens.css is the *only* place a colour is written down.
 *
 * Both are enforced mechanically because both fail silently otherwise: a
 * hardcoded `#34d399` looks correct in dark mode and only breaks in light.
 *
 * SUB-67 extends the same two rules to the type scale (§2.5). A hardcoded
 * `font-size: 11px` fails even more quietly than a colour does: it looks fine
 * on the machine it was written on and simply makes the product unreadable
 * one component at a time.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const webSrc = join(here, "..");
const repoRoot = join(webSrc, "..", "..");

const tokensCss = readFileSync(join(here, "tokens.css"), "utf8");
const designMd = readFileSync(join(repoRoot, "docs", "DESIGN.md"), "utf8");

/** Extracts `--name: value;` declarations from a block of CSS. */
function declarations(css: string): Map<string, string> {
  const found = new Map<string, string>();
  for (const match of css.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)) {
    found.set(match[1], match[2].trim());
  }
  return found;
}

/** Isolates one `[data-theme="…"]` rule from tokens.css. */
function themeBlock(theme: "dark" | "light"): string {
  const start = tokensCss.indexOf(`[data-theme="${theme}"]`);
  expect(start, `missing [data-theme="${theme}"] block`).toBeGreaterThan(-1);
  const end = tokensCss.indexOf("\n}", start);
  return tokensCss.slice(start, end);
}

const darkTokens = declarations(themeBlock("dark"));
const lightTokens = declarations(themeBlock("light"));

describe("tokens.css matches docs/DESIGN.md", () => {
  // §2.1 and §2.2 are fenced CSS blocks; §2.3 is a markdown table. Reading the
  // document rather than restating its values here is the whole point: a test
  // with the numbers copied into it drifts alongside the code it guards.
  function designHexes(section: string): Map<string, string> {
    const start = designMd.indexOf(section);
    expect(start, `missing section ${section}`).toBeGreaterThan(-1);
    const next = designMd.indexOf("\n### ", start + section.length);
    const body = designMd.slice(start, next === -1 ? undefined : next);
    const found = new Map<string, string>();
    for (const match of body.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-f]{3,8})/gi)) {
      found.set(match[1], match[2].toLowerCase());
    }
    return found;
  }

  it("uses the dark palette from §2.1", () => {
    const expected = designHexes("### 2.1 Colour — dark");
    expect(expected.size).toBeGreaterThan(5);
    for (const [name, hex] of expected) {
      expect(darkTokens.get(name), `dark ${name}`).toBe(hex);
    }
  });

  it("uses the light palette from §2.2", () => {
    const expected = designHexes("### 2.2 Colour — light");
    expect(expected.size).toBeGreaterThan(5);
    for (const [name, hex] of expected) {
      expect(lightTokens.get(name), `light ${name}`).toBe(hex);
    }
  });

  it("uses the status colours from the §2.3 table", () => {
    const start = designMd.indexOf("### 2.3 Status");
    const body = designMd.slice(start, designMd.indexOf("\n### ", start + 10));
    const rows = [...body.matchAll(/\|\s*`(--[a-z]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|/gi)];
    expect(rows.length, "expected four status rows").toBe(4);
    for (const [, name, dark, light] of rows) {
      expect(darkTokens.get(name), `dark ${name}`).toBe(dark.toLowerCase());
      expect(lightTokens.get(name), `light ${name}`).toBe(light.toLowerCase());
    }
  });

  it("uses the glow, radius and easing values from §2.4 and §2.6", () => {
    for (const match of designMd.matchAll(/(--glow-(?:up|warn|down)):\s*([^;]+);/g)) {
      expect(darkTokens.get(match[1]), match[1]).toBe(match[2].trim());
    }
    const root = declarations(tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")));
    for (const match of designMd.matchAll(/(--r-(?:sm|md|lg)):\s*([^;]+);/g)) {
      expect(root.get(match[1]), match[1]).toBe(match[2].trim());
    }
    const ease = designMd.match(/--ease:\s*([^;]+);/);
    expect(root.get("--ease")).toBe(ease?.[1].trim());
  });

  it("uses the type scale from the §2.5 table", () => {
    const root = declarations(tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")));
    const start = designMd.indexOf("### 2.5 Typography");
    expect(start, "missing §2.5").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start + 10));
    const rows = [...body.matchAll(/\|\s*`(--(?:type|lh)-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|/g)];
    expect(rows.length, "expected six type roles and three line heights").toBe(9);
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
  });

  it("keeps the documented iOS zoom workaround at 16px", () => {
    // §13: a focused input below 16px zooms iOS Safari in and never back out.
    const root = declarations(tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")));
    expect(root.get("--type-nozoom")).toBe("16px");
    expect(designMd).toContain("--type-nozoom: 16px");
  });

  it("keeps every type role at 12px or larger", () => {
    const root = declarations(tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")));
    for (const [name, value] of root) {
      if (!name.startsWith("--type-")) continue;
      expect(Number.parseFloat(value), `${name} is below the 12px floor`).toBeGreaterThanOrEqual(12);
    }
  });

  it("defines every dark token in light too, so no theme falls back silently", () => {
    for (const name of darkTokens.keys()) {
      expect(lightTokens.has(name), `${name} missing from the light theme`).toBe(true);
    }
    expect([...lightTokens.keys()].sort()).toEqual([...darkTokens.keys()].sort());
  });
});

describe("tokens.css is the only source of colour", () => {
  function sourceFiles(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        out.push(...sourceFiles(full));
        continue;
      }
      // tokens.css is the sanctioned home for colour, and the test files
      // that police it necessarily quote hex patterns in their regexes.
      if (/\.(tsx?|css)$/.test(entry) && entry !== "tokens.css" && !entry.includes(".test.")) {
        out.push(full);
      }
    }
    return out;
  }

  it("finds no hex literal anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      for (const match of contents.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
        // A hex in a URL fragment or an id selector is not a colour.
        if (/^#[0-9a-fA-F]{3,8}$/.test(match[0])) {
          offenders.push(`${relative(repoRoot, file)}: ${match[0]}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("finds no rgb()/hsl() literal anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      if (/\b(?:rgba?|hsla?)\(/.test(contents)) {
        offenders.push(relative(repoRoot, file));
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe("tokens.css is the only source of type size", () => {
  function sourceFiles(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        out.push(...sourceFiles(full));
        continue;
      }
      if (/\.(tsx?|css)$/.test(entry) && entry !== "tokens.css" && !entry.includes(".test.")) {
        out.push(full);
      }
    }
    return out;
  }

  it("finds no literal font size anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      // Both spellings: CSS `font-size: 13px` and Tailwind's `text-[13px]`.
      for (const match of contents.matchAll(/font-size:\s*[\d.]+(?:px|rem|em)|text-\[[\d.]+(?:px|rem|em)\]/g)) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0]}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe("tokens.css is the only source of line height", () => {
  function sourceFiles(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        out.push(...sourceFiles(full));
        continue;
      }
      if (/\.(tsx?|css)$/.test(entry) && entry !== "tokens.css" && !entry.includes(".test.")) {
        out.push(full);
      }
    }
    return out;
  }

  it("finds no stock Tailwind leading-* preset or literal line-height under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      // `leading-relaxed` and friends silently override the --lh-* token that
      // the text-* role utility carries, so a paragraph ends up at Tailwind's
      // 1.625 instead of --lh-prose. Only `leading-[var(--lh-*)]` is allowed.
      for (const match of contents.matchAll(
        /leading-(?!\[var\(--lh-)[\w[\].]+|line-height:\s*[\d.]+(?!\s*\/)/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0]}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
