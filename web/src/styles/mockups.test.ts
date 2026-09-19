// @vitest-environment node
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const mockups = join(root, "docs/mockups");
const tokensPath = join(root, "web/src/styles/tokens.css");
const read = (path: string) => readFileSync(path, "utf8");
const uncomment = (text: string) => text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/<!--[\s\S]*?-->/g, "");
function files(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap(entry =>
    entry.isDirectory() ? files(join(dir, entry.name)) : [join(dir, entry.name)],
  ).filter(path => /\.(html|css|js)$/.test(path)).sort();
}
const sources = files(mockups);
const html = sources.filter(path => path.endsWith(".html"));
const tokenNames = new Set([...uncomment(read(tokensPath)).matchAll(/(--[\w-]+)\s*:/g)].map(m => m[1]));

function styles(path: string) {
  const source = uncomment(read(path));
  if (path.endsWith(".css")) return source;
  return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map(m => m[1]).join("\n") +
    [...source.matchAll(/\bstyle="([^"]*)"/g)].map(m => `.inline { ${m[1]} }`).join("\n");
}
const declarations = (body: string) => [...body.matchAll(/(?:^|;)\s*([\w-]+)\s*:\s*([^;]+)(?=;|$)/g)]
  .map(m => [m[1], m[2].trim()] as const);

describe("LED markup in static pages and script templates", () => {
  it.each(html)("%s puts an accessible word next to every hidden lamp", path => {
    const source = uncomment(read(path));
    const lamps = [...source.matchAll(/<span\b[^>]*class="[^"]*\bled\b[^"]*"[^>]*><\/span>/g)]
      .filter(m => /class="(?:led|[^"]*\sled)(?:\s|")/.test(m[0]));
    expect(lamps.length, "LED scan must exercise markup").toBeGreaterThan(0);
    const bare = lamps.filter(m => {
      const after = source.slice(m.index! + m[0].length);
      return !/aria-hidden="true"/.test(m[0]) ||
        !/^\s*<span\b[^>]*class="(?:sr-only|led-label)"[^>]*>[^<]+<\/span>/.test(after);
    }).map(m => m[0]);
    expect(bare, "Every lamp, including generated HTML, needs an adjacent textual alternative").toEqual([]);
  });
});

// Check authored declarations (including templates), not only the visible first
// screen. Browser tests below complement this with resolved cascade/interaction.
describe("mockup typography and visual ladders", () => {
  it.each(sources)("%s uses paired roles and no private palette or geometry", path => {
    const errors: string[] = [];
    for (const rule of styles(path).matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      const decls = declarations(rule[2]);
      const props = new Map(decls);
      const label = rule[1].trim();
      const size = props.get("font-size");
      if (size) {
        const role = size.match(/^var\(--type-(page|card|row|body|helper|section)\)$/)?.[1];
        const leading = props.get("line-height");
        if (size === "inherit") {
          if (leading !== "inherit") errors.push(`${label}: inherited size needs inherited leading`);
        } else if (!role || (leading !== `var(--lead-${role})` && !(role === "helper" && leading === "var(--lead-prose)"))) {
          errors.push(`${label}: font-size ${size} needs its paired leading (got ${leading})`);
        }
      }
      for (const [prop, value] of decls) {
        if (prop === "line-height" && !/^(inherit|var\(--lead-(page|card|row|body|helper|section|prose)\))$/.test(value)) errors.push(`${label}: literal leading ${value}`);
        if (prop === "font-family" && !label.includes("@font-face") && !/^(inherit|var\(--font-(sans|mono)\))$/.test(value)) errors.push(`${label}: private font family ${value}`);
        if (prop === "font-feature-settings" && !label.includes("@font-face") && !/^var\(--feat-(sans|mono)\)$/.test(value)) errors.push(`${label}: private font features ${value}`);
        if (prop === "font" && value !== "inherit") errors.push(`${label}: font shorthand bypasses roles`);
        if (prop === "font-weight" && !/^var\(--weight-[\w-]+\)$|^inherit$/.test(value) && !label.includes("@font-face")) errors.push(`${label}: literal weight ${value}`);
        if (prop === "letter-spacing" && value !== "var(--track-body)") errors.push(`${label}: literal tracking ${value}`);
        if (/#(?:[\da-f]{3,8})\b|\b(?:rgb|rgba|hsl|hsla|oklch)\(/i.test(value)) errors.push(`${label}: private colour ${value}`);
        if (/^(?:padding|margin|gap|row-gap|column-gap|border-radius)(?:-|$)/.test(prop) && /(?:^|[^\w-])(?:\d*\.)?\d+px\b/.test(value)) {
          const large = [...value.matchAll(/(-?[\d.]+)px/g)].some(m => Math.abs(Number(m[1])) >= 4);
          if (large || prop === "border-radius") errors.push(`${label}: off-ladder ${prop}: ${value}`);
        }
        if (prop === "box-shadow" && value !== "none" && !/var\(--(?:shadow-|glow-|accent-ring|ring-down)/.test(value)) errors.push(`${label}: off-ladder shadow ${value}`);
        for (const match of value.matchAll(/var\((--[\w-]+)/g)) {
          if (!tokenNames.has(match[1])) errors.push(`${label}: undefined token ${match[1]}`);
        }
      }
    }
    if (/\.style\.(?:fontSize|lineHeight|font|letterSpacing)\s*=/.test(uncomment(read(path)))) errors.push("JS type assignment bypasses paired CSS roles; use a class");
    expect(errors, relative(mockups, path)).toEqual([]);
  });
});

describe("disk-openable mockups consume the live design system", () => {
  it("loads the product font files with exactly the product face descriptors", () => {
    const mockupCSS = read(join(mockups, "common.css"));
    const faces = (css: string) => [...uncomment(css).matchAll(/@font-face\s*\{([^}]+)\}/g)]
      .map(m => Object.fromEntries(declarations(m[1]).map(([key, value]) => [key, value.replace(/\s+/g, " ").replace("../../web/public/fonts/", "/fonts/")])));
    expect(faces(mockupCSS)).toEqual(faces(read(join(root, "web/src/styles/fonts.css"))));
  });
  it.each(html)("%s links the real token source, not a palette snapshot", path => {
    const source = uncomment(read(path));
    const links = [...source.matchAll(/<link\b[^>]*href="([^"]+)"[^>]*>/g)]
      .filter(m => /rel="stylesheet"/.test(m[0]))
      .map(m => resolve(dirname(path), m[1]));
    expect(links, relative(mockups, path) + " must link web/src/styles/tokens.css").toContain(tokensPath);
  });

  it("never redefines a live token, even with today's matching value", () => {
    const copies = sources.flatMap(path => [...uncomment(read(path)).matchAll(/(--[\w-]+)\s*:/g)]
      .filter(m => tokenNames.has(m[1]))
      .map(m => `${relative(mockups, path)}: ${m[1]}`));
    expect(copies, "Mockups must consume tokens, not shadow the live source").toEqual([]);
  });
});
