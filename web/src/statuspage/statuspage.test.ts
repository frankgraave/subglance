import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * The public status page's markup against the one stylesheet it ships with.
 *
 * The page is rendered by Go (internal/statuspage/page.gohtml) and styled by
 * a second CSS entrypoint (page.css), which imports only what the page draws.
 * A class the template wears that the product defines somewhere else, but
 * that page.css does not import, renders unstyled and fails nowhere: that is
 * how the summary's screen-reader word and each service's 90-day sentence
 * came to be printed as visible text, because `.sr-only` lives in
 * controls.css. These tests make that a build failure.
 */

const webSrc = join(fileURLToPath(new URL(".", import.meta.url)), "..");
const repoRoot = join(webSrc, "..", "..");
const template = readFileSync(join(repoRoot, "internal", "statuspage", "page.gohtml"), "utf8");

const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");

/** Every class a stylesheet's selectors name. */
function classesDefinedIn(css: string): Set<string> {
  const out = new Set<string>();
  for (const [, chunk] of stripComments(css).matchAll(/([^{}]+)\{/g)) {
    const selector = chunk.slice(chunk.lastIndexOf(";") + 1);
    if (/^\s*@/.test(selector)) continue;
    for (const [, name] of selector.matchAll(/\.([a-zA-Z_][\w-]*)/g)) out.add(name);
  }
  return out;
}

/** The stylesheets a CSS entrypoint pulls in, itself included, by relative @import. */
function importGraph(entry: string): string[] {
  const seen: string[] = [];
  const walk = (file: string) => {
    if (seen.includes(file)) return;
    seen.push(file);
    for (const [, target] of readFileSync(file, "utf8").matchAll(/@import\s+"([^"]+)"/g)) {
      if (target.startsWith(".")) walk(join(dirname(file), target));
    }
  };
  walk(entry);
  return seen;
}

function cssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return cssFiles(path);
    return name.endsWith(".css") ? [path] : [];
  });
}

/** The classes in the template's class attributes, template actions removed. */
function templateClasses(): string[] {
  const out = new Set<string>();
  for (const [, value] of template.matchAll(/class="([^"]*)"/g)) {
    for (const name of value.replace(/\{\{[\s\S]*?\}\}/g, " ").split(/\s+/)) if (name) out.add(name);
  }
  return [...out];
}

/** The declarations of the first rule whose selector is exactly `selector`. */
function ruleBody(css: string, selector: string): string {
  for (const [, chunk, body] of stripComments(css).matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (chunk.slice(chunk.lastIndexOf(";") + 1).trim() === selector) {
      return body.split(";").map((d) => d.trim()).filter(Boolean).join("; ");
    }
  }
  return "";
}

describe("the status page's markup and its stylesheet", () => {
  const graph = importGraph(join(webSrc, "statuspage", "page.css"));
  const shipped = new Set(graph.flatMap((file) => [...classesDefinedIn(readFileSync(file, "utf8"))]));

  it("reads the template's classes", () => {
    // A reader that stopped finding classes would pass the next test vacuously.
    expect(templateClasses()).toEqual(expect.arrayContaining(["sp", "sp-summary", "led", "sr-only", "sp-uptime"]));
  });

  it("ships a rule for every class the template wears that the product styles", () => {
    const elsewhere = new Map<string, string[]>();
    for (const file of cssFiles(webSrc)) {
      if (graph.includes(file)) continue;
      for (const name of classesDefinedIn(readFileSync(file, "utf8"))) {
        elsewhere.set(name, [...(elsewhere.get(name) ?? []), relative(webSrc, file)]);
      }
    }
    // A class no stylesheet defines (`sp-history`) is a hook for a test.
    const missing = templateClasses()
      .filter((name) => !shipped.has(name) && elsewhere.has(name))
      .map((name) => `.${name} is styled only in ${elsewhere.get(name)!.join(", ")}`);
    expect(missing).toEqual([]);
  });

  it("hides screen-reader text exactly as the product does", () => {
    const own = ruleBody(readFileSync(join(webSrc, "statuspage", "statuspage.css"), "utf8"), ".sr-only");
    const product = ruleBody(readFileSync(join(webSrc, "components", "controls.css"), "utf8"), ".sr-only");
    expect(product).toContain("clip-path: inset(50%)");
    expect(own).toBe(product);
  });
});
