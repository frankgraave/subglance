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
 *
 * SUB-69 extends them again to weight and tracking. Those drift the most
 * quietly of all: five uppercase labels at .02em, .07em, .08em, .09em and .1em
 * looked deliberate and were not, and nothing about the page said so.
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

/**
 * Every file that is subject to the token rules: everything under web/src
 * except tokens.css itself, which is their sanctioned home, and the test
 * files policing them, which necessarily quote the banned patterns.
 */
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

describe("tokens.css matches the §2.5 weight and tracking scales", () => {
  it("uses the weight and tracking values from the §2.5 tables", () => {
    const root = declarations(tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")));
    // Anchored at the prose that introduces the two tables so the Weight
    // column of the type-role table above cannot be mistaken for a row here.
    const start = designMd.indexOf("**Weight is a scale of three");
    expect(start, "missing the §2.5 weight prose").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start));
    const rows = [...body.matchAll(/\|\s*`(--(?:weight|track)-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|/g)];
    expect(rows.length, "expected three weights and three tracking roles").toBe(6);
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
  });

  it("binds every weight and tracking token to a Tailwind utility", () => {
    // Without a binding the token is reachable from CSS but not from a
    // className, and the next component quietly reaches for `font-medium`.
    const inline = tokensCss.slice(tokensCss.lastIndexOf("@theme inline {"));
    for (const step of ["plain", "mid", "strong"]) {
      expect(inline, `--font-weight-${step}`).toContain(`--font-weight-${step}: var(--weight-${step});`);
    }
    for (const role of ["body", "badge", "caps"]) {
      expect(inline, `--tracking-${role}`).toContain(`--tracking-${role}: var(--track-${role});`);
    }
  });
});

describe("tracking is decided once, on the body", () => {
  const indexCss = readFileSync(join(webSrc, "index.css"), "utf8");

  it("sets the body tracking in the base layer", () => {
    // The point of SUB-76: a component that forgets to ask for tracking still
    // gets it. If this declaration goes, 67 of 79 text elements silently fall
    // back to `normal` again and nothing on screen says so.
    const body = indexCss.slice(indexCss.indexOf("  body {"));
    expect(body.slice(0, body.indexOf("\n  }"))).toContain("letter-spacing: var(--track-body);");
  });

  it("keeps only the exceptions the body value is wrong for", () => {
    // Two faces the inherited value does not suit: the mono badge, which is
    // already wide, and uppercase, which needs the opposite sign. Any third
    // token is a per-component tweak wearing a token's name.
    const root = declarations(tokensCss.slice(0, tokensCss.indexOf("[data-theme=")));
    const tracks = [...root.keys()].filter((name) => name.startsWith("--track-"));
    expect(tracks.sort()).toEqual(["--track-badge", "--track-body", "--track-caps"]);
  });
});

describe("tokens.css is the only source of weight and tracking", () => {
  it("finds no literal font weight anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      // CSS `font-weight: 500`, Tailwind's stock `font-medium` presets, and
      // arbitrary `font-[600]`. `font-sans`/`font-mono` pick a family, not a
      // weight, and stay allowed.
      for (const match of contents.matchAll(
        /font-weight:(?!\s*var\(--weight-)\s*[^;]+|\bfont-(?:thin|extralight|light|normal|medium|semibold|bold|extrabold|black)\b|\bfont-\[\d/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("finds no literal letter spacing anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      const contents = readFileSync(file, "utf8");
      for (const match of contents.matchAll(
        /letter-spacing:(?!\s*var\(--track-)\s*[^;]+|\btracking-(?!body\b|badge\b|caps\b)[\w[\].-]+/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
