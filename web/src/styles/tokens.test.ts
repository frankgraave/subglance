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
    if (
      /\.(tsx?|css)$/.test(entry) &&
      entry !== "tokens.css" &&
      !entry.includes(".test.")
    ) {
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
    const rows = [
      ...body.matchAll(
        /\|\s*`(--[a-z]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|/gi,
      ),
    ];
    expect(rows.length, "expected four status rows").toBe(4);
    for (const [, name, dark, light] of rows) {
      expect(darkTokens.get(name), `dark ${name}`).toBe(dark.toLowerCase());
      expect(lightTokens.get(name), `light ${name}`).toBe(light.toLowerCase());
    }
  });

  it("uses the glow, radius and easing values from §2.4 and §2.6", () => {
    for (const match of designMd.matchAll(
      /(--glow-(?:up|warn|down)):\s*([^;]+);/g,
    )) {
      expect(darkTokens.get(match[1]), match[1]).toBe(match[2].trim());
    }
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    for (const match of designMd.matchAll(/(--r-(?:sm|md|lg)):\s*([^;]+);/g)) {
      expect(root.get(match[1]), match[1]).toBe(match[2].trim());
    }
    const ease = designMd.match(/--ease:\s*([^;]+);/);
    expect(root.get("--ease")).toBe(ease?.[1].trim());
  });

  it("uses the type scale from the §2.5 table", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const start = designMd.indexOf("### 2.5 Typography");
    expect(start, "missing §2.5").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start + 10));
    const rows = [
      ...body.matchAll(/\|\s*`(--(?:type|lh)-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|/g),
    ];
    expect(rows.length, "expected six type roles and three line heights").toBe(
      9,
    );
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
  });

  it("keeps the documented iOS zoom workaround at 16px", () => {
    // §13: a focused input below 16px zooms iOS Safari in and never back out.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    expect(root.get("--type-nozoom")).toBe("16px");
    expect(designMd).toContain("--type-nozoom: 16px");
  });

  it("keeps every type role at 12px or larger", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    for (const [name, value] of root) {
      if (!name.startsWith("--type-")) continue;
      expect(
        Number.parseFloat(value),
        `${name} is below the 12px floor`,
      ).toBeGreaterThanOrEqual(12);
    }
  });

  it("defines every dark token in light too, so no theme falls back silently", () => {
    for (const name of darkTokens.keys()) {
      expect(
        lightTokens.has(name),
        `${name} missing from the light theme`,
      ).toBe(true);
    }
    expect([...lightTokens.keys()].sort()).toEqual(
      [...darkTokens.keys()].sort(),
    );
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
      for (const match of contents.matchAll(
        /font-size:\s*[\d.]+(?:px|rem|em)|text-\[[\d.]+(?:px|rem|em)\]/g,
      )) {
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
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    // Anchored at the prose that introduces the two tables so the Weight
    // column of the type-role table above cannot be mistaken for a row here.
    const start = designMd.indexOf("**Weight is a scale of three");
    expect(start, "missing the §2.5 weight prose").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start));
    const rows = [
      ...body.matchAll(
        /\|\s*`(--(?:weight|track)-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|/g,
      ),
    ];
    expect(rows.length, "expected three weights and three tracking roles").toBe(
      6,
    );
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
  });

  it("binds every weight and tracking token to a Tailwind utility", () => {
    // Without a binding the token is reachable from CSS but not from a
    // className, and the next component quietly reaches for `font-medium`.
    const inline = tokensCss.slice(tokensCss.lastIndexOf("@theme inline {"));
    for (const step of ["plain", "mid", "strong"]) {
      expect(inline, `--font-weight-${step}`).toContain(
        `--font-weight-${step}: var(--weight-${step});`,
      );
    }
    for (const role of ["body", "badge", "caps"]) {
      expect(inline, `--tracking-${role}`).toContain(
        `--tracking-${role}: var(--track-${role});`,
      );
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
    expect(body.slice(0, body.indexOf("\n  }"))).toContain(
      "letter-spacing: var(--track-body);",
    );
  });

  it("keeps only the exceptions the body value is wrong for", () => {
    // Two faces the inherited value does not suit: the mono badge, which is
    // already wide, and uppercase, which needs the opposite sign. Any third
    // token is a per-component tweak wearing a token's name.
    const root = declarations(
      tokensCss.slice(0, tokensCss.indexOf("[data-theme=")),
    );
    const tracks = [...root.keys()].filter((name) =>
      name.startsWith("--track-"),
    );
    expect(tracks.sort()).toEqual([
      "--track-badge",
      "--track-body",
      "--track-caps",
    ]);
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

/**
 * SUB-75 extends the same rule to spacing and radius, which were the last two
 * scales left unguarded — and had already drifted: a 7px and a 9px padding,
 * each picked by hand to reach a rendered height that no token stated.
 *
 * The rule is not "no literal px". Below the 4px floor of the spacing ladder
 * there is nothing a token could say: a 1px optical nudge or a 2.5px lamp
 * radius is a hairline, not a spacing decision. At 4px and above the ladder
 * can express the value, so a literal there is drift and has to be either a
 * ladder value or an allow-listed exception with a reason.
 */
const SPACING_LADDER_FLOOR = 4;

/**
 * True when a value carries a literal length the ladder could have expressed.
 * Any rem/em counts, because the ladder is the only place relative spacing is
 * allowed to be defined; px counts from the floor upwards.
 */
function driftsFromLadder(value: string): boolean {
  return [...value.matchAll(/(-?[\d.]+)(px|rem|em)\b/g)].some(
    ([, length, unit]) =>
      unit === "px"
        ? Math.abs(Number.parseFloat(length)) >= SPACING_LADDER_FLOOR
        : true,
  );
}

/** Every padding/margin/gap/radius declaration in a stylesheet. */
function spacingDeclarations(css: string): string[] {
  const found: string[] = [];
  for (const match of css.matchAll(
    /(?:^|[\s;{])((?:row-|column-)?gap|(?:padding|margin)(?:-[a-z]+){0,2}|border(?:-(?:top|bottom|left|right|start|end|block|inline)){0,2}-radius)\s*:\s*([^;{}]+)/g,
  )) {
    found.push(`${match[1]}: ${match[2].trim()}`);
  }
  return found;
}

/** Arbitrary Tailwind spacing/radius utilities that are not token-only. */
function arbitrarySpacingUtilities(contents: string): string[] {
  const found: string[] = [];
  for (const match of contents.matchAll(
    /\b(?:p|px|py|pt|pb|pl|pr|m|mx|my|mt|mb|ml|mr|gap|gap-x|gap-y|rounded(?:-[a-z]+)?)-\[([^\]]+)\]/g,
  )) {
    // A token-only expression is the point of the ladder. Mixing a token with a
    // literal length is still drift, so the token alone does not excuse it.
    if (match[1].includes("var(--") && !driftsFromLadder(match[1])) continue;
    found.push(match[0]);
  }
  return found;
}

/**
 * Documented exceptions, keyed by `<path>: <declaration>`. An entry here is a
 * deliberate decision with its reasoning attached, which is the difference
 * between an exception and a leak. Adding one should feel like a small cost.
 */
const spacingExceptions = new Map<string, string>([
  [
    "web/src/live/connection.css: border-radius: 999px",
    "Pill: a radius larger than half the height, not a step on the radius ladder.",
  ],
  [
    "web/src/monitors/monitors.css: gap: 5px",
    "Matches the pitch of the 20x7 LED row it sits under (DESIGN.md §2.4); a ladder step would break the rhythm the lamps set.",
  ],
  [
    "web/src/shell/shell.css: padding: 1px 6px",
    "SOON badge: sized to the cap height of --type-section so it hugs the label rather than the line box.",
  ],
  [
    "web/src/wall/wall.css: padding: var(--space-16) clamp(var(--space-6), 5vw, 72px) 96px",
    "Wall gutter is fluid and intentionally above the ladder ceiling; tracked in SUB-77.",
  ],
  [
    "web/src/wall/wall.css: padding: 17px 18px",
    "Wall card padding, measured rather than derived; tracked in SUB-77.",
  ],
]);

describe("tokens.css is the only source of spacing and radius", () => {
  it("finds no literal spacing or radius at or above the ladder floor under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const path = relative(repoRoot, file);
      for (const declaration of spacingDeclarations(
        readFileSync(file, "utf8"),
      )) {
        if (!driftsFromLadder(declaration)) continue;
        const key = `${path}: ${declaration}`;
        if (spacingExceptions.has(key)) continue;
        offenders.push(key);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("finds no arbitrary Tailwind spacing or radius utility under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      for (const utility of arbitrarySpacingUtilities(
        readFileSync(file, "utf8"),
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${utility}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("keeps every documented exception real, so the allow-list cannot rot", () => {
    // An allow-list entry whose declaration no longer exists is worse than no
    // entry: it reads as a justified decision about live code and is not one.
    const present = new Set<string>();
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const path = relative(repoRoot, file);
      for (const declaration of spacingDeclarations(
        readFileSync(file, "utf8"),
      )) {
        present.add(`${path}: ${declaration}`);
      }
    }
    expect(
      [...spacingExceptions.keys()].filter((key) => !present.has(key)),
    ).toEqual([]);
  });

  it("catches corner and logical spacing properties, not just the short forms", () => {
    const css = `
      .a { border-top-left-radius: 6px; }
      .b { padding-block-start: 5px; }
      .c { margin-inline-end: 5px; }
      .d { border-start-end-radius: 6px; }
    `;
    expect(spacingDeclarations(css).filter(driftsFromLadder)).toEqual([
      "border-top-left-radius: 6px",
      "padding-block-start: 5px",
      "margin-inline-end: 5px",
      "border-start-end-radius: 6px",
    ]);
  });

  it("rejects an arbitrary utility that mixes a token with a literal length", () => {
    expect(
      arbitrarySpacingUtilities("p-[var(--space-2)] gap-[var(--space-1)]"),
    ).toEqual([]);
    expect(arbitrarySpacingUtilities("p-[calc(var(--space-1)+5px)]")).toEqual([
      "p-[calc(var(--space-1)+5px)]",
    ]);
    expect(arbitrarySpacingUtilities("m-[7px]")).toEqual(["m-[7px]"]);
  });

  it("states the ladder floor and the exception rule in docs/DESIGN.md", () => {
    expect(designMd).toContain("### 2.7 Spacing and radius");
    expect(designMd).toContain("4px floor");
  });
});
