/**
 * Generates docs/styleguide/index.html from the live sources.
 *
 * The page is generated rather than written because a hand-written style
 * guide documents what someone believed on the day they wrote it. This one
 * cannot: every value on the page is parsed out of `web/src/styles/tokens.css`
 * at build time, and the page embeds that same file as its stylesheet. If a
 * token changes, the guide changes. If a token is deleted, its row vanishes.
 *
 * It is checked by `styleguide.test.ts` rather than trusted:
 *   - every token in tokens.css appears on the page (no silent omissions)
 *   - every ladder the guards enforce has a section (no undocumented rule)
 *   - the committed HTML matches a fresh render (no stale artefact)
 *
 * Run: node scripts/build-styleguide.mjs
 */
import { readFileSync, readdirSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { renderToStaticMarkup } from "react-dom/server";

const here = dirname(fileURLToPath(import.meta.url));
const webSrc = join(here, "..", "src");
const repoRoot = join(here, "..", "..");
const outDir = join(repoRoot, "docs", "styleguide");

const tokensCss = readFileSync(join(webSrc, "styles", "tokens.css"), "utf8");

/*
 * The component specimens, rendered by React on the server through Vite's
 * SSR loader so the TSX compiles the way it does for the app. The result is
 * the real markup of the real components; the page then loads the app's
 * built stylesheet beside tokens.css, so what appears is what ships.
 *
 * The built CSS is required, not optional. Rendering markup against tokens
 * alone would show unstyled spans and call them chips.
 */
const distDir = join(here, "..", "..", "internal", "webui", "dist");
const builtCss = (() => {
  // The stylesheet the built index.html actually links, not whichever .css
  // happens to be in assets/ -- a stale build leaves its predecessor beside it.
  let indexHtml;
  try {
    indexHtml = readFileSync(join(distDir, "index.html"), "utf8");
  } catch {
    console.error(
      "build-styleguide: no built frontend at internal/webui/dist. Run `npm run build` first.",
    );
    process.exit(1);
  }
  const link = indexHtml.match(/href="\/?(assets\/[^"]+\.css)"/);
  if (!link) {
    console.error("build-styleguide: built index.html links no stylesheet.");
    process.exit(1);
  }
  return readFileSync(join(distDir, link[1]), "utf8");
})();

const vite = await createServer({
  root: join(here, ".."),
  server: { middlewareMode: true },
  appType: "custom",
  logLevel: "error",
});
let specimens;
try {
  ({ specimens } = await vite.ssrLoadModule("/src/styleguide-specimens.tsx"));
} finally {
  await vite.close();
}

/** Strip comments so prose about a token is never read as a token. */
function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

/**
 * Parse `--name: value;` declarations out of a block.
 *
 * Scoped to a specific selector because tokens.css declares the same names
 * three times: once in `:root`, then again per theme. Reading all of them
 * into one map would silently show the last theme's value as "the" value.
 */
function parseBlock(css, selector) {
  const start = css.indexOf(selector);
  if (start === -1) return new Map();
  let depth = 0;
  let i = css.indexOf("{", start);
  const open = i;
  for (; i < css.length; i += 1) {
    if (css[i] === "{") depth += 1;
    else if (css[i] === "}") {
      depth -= 1;
      if (depth === 0) break;
    }
  }
  const body = css.slice(open + 1, i);
  const out = new Map();
  for (const m of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    out.set(m[1], m[2].trim());
  }
  return out;
}

const clean = stripComments(tokensCss);
const root = parseBlock(clean, ":root");
const dark = parseBlock(clean, '[data-theme="dark"]');
const light = parseBlock(clean, '[data-theme="light"]');

/**
 * The comment immediately above a token, which is where this codebase keeps
 * the reasoning. Carrying it onto the page is the whole point: a hex value
 * tells you what, and only the prose tells you when to reach for it.
 */
function rationaleFor(name) {
  /*
   * The comment that explains a token, which in this codebase sits either
   * immediately above the declaration or above the run it belongs to.
   *
   * Two earlier versions were wrong in opposite directions, and both were
   * only visible by rendering the page. Anchoring on the token name alone
   * took whatever comment appeared anywhere before it, so the first token in
   * the file printed the entire 40-line file header as its rationale.
   * Restricting to "immediately above" then blanked 64 of 93 rows, because
   * runs like `--type-row / --type-body / --type-helper` are deliberately
   * documented once for the group rather than line by line.
   *
   * So: prefer the token's own comment; fall back to the comment that opens
   * its run. A run ends at a blank line, which is how this file separates
   * groups.
   */
  /*
   * All declarations of this name, not just the first.
   *
   * A colour is declared three times -- once per theme, sometimes once in
   * :root -- and the reasoning is attached to whichever one the author was
   * writing when they explained it. Taking only the first occurrence left 84
   * of 138 rows blank while the explanation sat forty lines below.
   */
  const declaration = new RegExp(`(^|\\n)\\s*${name}\\s*:`, "gm");
  const positions = [...tokensCss.matchAll(declaration)].map((m) => m.index);
  if (positions.length === 0) return "";
  for (const position of positions) {
    const text = rationaleAt(position);
    if (text) return text;
  }
  return "";
}

/**
 * Where the previous declaration ended, ignoring punctuation inside comments.
 *
 * `lastIndexOf(";")` on raw text finds a semicolon inside the prose ("at a
 * glance; which is...") and cuts the comment in half, so the fragment above
 * a declaration held no opening marker and read as having no comment at all.
 * That single character blanked 87 of 138 rows. Boundaries are found on a
 * copy with comments blanked to spaces, so offsets still line up.
 */
// Comments become runs of "x" (newlines kept) rather than spaces: a line that
// held only a comment must still read as occupied, or the blank-line test
// sees a group boundary at every heading.
const blanked = tokensCss.replace(/\/\*[\s\S]*?\*\//g, (m) =>
  m.replace(/[^\n]/g, "x"),
);
function boundaryBefore(offset) {
  const head = blanked.slice(0, offset);
  return Math.max(
    head.lastIndexOf(";"),
    head.lastIndexOf("{"),
    head.lastIndexOf("}"),
  );
}

/** The comment attached to the declaration at this offset, if any. */
function rationaleAt(at) {
  /*
   * A trailing comment first: a comment on the same line as the value
   * explains the declaration it sits behind. The first version only looked
   * *before* a declaration, so every trailing comment was credited to the
   * next row and the whole size table read one rung out of step -- 14px was
   * labelled "tooltip arrow", 30px "the LED". Wrong reasons beside right
   * values is worse than no reasons at all.
   */
  const lineEnd = tokensCss.indexOf("\n", at + 1);
  const restOfLine = tokensCss.slice(at, lineEnd === -1 ? undefined : lineEnd);
  const trailing = restOfLine.match(/;\s*\/\*([\s\S]*?)\*\//);
  if (trailing) return collect(`/*${trailing[1]}*/`);

  const prevEnd = boundaryBefore(at);
  // The previous declaration's own trailing comment is not ours.
  const own = collect(stripTrailing(tokensCss.slice(prevEnd + 1, at)));
  if (own) return own;
  // Nothing of our own, and a blank line above: we open a group without a
  // heading, and must not borrow the previous group's.
  if (/\n[ \t]*\n/.test(blanked.slice(prevEnd + 1, at))) return "";

  /*
   * Walk back over the run to the comment that opens it.
   *
   * A run is a set of declarations with no blank line between them. The
   * comment at the top of the run is the group's heading and applies to
   * every member that has no comment of its own. A comment *inside* the run
   * -- one that sits directly above some earlier member -- is that member's
   * and nobody else's: `--type-card`'s "16px, not 18" must not become
   * `--type-body`'s reason just because body follows card. So a comment is
   * only inherited if it is the first thing in the run.
   */
  let cursor = prevEnd;
  for (let hops = 0; hops < 24 && cursor > 0; hops += 1) {
    const stop = boundaryBefore(cursor);
    if (stop === -1) break;
    const between = blanked.slice(stop + 1, cursor);
    const gap = stripTrailing(tokensCss.slice(stop + 1, cursor));
    const text = collect(gap);
    if (/\n[ \t]*\n/.test(between)) {
      // The run starts here. Whatever comment sits in this gap is its
      // heading, provided it sits *below* the blank line.
      const afterBlank = gap.slice(gap.search(/\n[ \t]*\n/) + 1);
      return collect(stripTrailing(afterBlank));
    }
    if (text) return ""; // an earlier member's own comment: not a heading
    cursor = stop;
  }
  return "";
}

/**
 * Drop a comment that sits on the same line as the `;` this gap starts
 * after: that comment belongs to the declaration before the gap.
 */
function stripTrailing(gap) {
  return gap.replace(/^[ \t]+\/\*[^\n]*?\*\/[ \t]*(?=\n)/, "");
}

/** Flatten any comment blocks in a fragment into one line of prose. */
function collect(fragment) {
  const comments = [...fragment.matchAll(/\/\*([\s\S]*?)\*\//g)].map(
    (m) => m[1],
  );
  if (comments.length === 0) return "";
  return comments
    .join(" ")
    .split("\n")
    .map((line) => line.replace(/^\s*\*?\s?/, "").trimEnd())
    .join(" ")
    .replace(/\s+/g, " ")
    .replace(/^-{3,}\s*§?[\d.]*\s*/, "")
    .trim();
}

/*
 * The first two sentences, with the rest behind a disclosure.
 *
 * Some rationales run past a thousand characters, because the reasoning in
 * tokens.css is genuinely that long and cutting it in the source would be
 * the wrong repair -- that prose is why the file is trustworthy. But a
 * 1300-character cell renders as a 1300-pixel-tall row and the table stops
 * being scannable, so the page shows the claim and keeps the argument one
 * click away.
 */
const SUMMARY_LIMIT = 220;

function summarise(text) {
  if (text.length <= SUMMARY_LIMIT) return { head: text, rest: "" };
  // Prefer a sentence boundary; fall back to a word boundary.
  const window = text.slice(0, SUMMARY_LIMIT);
  const sentence = Math.max(
    window.lastIndexOf(". "),
    window.lastIndexOf("? "),
    window.lastIndexOf("! "),
  );
  const cut = sentence > 80 ? sentence + 1 : window.lastIndexOf(" ");
  return { head: text.slice(0, cut).trim(), rest: text.slice(cut).trim() };
}

function why(text) {
  const { head, rest } = summarise(text);
  if (!rest) return esc(head);
  return (
    esc(head) +
    ' <details class="sg-more"><summary>more</summary>' +
    esc(rest) +
    "</details>"
  );
}

function esc(s) {
  return String(s).replace(
    /[&<>"]/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c],
  );
}

/** Tokens whose name starts with any of the given prefixes. */
function group(map, ...prefixes) {
  return [...map.entries()].filter(([name]) =>
    prefixes.some((p) => name.startsWith(p)),
  );
}

function swatchRow([name, value], themed) {
  const rationale = rationaleFor(name);
  const darkValue = themed ? dark.get(name) : undefined;
  const lightValue = themed ? light.get(name) : undefined;
  return `
      <tr>
        <td class="sg-cell-swatch">
          <span class="sg-checker"
            ><span class="sg-swatch" style="background: ${esc(darkValue ?? value)}"></span
            ><span class="sg-swatch sg-swatch-light" style="background: ${esc(
              lightValue ?? value,
            )}"></span
          ></span>
        </td>
        <td><code>${esc(name)}</code></td>
        <td class="sg-value"><code>${esc(darkValue ?? value)}</code>${
          lightValue
            ? `<br><code class="sg-light">${esc(lightValue)}</code>`
            : ""
        }</td>
        <td class="sg-why">${why(rationale)}</td>
      </tr>`;
}

function plainRow([name, value]) {
  return `
      <tr>
        <td><code>${esc(name)}</code></td>
        <td class="sg-value"><code>${esc(value)}</code></td>
        <td class="sg-why">${why(rationaleFor(name))}</td>
      </tr>`;
}

function sizeRow([name, value]) {
  return `
      <tr>
        <td class="sg-cell-swatch">
          <span class="sg-bar" style="width: var(${name})"${
            Number.parseFloat(value) > 116 ? " data-overflows" : ""
          }></span>
        </td>
        <td><code>${esc(name)}</code></td>
        <td class="sg-value"><code>${esc(value)}</code></td>
        <td class="sg-why">${why(rationaleFor(name))}</td>
      </tr>`;
}

function typeRow(name) {
  const size = root.get(name);
  const leadName = name.replace("--type-", "--lead-");
  const lead = root.get(leadName);
  return `
      <tr>
        <td class="sg-cell-specimen">
          <span style="font-size: var(${name}); line-height: var(${leadName})">Ag</span>
        </td>
        <td><code>${esc(name)}</code></td>
        <td class="sg-value"><code>${esc(size)}</code>${
          lead ? ` / <code>${esc(lead)}</code>` : ""
        }</td>
        <td class="sg-why">${why(rationaleFor(name))}</td>
      </tr>`;
}

const colourTokens = group(dark, "--surface", "--ink", "--border", "--accent");
const accentTokens = [
  ...group(root, "--accent", "--on-", "--scrim", "--ring-"),
  ...group(dark, "--accent", "--on-", "--scrim", "--ring-"),
].filter(
  // A name declared in :root and restated per theme must appear once.
  ([name], i, all) => all.findIndex(([n]) => n === name) === i,
);
const fontTokens = group(
  root,
  "--font-",
  "--weight-",
  "--track",
  "--tracking-",
  "--feat-",
  "--ligatures-",
  "--numeric-",
  "--render-",
  "--leading-",
);
const effectTokens = group(root, "--blur-", "--outline-");
const statusTokens = group(dark, "--up", "--down", "--warn", "--idle", "--zero");
const textTokens = [...root.keys()].filter((n) => n.startsWith("--type-"));
const leadTokens = group(root, "--lead-");
const spaceTokens = group(root, "--space-");
const sizeTokens = group(root, "--size-");
const radiusTokens = group(root, "--r-");
const depthTokens = [
  ...group(root, "--z-", "--shadow", "--glow"),
  ...group(dark, "--z-", "--shadow", "--glow"),
].filter(([name], i, all) => all.findIndex(([n]) => n === name) === i);
const motionTokens = group(root, "--dur-", "--ease");
const bpTokens = group(root, "--bp-");
const controlTokens = group(root, "--control-");

function specimenBlock(spec) {
  return `
    <article class="sg-specimen" id="c-${esc(spec.id)}">
      <h3>${esc(spec.title)}</h3>
      <p class="sg-why">${esc(spec.rule)} <code>${esc(spec.source)}</code></p>
      <div class="sg-stage">${renderToStaticMarkup(spec.node)}</div>
    </article>`;
}

const sections = [
  {
    id: "components",
    title: "Components",
    lead: `The real components, rendered by React from
      <code>web/src/styleguide-specimens.tsx</code> against the built
      stylesheet. Not drawings of them: the markup and class names here are the
      ones the product ships, so a chip that looks wrong on this page looks
      wrong in the product. Each carries the rule it exists to keep.`,
    html: specimens.map(specimenBlock).join(""),
  },
  {
    id: "colour",
    title: "Colour",
    lead: `Every colour in the product resolves to one of these. The guard in
      <code>tokens.test.ts</code> finds zero hex, rgb() or hsl() literals
      anywhere else under <code>web/src</code> — that is measured, not aspired
      to. Two values per row: dark first, light beneath it.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Dark / light</th><th>What it is for</th></tr></thead>
      <tbody>${colourTokens.map((e) => swatchRow(e, true)).join("")}</tbody>
    </table>`,
  },
  {
    id: "status",
    title: "Status colour",
    lead: `Status colour marks data and nothing else; the accent fills controls
      and nothing else. A dashboard that says "blue" in two senses says nothing.
      Colour never carries a state alone — every status also has a word.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Dark / light</th><th>What it is for</th></tr></thead>
      <tbody>${statusTokens.map((e) => swatchRow(e, true)).join("")}</tbody>
    </table>`,
  },
  {
    id: "type",
    title: "Type",
    lead: `Every size is paired with a leading, and the guard asserts the
      pairing in both directions: a role without a leading fails, and a leading
      without a role fails. Specimens render at the real value.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Specimen</th><th>Token</th><th>Size / leading</th><th>What it is for</th></tr></thead>
      <tbody>${textTokens.map(typeRow).join("")}</tbody>
    </table>
    <p class="sg-lead">Leadings, including the ones no size claims directly.</p>
    <table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${leadTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "space",
    title: "Space",
    lead: `Steps of 4px, with measured half-steps where the grid is too coarse
      for chrome. A literal at or above 4px is drift and fails the build unless
      it is allow-listed with a reason.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${spaceTokens.map(sizeRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "size",
    title: "Size",
    lead: `The newest ladder, and the one that proves the point. Before it
      existed there were 88 loose pixel values across 26 stylesheets, against
      zero loose colours — same authors, same care, different guards. One
      control even introduced a new token two pixels away from three files
      already drawing that square.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${sizeTokens.map(sizeRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "control",
    title: "Controls",
    lead: `The height a row of interactive chrome settles on, and the square a
      compact icon control occupies.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${controlTokens.map(sizeRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "radius",
    title: "Radius",
    lead: `Radii are concentric: an inner corner is the outer radius minus the
      inset between them, so a nested box never looks pinched. The guard checks
      the arithmetic, which is why the inset has to be a token too.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${radiusTokens
        .map(
          ([name, value]) => `
      <tr>
        <td class="sg-cell-swatch">
          <span class="sg-radius" style="border-radius: var(${name})"></span>
        </td>
        <td><code>${esc(name)}</code></td>
        <td class="sg-value"><code>${esc(value)}</code></td>
        <td class="sg-why">${why(rationaleFor(name))}</td>
      </tr>`,
        )
        .join("")}</tbody>
    </table>`,
  },
  {
    id: "depth",
    title: "Depth",
    lead: `Four named rungs, spaced by ten so a scrim can take rung-minus-one.
      Every bare z-index in the product was replaced by one of these after a
      tooltip at z-index 10 was clipped by a topbar at 20.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${depthTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "motion",
    title: "Motion",
    lead: `Durations and easing. Everything that moves is suppressed under
      <code>prefers-reduced-motion</code>; where motion carried meaning, a
      second non-moving signal carries it too.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${motionTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "accent",
    title: "Accent and overlay",
    lead: `The accent fills controls; it never marks data. Its ring, its ink
      and the scrim that dims the page behind an overlay belong to the same
      family and are stated together so a new control cannot invent its own.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-sample"><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th></th><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${accentTokens.map((e) => swatchRow(e, false)).join("")}</tbody>
    </table>`,
  },
  {
    id: "font",
    title: "Faces and weights",
    lead: `A face is applied as a role, never as a family name -- the guard
      rejects a raw font stack in a stylesheet. Weights, tracking and the
      OpenType features that make figures line up in a table live here too,
      because a column of numbers that is not tabular reads as jitter.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${fontTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "effect",
    title: "Blur and outline",
    lead: `The blur behind a sticky bar and behind a scrim, and the keyboard
      outline. The outline is a different mechanism from the focus ring and
      deliberately thinner: it draws outside the box where the ring draws
      inside it.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${effectTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
  {
    id: "breakpoints",
    title: "Breakpoints",
    lead: `Documented here, enforced as literals. A custom property inside a
      media query never resolves — verified in Chromium, not assumed — and it
      fails silently, so the block is simply dropped and the layout is quietly
      wrong at one width. The guard asserts every <code>@media</code> width in
      the product is one of these, and that none of them is written as
      <code>var()</code>.`,
    html: `<table class="sg-table">
      <colgroup><col class="sg-col-token"><col class="sg-col-value"><col></colgroup>
      <thead><tr><th>Token</th><th>Value</th><th>What it is for</th></tr></thead>
      <tbody>${bpTokens.map(plainRow).join("")}</tbody>
    </table>`,
  },
];

const html = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>SubGlance — living style guide</title>
<!--
  GENERATED FILE — do not edit by hand.
  Source: web/scripts/build-styleguide.mjs, from web/src/styles/tokens.css.
  Regenerate: npm run styleguide   (web/)
  A test asserts this file matches a fresh render, so an edit here fails CI.
-->
<link rel="stylesheet" href="./tokens.css">
<link rel="stylesheet" href="./app.css">
<style>
  body {
    margin: 0;
    padding: var(--space-10) var(--space-8) var(--space-16);
    background: var(--canvas);
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: var(--type-body);
    line-height: var(--lead-body);
    letter-spacing: var(--track-body);
  }
  .sg-wrap { max-width: var(--size-pane-lg); margin: 0 auto; }
  h1 { font-size: var(--type-page); line-height: var(--lead-page); margin: 0 0 var(--space-2); }
  h2 { font-size: var(--type-card); line-height: var(--lead-card); margin: var(--space-12) 0 var(--space-2); }
  .sg-lead { color: var(--ink-2); margin: 0 0 var(--space-5); max-width: 60ch; }
  .sg-note {
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    background: var(--surface);
    padding: var(--space-5);
    margin: var(--space-6) 0;
  }
  .sg-toc { display: flex; flex-wrap: wrap; gap: var(--space-2); padding: 0; margin: var(--space-6) 0 0; list-style: none; }
  .sg-toc a {
    display: inline-block;
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--border-control);
    border-radius: var(--r-pill);
    color: var(--ink-2);
    text-decoration: none;
    font-size: var(--type-helper);
    line-height: var(--lead-helper);
  }
  .sg-toc a:hover { color: var(--ink); border-color: var(--accent-border); }
  /*
   * Fixed layout with stated column widths. Without it the browser sizes
   * columns by content, and one 1300-character rationale collapsed the prose
   * column to a single word per line while the swatch column claimed most of
   * the page. A fixed table layout makes the widths a decision instead of an
   * emergent property of the longest cell.
   */
  .sg-table { width: 100%; border-collapse: collapse; margin: 0; table-layout: fixed; }
  .sg-col-sample { width: var(--size-col-lg); }
  .sg-col-token { width: 18%; }
  .sg-col-value { width: 22%; }
  /* A specimen: the rule above, the component below, on the page canvas so a
     card composites against what it composites against in the product. */
  .sg-specimen { margin: 0 0 var(--space-8); }
  .sg-specimen h3 { font-size: var(--type-row); line-height: var(--lead-row); margin: 0 0 var(--space-1); }
  .sg-specimen .sg-why { max-width: 70ch; margin: 0 0 var(--space-3); }
  .sg-stage { padding: var(--space-5); border: 1px dashed var(--border); border-radius: var(--r-lg); }
  .sg-specimen-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-4); }
  .sg-table th {
    text-align: left;
    font: inherit;
    font-size: var(--type-section);
    line-height: var(--lead-section);
    letter-spacing: var(--track-caps);
    text-transform: uppercase;
    color: var(--ink-3);
    padding: 0 var(--space-3) var(--space-2) 0;
    border-bottom: 1px solid var(--border);
  }
  .sg-table td {
    padding: var(--space-3) var(--space-3) var(--space-3) 0;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  .sg-table code {
    font-family: var(--font-mono);
    font-size: var(--type-helper);
    line-height: var(--lead-helper);
    color: var(--ink);
  }
  .sg-value code { color: var(--ink-2); }
  .sg-table code { overflow-wrap: anywhere; }
  .sg-light { opacity: .7; }
  .sg-why { color: var(--ink-2); font-size: var(--type-helper); line-height: var(--lead-helper); }
  .sg-more { display: inline; }
  .sg-more summary { display: inline; cursor: pointer; color: var(--accent-border); }
  .sg-more[open] summary { display: none; }
  .sg-cell-swatch, .sg-cell-specimen { width: var(--size-col-lg); }
  /*
   * The checkerboard is not decoration. Several surface tokens are
   * deliberately translucent (--surface is rgba(255,255,255,.03)), and on a
   * flat dark cell a 3% white is indistinguishable from the cell itself — the
   * swatch renders as an empty box and the page lies by omission. Over a
   * checker the alpha is the thing you actually see.
   */
  /*
   * Both themes on one chip. The row states a dark and a light value, and a
   * single swatch could only ever show one of them -- which quietly made the
   * light column unverifiable on the page that exists to verify it.
   */
  .sg-swatch {
    display: block;
    width: var(--size-col-sm);
    height: var(--control-h);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
  }
  .sg-cell-swatch { position: relative; }
  .sg-checker .sg-swatch { flex: 1 1 50%; width: auto; min-width: 0; border-radius: 0; }
  .sg-checker .sg-swatch:first-child { border-radius: var(--r-md) 0 0 var(--r-md); border-right: 0; }
  .sg-checker .sg-swatch-light { border-radius: 0 var(--r-md) var(--r-md) 0; }
  .sg-checker {
    display: flex;
    width: var(--size-col-md);
    border-radius: var(--r-md);
    background-image:
      linear-gradient(45deg, #808080 25%, transparent 25%),
      linear-gradient(315deg, #808080 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, #808080 75%),
      linear-gradient(315deg, transparent 75%, #808080 75%);
    background-size: var(--space-3) var(--space-3);
    background-position: 0 0, 0 6px, 6px -6px, -6px 0;
    background-color: #4a4a4a;
  }
  /*
   * The bar is drawn at the token's real width, and the cell is 116px wide,
   * so a 860px rung used to paint straight across the token name, the value
   * and the reason. Capped at the cell; a rung wider than the cell shows a
   * fade at its right edge so "cut off" reads as "longer than this" rather
   * than as a bar that happens to be exactly cell-width.
   */
  .sg-bar { display: block; height: var(--space-3); max-width: 100%; background: var(--accent); border-radius: var(--r-sm); }
  .sg-bar[data-overflows] { border-radius: var(--r-sm) 0 0 var(--r-sm); mask-image: linear-gradient(to right, #000 60%, transparent); }
  .sg-radius { display: block; width: var(--size-col-md); height: var(--control-h); background: var(--surface-2); border: 1px solid var(--border-control); }
</style>
</head>
<body>
<div class="sg-wrap">

<h1>SubGlance — living style guide</h1>
<p class="sg-lead">
  Every value on this page is read out of <code>web/src/styles/tokens.css</code>
  when the page is generated, and this page loads that same file as its
  stylesheet. There is no second copy to drift. A token that changes changes
  here; a token that is deleted disappears from the table.
</p>

<div class="sg-note">
  <strong>This page is not the guarantee.</strong> Documentation that is only
  read when someone remembers to read it is how a design system drifts in the
  first place — this repository already had 1797 lines of design documentation
  while 88 loose pixel values accumulated across 26 stylesheets. The guarantee
  is <code>web/src/styles/tokens.test.ts</code>, which fails the build on a
  literal colour, size, type role, leading, tracking, spacing, radius, border,
  depth value or breakpoint. This page exists so a human can see what the
  guards enforce, and so a reviewer can tell whether a new rung deserves to
  exist. When the two disagree, the test wins and the page is the thing that
  is broken.
</div>

<ul class="sg-toc">
${sections.map((s) => `  <li><a href="#${s.id}">${esc(s.title)}</a></li>`).join("\n")}
</ul>

${sections
  .map(
    (s) => `<section id="${s.id}">
  <h2>${esc(s.title)}</h2>
  <p class="sg-lead">${s.lead}</p>
  ${s.html}
</section>`,
  )
  .join("\n\n")}

</div>
</body>
</html>
`;

mkdirSync(outDir, { recursive: true });
writeFileSync(join(outDir, "index.html"), html);
// The page loads the real token file, so it has to sit beside it.
writeFileSync(join(outDir, "tokens.css"), tokensCss);
// The built stylesheet too, so the component specimens are styled.
writeFileSync(join(outDir, "app.css"), builtCss);

const counts = {
  colour: colourTokens.length,
  status: statusTokens.length,
  type: textTokens.length,
  space: spaceTokens.length,
  size: sizeTokens.length,
  radius: radiusTokens.length,
  depth: depthTokens.length,
  motion: motionTokens.length,
  breakpoints: bpTokens.length,
};
console.log(`docs/styleguide/index.html written`);
console.log(JSON.stringify(counts, null, 2));
