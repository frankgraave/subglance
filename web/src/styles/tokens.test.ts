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
 *
 * SUB-74 pairs each size with a whole-pixel leading and guards the pairing
 * itself. A size declared alone is the defect: it inherits whatever leading is
 * above it, which is how one token rendered at three leadings on one screen,
 * and no amount of reading the stylesheet made that visible.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const webSrc = join(here, "..");
const repoRoot = join(webSrc, "..", "..");

const tokensCss = readFileSync(join(here, "tokens.css"), "utf8");
const designMd = readFileSync(join(repoRoot, "docs", "DESIGN.md"), "utf8");

/**
 * A stylesheet with its comments blanked to spaces, so a guard that scans for
 * a declaration cannot be tripped by prose that names one. Line structure is
 * preserved so offsets still line up with the file.
 */
function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, " "));
}

/**
 * Extracts `--name: value;` declarations from a block of CSS.
 *
 * Comments first: this file documents its tokens in prose beside them, and a
 * comment that writes `--token: value` reads to a plain scan as a declaration.
 * Worse, it wins — it sits above the real one and the last match is kept — so
 * a token could be asserted against a sentence about it rather than against
 * its value.
 */
function declarations(css: string): Map<string, string> {
  const found = new Map<string, string>();
  for (const match of stripComments(css).matchAll(
    /(--[a-z0-9-]+)\s*:\s*([^;]+);/g,
  )) {
    found.set(match[1], match[2].trim());
  }
  return found;
}

describe("the token parser reads declarations, not prose about them", () => {
  it("ignores a token named inside a comment", () => {
    const parsed = declarations(`
      :root {
        /* --r-lg: 14px was the old value. */
        --r-lg: 12px;
      }
    `);
    expect(parsed.get("--r-lg")).toBe("12px");
  });
});

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

/**
 * The §2.5 role table, as the document states it: one row per role carrying
 * both the size token and its paired leading token. Reading the table rather
 * than restating its numbers here is the point — a test with the values copied
 * into it drifts alongside the code it guards.
 */
function typeRoleRows(): {
  size: string;
  sizeValue: string;
  lead: string;
  leadValue: string;
}[] {
  const start = designMd.indexOf("### 2.5 Typography");
  expect(start, "missing §2.5").toBeGreaterThan(-1);
  const body = designMd.slice(start, designMd.indexOf("\n### ", start + 10));
  // The leading cell holds a token and its value; tolerate a pipe, a space or
  // nothing between them so reformatting the table reads as formatting rather
  // than as token drift. Row count is asserted by the caller, so a regex that
  // stopped matching fails loudly instead of passing vacuously.
  return [
    ...body.matchAll(
      /\|\s*`(--type-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|\s*`(--lead-[a-z]+)`\s*\|?\s*`([^`]+)`\s*\|/g,
    ),
  ].map(([, size, sizeValue, lead, leadValue]) => ({
    size,
    sizeValue,
    lead,
    leadValue,
  }));
}

/**
 * Every `{ … }` declaration block in a stylesheet, as `[selector, body]`.
 * Comments are blanked to spaces first: a commented-out rule still contains
 * braces, and scanning them yields a phantom block whose "selector" is a
 * fragment of prose. None of these stylesheets use CSS nesting, so a flat brace
 * scan is exact; a nested rule would need a real parser.
 */
function declarationBlocks(css: string): { selector: string; body: string }[] {
  const bare = css.replace(/\/\*[\s\S]*?\*\//g, (c) =>
    c.replace(/[^\n]/g, " "),
  );
  const blocks: { selector: string; body: string }[] = [];
  for (const match of bare.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    blocks.push({
      selector: match[1].split(/\s+/).join(" ").trim(),
      body: match[2],
    });
  }
  return blocks;
}

describe("tokens.css matches docs/DESIGN.md", () => {
  // §2.1 and §2.2 are fenced CSS blocks; §2.3 is a markdown table. Reading the
  // document rather than restating its values here is the whole point: a test
  // with the numbers copied into it drifts alongside the code it guards.
  //
  // Colours are no longer all hex: dark surfaces are white at low alpha and
  // the neutral scale is written in oklch, because both say something the hex
  // could not — an alpha surface inherits what is under it, and `0` chroma
  // states that a grey is deliberately neutral rather than incidentally so.
  // The parser therefore takes any value up to the semicolon.
  function designColours(section: string): Map<string, string> {
    const start = designMd.indexOf(section);
    expect(start, `missing section ${section}`).toBeGreaterThan(-1);
    const next = designMd.indexOf("\n### ", start + section.length);
    const body = designMd.slice(start, next === -1 ? undefined : next);
    const found = new Map<string, string>();
    for (const match of body.matchAll(
      /(--[a-z0-9-]+):\s*(#[0-9a-f]{3,8}|oklch\([^)]*\)|rgba?\([^)]*\))/gi,
    )) {
      found.set(match[1], match[2].toLowerCase().replace(/\s+/g, " "));
    }
    return found;
  }

  /** Normalises a declaration so `rgba(255,255,255,.03)` matches the doc. */
  function sameColour(a: string | undefined, b: string): boolean {
    if (a === undefined) return false;
    const norm = (s: string) => s.toLowerCase().replace(/\s+/g, "");
    return norm(a) === norm(b);
  }

  it("uses the dark palette from §2.1", () => {
    const expected = designColours("### 2.1 Colour — dark");
    expect(expected.size).toBeGreaterThan(5);
    for (const [name, value] of expected) {
      expect(
        sameColour(darkTokens.get(name), value),
        `dark ${name}: tokens.css has ${darkTokens.get(name)}, §2.1 says ${value}`,
      ).toBe(true);
    }
  });

  it("uses the light palette from §2.2", () => {
    const expected = designColours("### 2.2 Colour — light");
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
    const radii = [
      ...designMd.matchAll(/(--r-(?:2xs|xs|sm|md|lg)):\s*(\S+);/g),
    ];
    expect(radii.length, "expected five radius steps in §2.6").toBe(5);
    for (const match of radii) {
      expect(root.get(match[1]), match[1]).toBe(match[2].trim());
    }
    const ease = designMd.match(/--ease:\s*([^;]+);/);
    expect(root.get("--ease")).toBe(ease?.[1].trim());
  });

  it("uses the type scale and its paired leadings from the §2.5 tables", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const rows = typeRoleRows();
    expect(rows.length, "expected six type roles").toBe(6);
    for (const { size, sizeValue, lead, leadValue } of rows) {
      expect(root.get(size), size).toBe(sizeValue);
      expect(root.get(lead), lead).toBe(leadValue);
    }
    // The opt-in leading has its own single-column table.
    const prose = designMd.match(/\|\s*`(--lead-prose)`\s*\|\s*`([^`]+)`\s*\|/);
    expect(prose, "missing the --lead-prose row").not.toBeNull();
    expect(root.get(prose![1])).toBe(prose![2]);
  });

  it("keeps every size and leading a whole number of pixels", () => {
    // The defect SUB-74 closes: `12.5px` times a ratio produced leadings like
    // 18.125px, and which way the engine rounded that depended on the font and
    // the device pixel ratio. A stated integer has no such question.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    for (const [name, value] of root) {
      if (!/^--(?:type|lead)-/.test(name)) continue;
      expect(value, `${name} must be a whole number of px`).toMatch(/^\d+px$/);
    }
  });

  it("keeps every leading on the 4px baseline grid", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    for (const [name, value] of root) {
      if (!name.startsWith("--lead-")) continue;
      // Anchored rather than parseInt: `parseInt("0.5rem")` is 0, which divides
      // by 4 and would pass. The whole-pixel test above blocks that today, but
      // a guard that depends on another guard's coverage is one edit from
      // being silently useless.
      const px = value.match(/^(\d+)px$/);
      expect(
        px,
        `${name} is ${value}, which is not a whole-pixel length`,
      ).not.toBeNull();
      expect(
        Number(px![1]) % 4,
        `${name} is ${value}, which is off the 4px grid`,
      ).toBe(0);
    }
  });

  it("gives every type role a leading partner, and every leading a role", () => {
    // A size with no partner is a size that will inherit one, which is the
    // half of the defect that rotted quietly rather than visibly.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const suffix = (prefix: string) =>
      [...root.keys()]
        .filter((name) => name.startsWith(prefix))
        .map((name) => name.slice(prefix.length))
        .sort();
    // `--lead-prose` is the documented opt-in and has no `--type-prose`.
    expect(suffix("--lead-").filter((s) => s !== "prose")).toEqual(
      suffix("--type-"),
    );
  });

  it("keeps the documented iOS zoom workaround paired, at 16px", () => {
    // §13: a focused input below 16px zooms iOS Safari in and never back out.
    // It sits outside the role table but inside the pairing rule, so the
    // document has to state both halves or the test cannot check them.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    expect(root.get("--type-nozoom")).toBe("16px");
    expect(designMd).toContain("`--type-nozoom: 16px`");
    const lead = designMd.match(/`--lead-nozoom:\s*(\d+px)`/);
    expect(lead, "DESIGN.md must state the nozoom leading").not.toBeNull();
    expect(root.get("--lead-nozoom")).toBe(lead![1]);
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

  it("keeps the zero tone between a real reading and an absent one", () => {
    // The ordering is the whole point of the token, and it is the kind of
    // thing a later "simplification" collapses: someone notices --ink-zero
    // sits close to --ink-3 and reuses the existing step. That would make a
    // measured zero and a missing reading render identically, which is a
    // statement the screen has to be able to make differently.
    //
    // Compared on oklch lightness rather than on a computed contrast ratio:
    // the scale is achromatic, so lightness *is* the ordering, and it reads
    // the same way in both themes without a colour-space conversion that
    // would itself need testing.
    // Reads either notation: the dark scale is oklch (chroma 0 states that the
    // grey is deliberately neutral), the light scale is still hex. Within one
    // theme the three tones share a notation, so the comparison stays
    // like-for-like; a tone that parses as neither fails loudly rather than
    // skipping, because a guard that quietly excuses itself is worse than none.
    const tone = (value: string, label: string): number => {
      const ok = /oklch\(\s*(\.\d+|\d*\.?\d+%?)/.exec(value);
      if (ok) {
        const raw = ok[1];
        return raw.endsWith("%")
          ? Number.parseFloat(raw) / 100
          : Number.parseFloat(raw);
      }
      const hex = /^#([0-9a-f]{6})$/i.exec(value.trim());
      if (hex) {
        const channel = (c: number) =>
          c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
        const [r, g, b] = [0, 2, 4].map((i) =>
          channel(Number.parseInt(hex[1].slice(i, i + 2), 16) / 255),
        );
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      }
      throw new Error(`cannot read a lightness from ${label}: ${value}`);
    };

    for (const [theme, tokens] of [
      ["dark", darkTokens],
      ["light", lightTokens],
    ] as const) {
      const get = (name: string) => {
        const v = tokens.get(name);
        expect(v, `${theme} is missing ${name}`).toBeTruthy();
        return tone(v as string, `${theme} ${name}`);
      };
      const zero = get("--ink-zero");
      const second = get("--ink-2");
      const third = get("--ink-3");

      // Dark text gets lighter as it recedes; light text gets darker. Compare
      // in the direction that theme recedes, not on raw lightness.
      const quieterThan =
        theme === "dark"
          ? (a: number, b: number) => a < b
          : (a: number, b: number) => a > b;

      expect(
        quieterThan(zero, second),
        `${theme}: --ink-zero should be quieter than --ink-2`,
      ).toBe(true);
      expect(
        quieterThan(third, zero),
        `${theme}: --ink-zero should stay louder than --ink-3`,
      ).toBe(true);
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

  it("finds no inline fontSize style object under web/src", () => {
    // A `style={{ fontSize: … }}` sets a size in a place no stylesheet rule
    // covers, so the pairing guard below — which reads .css files — cannot see
    // it, and the size arrives with whatever leading it inherits. Size belongs
    // in a class, where its partner can sit beside it.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (file.endsWith(".css")) continue;
      const contents = readFileSync(file, "utf8");
      for (const match of contents.matchAll(/\bfontSize\s*:/g)) {
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
      // `leading-relaxed` and friends silently override the leading that the
      // text-* role utility carries, so a paragraph ends up at Tailwind's 1.625
      // instead of its paired value. Only the bound `leading-prose` utility and
      // an explicit `leading-[var(--lead-*)]` are allowed.
      for (const match of contents.matchAll(
        /leading-(?!prose\b|\[var\(--lead-)[\w[\].]+|line-height:\s*[\d.]+(?!\s*\/)/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0]}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("pairs every font-size declaration with a line-height in the same block", () => {
    // The half of SUB-74's defect that failed silently: 56 rules set a size and
    // inherited whatever leading sat above them — Tailwind preflight's 1.5 or
    // one of the old ratios — so `--type-helper` rendered at three different
    // leadings on one screen. A rule that states only the size is that bug.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const { selector, body } of declarationBlocks(
        readFileSync(file, "utf8"),
      )) {
        if (!/font-size:/.test(body)) continue;
        if (/line-height:/.test(body)) continue;
        offenders.push(`${relative(repoRoot, file)}: ${selector}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("pairs each size with its own leading, or with the documented prose opt-in", () => {
    // Pairing the wrong leading is as much a defect as pairing none: it is how
    // a 13px helper ends up on a 24px card leading and looks like a mistake
    // nobody can name. The only allowed mismatch is helper text that wraps,
    // which opts into --lead-prose.
    const partner = new Map(
      typeRoleRows().map(({ size, lead }) => [size, lead]),
    );
    // Named explicitly: with an empty map every paired rule becomes an
    // offender, which fails, but blames the stylesheet for a parse that broke
    // in DESIGN.md.
    expect(partner.size, "§2.5 table did not parse into six roles").toBe(6);
    // §13's zoom workaround is outside the role table but still paired.
    partner.set("--type-nozoom", "--lead-nozoom");
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const { selector, body } of declarationBlocks(
        readFileSync(file, "utf8"),
      )) {
        const size = body.match(/font-size:\s*var\((--type-[a-z]+)\)/);
        const lead = body.match(/line-height:\s*var\((--lead-[a-z]+)\)/);
        if (!size || !lead) continue;
        const want = partner.get(size[1]);
        const prose = lead[1] === "--lead-prose" && size[1] === "--type-helper";
        if (lead[1] === want || prose) continue;
        offenders.push(
          `${relative(repoRoot, file)}: ${selector} pairs ${size[1]} with ${lead[1]}, want ${want}`,
        );
      }
    }
    expect(offenders).toEqual([]);
  });

  it("catches a size-only rule, a mispaired rule and a nested rule in a fixture", () => {
    // Guards the guard: a brace scan that silently matched nothing, or that
    // read a commented-out rule as live, would let the defects back in while
    // the suite stayed green.
    const css = `
      /* .commented { font-size: var(--type-body); } */
      .size-only { font-size: var(--type-helper); color: red; }
      .paired { font-size: var(--type-helper); line-height: var(--lead-helper); }
      @media (max-width: 640px) {
        .nested-size-only { font-size: var(--type-body); }
      }
      .mispaired { font-size: var(--type-card); line-height: var(--lead-section); }
    `;
    const blocks = declarationBlocks(css);
    const named = (selector: string) =>
      blocks.find((b) => b.selector.endsWith(selector));

    expect(named(".commented"), "a commented-out rule is not a rule").toBe(
      undefined,
    );
    // The size-only check, including the rule nested inside the media query.
    expect(
      blocks
        .filter(
          (b) => /font-size:/.test(b.body) && !/line-height:/.test(b.body),
        )
        .map((b) => b.selector.replace(/^.*\{\s*/, "")),
    ).toEqual([".size-only", ".nested-size-only"]);

    // The partner check: same shape as the real guard, run over the fixture.
    const partner = new Map([
      ["--type-card", "--lead-card"],
      ["--type-helper", "--lead-helper"],
      ["--type-body", "--lead-body"],
    ]);
    const mismatched = blocks.filter((b) => {
      const size = b.body.match(/font-size:\s*var\((--type-[a-z]+)\)/);
      const lead = b.body.match(/line-height:\s*var\((--lead-[a-z]+)\)/);
      return size && lead && partner.get(size[1]) !== lead[1];
    });
    expect(mismatched.map((b) => b.selector)).toEqual([".mispaired"]);
    expect(named(".paired")).toBeDefined();
  });
});

describe("tokens.css matches the §2.5 weight and tracking scales", () => {
  it("uses the weight and tracking values from the §2.5 tables", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    // Anchored at the prose that introduces the two tables so the Weight
    // column of the type-role table above cannot be mistaken for a row here.
    const start = designMd.indexOf("**Weight is a scale of four");
    expect(start, "missing the §2.5 weight prose").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start));
    const rows = [
      ...body.matchAll(
        /\|\s*`(--(?:weight|track)-[a-z]+)`\s*\|\s*`([^`]+)`\s*\|/g,
      ),
    ];
    expect(rows.length, "expected four weights and one tracking role").toBe(5);
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
  });

  it("keeps tracking to a single inherited value, with no per-face exception", () => {
    // This is the rule that keeps re-breaking itself, because every exception
    // has a plausible argument behind it: uppercase wants opening up, mono is
    // already on a fixed advance, a badge is small. Measured, none of them
    // hold — and the caps one cost +1.08px per letter pair against a body of
    // -0.32px, which is what made section labels read as spaced-out small caps.
    //
    // So: no `--track-*` token other than the body value may exist, and no
    // stylesheet may set letter-spacing to anything else.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const trackTokens = [...root.keys()].filter((k) =>
      k.startsWith("--track-"),
    );
    expect(
      trackTokens,
      "a second tracking token is a per-face exception wearing a token's clothes",
    ).toEqual(["--track-body"]);

    // And nothing may hand-roll one. Every stylesheet the app ships is walked,
    // not just tokens.css, because the exceptions historically lived in
    // component sheets where nobody was looking.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const css = stripComments(readFileSync(file, "utf8"));
      for (const match of css.matchAll(/letter-spacing:\s*([^;}]+)/g)) {
        const value = match[1].trim();
        if (value === "var(--track-body)" || value === "inherit") continue;
        offenders.push(`${relative(repoRoot, file)}: letter-spacing: ${value}`);
      }
    }
    expect(
      offenders,
      "tracking is set once on body and inherited; see §2.5",
    ).toEqual([]);
  });

  it("binds every weight and tracking token to a Tailwind utility", () => {
    // Without a binding the token is reachable from CSS but not from a
    // className, and the next component quietly reaches for `font-medium`.
    const inline = tokensCss.slice(tokensCss.lastIndexOf("@theme inline {"));
    for (const step of ["plain", "mid", "strong", "heavy"]) {
      expect(inline, `--font-weight-${step}`).toContain(
        `--font-weight-${step}: var(--weight-${step});`,
      );
    }
    for (const role of ["body"]) {
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
    // back to `normal` and nothing on screen says so.
    //
    // Worth recording because it is easy to get wrong in the other direction:
    // an em letter-spacing does NOT re-resolve against each child's font-size.
    // It is computed once on body (16px x -.02em = -0.32px) and that absolute
    // value is what descendants inherit, so a 12px label carries -0.32px and
    // not -0.24px. Measured in a browser; jsdom cannot answer this.
    const body = indexCss.slice(indexCss.indexOf("  body {"));
    expect(body.slice(0, body.indexOf("\n  }"))).toContain(
      "letter-spacing: var(--track-body);",
    );
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
      //
      // So are the `font-weight` descriptors inside an `@font-face` rule: they
      // state which weights the file on disk actually contains, which is a
      // fact about the face rather than a design decision, and there is no
      // token that could express it. Stripping the rules is safer than
      // exempting the file, so a plain declaration added to fonts.css later is
      // still caught.
      const source = contents.replace(/@font-face\s*\{[^}]*\}/g, "");
      for (const match of source.matchAll(
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
]);

/**
 * SUB-137 extends the rule to the last two unguarded scales: size and
 * breakpoint.
 *
 * The audit that prompted it is the argument for it. Across 26 stylesheets
 * there were zero literal colours and zero literal font sizes -- both are
 * guarded -- and 88 literal sizes, which were not. The same people wrote
 * both. The difference is that one of them fails a test.
 *
 * The clearest case: `--control-icon: 28px` was added as a *new token*,
 * correctly following the "name your dimensions" rule, two pixels away from
 * three files that already drew that square at 26px. Naming a value does not
 * prevent drift if nothing compares it to the values that already exist.
 *
 * Floor and exceptions work as they do for spacing: below 2px there is
 * nothing a token could usefully say (a hairline, an sr-only clip), and an
 * exception must be listed with a reason.
 */
const SIZE_LADDER_FLOOR = 2;

/**
 * Every width/height/track declaration in a stylesheet, collapsed to one line.
 *
 * The collapse is the point. A declaration may span a dozen lines when its
 * value carries explanatory comments -- the card grid's `minmax()` does --
 * and a line-oriented scan reads only the first fragment, finds no literal,
 * and reports the file clean. The first version of this guard did exactly
 * that and passed while a 380px floor sat three lines below it.
 *
 * Every track-sizing property is matched, not only `grid-template-columns`:
 * a `48px` row in `grid-template-rows` or `grid-auto-rows` is the same kind
 * of unnamed dimension as a `48px` column, and the narrower matcher let it
 * through. Placement properties (`grid-column`, `grid-row`, `grid-area`,
 * `grid-auto-flow`) are deliberately excluded -- their integers are line
 * numbers and spans, not lengths the ladder could express. The alternation
 * is ordered longest-first because `grid` is a prefix of every other name,
 * and `grid` matching first would capture `grid-template-rows` as `grid`.
 */
function sizeDeclarations(css: string): string[] {
  const found: string[] = [];
  const flat = css.replace(/\s+/g, " ");
  for (const match of flat.matchAll(
    /(?:^|[\s;{])((?:min-|max-)?(?:width|height)|grid-template-columns|grid-template-rows|grid-auto-columns|grid-auto-rows|grid-template|grid|flex-basis)\s*:\s*([^;{}]+)/g,
  )) {
    found.push(`${match[1]}: ${match[2].trim().replace(/\s+/g, " ")}`);
  }
  return found;
}

/** True when a declaration carries a literal length the ladder could express. */
function driftsFromSizeLadder(value: string): boolean {
  return [...value.matchAll(/(-?[\d.]+)(px)\b/g)].some(
    ([, length]) => Math.abs(Number.parseFloat(length)) >= SIZE_LADDER_FLOOR,
  );
}

const sizeExceptions = new Map<string, string>([
  [
    "web/src/monitors/led.css: width: 20px",
    "The lamp is measured against DESIGN.md §3 by ledSizes.test.ts, which requires the literal. A token here would satisfy this guard, break that one, and invite the next mark to borrow a size that is a claim about legibility rather than a rung.",
  ],
  [
    "web/src/monitors/led.css: height: 7px",
    "The other half of the lamp; same reason.",
  ],
  [
    "web/src/shell/shell.css: width: min(var(--size-pane-drawer), 86vw)",
    "Token plus a viewport cap: the cap is a relationship to the screen, not a size the ladder could state.",
  ],
]);

/**
 * Breakpoints cannot be tokens, so they are guarded as literals.
 *
 * Verified in Chromium rather than assumed: `@media (max-width: var(--bp))`
 * never matches -- media queries are evaluated before custom properties are
 * substituted, so the block is silently dropped. That silence is the reason
 * this guard exists: a breakpoint written as a token would not fail loudly,
 * it would simply stop applying, and the layout would quietly be wrong at
 * one width.
 *
 * So the ladder lives in tokens.css as documentation, and this list is what
 * actually holds the literals together. The `+1` partners are separate rungs
 * because `max-width: 640px` and `min-width: 641px` must not both match at
 * exactly 640px.
 */
const BREAKPOINTS = new Set(["640px", "641px", "900px"]);

/**
 * The top-level parenthesised conditions of a media query, balanced.
 *
 * Regex cannot count brackets, and a media condition may legitimately
 * contain them: `(width <= calc(640px + 1px))` nests one level, and a
 * `[^)]+` capture stops at the inner `)` and hands back the truncated
 * `calc(640px + 1px`, which is not a value anything can check. Scanning for
 * balance costs a few lines and is the only form that is actually correct.
 */
function mediaConditions(query: string): string[] {
  const found: string[] = [];
  let depth = 0;
  let start = -1;
  for (let i = 0; i < query.length; i += 1) {
    const ch = query[i];
    if (ch === "(") {
      if (depth === 0) start = i + 1;
      depth += 1;
    } else if (ch === ")") {
      depth -= 1;
      if (depth === 0 && start !== -1) {
        found.push(query.slice(start, i));
        start = -1;
      }
      // A stray `)` cannot take the scanner negative and desynchronise it.
      if (depth < 0) depth = 0;
    }
  }
  return found;
}

/**
 * Every width bound stated by a media query, whatever syntax states it.
 *
 * Two syntaxes express the same thing and both have to be read. The legacy
 * form is `(max-width: 640px)`; the range form is `(width <= 640px)` and its
 * chained variant `(400px <= width <= 700px)`, which states two bounds in one
 * condition. A matcher that knows only `width:` returns nothing at all for a
 * range query -- and nothing at all reads, to an `offenders` assertion, as
 * clean. That is how the px-only expression this replaces let `40rem`
 * through, so it is worth not repeating one layer up.
 *
 * Both forms are read out of balanced conditions rather than by regex, so a
 * value that nests brackets survives extraction whole and can be compared
 * against the ladder instead of being silently truncated past checking.
 */
function mediaWidths(css: string): string[] {
  const found: string[] = [];
  for (const query of css.matchAll(/@media[^{]+/g)) {
    for (const condition of mediaConditions(query[0])) {
      const legacy = condition.match(/^\s*(?:min-|max-)?width\s*:\s*(.+)$/);
      if (legacy) {
        found.push(legacy[1].trim());
        continue;
      }
      /*
       * The range form, read as the bounds on either side of `width`. Split
       * on the comparison operators so a chained condition yields both of
       * its bounds rather than only the first, and drop the `width` keyword
       * itself along with anything carrying no digit -- `(orientation:
       * portrait)` and a bare `(width)` presence check state no length.
       *
       * `=` is one of the operators (MQ4 permits `(width = 700px)`), and the
       * split alternation puts the two-character forms first so `<=` is not
       * cut in half into a `<` bound and an empty one.
       */
      if (!/[<>=]/.test(condition)) continue;
      if (!/\bwidth\b/.test(condition)) continue;
      for (const part of condition.split(/<=|>=|[<>=]/)) {
        const bound = part.trim();
        if (bound === "" || bound === "width") continue;
        if (!/\d/.test(bound)) continue;
        found.push(bound);
      }
    }
  }
  return found;
}

describe("tokens.css is the only source of size", () => {
  it("finds no literal width or height at or above the floor under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const path = relative(repoRoot, file);
      for (const declaration of sizeDeclarations(
        stripComments(readFileSync(file, "utf8")),
      )) {
        if (!driftsFromSizeLadder(declaration)) continue;
        const key = `${path}: ${declaration}`;
        if (sizeExceptions.has(key)) continue;
        offenders.push(key);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("keeps every documented size exception real, so the allow-list cannot rot", () => {
    const present = new Set<string>();
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const path = relative(repoRoot, file);
      for (const declaration of sizeDeclarations(
        stripComments(readFileSync(file, "utf8")),
      )) {
        present.add(`${path}: ${declaration}`);
      }
    }
    expect(
      [...sizeExceptions.keys()].filter((key) => !present.has(key)),
    ).toEqual([]);
  });

  it("uses only ladder breakpoints in media queries", () => {
    /*
     * The complete value of each width condition, not the px numbers in it.
     *
     * Extracting `(\d+px)` and checking those meant a query with no px at
     * all -- `@media (max-width: 40rem)` -- produced no match and therefore
     * no offender, so the one form of drift this guard exists to stop was
     * the one form it could not see. Every `*-width` condition is now read
     * whole and required to be a rung, whatever unit it is written in.
     */
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const css = stripComments(readFileSync(file, "utf8"));
      for (const value of mediaWidths(css)) {
        if (BREAKPOINTS.has(value)) continue;
        offenders.push(`${relative(repoRoot, file)}: @media ... ${value}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("rejects a media width that is off the ladder, in any unit or syntax", () => {
    /*
     * The guard's own fixture. `40rem` is 640px at the default root size, so
     * it is the most tempting way to write a rung that is not one -- and the
     * earlier px-only expression passed it silently.
     *
     * The range forms are here for the same reason one layer up: a matcher
     * that reads only `width:` returns nothing for `(width <= 700px)`, and
     * nothing reads as clean to an `offenders` assertion.
     */
    const off = (css: string) =>
      mediaWidths(css).filter((w) => !BREAKPOINTS.has(w));

    expect(off("@media (max-width: 40rem) {}")).toEqual(["40rem"]);
    expect(off("@media (width <= 700px) {}")).toEqual(["700px"]);
    expect(off("@media (400px <= width <= 700px) {}")).toEqual([
      "400px",
      "700px",
    ]);
    expect(off("@media (width > 40rem) {}")).toEqual(["40rem"]);
    expect(off("@media (width = 700px) {}")).toEqual(["700px"]);

    // A value that nests brackets survives extraction whole, in both forms.
    expect(off("@media (width <= calc(640px + 1px)) {}")).toEqual([
      "calc(640px + 1px)",
    ]);
    expect(off("@media (max-width: calc(640px + 1px)) {}")).toEqual([
      "calc(640px + 1px)",
    ]);

    // The rungs, in both syntaxes, must keep passing.
    expect(off("@media (max-width: 640px) {}")).toEqual([]);
    expect(off("@media (min-width: 641px) and (max-width: 900px) {}")).toEqual(
      [],
    );
    expect(off("@media (width <= 640px) {}")).toEqual([]);
    expect(off("@media (641px <= width <= 900px) {}")).toEqual([]);
    expect(off("@media (width = 640px) {}")).toEqual([]);

    // A query stating no width contributes no bound to check.
    expect(mediaWidths("@media (prefers-reduced-motion: reduce) {}")).toEqual(
      [],
    );
    expect(mediaWidths("@media (orientation: portrait) {}")).toEqual([]);
    expect(mediaWidths("@media (min-resolution: 2dppx) {}")).toEqual([]);
  });

  it("catches a literal track in every grid sizing property, not just columns", () => {
    /*
     * The guard's own fixture. `grid-template-columns` was the only property
     * matched, so a 48px row was as invisible to this suite as a 48px column
     * was visible -- and placement integers must stay out of it, or every
     * `grid-column: 1 / 3` in the product becomes an offender.
     */
    const css = `
      .a { grid-template-rows: 48px 1fr; }
      .b { grid-auto-rows: 64px; }
      .c { grid-auto-columns: 32px; }
      .d { grid-template-columns: repeat(2, 120px); }
      .e { grid-column: 1 / 3; }
      .f { grid-row: 2 / span 4; }
      .g { grid-auto-flow: column dense; }
    `;
    expect(sizeDeclarations(css).filter(driftsFromSizeLadder)).toEqual([
      "grid-template-rows: 48px 1fr",
      "grid-auto-rows: 64px",
      "grid-auto-columns: 32px",
      "grid-template-columns: repeat(2, 120px)",
    ]);
  });

  it("never writes a breakpoint as a custom property, which silently never matches", () => {
    // Not a style preference: the query is dropped entirely, so the guarded
    // layout simply stops existing at that width with nothing to show for it.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const css = stripComments(readFileSync(file, "utf8"));
      for (const match of css.matchAll(/@media[^{]+/g)) {
        if (!match[0].includes("var(--")) continue;
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("states every breakpoint in the ladder as a token, so the guide can show them", () => {
    const declared = new Set(
      [...readFileSync(join(webSrc, "styles/tokens.css"), "utf8").matchAll(
        /--bp-[a-z-]+:\s*([^;]+);/g,
      )].map(([, value]) => value.trim()),
    );
    for (const breakpoint of BREAKPOINTS) {
      expect(declared, `${breakpoint} is enforced but not documented`).toContain(
        breakpoint,
      );
    }
  });
});

/**
 * SUB-139: the column ladder is a *proportion*, and a proportion nothing
 * checks is a story told about numbers that were picked one at a time.
 *
 * The rungs `--size-col-1..5` are 8 x the Fibonacci sequence (5, 8, 13, 21,
 * 34), which approximates phi to within 1.2% at its worst step while landing
 * on whole pixels — see tokens.css §2.14 for why the exact phi value is the
 * wrong choice here. The guards below are what make that claim falsifiable
 * rather than decorative:
 *
 *   1. every consecutive pair is within tolerance of phi;
 *   2. every rung is a whole number of pixels and a multiple of 8;
 *   3. every column width in the product's screens is a rung.
 *
 * (1) is the one that bites hardest. It is easy to "fix" a clipped column by
 * nudging one rung, and the nudge is invisible in review — the value still
 * looks like a considered number, the comment above it still reads true, and
 * the relationship it was part of is gone. A ratio check fails on exactly
 * that edit, and names the pair it broke.
 */
const PHI = (1 + Math.sqrt(5)) / 2;

/**
 * 2%, and it is a measurement rather than a round number.
 *
 * The worst consecutive pair on the ladder is 64/40 = 1.600, which sits 1.11%
 * under phi; the best is 272/168 = 1.61905, 0.06% over. 2% admits every rung
 * of the Fibonacci ladder with room for the next one up (440/272 = 1.61765)
 * and refuses the nearest plausible alternatives — a doubling ladder (2.0,
 * 23.6% off), a 1.5 ladder (7.3% off) and a single rung nudged by one 8px
 * step (104 -> 112 gives 1.75, 8.2% off).
 */
const PHI_TOLERANCE = 0.02;

/**
 * The Fibonacci column rungs, read from tokens.css rather than restated, in
 * RUNG ORDER — `--size-col-1` first, whatever pixel value it carries.
 *
 * Ordering by rung number rather than by value is the whole point, and it was
 * wrong here. Sorting by px made the ratio check a test of the *set* of
 * numbers rather than of the ladder: exchange the values of `--size-col-1`
 * and `--size-col-2` and the sort puts them back in ascending order, every
 * ratio assertion passes, and every consumer of rung 1 gets a 64px column
 * where it asked for 40. The sort restored exactly the property the swap
 * destroyed. A rung is a name bound to a value, so the name has to lead.
 */
function columnLadder(): { name: string; px: number }[] {
  const css = stripComments(readFileSync(join(webSrc, "styles/tokens.css"), "utf8"));
  const rungs: { name: string; px: number; rung: number }[] = [];
  for (const [, name, rung, px] of css.matchAll(
    /(--size-col-(\d+))\s*:\s*(\d+(?:\.\d+)?)px/g,
  )) {
    rungs.push({ name, px: Number(px), rung: Number(rung) });
  }
  return rungs
    .sort((a, b) => a.rung - b.rung)
    .map(({ name, px }) => ({ name, px }));
}

/**
 * Every width a screen gives a data column, as `path: declaration`.
 *
 * Only the product's own screens: the style guide's tables are chrome for
 * reading the ladder, not a use of it, and holding them to it would mean the
 * page documenting the system had to be built from the system it documents in
 * places where that says nothing.
 */
const COLUMN_RULE = /\.(?:mon-col--|mon-cell--|mon-head--|inc-col-|inv-col--|inc-tl-at)/;

/**
 * Terms in a column width that state a *relationship* to the container rather
 * than a size: `auto`, `0`, a percentage, an intrinsic keyword, and the
 * `minmax()`/`fr` pieces of an elastic track. The ladder has nothing to say
 * about any of them — only the fixed columns beside them take a rung.
 */
const RELATIONSHIP_TERM =
  /^(?:auto|0|100%|max-content|min-content|fit-content|minmax\(0,|\d+(?:\.\d+)?fr\)?|\d+(?:\.\d+)?%|,)$/;

/**
 * Everything in a data-column width declaration that is not a rung of the
 * ladder: a `var()` naming some other token, or a fixed literal.
 *
 * The whole declaration is judged, term by term. The earlier form walked only
 * `var(--size-col-…)` matches and reported those that were not rungs, so a
 * declaration naming no column token at all produced no match and passed —
 * `width: 92px` and `width: var(--size-pane-xs)` are exactly the drift §2.14
 * exists to stop, and both were invisible to a guard that could only see the
 * tokens it was already happy about.
 */
function notOnTheLadder(value: string, ladder: Set<string>): string[] {
  const bad: string[] = [];
  for (const term of value.split(/\s+/)) {
    if (!term || RELATIONSHIP_TERM.test(term)) continue;
    const token = /^var\((--[\w-]+)\)/.exec(term);
    if (token) {
      if (!ladder.has(token[1])) bad.push(token[1]);
      continue;
    }
    if (/\d/.test(term)) bad.push(term);
  }
  return bad;
}

describe("the column ladder is phi, and stays phi (§2.14)", () => {
  it("has at least four rungs, or there is no ladder to check", () => {
    expect(columnLadder().length).toBeGreaterThanOrEqual(4);
  });

  it("steps by phi from each rung to the next", () => {
    const rungs = columnLadder();
    const broken: string[] = [];
    for (let i = 1; i < rungs.length; i += 1) {
      const lower = rungs[i - 1];
      const upper = rungs[i];
      const ratio = upper.px / lower.px;
      const drift = Math.abs(ratio - PHI) / PHI;
      if (drift <= PHI_TOLERANCE) continue;
      broken.push(
        `${upper.name} (${upper.px}px) / ${lower.name} (${lower.px}px) = ` +
          `${ratio.toFixed(4)}, which is ${(drift * 100).toFixed(1)}% from phi ` +
          `(${PHI.toFixed(4)}); the ladder allows ${(PHI_TOLERANCE * 100).toFixed(0)}%. ` +
          `The nearest whole-pixel rung that keeps the proportion is ` +
          `${Math.round((lower.px * PHI) / 8) * 8}px.`,
      );
    }
    expect(broken).toEqual([]);
  });

  it("puts every rung on a whole pixel and on the 8px grid", () => {
    // Whole pixels because a fractional width renders a border differently per
    // device pixel ratio -- the defect the type scale and the lamp's corner
    // each rejected already. Multiples of 8 so a column is also a multiple of
    // the gaps beside it.
    const offenders = columnLadder().filter(
      ({ px }) => !Number.isInteger(px) || px % 8 !== 0,
    );
    expect(offenders).toEqual([]);
  });

  it("gives every data column in a screen a rung, never a loose token", () => {
    const ladder = new Set(columnLadder().map(({ name }) => name));
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const css = stripComments(readFileSync(file, "utf8")).replace(/\s+/g, " ");
      for (const match of css.matchAll(
        /([^{}]*)\{([^{}]*(?:width|grid-template-columns)\s*:[^{}]*)\}/g,
      )) {
        const [, selector, body] = match;
        if (!COLUMN_RULE.test(selector)) continue;
        for (const declaration of body.matchAll(
          /((?:min-|max-)?width|grid-template-columns)\s*:\s*([^;}]+)/g,
        )) {
          const value = declaration[2].trim();
          for (const term of notOnTheLadder(value, ladder)) {
            offenders.push(
              `${relative(repoRoot, file)}: ${selector.trim()} { ${declaration[1]}: ${value} } ` +
                `uses \`${term}\`, which is not a rung of the phi column ladder ` +
                `(${[...ladder].join(", ")}).`,
            );
          }
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("bites on a fixed literal and on a size token that is not a rung", () => {
    /*
     * The two forms the old matchAll version could not see, pinned directly.
     * Both are shaped exactly like the declarations it did catch and neither
     * names a `--size-col-*` token, which is why they walked straight through.
     */
    const ladder = new Set(columnLadder().map(({ name }) => name));
    expect(notOnTheLadder("92px", ladder)).toEqual(["92px"]);
    expect(notOnTheLadder("var(--size-pane-xs)", ladder)).toEqual([
      "--size-pane-xs",
    ]);
    expect(notOnTheLadder("5rem", ladder)).toEqual(["5rem"]);
    // And the shapes that must keep passing, or the guard is unusable.
    expect(notOnTheLadder("var(--size-col-3)", ladder)).toEqual([]);
    expect(notOnTheLadder("auto", ladder)).toEqual([]);
    expect(
      notOnTheLadder(
        "minmax(0, 1.1fr) minmax(0, 1.2fr) var(--size-col-2) var(--size-col-2)",
        ladder,
      ),
    ).toEqual([]);
  });

  it("states the ladder and its ratio in docs/DESIGN.md", () => {
    expect(designMd).toContain("### 2.14 The column ladder");
    // The number itself, so the document cannot claim a proportion it does not
    // name -- and the honest part, which is that the rungs are Fibonacci
    // rather than phi exactly.
    expect(designMd).toContain("1.618");
    expect(designMd).toContain("Fibonacci");
  });
});

/*
 * SUB-139: every `var(--token)` names a token that exists.
 *
 * This is the failure mode none of the guards above can see, and it is the
 * quietest one in the whole system. A custom property that was never declared
 * does not error, does not warn and does not fail a build: the declaration is
 * simply dropped at computed-value time and the element keeps whatever it
 * inherited. Nothing on screen says which rule went missing. Every other
 * guard here asks "is this value from the ladder"; none of them asks whether
 * the name on the right-hand side resolves to anything at all, so a token
 * renamed in tokens.css leaves its old name working perfectly in review and
 * silently doing nothing in the browser.
 *
 * It is not hypothetical. Writing this guard found two live ones on a file
 * nobody had flagged: `led.css` asked for `--stroke-fine`, which has never
 * been declared, so the lamp's lens highlight has been drawing square corners
 * inside a rounded lamp; and `.push-reveal-warn` asked for `--ink-1`, which
 * does not exist either — the scale is `--ink`, `--ink-2`, `--ink-3`,
 * `--ink-4` — so a caveat the reader is meant to notice took whatever colour
 * the panel behind it happened to have.
 *
 * The concrete edit it exists to stop is a rename across branches. Rename
 * `--size-col-sm-alt` to `--size-col-gutter` here while another branch writes
 * `var(--size-col-sm-alt)` in its own stylesheet, and both branches are green,
 * both merge cleanly, and the result is a layout that is quietly wrong with no
 * test, no error and no diff to point at.
 *
 * The style guide's generator is in scope for the same reason: it writes CSS
 * as string literals, so its `var()`s are not in any stylesheet a linter
 * reads, and the page documenting the system is the last place that may
 * reference a token the system does not have.
 */

/** Every custom property tokens.css declares. */
function declaredTokens(): Set<string> {
  const declared = new Set<string>();
  for (const [, name] of stripComments(tokensCss).matchAll(
    /(--[\w-]+)\s*:/g,
  )) {
    declared.add(name);
  }
  return declared;
}

/**
 * Custom properties the product declares for itself, which are legitimate
 * without being tokens: `shell.css` computes `--sidebar-w` from
 * `--size-sidebar` and flips it to `--rail-w` when collapsed, and
 * `MonitorCardList` sets `--mon-card-cols` as an inline style which
 * `monitors.css` then reads. They are local plumbing rather than design
 * decisions, so they do not belong to the ladder — but they must still be
 * declared *somewhere*, which is what this collects.
 *
 * Collected across the whole tree rather than per file, because the
 * `--mon-card-cols` pairing is a component declaring a property for its own
 * stylesheet one directory over. Scope is still enforced by the thing that
 * matters here: rename either half and the name disappears from the tree
 * entirely, which is exactly what the guard fails on.
 *
 * Takes file *contents* rather than paths so the fixture below can exercise
 * it directly. Reading the disk inside it would make the declaration half of
 * the guard testable only through the product's own files, which is how an
 * underscored declaration would have stayed unprotected.
 */
function locallyDeclared(sources: string[]): Set<string> {
  const declared = new Set<string>();
  for (const contents of sources) {
    for (const [, name] of stripComments(contents).matchAll(
      /(--[\w-]+)\s*:/g,
    )) {
      declared.add(name);
    }
    // `{ "--mon-card-cols": columns }` — a custom property set from TSX.
    for (const [, name] of contents.matchAll(/["'](--[\w-]+)["']\s*:/g)) {
      declared.add(name);
    }
  }
  return declared;
}

/**
 * Every `var(--token)` in a file that has NO fallback, with the token it
 * names.
 *
 * The fallback is the whole distinction. `var(--x, 1)` cannot fail silently:
 * an undeclared `--x` yields `1`, which is a stated decision about what
 * happens when the property is absent. `var(--x)` with nothing after it is
 * the dangerous form — the declaration is dropped and the element keeps what
 * it inherited, with nothing anywhere saying so.
 */
function tokenReferences(contents: string): string[] {
  return [
    ...stripComments(contents).matchAll(/var\(\s*(--[\w-]+)\s*([,)])/g),
  ]
    .filter(([, , next]) => next === ")")
    .map(([, name]) => name);
}

describe("every var() names a token that exists", () => {
  /*
   * The generator's own source, read as one more file. It emits the style
   * guide's CSS from template literals, so its `var()` calls are invisible to
   * every stylesheet-shaped check in this file.
   */
  const generator = join(repoRoot, "web", "scripts", "build-styleguide.mjs");

  /**
   * The guard itself, over a set of `[label, contents]` sources.
   *
   * Both halves run here — `locallyDeclared` over every source, then
   * `tokenReferences` over each — so a fixture exercises the same code path
   * the tree does rather than a re-implementation of half of it.
   */
  function unresolved(sources: [string, string][]): string[] {
    const declared = declaredTokens();
    const local = locallyDeclared(sources.map(([, contents]) => contents));
    const offenders: string[] = [];
    for (const [label, contents] of sources) {
      for (const name of tokenReferences(contents)) {
        if (declared.has(name) || local.has(name)) continue;
        offenders.push(
          `${label} references var(${name}), which is declared nowhere in ` +
            `tokens.css or under web/src. An undeclared custom property ` +
            `fails silently: the declaration is dropped and the element ` +
            `keeps what it inherited.`,
        );
      }
    }
    return offenders;
  }

  it("resolves every var() under web/src and in the style guide generator", () => {
    expect(
      unresolved(
        [...sourceFiles(webSrc), generator].map((file) => [
          relative(repoRoot, file),
          readFileSync(file, "utf8"),
        ]),
      ),
    ).toEqual([]);
  });

  it("bites on an undeclared token, and only on an undeclared one", () => {
    const judge = (contents: string) =>
      unresolved([["fixture.css", contents]]).map(
        (line) => /var\((--[\w-]+)\)/.exec(line)![1],
      );
    // The rename case: the old name still parses and resolves to nothing.
    // `--size-col-sm-alt` is this branch's own former name for
    // `--size-col-gutter`, which is the edit the guard exists to catch.
    expect(judge(".a { width: var(--size-col-sm-alt); }")).toEqual([
      "--size-col-sm-alt",
    ]);
    // Tokens that exist pass.
    expect(judge(".a { color: var(--ink); padding: var(--space-3); }")).toEqual(
      [],
    );
    // A fallback is a stated decision about absence, not a silent failure.
    expect(judge(".a { grid-template-columns: var(--cols, 1); }")).toEqual([]);
    // Prose naming a token it does not use is not a reference.
    expect(
      judge("/* var(--gone) was removed. */ .a { color: var(--ink); }"),
    ).toEqual([]);
  });

  it("sees an underscore on both sides of the comparison", () => {
    /*
     * A custom property name may contain an underscore, and a scanner that
     * excludes them is blind in exactly the shape of the bug this guard
     * exists to catch: the name parses, resolves to nothing, and is skipped
     * rather than reported.
     *
     * Both halves are asserted, because widening only the reference scan
     * turns every legitimately declared underscored property into a false
     * offender — which is the failure mode of fixing one site of four.
     */
    expect(
      unresolved([["fixture.css", ".a { color: var(--missing_token); }"]]),
    ).toEqual([
      "fixture.css references var(--missing_token), which is declared " +
        "nowhere in tokens.css or under web/src. An undeclared custom " +
        "property fails silently: the declaration is dropped and the element " +
        "keeps what it inherited.",
    ]);
    // Declared and referenced in one file.
    expect(
      unresolved([["fixture.css", ".a { --local_w: 10px; width: var(--local_w); }"]]),
    ).toEqual([]);
    // Declared in one source and referenced from another, which is the
    // `--mon-card-cols` shape: a component sets it, a stylesheet reads it.
    expect(
      unresolved([
        ["setter.tsx", 'const s = { "--local_w": 10 };'],
        ["reader.css", ".a { width: var(--local_w); }"],
      ]),
    ).toEqual([]);
  });
});

describe("tokens.css is the only source of spacing and radius", () => {
  it("finds no literal spacing or radius at or above the ladder floor under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      const path = relative(repoRoot, file);
      for (const declaration of spacingDeclarations(
        // Comments are stripped first. Prose explaining *why* a value is six
        // pixels is not a declaration, and reading it as one cuts both ways:
        // it invents offenders out of sentences, and it would just as happily
        // let a commented-out rule satisfy the allow-list check.
        stripComments(readFileSync(file, "utf8")),
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
        // Comments are stripped first. Prose explaining *why* a value is six
        // pixels is not a declaration, and reading it as one cuts both ways:
        // it invents offenders out of sentences, and it would just as happily
        // let a commented-out rule satisfy the allow-list check.
        stripComments(readFileSync(file, "utf8")),
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

/**
 * SUB-103 adds the three scales that were still unguarded: the accent and the
 * control/status split it depends on, the border roles, and the depth ladder.
 *
 * The accent guard is the one that matters most. An accent and a status colour
 * are the same kind of object to CSS and completely different objects to a
 * person reading a dashboard, so nothing mechanical stopped a button from
 * being painted `--up` to look lively, or a chart bar from being painted
 * `--accent` because it looked tidy. Either one, done once, collapses the
 * split back into a single colour that means two things.
 */

/** Properties that paint a control's own surface, as opposed to a data mark. */
const CONTROL_SURFACE =
  /(?:^|[\s;{])(background|background-color|border(?:-[a-z]+)?-color|border(?:-[a-z]+)?|fill|color)\s*:\s*([^;{}]+)/g;

/** Names that describe a control rather than a piece of data. */
const CONTROL_SELECTOR =
  /(button|btn|input|select|segment|switch|toggle|tab|submit|link|nav-item|checkbox|radio)/i;

/** Names that describe a data mark: a status, a lamp, a bar, a reading. */
const DATA_SELECTOR =
  /(led|heartbeat|hb-|bar|spark|chart|status|wall-card|tile)/i;

/**
 * The class, id and element names in one compound selector. Pseudo-classes,
 * pseudo-elements and attribute selectors are dropped first, because they are
 * state rather than identity and they collide with the role words: `:disabled`
 * contains `led`, and classifying on raw selector text made every disabled
 * control read as a lamp.
 */
function selectorNames(compound: string): string {
  return compound
    .replace(/\[[^\]]*\]/g, " ")
    .replace(/::?[a-z-]+(?:\([^)]*\))?/gi, " ")
    .replace(/[.#]/g, " ");
}

/**
 * Classifies each item of a selector list by its right-most compound, which is
 * the element the rule actually paints. `.wall-card .add-button` is a control
 * and `.add-button .wall-card` is a data mark; matching anywhere in the
 * selector made both of them neither, and a rule that matches both classifiers
 * was skipped by both guards.
 */
function subjectRoles(
  selector: string,
): { subject: string; control: boolean; data: boolean }[] {
  return selector
    .split(",")
    .map((item) => {
      const parts = item
        .trim()
        .split(/[\s>+~]+/)
        .filter(Boolean);
      const subject = parts[parts.length - 1] ?? "";
      const names = selectorNames(subject);
      return {
        subject,
        control: CONTROL_SELECTOR.test(names),
        data: DATA_SELECTOR.test(names),
      };
    })
    .filter((role) => role.subject !== "");
}

/** True when some subject of the rule is a data mark and not a control. */
function paintsData(selector: string): boolean {
  return subjectRoles(selector).some((role) => role.data && !role.control);
}

/** True when some subject of the rule is a control and not a data mark. */
function paintsControl(selector: string): boolean {
  return subjectRoles(selector).some((role) => role.control && !role.data);
}

describe("the accent fills controls and status colour marks data", () => {
  it("declares the accent once, outside both theme blocks", () => {
    // Identical in dark and light is the stated decision (§2.8). Declaring it
    // inside a theme block is how that decision gets quietly reversed.
    const root = declarations(
      tokensCss.slice(0, tokensCss.indexOf("[data-theme=")),
    );
    expect(root.get("--accent")).toBeDefined();
    expect(root.get("--accent-border")).toBeDefined();
    for (const theme of ["dark", "light"] as const) {
      const block = declarations(themeBlock(theme));
      expect(
        [...block.keys()].filter((name) => name.startsWith("--accent")),
        `${theme} redefines the accent`,
      ).toEqual([]);
    }
  });

  it("uses the accent values from §2.8", () => {
    const start = designMd.indexOf("### 2.8 The accent");
    expect(start, "missing §2.8").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start + 10));
    const root = declarations(
      tokensCss.slice(0, tokensCss.indexOf("[data-theme=")),
    );
    const rows = [...body.matchAll(/(--accent(?:-[a-z]+)?):\s*([^;]+);/g)];
    expect(rows.length, "expected four accent tokens in §2.8").toBe(4);
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value.trim());
    }
  });

  it("never paints a data mark with the control accent", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const block of declarationBlocks(readFileSync(file, "utf8"))) {
        if (!paintsData(block.selector)) continue;
        if (/var\(--accent/.test(block.body)) {
          offenders.push(`${relative(repoRoot, file)}: ${block.selector}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("never fills a control surface with a status colour", () => {
    // A status colour on a control's *text* is allowed — a destructive button
    // labels itself — so this looks at the surface properties only.
    //
    // Validation is the one place a control legitimately wears a status
    // colour: an invalid field is reporting a fact about its contents, which
    // is exactly what the status scale is for. It stays an edge, never a fill,
    // and aria-invalid carries the same fact a second time for anyone who
    // cannot see the difference.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const block of declarationBlocks(readFileSync(file, "utf8"))) {
        if (!paintsControl(block.selector)) continue;
        if (/\[aria-invalid/.test(block.selector)) {
          // Allowed as a border, still banned as a fill.
          const filled = [...block.body.matchAll(CONTROL_SURFACE)].filter(
            ([, property, value]) =>
              /^background/.test(property) &&
              /var\(--(?:up|down|warn|idle)\b/.test(value),
          );
          for (const [, property] of filled) {
            offenders.push(
              `${relative(repoRoot, file)}: ${block.selector} { ${property} }`,
            );
          }
          continue;
        }
        for (const [, property, value] of block.body.matchAll(
          CONTROL_SURFACE,
        )) {
          if (property === "color") continue;
          if (!/var\(--(?:up|down|warn|idle)\b/.test(value)) continue;
          offenders.push(
            `${relative(repoRoot, file)}: ${block.selector} { ${property} }`,
          );
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("catches both directions of the split in a fixture", () => {
    // Proves the two guards above bite, without waiting for a real violation.
    const bad = `
      .led[data-state="up"] { background: var(--accent); }
      .mon-button { background: var(--up); }
    `;
    const blocks = declarationBlocks(bad);
    expect(
      blocks
        .filter((b) => paintsData(b.selector) && /var\(--accent/.test(b.body))
        .map((b) => b.selector),
    ).toEqual(['.led[data-state="up"]']);
    expect(
      blocks
        .filter(
          (b) =>
            paintsControl(b.selector) &&
            [...b.body.matchAll(CONTROL_SURFACE)].some(
              ([, property, value]) =>
                property !== "color" && /var\(--up\b/.test(value),
            ),
        )
        .map((b) => b.selector),
    ).toEqual([".mon-button"]);
  });

  it("classifies a descendant rule by the element it paints", () => {
    // The hole this closes: a selector containing both a control word and a
    // data word used to match both classifiers and be skipped by both guards,
    // so `.wall-card .add-button { background: var(--up) }` passed.
    expect(paintsControl(".wall-card .add-button")).toBe(true);
    expect(paintsData(".wall-card .add-button")).toBe(false);
    expect(paintsData(".add-button .wall-card")).toBe(true);
    expect(paintsControl(".add-button .wall-card")).toBe(false);
    // State, not identity: `:disabled` ends in the letters of `led`.
    expect(paintsData(".mon-button:disabled")).toBe(false);
    expect(paintsControl(".mon-button:disabled")).toBe(true);
    // A selector list is classified item by item.
    expect(paintsControl(".led, .mon-button")).toBe(true);
    expect(paintsData(".led, .mon-button")).toBe(true);
  });
});

describe("borders come from the three roles in §2.9", () => {
  it("defines the control role in both themes", () => {
    for (const theme of ["dark", "light"] as const) {
      expect(
        declarations(themeBlock(theme)).get("--border-control"),
        `${theme} --border-control`,
      ).toMatch(/^#[0-9a-f]{6}$/i);
    }
  });

  it("resolves every border colour to a role token", () => {
    // A literal border colour is the same failure as a literal hex fill: it
    // looks right in dark and is wrong in light, and nothing says so. A
    // *token* colour is not automatically right either: `1px solid var(--up)`
    // paints an edge with the status scale and used to pass, because the check
    // skipped any value containing `var(--`.
    const offenders: string[] = [];
    for (const declaration of borderDeclarations()) {
      const value = declaration.value;
      if (BORDER_NEUTRAL.test(value)) continue;
      if (BORDER_ROLE.test(value)) continue;
      if (statusBorders.has(site(declaration))) continue;
      offenders.push(site(declaration));
    }
    expect(offenders).toEqual([]);
  });

  it("bites on a status colour used as an ordinary edge", () => {
    // The hole this closes. Neither value is in the allow-list, and neither
    // site is a documented status border.
    expect(BORDER_ROLE.test("1px solid var(--accent)")).toBe(false);
    expect(BORDER_ROLE.test("1px solid var(--up)")).toBe(false);
    expect(BORDER_ROLE.test("1px solid var(--border-control)")).toBe(true);
    expect(BORDER_ROLE.test("var(--border-hi)")).toBe(true);
  });

  it("keeps border width at 1px, bar the documented status stripe", () => {
    const offenders: string[] = [];
    for (const declaration of borderDeclarations()) {
      const width = /(\d+(?:\.\d+)?)px/.exec(declaration.value);
      if (!width || width[1] === "1") continue;
      if (statusBorders.has(site(declaration))) continue;
      offenders.push(site(declaration));
    }
    expect(offenders).toEqual([]);
  });

  it("ties each exception to one file and one selector", () => {
    // Every allow-listed site must still exist, or the list is quietly
    // granting permission to something that moved.
    const sites = new Set(borderDeclarations().map(site));
    expect([...statusBorders].filter((entry) => !sites.has(entry))).toEqual([]);
    // And the same declaration elsewhere is not covered.
    expect(
      statusBorders.has(
        "web/src/monitors/other.css | .other-row | border-left: 2px solid var(--down)",
      ),
    ).toBe(false);
  });

  it("states the border roles in docs/DESIGN.md", () => {
    expect(designMd).toContain("### 2.9 Border roles");
    expect(designMd).toContain("--border-control");
  });
});

/** Colours that encode "what a thing is", per §2.9, plus the accent edge. */
const BORDER_ROLE =
  /^(?:\d+(?:\.\d+)?px\s+(?:solid|dashed|dotted)\s+)?var\(--(?:border|border-hi|border-control|accent-border)\)$/;

/**
 * Border values that carry no colour at all: removed, inherited, or reserving
 * the space a border will occupy so nothing shifts by a pixel when the state
 * arrives. That last one is the pattern, not a missing token.
 */
const BORDER_NEUTRAL =
  /^(?:none|0|inherit|unset|(?:\d+(?:\.\d+)?px\s+(?:solid|dashed|dotted)\s+)?transparent|\d+(?:\.\d+)?px\s+(?:solid|dashed|dotted))$/;

type BorderDeclaration = {
  path: string;
  selector: string;
  property: string;
  value: string;
};

/** Every border declaration under web/src, with the rule it belongs to. */
function borderDeclarations(): BorderDeclaration[] {
  const found: BorderDeclaration[] = [];
  for (const file of sourceFiles(webSrc)) {
    if (!file.endsWith(".css")) continue;
    const path = relative(repoRoot, file);
    for (const block of declarationBlocks(readFileSync(file, "utf8"))) {
      for (const [, property, value] of block.body.matchAll(
        /(?:^|[\s;{])(border(?:-(?:top|bottom|left|right|block|inline))?(?:-color|-width)?)\s*:\s*([^;{}]+)/g,
      )) {
        found.push({
          path,
          selector: block.selector,
          property,
          value: value.replace(/\s+/g, " ").trim(),
        });
      }
    }
  }
  return found;
}

/** One allow-list key: the file, the selector and the declaration together. */
function site(declaration: BorderDeclaration): string {
  return `${declaration.path} | ${declaration.selector} | ${declaration.property}: ${declaration.value}`;
}

/**
 * The borders that intentionally carry a status colour or a second width.
 * §2.9 allows one width exception — the stripe down the left of a row, which
 * is a signal carried by position and thickness rather than an edge around a
 * box — and validation legitimately edges a control in `--down`.
 *
 * Keyed by file, selector and declaration together, so the same declaration
 * copied to a second rule still fails. That is the point: an exception is a
 * permission for one place, not for a string.
 */
const statusBorders = new Set<string>([
  /*
   * The detail page's status pill: trouble warms its edge, the same statement
   * the rows and cards make with their left stripe. The fill stays neutral on
   * purpose — the pill already says "Down" in words, and a red block behind
   * that word would state it twice in a louder voice.
   */
  'web/src/monitors/detail.css | .mon-detail-status[data-status="down"] | border-color: var(--down)',
  'web/src/monitors/detail.css | .mon-detail-status[data-status="pending"] | border-color: var(--warn)',
  'web/src/monitors/detail.css | .mon-detail-status[data-status="waiting"] | border-color: var(--idle)',
  "web/src/live/connection.css | .conn-badge | border: 1px solid var(--warn)",
  /*
   * The two notes above the incidents list.
   *
   * The churn note edges in `--warn` like the connection badge: it explains
   * why a bouncing monitor has gone quiet rather than announcing a new
   * outage. The ack failure edges in `--down` because a write that did not
   * land is a failure of this screen, exactly like an invalid input.
   *
   * The incident rows themselves need no entry here: they carry their state as
   * a background tint plus a word, not as a coloured edge.
   */
  "web/src/incidents/incidents.css | .inc-churn, .inc-notice | border: 1px solid var(--warn)",
  "web/src/incidents/incidents.css | .inc-notice | border-color: var(--down)",
  "web/src/live/connection.css | .conn-badge-retry | border: 1px solid var(--warn)",
  /*
   * The rows layout's status stripe, drawn on the row's FIRST CELL.
   *
   * These read `> :first-child` rather than the row itself, and the change is
   * not cosmetic: under `border-collapse: separate` a `<tr>` paints no border
   * at all, so the four rules that used to sit on `.mon-row[data-status=…]`
   * were live in the computed style and invisible on screen. Verified by
   * sampling the row's leftmost pixels in Chromium — the neutral cell border,
   * then the fill, with no status colour anywhere. The `.mon-card` and
   * `.mon-line` entries below never had the problem; they are ordinary
   * elements.
   *
   * `border-left-color` alone for three of them, because the resting rule
   * already reserves the 2px so the cells do not shift when a status arrives;
   * paused also changes the style, which is the second, non-colour signal.
   */
  'web/src/monitors/monitors.css | .mon-row[data-status="down"] > :first-child | border-left-color: var(--down)',
  'web/src/monitors/monitors.css | .mon-row[data-status="pending"] > :first-child | border-left-color: var(--warn)',
  'web/src/monitors/monitors.css | .mon-row[data-status="paused"] > :first-child | border-left-color: var(--ink-3)',
  'web/src/monitors/monitors.css | .mon-row[data-status="waiting"] > :first-child | border-left-color: var(--idle)',
  /*
   * The resting edge those four colour in. 2px rather than 1 so the row's
   * contents do not move one pixel right the moment a monitor goes down —
   * `--border` is a role token, but the width needs an entry.
   */
  "web/src/monitors/monitors.css | .mon-row > :first-child | border-left: 2px solid var(--border)",
  /*
   * The two headers above those rows, carrying the SAME 2px as a transparent
   * edge so their text starts on the line the row content starts on.
   *
   * `:first-child` on the header, because only the row's first child carries
   * the real border. `.mon-head` alone applied the reserve to all five header
   * cells and pushed the four with an unbordered cell beneath them two pixels
   * right of their own data — MONITOR at x=381 over a name at 379, measured
   * in Chromium at 1280px.
   *
   * This is the width guard doing its job and being answered rather than
   * silenced: 2px here is not a second status stripe, it is the *absence* of
   * one, reserved so the column reads as one line. A padding of 14px would
   * produce the same pixels and would not survive the next edit to the row's
   * border, because nothing would connect the two numbers.
   */
  "web/src/monitors/monitors.css | .mon-head:first-child | border-left: 2px solid transparent",
  "web/src/monitors/monitors.css | .mon-section-title | border-left: 2px solid transparent",
  'web/src/monitors/monitors.css | .mon-card[data-status="down"] | border-left: 2px solid var(--down)',
  'web/src/monitors/monitors.css | .mon-card[data-status="pending"] | border-left: 2px solid var(--warn)',
  'web/src/monitors/monitors.css | .mon-card[data-status="paused"] | border-left: 2px dotted var(--ink-3)',
  'web/src/monitors/monitors.css | .mon-card[data-status="waiting"] | border-left: 2px solid var(--idle)',
  'web/src/monitors/monitors.css | .mon-line[data-status="down"] | border-left: 2px solid var(--down)',
  'web/src/monitors/monitors.css | .mon-line[data-status="pending"] | border-left: 2px solid var(--warn)',
  'web/src/monitors/monitors.css | .mon-line[data-status="paused"] | border-left: 2px dotted var(--ink-3)',
  'web/src/monitors/monitors.css | .mon-line[data-status="waiting"] | border-left: 2px solid var(--idle)',
  "web/src/monitors/monitors.css | .push-reveal-warn | border-left: 2px solid var(--warn)",
  'web/src/monitors/monitors.css | .add-input[aria-invalid="true"] | border-color: var(--down)',
  'web/src/monitors/monitors.css | .add-input[aria-invalid="true"]:focus | border-color: var(--down)',
  'web/src/auth/auth.css | .auth-input[aria-invalid="true"] | border-color: var(--down)',
  'web/src/auth/auth.css | .auth-input[aria-invalid="true"]:focus | border-color: var(--down)',
  'web/src/wall/wall.css | .wall-card[data-status="down"] | border-color: color-mix(in srgb, var(--down) 40%, var(--border))',
  'web/src/wall/wall.css | .wall-card[data-status="pending"] | border-color: color-mix(in srgb, var(--warn) 34%, var(--border))',
]);

describe("depth comes from the ladder in §2.10", () => {
  it("defines all three rungs in both themes", () => {
    for (const theme of ["dark", "light"] as const) {
      const block = declarations(themeBlock(theme));
      for (const rung of ["flat", "raised", "float"]) {
        expect(
          block.get(`--shadow-${rung}`),
          `${theme} --shadow-${rung}`,
        ).toBeDefined();
      }
    }
  });

  it("keeps the raised and floating rungs two-layered", () => {
    // One hard layer is what the old single token did, and on a near-black
    // canvas it read as a seam rather than as height. Two layers, a contact
    // shadow and an ambient one, is the thing that makes it depth.
    for (const theme of ["dark", "light"] as const) {
      const block = declarations(themeBlock(theme));
      for (const rung of ["raised", "float"]) {
        expect(
          block.get(`--shadow-${rung}`)?.split("),").length,
          `${theme} --shadow-${rung} layer count`,
        ).toBe(2);
      }
      expect(block.get("--shadow-flat")).toBe("none");
    }
  });

  it("takes every box-shadow from the ladder or the glow scale", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const [, value] of readFileSync(file, "utf8").matchAll(
        /(?:^|[\s;{])box-shadow\s*:\s*([^;{}]+)/g,
      )) {
        const shadow = value.trim();
        if (/^(none|inherit|unset)$/.test(shadow)) continue;
        if (SHADOW_RUNG.test(shadow)) continue;
        if (shadowExceptions.has(shadow)) continue;
        offenders.push(`${relative(repoRoot, file)}: box-shadow: ${shadow}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("rejects a rung with anything appended to it", () => {
    expect(SHADOW_RUNG.test("var(--shadow-raised)")).toBe(true);
    expect(SHADOW_RUNG.test("var(--shadow-raised), 0 0 4px red")).toBe(false);
    expect(SHADOW_RUNG.test("var(--shadow-raised-custom)")).toBe(false);
    expect(SHADOW_RUNG.test("0 0 2px var(--glow-up)")).toBe(false);
  });

  it("states the ladder in docs/DESIGN.md", () => {
    expect(designMd).toContain("### 2.10 Depth is a ladder of three");
    expect(designMd).toContain("--shadow-raised");
  });
});

/**
 * One complete rung of the ladder, or one complete glow. Anchored on purpose:
 * an unanchored match accepted `var(--shadow-raised), 0 0 4px red` and names
 * such as `var(--shadow-raised-custom)`, both of which leave the ladder.
 */
const SHADOW_RUNG =
  /^var\(--(?:shadow-(?:flat|raised|float)|glow-(?:up|warn|down|idle))\)$/;

/**
 * Shadows that are not depth. An `inset` ring draws an edge without changing
 * the box's size and a focus ring is a state, not a height; neither is a rung
 * on a ladder about how far a surface sits from the page.
 */
const shadowExceptions = new Set<string>([
  "inset 0 0 0 2px var(--warn)",
  "inset 0 0 0 1.5px var(--ink-2)",
  "0 0 0 3px var(--accent-ring)",
  "0 0 0 3px var(--ring-down)",
]);

/**
 * A declaration of any border property that paints with the static `--border`
 * token, and the narrower "colour only" form of the same.
 *
 * Both match every border property name rather than `border` plus a single
 * suffix. The single-suffix version had a hole with real consequences:
 * `border-inline-start: 1px solid var(--border)` matched neither the
 * violation pattern nor the state-rule exemption, so a `[data-state]` rule
 * could introduce a whole static edge on a control and this contract would
 * pass. Logical properties are the natural way to write that edge, which is
 * what made the gap reachable rather than theoretical.
 *
 * The exemption is keyed on the property being colour-only — a name ending in
 * `-color` — rather than on the value having no digits. `border-color` cannot
 * create an edge that the resting rule has not already reserved; any form
 * carrying a width can.
 */
const BORDER_ON_STATIC = /(?:^|[;{\s])(border[a-z-]*)\s*:[^;]*var\(--border\)/;
const BORDER_COLOUR_ONLY = /^border(?:-[a-z]+)*-color$/;

/**
 * Does this declaration block paint a static `--border` edge through a
 * property that could introduce one?
 */
function restsOnStaticBorder(body: string): boolean {
  return BORDER_ON_STATIC.test(body);
}

/**
 * A `[data-state]` rule that only re-*paints* an edge the resting rule has
 * already reserved. Colour-only declarations, and nothing else.
 */
function repaintsReservedEdge(selector: string, body: string): boolean {
  if (!/\[data-state=/.test(selector)) return false;
  for (const [, property] of body.matchAll(
    /(?:^|[;{\s])(border[a-z-]*)\s*:/g,
  )) {
    if (!BORDER_COLOUR_ONLY.test(property)) return false;
  }
  return true;
}

describe("an interactive element does not rest on the static border", () => {
  it("gives every control the control role at rest", () => {
    // The defect §2.9 closes: --border-hi was used only on :hover and :active,
    // so at rest a button carried exactly the same edge as a static card and
    // looked clickable only once the pointer arrived.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const block of declarationBlocks(readFileSync(file, "utf8"))) {
        if (!paintsControl(block.selector)) continue;
        // A state rule describes the change, not the resting edge.
        if (
          /:(hover|focus|active|disabled|checked)|\[aria-invalid/.test(
            block.selector,
          )
        )
          continue;
        // So does a `[data-state=…]` rule that only re-*paints* an edge the
        // resting rule has already reserved (SUB-140).
        //
        // The sidebar's current destination carries the same subtle `--border`
        // a panel does, which is what the product owner asked for, and §8.2
        // now states. It is not a resting edge: `.shell-nav-item` declares
        // `border: 1px solid transparent` at rest — the reserve pattern this
        // repo uses everywhere so a box does not change size when its state
        // changes — and the state rule changes only the paint.
        //
        // Narrow on purpose. Only a colour-only declaration is exempt, and a
        // colour cannot introduce an edge where none was reserved, so this
        // cannot be used to declare a control resting on the static token; a
        // `border: 1px solid var(--border)` shorthand under a `[data-state]`
        // selector — or its `border-inline-start` equivalent — still fails
        // here.
        if (repaintsReservedEdge(block.selector, block.body)) continue;
        if (restsOnStaticBorder(block.body)) {
          offenders.push(`${relative(repoRoot, file)}: ${block.selector}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("still bites on a `[data-state]` rule that declares a whole static edge", () => {
    // The exemption above is for re-painting a reserved edge, never for
    // introducing one. A shorthand under a data-state selector is a resting
    // edge wearing a state's clothes, and must still fail — including when it
    // is written as a logical property, which is the form that used to slip
    // past both the exemption and the violation matcher.
    const blocks = declarationBlocks(`
      .a-button[data-state="current"] { border-color: var(--border); }
      .b-button[data-state="current"] { border: 1px solid var(--border); }
      .c-button[data-state="current"] { border-inline-start: 1px solid var(--border); }
      .d-button[data-state="current"] { border-inline-start-color: var(--border); }
    `);
    expect(
      blocks
        .filter(
          (b) =>
            paintsControl(b.selector) &&
            !repaintsReservedEdge(b.selector, b.body) &&
            restsOnStaticBorder(b.body),
        )
        .map((b) => b.selector),
    ).toEqual([
      '.b-button[data-state="current"]',
      '.c-button[data-state="current"]',
    ]);
  });

  it("bites on a control that rests on the static token", () => {
    const blocks = declarationBlocks(`
      .a-button { border: 1px solid var(--border); }
      .a-button:hover { border-color: var(--border-hi); }
      .a-card { border: 1px solid var(--border); }
    `);
    expect(
      blocks
        .filter(
          (b) =>
            paintsControl(b.selector) &&
            !/:(hover|focus|active|disabled|checked)/.test(b.selector) &&
            restsOnStaticBorder(b.body),
        )
        .map((b) => b.selector),
    ).toEqual([".a-button"]);
  });
});

/**
 * SUB-106: a face is a whole configuration, not a family name.
 *
 * The defect these close is the quiet kind. `font-family: var(--font-mono)`
 * got the shapes and nothing else, so which mono elements had tabular figures
 * depended on whether whoever wrote the rule remembered to ask: seven call
 * sites did, seven did not, and a column of latencies in one layout lined up
 * while the same numbers in the next did not. Nothing on screen names that as
 * a bug — it just looks slightly wrong and no one can say why.
 */
/**
 * SUB-106: the caps legend is a role, not five declarations repeated.
 *
 * The small uppercase label over a column, a panel, a nav group or a form
 * field was written out by hand in nine rules across five stylesheets, and
 * they disagreed: three tones (`--ink-2`, `--ink-3`, `--ink-4`), two weights,
 * and sans in every one of them. Two of those tones do not clear any contrast
 * floor at 12px — `--ink-4` measures 1.90:1 against `--surface` in dark — on
 * text whose entire job is to say what the number under it means.
 *
 * Repetition is what let them drift, so the guard is on the repetition: a rule
 * that spells out the casing has opted out of the role, and the next tone is
 * already chosen by hand.
 */
describe("the caps legend is applied as a role", () => {
  const indexCss = readFileSync(join(webSrc, "index.css"), "utf8");

  it("defines the role with the whole configuration", () => {
    const start = indexCss.indexOf("@utility caps-legend {");
    expect(start, "missing the caps-legend utility").toBeGreaterThan(-1);
    const body = indexCss.slice(start, indexCss.indexOf("\n}", start));
    // The face comes first: a legend is quiet because it is mono, small and
    // uppercase, which is exactly why it does not also have to be faded.
    expect(body, "face").toContain("@apply face-mono;");
    expect(body, "size").toContain("font-size: var(--type-section);");
    expect(body, "leading").toContain("line-height: var(--lead-section);");
    expect(body, "weight").toContain("font-weight: var(--weight-plain);");
    expect(body, "casing").toContain("text-transform: uppercase;");
    // Tracking is deliberately absent: the role inherits the body value like
    // every other face. Asserting its absence rather than saying nothing,
    // because re-adding a positive caps tracking is the exact edit that made
    // these labels read as spaced-out small caps.
    expect(body, "tracking").not.toContain("letter-spacing:");
  });

  it("gives the legend a tone that clears AA rather than a faded one", () => {
    // `--ink-3` and `--ink-4` are the two this role was written with and the
    // two it may not use: measured against `--surface` they reach 3.37:1 and
    // 1.90:1 in dark, 3.19:1 and 1.94:1 in light. This is text, so it owes
    // 4.5:1, and `--ink-2` is the first rung that pays it (7.31 / 6.26).
    const start = indexCss.indexOf("@utility caps-legend {");
    const body = indexCss.slice(start, indexCss.indexOf("\n}", start));
    expect(body).toContain("color: var(--ink-2);");
  });

  it("finds no hand-rolled legend anywhere else under web/src", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (file === join(webSrc, "index.css")) continue;
      const contents = stripComments(readFileSync(file, "utf8"));
      for (const match of contents.matchAll(
        /text-transform:\s*uppercase|\buppercase\b/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe("a face is applied as a role, not as a family name", () => {
  const indexCss = readFileSync(join(webSrc, "index.css"), "utf8");

  it("defines both roles with all four properties", () => {
    for (const face of ["sans", "mono"]) {
      const start = indexCss.indexOf(`@utility face-${face} {`);
      expect(start, `missing the face-${face} utility`).toBeGreaterThan(-1);
      const body = indexCss.slice(start, indexCss.indexOf("\n}", start));
      expect(body, `face-${face} family`).toContain(
        `font-family: var(--font-${face});`,
      );
      expect(body, `face-${face} ligatures`).toContain(
        `font-variant-ligatures: var(--ligatures-${face});`,
      );
      expect(body, `face-${face} numeric`).toContain(
        `font-variant-numeric: var(--numeric-${face});`,
      );
      expect(body, `face-${face} rendering`).toContain(
        `text-rendering: var(--render-${face});`,
      );
    }
  });

  it("takes every face value from the §2.5 table", () => {
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const start = designMd.indexOf("**A face is a configuration");
    expect(start, "missing the §2.5 face prose").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n###", start));
    const rows = [
      ...body.matchAll(
        /\|\s*(?:Sans|Mono)\s*\|\s*`(--ligatures-[a-z]+)`\s*`([^`]+)`\s*\|\s*`(--numeric-[a-z]+)`\s*`([^`]+)`\s*\|\s*`(--render-[a-z]+)`\s*`([^`]+)`\s*\|/g,
      ),
    ];
    expect(rows.length, "expected a sans row and a mono row").toBe(2);
    for (const row of rows) {
      for (let i = 1; i < row.length; i += 2) {
        expect(root.get(row[i]), row[i]).toBe(row[i + 1]);
      }
    }
  });

  it("names no font family outside the two roles", () => {
    // A rule that reaches for the family directly is a rule that opted out of
    // the face without saying so, and it is exactly how the tabular-figure
    // drift started.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      // index.css is where the two roles are defined, so it is the one file
      // that names a family on purpose.
      if (file === join(webSrc, "index.css")) continue;
      // An @font-face block is the one place a family name is the subject
      // rather than a shortcut past a role: it is the declaration that names
      // the family, so the two roles have something to point at.
      const contents = stripComments(readFileSync(file, "utf8")).replace(
        /@font-face\s*\{[^}]*\}/g,
        "",
      );
      for (const match of contents.matchAll(
        /font-family:\s*[^;]+|\bfont-(?:sans|mono|serif)\b/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("sets no font-variant-numeric or text-rendering outside the roles", () => {
    // Both belong to a face. Set per component they are either a duplicate of
    // what the role already says, or a silent disagreement with it — and the
    // second is invisible: a rule that re-states `tabular-nums` next to a face
    // that already carries it looks identical to one that overrides it.
    // Matched as declarations, not as bare words, so that prose explaining the
    // rule does not read as a violation of it.
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (file === join(webSrc, "index.css")) continue;
      const contents = stripComments(readFileSync(file, "utf8"));
      for (const match of contents.matchAll(
        /(?:font-variant-numeric|font-variant-ligatures|text-rendering)\s*:\s*[^;]+|\b(?:tabular-nums|slashed-zero|normal-nums|ordinal|oldstyle-nums)\b/g,
      )) {
        offenders.push(`${relative(repoRoot, file)}: ${match[0].trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("catches a re-stated variant and spares the prose that explains it", () => {
    const offenders = (css: string) =>
      [
        ...stripComments(css).matchAll(
          /(?:font-variant-numeric|font-variant-ligatures|text-rendering)\s*:\s*[^;]+|\b(?:tabular-nums|slashed-zero|normal-nums|ordinal|oldstyle-nums)\b/g,
        ),
      ].map((m) => m[0].trim());
    expect(
      offenders("/* tabular-nums belongs to the face. */\n.a { color: red; }"),
    ).toEqual([]);
    expect(offenders(".a { font-variant-numeric: tabular-nums; }")).toEqual([
      "font-variant-numeric: tabular-nums",
    ]);
  });

  it("keeps the mono face's zero slashed and its ligatures off", () => {
    // The two decisions the mono face exists for. Stated as their own test so
    // that reversing either one fails with the reason attached rather than as
    // a token mismatch.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    expect(root.get("--numeric-mono")).toContain("slashed-zero");
    expect(root.get("--numeric-mono")).toContain("tabular-nums");
    expect(root.get("--ligatures-mono")).toBe("none");
  });
});

/**
 * The concentric rule (§2.7): an inner radius is the outer radius minus the
 * padding between them. Equal radii look right in a mockup and pinch at the
 * corners on screen, because the gap between two curves of the same radius is
 * not constant — the corner of the inner element crowds the corner of the
 * outer one while the straight edges stay parallel.
 *
 * It is the kind of rule that is obeyed once and then broken by the next
 * component, which is why it is asserted rather than written down. Today no
 * component nests a radius inside a padded radius — the segmented control that
 * will is still open on SUB-106 — so the guard has nothing live to catch and
 * is proved against fixtures instead. That is the point of landing it now: it
 * is in place before the components that have to obey it are written, rather
 * than after the first one has already chosen a number by eye.
 */
/**
 * Both ladders, read from tokens.css rather than restated here.
 *
 * They used to be literals, and they drifted: `--r-md` was listed as 10px
 * while the stylesheet had moved to 8px, and the half-steps `--space-1h` (6)
 * and `--space-2h` (10) were missing entirely. A guard doing arithmetic on
 * stale copies of the values it is checking reports failures that are its own
 * and, worse, passes things it should catch. Parsing the file removes the copy.
 */
function pxLadder(prefix: string): Map<string, number> {
  const ladder = new Map<string, number>();
  for (const [, name, px] of tokensCss.matchAll(
    new RegExp(`(--${prefix}-[a-z0-9]+):\\s*(\\d+)px`, "g"),
  )) {
    if (!ladder.has(name)) ladder.set(name, Number(px));
  }
  return ladder;
}

const RADIUS_LADDER = pxLadder("r");
const SPACE_LADDER = pxLadder("space");

function radiusOf(body: string): string | undefined {
  return body.match(/border-radius:\s*var\((--r-[a-z0-9]+)\)\s*;/)?.[1];
}

/**
 * The gap a `padding` shorthand leaves on all four sides, or undefined if it
 * does not leave the same one on each.
 *
 * The shorthand has four forms and every one of them can be uniform:
 * `var(--space-1)`, `var(--space-1) var(--space-1)`, and the three- and
 * four-value spellings. Matching only the single-value form let the other
 * three through unchecked, which is a silent hole in a guard — the rule was
 * still broken, the parser just stopped looking.
 *
 * Asymmetric padding still yields nothing, deliberately: with no single gap
 * there is no single inner radius, and picking one of the four values would
 * enforce a rule §2.7 does not state.
 */
function uniformGap(body: string): number | undefined {
  const declared = body.match(/(?:^|[;{\s])padding:\s*([^;}]+)/)?.[1];
  if (!declared) return undefined;

  // `!important` is a valid tail on the declaration and says nothing about the
  // gap; left in place it reads as a second, unequal value and the guard goes
  // quiet on a rule that is still broken.
  const values = declared
    .replace(/\s*!\s*important\s*$/i, "")
    .trim()
    .split(/\s+/);
  // CSS shorthand expansion: 1 → all four, 2 → block/inline, 3 → the middle
  // value repeats for both inline sides, 4 → top right bottom left.
  const sides =
    values.length === 1
      ? [values[0], values[0], values[0], values[0]]
      : values.length === 2
        ? [values[0], values[1], values[0], values[1]]
        : values.length === 3
          ? [values[0], values[1], values[2], values[1]]
          : values.length === 4
            ? values
            : undefined;
  if (!sides) return undefined;
  if (!sides.every((side) => side === sides[0])) return undefined;

  // `--space-\d+` missed every half-step — `--space-1h`, `--space-2h` and the
  // quarter-step `--space-0h` — so any rule whose inset was one of them fell
  // out of the scan without a word. That is how the product's only concentric
  // pair became invisible to the guard that exists to check it.
  const token = sides[0].match(/^var\((--space-[0-9]+h?)\)$/)?.[1];
  return token === undefined ? undefined : SPACE_LADDER.get(token);
}

/**
 * Outer/inner radius pairs in one stylesheet, as the rule requires them to be.
 *
 * A pair is an element whose selector is a descendant of a padded, rounded
 * one — `.panel .segment` inside `.panel` — where both state a radius. Only a
 * uniform `padding` counts; see `uniformGap`.
 */
function concentricPairs(css: string): {
  selector: string;
  want: number;
  got: number;
}[] {
  const blocks = declarationBlocks(css);
  const outers = blocks.flatMap(({ selector, body }) => {
    const radius = radiusOf(body);
    if (!radius) return [];
    const outer = RADIUS_LADDER.get(radius);
    const gap = uniformGap(body);
    if (outer === undefined || gap === undefined) return [];
    return [{ selector, want: outer - gap }];
  });

  const pairs = [];
  for (const { selector, body } of blocks) {
    const radius = radiusOf(body);
    if (!radius) continue;
    const got = RADIUS_LADDER.get(radius);
    if (got === undefined) continue;
    for (const outer of outers) {
      if (selector === outer.selector) continue;
      if (!selector.startsWith(`${outer.selector} `)) continue;
      pairs.push({ selector, want: outer.want, got });
    }
  }
  return pairs;
}

describe("the concentric radius rule", () => {
  it("is stated in docs/DESIGN.md", () => {
    expect(designMd).toContain("**The concentric rule.**");
  });

  it("keeps every nested radius concentric with the one around it", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const { selector, want, got } of concentricPairs(
        readFileSync(file, "utf8"),
      )) {
        if (got === want) continue;
        offenders.push(
          `${relative(repoRoot, file)}: ${selector} is ${got}px inside a corner that leaves ${want}px`,
        );
      }
    }
    expect(offenders).toEqual([]);
  });

  it("has a real pair to check, so the guard cannot pass vacuously", () => {
    // The guard above is a loop over the pairs found under web/src. If the
    // scan ever finds none — a selector shape it cannot read, a stylesheet
    // moved out of the tree — the loop runs zero times and the suite reports
    // the rule as upheld while nothing at all was examined. That is the
    // failure mode of every "no offenders" test, and the only defence is to
    // assert that the scan is looking at something.
    //
    // It is not asserted against one named component on purpose: which
    // component nests a padded radius is a layout decision that may move, and
    // a guard that names it would have to be edited every time it did.
    const found: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      for (const { selector } of concentricPairs(readFileSync(file, "utf8"))) {
        found.push(`${relative(repoRoot, file)}: ${selector}`);
      }
    }
    expect(
      found.length,
      "no live outer/inner radius pair under web/src — the concentric guard is checking nothing",
    ).toBeGreaterThan(0);
  });

  it("subtracts the padding rather than comparing the tokens to themselves", () => {
    // The arithmetic the rule actually states, on values taken from the two
    // ladders rather than from the stylesheet being checked: --r-md (8px) of
    // outer radius with --space-1 (4px) of padding inside it leaves 4px, which
    // is --r-xs. A guard that only asked "is the inner token different from
    // the outer one" would accept any of the five steps here.
    //
    // The expectations are written as the arithmetic rather than as literals,
    // so this fixture cannot go stale the way the hardcoded ladders did: when
    // a radius token moves in tokens.css, the sum moves with it.
    const md = RADIUS_LADDER.get("--r-md") ?? 0;
    const s1 = SPACE_LADDER.get("--space-1") ?? 0;
    const xs = RADIUS_LADDER.get("--r-xs") ?? 0;
    const sm = RADIUS_LADDER.get("--r-sm") ?? 0;
    expect(md - s1, "--r-md minus --space-1 should land on --r-xs").toBe(xs);

    expect(
      concentricPairs(`
        .outer { border-radius: var(--r-md); padding: var(--space-1); }
        .outer .inner { border-radius: var(--r-xs); }
      `),
    ).toEqual([{ selector: ".outer .inner", want: md - s1, got: xs }]);

    expect(
      concentricPairs(`
        .outer { border-radius: var(--r-md); padding: var(--space-1); }
        .outer .inner { border-radius: var(--r-sm); }
      `),
    ).toEqual([{ selector: ".outer .inner", want: md - s1, got: sm }]);
  });

  it("bites on an inner radius that copies the outer one", () => {
    // The mistake the rule exists to stop: reaching for the same token inside
    // and out, which reads as consistency and draws as a pinched corner.
    const copied = `
      .panel { border-radius: var(--r-sm); padding: var(--space-1); }
      .panel .segment { border-radius: var(--r-sm); }
    `;
    expect(concentricPairs(copied)).toEqual([
      { selector: ".panel .segment", want: 2, got: 6 },
    ]);

    const concentric = `
      .panel { border-radius: var(--r-sm); padding: var(--space-1); }
      .panel .segment { border-radius: var(--r-2xs); }
    `;
    expect(concentricPairs(concentric)).toEqual([
      { selector: ".panel .segment", want: 2, got: 2 },
    ]);
  });

  it("reads every uniform spelling of the padding shorthand", () => {
    // All four forms state the same 4px gap, so all four have to produce the
    // same pair. Only the first was being read before, which meant the other
    // three could hide a broken inner radius from the guard entirely.
    const forms = [
      "var(--space-1)",
      "var(--space-1) var(--space-1)",
      "var(--space-1) var(--space-1) var(--space-1)",
      "var(--space-1) var(--space-1) var(--space-1) var(--space-1)",
    ];
    for (const padding of forms) {
      expect(
        concentricPairs(`
          .panel { border-radius: var(--r-sm); padding: ${padding}; }
          .panel .segment { border-radius: var(--r-sm); }
        `),
      ).toEqual([{ selector: ".panel .segment", want: 2, got: 6 }]);
    }
  });

  it("reads a uniform shorthand that carries !important", () => {
    // The suffix is part of the declaration, not of the gap. Reading it as a
    // value made the padding look asymmetric and let the pair through.
    expect(
      concentricPairs(`
        .panel { border-radius: var(--r-sm); padding: var(--space-1) !important; }
        .panel .segment { border-radius: var(--r-sm); }
      `),
    ).toEqual([{ selector: ".panel .segment", want: 2, got: 6 }]);
  });

  it("ignores a container whose padding is not uniform", () => {
    // Four gaps, so no single inner radius. Checking it against one of them
    // would enforce a rule §2.7 does not state.
    const asymmetric = [
      "var(--space-1) var(--space-2)",
      "var(--space-1) var(--space-1) var(--space-2)",
      "var(--space-1) var(--space-1) var(--space-1) var(--space-2)",
    ];
    for (const padding of asymmetric) {
      expect(
        concentricPairs(`
          .panel { border-radius: var(--r-sm); padding: ${padding}; }
          .panel .segment { border-radius: var(--r-sm); }
        `),
      ).toEqual([]);
    }
  });
});

/**
 * SUB-106: the chip family (§8.1).
 *
 * Two things here fail silently and so are asserted rather than written down.
 *
 * The first is the label on a filled status badge. `--ink` measures 1.59:1 on
 * `--up` in dark; a badge painted that way is legible to whoever wrote it on
 * whatever monitor they wrote it on, and the CI has no opinion. The `--on-*`
 * pair is the measured answer, and the guard is that a solid status fill uses
 * it and nothing else does.
 *
 * The second is the dashed convention. A dashed edge means the chip is *about*
 * the data — partial, absent, unassigned — and a solid one means it *is* data.
 * That distinction lives entirely in one character of one declaration, so the
 * next person to add a chip has nothing stopping them from filling the dashed
 * one and collapsing the two meanings back together.
 */

/** The `--on-*` label tokens, by the status they sit on. */
const STATUS_LABEL_TOKENS = ["--on-up", "--on-warn", "--on-down", "--on-idle"];

/** Every rule under web/src, with its file, keyed for reporting. */
function cssRules(): { path: string; selector: string; body: string }[] {
  const found: { path: string; selector: string; body: string }[] = [];
  for (const file of sourceFiles(webSrc)) {
    if (!file.endsWith(".css")) continue;
    const path = relative(repoRoot, file);
    for (const block of declarationBlocks(readFileSync(file, "utf8"))) {
      found.push({ path, selector: block.selector, body: block.body });
    }
  }
  return found;
}

/**
 * True when a rule types something: it sets a text colour, a size, or applies
 * a face role. A status fill that types nothing is a mark — a lamp, a dot —
 * and has no label to make readable.
 */
type CssRule = { path: string; selector: string; body: string };

/** Solid status fills that carry text but do not use their own label token. */
function unlabelledStatusFills(rules: CssRule[]): string[] {
  const offenders: string[] = [];
  for (const { path, selector, body } of rules) {
    const background = valueOf(body, "background");
    const fill = background?.match(/^var\(--(up|warn|down|idle)\)$/);
    if (!fill) continue;
    if (!carriesText(body)) continue;
    const colour = valueOf(body, "color");
    if (colour === `var(--on-${fill[1]})`) continue;
    offenders.push(`${path} | ${selector} | ${colour ?? "no colour"}`);
  }
  return offenders;
}

/** Label tokens used anywhere other than on top of their own status fill. */
function strayLabelTokens(rules: CssRule[]): string[] {
  const offenders: string[] = [];
  for (const { path, selector, body } of rules) {
    const used = STATUS_LABEL_TOKENS.filter((name) =>
      new RegExp(`var\\(${name}\\)`).test(stripComments(body)),
    );
    if (used.length === 0) continue;
    const background = valueOf(body, "background");
    const fill = background?.match(/^var\(--(up|warn|down|idle)\)$/);
    if (fill && used.length === 1 && used[0] === `--on-${fill[1]}`) continue;
    offenders.push(`${path} | ${selector} | ${used.join(" ")}`);
  }
  return offenders;
}

function carriesText(body: string): boolean {
  const clean = stripComments(body);
  return (
    /(?:^|[;{\s])color\s*:/.test(clean) ||
    /(?:^|[;{\s])font-size\s*:/.test(clean) ||
    /@apply[^;]*(?:face-(?:sans|mono)|caps-legend)/.test(clean)
  );
}

/** The value of one property in a declaration block, comments stripped. */
function valueOf(body: string, property: string): string | undefined {
  const match = new RegExp(`(?:^|[;{\\s])${property}\\s*:\\s*([^;{}]+)`).exec(
    stripComments(body),
  );
  return match?.[1].trim();
}

describe("a filled status mark labels itself with the measured pair (§2.3)", () => {
  it("defines all four label tokens in both themes", () => {
    for (const theme of ["dark", "light"] as const) {
      const block = declarations(themeBlock(theme));
      for (const name of STATUS_LABEL_TOKENS) {
        expect(block.get(name), `${theme} ${name}`).toMatch(/^#[0-9a-f]{6}$/i);
      }
    }
  });

  it("uses the values documented in the §2.3 table", () => {
    const start = designMd.indexOf("**The label on a filled status mark.**");
    expect(start, "missing the §2.3 label table").toBeGreaterThan(-1);
    const body = designMd.slice(start, designMd.indexOf("\n### ", start));
    const rows = [
      ...body.matchAll(
        /\|\s*`(--on-[a-z]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|\s*`(#[0-9a-f]+)`\s*\|/gi,
      ),
    ];
    expect(rows.length, "expected four label rows").toBe(4);
    for (const [, name, dark, light] of rows) {
      expect(declarations(themeBlock("dark")).get(name), `dark ${name}`).toBe(
        dark.toLowerCase(),
      );
      expect(declarations(themeBlock("light")).get(name), `light ${name}`).toBe(
        light.toLowerCase(),
      );
    }
  });

  it("pairs every solid status fill that carries text with its own label token", () => {
    // The failure this catches: a chip filled `--down` whose text stays on the
    // ink scale. It reads as a design choice in the diff and as unreadable
    // text on screen.
    //
    // A fill with no text on it is not a label and is excluded: `.led` and
    // `.conn-badge-dot` are the status colour drawn as a *mark*, a 7px lamp
    // with no glyph in it, and demanding a label colour there would mean
    // declaring a text colour for text that does not exist. So the guard asks
    // whether the rule types anything — a colour, a size, a face role — and
    // only then insists the colour be the measured one.
    expect(unlabelledStatusFills(cssRules())).toEqual([]);
  });

  it("keeps the label tokens off anything that is not a solid status fill", () => {
    // The mirror failure: `--on-down` used as ordinary text colour because it
    // happened to look right in one theme. It is only measured against its
    // own fill, so anywhere else it is an unmeasured colour.
    expect(strayLabelTokens(cssRules())).toEqual([]);
  });

  it("bites on a status fill wearing ink, and on a label token off its fill", () => {
    const rule = (css: string): CssRule => ({
      path: "fixture.css",
      ...declarationBlocks(css)[0],
    });

    const wrongLabel = rule(`
      .chip--bad { background: var(--down); color: var(--ink); }
    `);
    expect(unlabelledStatusFills([wrongLabel])).toEqual([
      "fixture.css | .chip--bad | var(--ink)",
    ]);
    expect(strayLabelTokens([wrongLabel])).toEqual([]);

    const strayToken = rule(`
      .note { color: var(--on-down); }
    `);
    expect(strayLabelTokens([strayToken])).toEqual([
      "fixture.css | .note | --on-down",
    ]);
    expect(unlabelledStatusFills([strayToken])).toEqual([]);

    // And the legitimate pairing stays silent in both directions.
    const correct = rule(`
      .chip--status { background: var(--down); color: var(--on-down); }
    `);
    expect(unlabelledStatusFills([correct])).toEqual([]);
    expect(strayLabelTokens([correct])).toEqual([]);
  });
});

describe("a dashed edge means the chip is about the data (§8.1)", () => {
  it("states the convention in docs/DESIGN.md", () => {
    expect(designMd).toContain("### 8.1 The chip family");
    expect(designMd).toContain("### 8.2 The icon tile");
  });

  it("never fills a rule that draws a dashed edge", () => {
    // A dashed border plus a fill is the two meanings collapsed back into one:
    // it draws as a status badge and claims to be a statement about the data.
    // The row stripes are excluded by subject — they are an edge on a row, not
    // a chip, and `.mon-row` carries a fill for a different reason entirely.
    const offenders: string[] = [];
    for (const { path, selector, body } of cssRules()) {
      if (!/\bchip\b|chip--|chip-avatar/.test(selector)) continue;
      const clean = stripComments(body);
      if (!/border(?:-[a-z]+)?(?:-style)?:[^;]*\bdashed\b/.test(clean))
        continue;
      const background = valueOf(body, "background");
      if (
        !background ||
        background === "none" ||
        background === "transparent"
      ) {
        continue;
      }
      offenders.push(`${path} | ${selector} | background: ${background}`);
    }
    expect(offenders).toEqual([]);
  });

  it("keeps the two dashed kinds dashed", () => {
    // The convention only works if the kinds that carry it actually carry it.
    // Losing the dash is a one-character edit that no reviewer would query.
    const dashed = new Set<string>();
    for (const { selector, body } of cssRules()) {
      if (
        !/border(?:-[a-z]+)?(?:-style)?:[^;]*\bdashed\b/.test(
          stripComments(body),
        )
      ) {
        continue;
      }
      dashed.add(selector);
    }
    expect([...dashed]).toContain(".chip--state");
    expect([...dashed]).toContain(".chip-avatar");
  });

  it("loads every stylesheet it ships", () => {
    // A component stylesheet that nothing imports is invisible to the whole
    // suite: jsdom applies no CSS, so every test still passes while the
    // component renders unstyled in the browser. Four of these shipped
    // together once — the components were built in isolation and the file
    // that collects them was owned by someone else.
    //
    // Walks the import graph from the entrypoint rather than checking that
    // index.css names each file, because a stylesheet may legitimately be
    // pulled in by the one next to it (heartbeat.css imports chart.css).
    const seen = new Set<string>();
    const walk = (file: string) => {
      if (seen.has(file)) return;
      seen.add(file);
      let contents: string;
      try {
        contents = readFileSync(file, "utf8");
      } catch {
        return;
      }
      for (const match of contents.matchAll(/@import\s+"([^"]+)"/g)) {
        const target = match[1];
        if (!target.startsWith(".")) continue; // a package, not one of ours
        walk(join(file, "..", target));
      }
    };
    walk(join(webSrc, "index.css"));

    const orphans: string[] = [];
    for (const file of sourceFiles(webSrc)) {
      if (!file.endsWith(".css")) continue;
      if (seen.has(file)) continue;
      orphans.push(relative(repoRoot, file));
    }
    expect(orphans).toEqual([]);
  });

  it("keeps every focus ring on the accent", () => {
    // Focus is a control state: it says "your keyboard is here". §2.8 gives
    // that job to the accent precisely so it never reads as a fact about the
    // data — a neutral ring is the same grey the product uses for text it is
    // de-emphasising, which is the opposite of what focus means.
    //
    // This regressed once already: every ring moved to the accent except one
    // in shell.css, which kept --ink-2 and went unnoticed because nothing
    // looked at it. Scanning the rules is what makes "every" true.
    const offenders: string[] = [];
    for (const { path, selector, body } of cssRules()) {
      if (!/:focus(?:-visible|-within)?\b/.test(selector)) continue;

      const clean = stripComments(body);
      const outline = /(?:^|[;{\s])outline(?:-color)?:\s*([^;]+)/.exec(clean);
      const ring = /box-shadow:\s*([^;]+)/.exec(clean);

      for (const [property, value] of [
        ["outline", outline?.[1]],
        ["box-shadow", ring?.[1]],
      ] as const) {
        if (!value) continue;
        // `outline: none` and a shadow that only lifts the surface are not
        // rings and carry no colour claim.
        if (/^\s*(?:none|0)\s*$/.test(value)) continue;
        if (!/var\(--/.test(value)) continue;

        const tokens = [...value.matchAll(/var\((--[\w-]+)/g)].map((m) => m[1]);
        const carriesAccent = tokens.some((t) => t.startsWith("--accent"));
        // A ring may legitimately reference an error tone; what it may not do
        // is sit on a neutral from the ink or border scale.
        const carriesNeutral = tokens.some(
          (t) => /^--ink(?:-|$)/.test(t) || /^--border(?:-|$)/.test(t),
        );

        if (carriesNeutral && !carriesAccent) {
          offenders.push(
            `${path} | ${selector} | ${property}: ${value.trim()}`,
          );
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe("hover does not overwrite a status tint with a neutral one", () => {
  // The failure this exists to stop is quiet and specific: a row is red
  // because the monitor is down, the shared hover rule paints --surface-2 over
  // it, and for as long as the pointer rests there the broken monitor looks
  // ordinary. It is invisible in a screenshot and invisible in jsdom, because
  // it only exists while something is hovered.
  //
  // The rule: any selector that sets a resting status fill must either leave
  // hover alone or answer it with a status tone. What it may not do is let a
  // neutral surface win by specificity.

  /** Selectors with a resting fill from the status palette. */
  const tinted = (): { path: string; selector: string; token: string }[] => {
    const found: { path: string; selector: string; token: string }[] = [];
    for (const { path, selector, body } of cssRules()) {
      if (/:hover|:focus/.test(selector)) continue;
      const clean = stripComments(body);
      const fill =
        /(?:^|[;{\s])background(?:-color)?:\s*var\((--(?:up|warn|down|idle)-dim)\)/.exec(
          clean,
        );
      if (fill) found.push({ path, selector, token: fill[1] });
    }
    return found;
  };

  it("answers a tinted row's hover with a status tone, or not at all", () => {
    const rules = cssRules();
    const offenders: string[] = [];

    for (const { path, selector, token } of tinted()) {
      // What does this element look like under the pointer? Either its own
      // :hover rule, or an inherited one from a base class it also carries.
      const hoverRules = rules.filter(
        (r) =>
          /:hover/.test(r.selector) &&
          // The tinted selector's own hover, e.g. `.mon-line[data-status=down]`
          // -> `.mon-line[data-status=down]:hover`
          r.selector.includes(selector.replace(/\s+/g, " ").trim()),
      );

      for (const hover of hoverRules) {
        const clean = stripComments(hover.body);
        const fill =
          /(?:^|[;{\s])background(?:-color)?:\s*var\((--[a-z0-9-]+)\)/.exec(
            clean,
          );
        if (!fill) continue;
        const isStatus = /^--(?:up|warn|down|idle)-(?:dim|deep)$/.test(fill[1]);
        if (!isStatus) {
          offenders.push(
            `${path} | ${hover.selector} paints ${fill[1]} over ${token}`,
          );
        }
      }
    }

    expect(
      offenders,
      "a hovered status row must deepen its own tint, not take a neutral fill",
    ).toEqual([]);
  });

  it("keeps a -deep tone for every status that has a resting fill", () => {
    // The inverse: a status that grew a hover tint but no token to hold it
    // would be spelling the colour inline. Down is currently the only status
    // with a resting fill; if another gains one, this fails and asks for the
    // matching token rather than letting the fill be hand-written.
    const root = declarations(
      tokensCss.slice(tokensCss.indexOf(":root"), tokensCss.indexOf("\n}")),
    );
    const restingFills = new Set(
      tinted().map(({ token }) => token.replace(/-dim$/, "")),
    );
    for (const base of restingFills) {
      // Only rows and cards deepen; a badge or a chip fills itself at rest and
      // has no hover state, so the token is only required where a :hover rule
      // actually reaches for it.
      const used = cssRules().some(
        (r) =>
          /:hover/.test(r.selector) &&
          stripComments(r.body).includes(`var(${base}-deep)`),
      );
      if (!used) continue;
      expect(
        root.has(`${base}-deep`) ||
          declarations(themeBlock("dark")).has(`${base}-deep`),
        `${base}-deep is used on hover but not defined`,
      ).toBe(true);
    }
  });

  it("defines no `-deep` tone, because nothing has a resting fill to deepen", () => {
    // This replaces a test that asserted `--down-deep` exists and moves away
    // from the page in each theme. It was right for as long as a down row
    // carried a resting `--down-dim` fill; the product owner asked for that
    // fill to go (DESIGN.md §2.3), and with no resting tint a hover deepening
    // would *introduce* red rather than intensify it — the exact failure the
    // original rule forbade.
    //
    // So the assertion is inverted rather than deleted: the decision under
    // test is still "a `-deep` tone exists exactly when a status has a resting
    // fill to deepen". Re-adding the token without re-adding a caller now
    // fails here, which is the repo's own rule that a token with no caller is
    // a decision nobody made.
    for (const theme of ["dark", "light"] as const) {
      const declared = declarations(themeBlock(theme));
      const deep = [...declared.keys()].filter((name) =>
        /^--(?:up|warn|down|idle)-deep$/.test(name),
      );
      expect(
        deep,
        `${theme}: a -deep tone with no resting fill to deepen is a token with no caller`,
      ).toEqual([]);
    }
  });
});

describe("the card pattern is the only way to frame a group of panels", () => {
  // The gap this closes is the one the coverage audit named: tokens were
  // thoroughly guarded and *patterns* were not, so a new screen inherited the
  // colour scale automatically and the nesting not at all. Every rule below is
  // about shape rather than value.

  /** Rules that frame something: a border plus the outer radius. */
  const framingRules = () =>
    cssRules().filter(({ body }) => {
      const clean = stripComments(body);
      return (
        /border(?:-[a-z]+)?:\s*1px/.test(clean) &&
        /border-radius:\s*var\(--r-lg\)/.test(clean)
      );
    });

  it("gives every --r-lg frame the padding that makes it a surface", () => {
    // A frame at the outer radius with no padding sits flush against whatever
    // it contains, and two borders at the same offset read as one thick seam
    // rather than as nesting. This is the declaration that looks most
    // droppable and is the one carrying the pattern.
    const offenders: string[] = [];
    for (const { path, selector, body } of framingRules()) {
      const clean = stripComments(body);
      if (!/padding:/.test(clean)) {
        offenders.push(`${path} | ${selector} | frames without padding`);
      }
    }
    expect(
      offenders,
      "a card is padding with an edge; see DESIGN.md §2.7",
    ).toEqual([]);
  });

  it("never nests a radius inside an equal or larger one", () => {
    // Two boxes at one radius, one inside the other, read as a mistake: the
    // corners run parallel at the wrong offset and the inner box looks like it
    // has escaped. One step down per level is the whole grammar, and this
    // caught a real inversion — .mon-card sat at --r-lg inside the --r-lg card
    // that now frames it.
    const LADDER: Record<string, number> = {
      "--r-2xs": 2,
      "--r-xs": 4,
      "--r-sm": 6,
      "--r-md": 8,
      "--r-lg": 12,
    };
    // Selectors known to sit inside a card, with the radius they may not meet
    // or exceed. Listed rather than inferred: a stylesheet does not say what
    // contains what, and guessing from selector names would be a test that
    // passes for the wrong reason.
    const NESTED: [string, string][] = [
      ["web/src/monitors/monitors.css", ".mon-card"],
      ["web/src/components/panellist.css", ".panel-row"],
    ];
    const cardRadius = LADDER["--r-lg"];

    for (const [path, selector] of NESTED) {
      const rule = cssRules().find(
        (r) => r.path === path && r.selector.trim() === selector,
      );
      expect(rule, `${selector} not found in ${path}`).toBeTruthy();
      const token = /border-radius:\s*var\((--r-[a-z0-9]+)\)/.exec(
        stripComments(rule?.body ?? ""),
      )?.[1];
      if (!token) continue;
      expect(
        LADDER[token],
        `${selector} is ${token}; it sits inside a --r-lg card and must be tighter`,
      ).toBeLessThan(cardRadius);
    }
  });

  it("keeps one heading treatment for card titles across every screen", () => {
    // Before the Card component there were three: .mon-cards-title,
    // .mon-line-group-title and .mon-detail-panel-title, each applying
    // caps-legend by hand. They agreed by luck. Any new per-screen title rule
    // is the same divergence starting again.
    //
    // "Card title" means the heading on a card, not every element with
    // "title" in its name. A label *inside* a panel — a column header, a
    // legend, a row of units — is the mono-caps role doing exactly its job,
    // and sweeping those up here would be a guard that punishes correct code
    // until someone silences it.
    const offenders: string[] = [];
    for (const { path, selector, body } of cssRules()) {
      if (path.includes("components/card.css")) continue;
      if (!/-(?:title|heading)\b/.test(selector)) continue;
      const clean = stripComments(body);
      if (!/@apply[^;]*(?:caps-legend|face-sans|face-mono)/.test(clean)) {
        continue;
      }
      offenders.push(`${path} | ${selector}`);
    }

    // Two genuine exceptions, listed rather than pattern-matched so each one
    // has to be argued for in writing:
    //
    // `.mon-section-title` is a `<th scope="colgroup">` inside the monitor
    // table — a column-group header, which is a label within a panel and not
    // the panel's own title. It is the caps role used correctly.
    //
    // `.wall-title` is a full-screen display read from across a room. It has
    // no card to inherit from and is not a document heading.
    const allowed = new Set([
      "web/src/monitors/monitors.css | .mon-section-title",
      "web/src/wall/wall.css | .wall-title",
    ]);
    expect(
      offenders.filter((o) => !allowed.has(o)),
      "card titles come from the Card component; see DESIGN.md §2.7",
    ).toEqual([]);
  });

  it("keeps every exception on that list real", () => {
    // An allow-list entry whose selector no longer exists reads as a justified
    // decision about live code and is not one — the same rot the spacing
    // allow-list guards against.
    const live = new Set(
      cssRules().map(({ path, selector }) => `${path} | ${selector.trim()}`),
    );
    for (const entry of [
      "web/src/monitors/monitors.css | .mon-section-title",
      "web/src/wall/wall.css | .wall-title",
    ]) {
      expect(live.has(entry), `${entry} is allow-listed but gone`).toBe(true);
    }
  });
});
