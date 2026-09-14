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
      expect(value, `${name} must be a whole number of px`).toMatch(
        /^\d+px$/,
      );
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
      expect(px, `${name} is ${value}, which is not a whole-pixel length`)
        .not.toBeNull();
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
        .filter((b) => /font-size:/.test(b.body) && !/line-height:/.test(b.body))
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
    expect(rows.length, "expected four weights and three tracking roles").toBe(
      7,
    );
    for (const [, name, value] of rows) {
      expect(root.get(name), name).toBe(value);
    }
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
const DATA_SELECTOR = /(led|heartbeat|hb-|bar|spark|chart|status|wall-card|tile)/i;

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
      const parts = item.trim().split(/[\s>+~]+/).filter(Boolean);
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
  "web/src/live/connection.css | .conn-badge | border: 1px solid var(--warn)",
  "web/src/live/connection.css | .conn-badge-retry | border: 1px solid var(--warn)",
  "web/src/monitors/monitors.css | .mon-row[data-status=\"down\"] | border-left: 2px solid var(--down)",
  "web/src/monitors/monitors.css | .mon-row[data-status=\"pending\"] | border-left: 2px solid var(--warn)",
  "web/src/monitors/monitors.css | .mon-row[data-status=\"paused\"] | border-left: 2px dotted var(--ink-3)",
  "web/src/monitors/monitors.css | .mon-row[data-status=\"waiting\"] | border-left: 2px solid var(--idle)",
  "web/src/monitors/monitors.css | .mon-card[data-status=\"down\"] | border-left: 2px solid var(--down)",
  "web/src/monitors/monitors.css | .mon-card[data-status=\"pending\"] | border-left: 2px solid var(--warn)",
  "web/src/monitors/monitors.css | .mon-card[data-status=\"paused\"] | border-left: 2px dotted var(--ink-3)",
  "web/src/monitors/monitors.css | .mon-card[data-status=\"waiting\"] | border-left: 2px solid var(--idle)",
  "web/src/monitors/monitors.css | .mon-line[data-status=\"down\"] | border-left: 2px solid var(--down)",
  "web/src/monitors/monitors.css | .mon-line[data-status=\"pending\"] | border-left: 2px solid var(--warn)",
  "web/src/monitors/monitors.css | .mon-line[data-status=\"paused\"] | border-left: 2px dotted var(--ink-3)",
  "web/src/monitors/monitors.css | .mon-line[data-status=\"waiting\"] | border-left: 2px solid var(--idle)",
  "web/src/monitors/monitors.css | .push-reveal-warn | border-left: 2px solid var(--warn)",
  "web/src/monitors/monitors.css | .add-input[aria-invalid=\"true\"] | border-color: var(--down)",
  "web/src/monitors/monitors.css | .add-input[aria-invalid=\"true\"]:focus | border-color: var(--down)",
  "web/src/auth/auth.css | .auth-input[aria-invalid=\"true\"] | border-color: var(--down)",
  "web/src/auth/auth.css | .auth-input[aria-invalid=\"true\"]:focus | border-color: var(--down)",
  "web/src/wall/wall.css | .wall-card[data-status=\"down\"] | border-color: color-mix(in srgb, var(--down) 40%, var(--border))",
  "web/src/wall/wall.css | .wall-card[data-status=\"pending\"] | border-color: color-mix(in srgb, var(--warn) 34%, var(--border))",
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
        if (/:(hover|focus|active|disabled|checked)|\[aria-invalid/.test(block.selector)) continue;
        if (/border(?:-[a-z]+)?(?:-color)?\s*:[^;]*var\(--border\)/.test(block.body)) {
          offenders.push(`${relative(repoRoot, file)}: ${block.selector}`);
        }
      }
    }
    expect(offenders).toEqual([]);
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
            /border(?:-[a-z]+)?(?:-color)?\s*:[^;]*var\(--border\)/.test(b.body),
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
    expect(body, "tracking").toContain("letter-spacing: var(--track-caps);");
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
      const contents = stripComments(readFileSync(file, "utf8"));
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
    expect(offenders("/* tabular-nums belongs to the face. */\n.a { color: red; }"))
      .toEqual([]);
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
const RADIUS_LADDER = new Map([
  ["--r-2xs", 2],
  ["--r-xs", 4],
  ["--r-sm", 6],
  ["--r-md", 10],
  ["--r-lg", 12],
]);

const SPACE_LADDER = new Map(
  [1, 2, 3, 4, 5, 6, 8, 10, 12, 16].map((step) => [
    `--space-${step}`,
    step * 4,
  ]),
);

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

  const token = sides[0].match(/^var\((--space-\d+)\)$/)?.[1];
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
  const match = new RegExp(
    `(?:^|[;{\\s])${property}\\s*:\\s*([^;{}]+)`,
  ).exec(stripComments(body));
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
    const offenders: string[] = [];
    for (const { path, selector, body } of cssRules()) {
      const background = valueOf(body, "background");
      const fill = background?.match(/^var\(--(up|warn|down|idle)\)$/);
      if (!fill) continue;
      if (!carriesText(body)) continue;
      const colour = valueOf(body, "color");
      if (colour === `var(--on-${fill[1]})`) continue;
      offenders.push(`${path} | ${selector} | ${colour ?? "no colour"}`);
    }
    expect(offenders).toEqual([]);
  });

  it("keeps the label tokens off anything that is not a solid status fill", () => {
    // The mirror failure: `--on-down` used as ordinary text colour because it
    // happened to look right in one theme. It is only measured against its
    // own fill, so anywhere else it is an unmeasured colour.
    const offenders: string[] = [];
    for (const { path, selector, body } of cssRules()) {
      const used = STATUS_LABEL_TOKENS.filter((name) =>
        new RegExp(`var\\(${name}\\)`).test(stripComments(body)),
      );
      if (used.length === 0) continue;
      const background = valueOf(body, "background");
      const fill = background?.match(/^var\(--(up|warn|down|idle)\)$/);
      if (fill && used.length === 1 && used[0] === `--on-${fill[1]}`) continue;
      offenders.push(`${path} | ${selector} | ${used.join(" ")}`);
    }
    expect(offenders).toEqual([]);
  });

  it("bites on a status fill wearing ink, and on a label token off its fill", () => {
    const wrongLabel = declarationBlocks(`
      .chip--bad { background: var(--down); color: var(--ink); }
    `)[0];
    expect(valueOf(wrongLabel.body, "color")).toBe("var(--ink)");
    expect(valueOf(wrongLabel.body, "background")).toBe("var(--down)");

    const strayToken = declarationBlocks(`
      .note { color: var(--on-down); }
    `)[0];
    expect(valueOf(strayToken.body, "background")).toBeUndefined();
    expect(/var\(--on-down\)/.test(strayToken.body)).toBe(true);
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
      if (!/border(?:-[a-z]+)?(?:-style)?:[^;]*\bdashed\b/.test(clean)) continue;
      const background = valueOf(body, "background");
      if (!background || background === "none" || background === "transparent") {
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
      if (!/border(?:-[a-z]+)?(?:-style)?:[^;]*\bdashed\b/.test(stripComments(body))) {
        continue;
      }
      dashed.add(selector);
    }
    expect([...dashed]).toContain(".chip--state");
    expect([...dashed]).toContain(".chip-avatar");
  });
});
