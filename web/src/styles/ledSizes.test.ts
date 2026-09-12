import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * SUB-71: the lamp has two sizes, and both have to be a rule.
 *
 * DESIGN.md §3 sizes the *scanned* lamp at 20x7 and §3.2 documents the one
 * exception — a lamp inside a text badge, drawn at text scale. The mock-up used
 * to express that exception as `style="width:12px;height:5px"` repeated on every
 * badge, which made it look like the mock-up was violating its own design
 * document, and made a size that carries meaning unchangeable in one place.
 *
 * These assertions read the files off disk rather than a rendered DOM: jsdom
 * applies no CSS, so a rendered check would pass just as happily against a
 * stylesheet that had been deleted.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const repoRoot = join(here, "..", "..", "..");
const mockups = join(repoRoot, "docs", "mockups");

const designMd = readFileSync(join(repoRoot, "docs", "DESIGN.md"), "utf8");
const ledCss = readFileSync(join(here, "..", "monitors", "led.css"), "utf8");
const componentsHtml = readFileSync(join(mockups, "components.html"), "utf8");

/** The body of the first rule whose selector matches exactly. */
function rule(sheet: string, selector: string): string {
  const at = sheet.indexOf(`${selector} {`);
  expect(at, `missing rule for ${selector}`).toBeGreaterThan(-1);
  return sheet.slice(at, sheet.indexOf("}", at));
}

/** `width`/`height` pixel values declared in a rule body, as `WxH`. */
function boxOf(body: string): string {
  const width = /width\s*:\s*(\d+(?:\.\d+)?)px/.exec(body);
  const height = /height\s*:\s*(\d+(?:\.\d+)?)px/.exec(body);
  expect(width, "no width declared").not.toBeNull();
  expect(height, "no height declared").not.toBeNull();
  return `${width?.[1]}x${height?.[1]}`;
}

/**
 * Sizes DESIGN.md documents for the lamp, as `WxH`, in the order §3.2 lists
 * them. Read from the document rather than hardcoded, so the document stays the
 * thing that decides and the test cannot drift away from it silently.
 */
function documentedSizes(): string[] {
  const section = designMd.slice(designMd.indexOf("### 3.2"));
  const body = section.slice(0, section.indexOf("\n### ") + 1 || undefined);
  const sizes = [...body.matchAll(/\*\*(\d+)\s*×\s*(\d+)\s*px\*\*/g)].map(
    (m) => `${m[1]}x${m[2]}`,
  );
  expect(sizes.length, "§3.2 should document exactly two sizes").toBe(2);
  return sizes;
}

describe("the lamp's two sizes are documented and ruled, never inline", () => {
  it("documents the scanned size and the badge size, in that order", () => {
    // Order matters in the table: the scanned signal is the default and the
    // badge lamp is the exception, not the other way round.
    expect(documentedSizes()).toEqual(["20x7", "12x5"]);
  });

  it("draws the app's lamp at the documented scanned size", () => {
    expect(boxOf(rule(ledCss, ".led"))).toBe(documentedSizes()[0]);
  });

  it("gives the mock-up's badge lamp a rule at the documented badge size", () => {
    expect(boxOf(rule(componentsHtml, ".badge .led"))).toBe(
      documentedSizes()[1],
    );
  });

  it("states in §3 that one size covers the signal, not the whole app", () => {
    // The old headline read "one size, everywhere", which §3.2 contradicts.
    // Leaving it would put two rules in the document and let a reader pick.
    const headline = designMd.slice(designMd.indexOf("## 3. The LED"));
    expect(headline.slice(0, headline.indexOf("### 3.1"))).not.toContain(
      "one size, everywhere",
    );
  });

  for (const file of readdirSync(mockups).filter((f) => f.endsWith(".html"))) {
    it(`gives no lamp in ${file} an inline size`, () => {
      const html = readFileSync(join(mockups, file), "utf8");
      const attr = (tag: string, name: string) =>
        tag.match(new RegExp(`${name}\\s*=\\s*("([^"]*)"|'([^']*)')`, "i"));
      const inline = [...html.matchAll(/<span\b[^>]*>/g)]
        .map((m) => m[0])
        .filter((tag) => {
          const cls = attr(tag, "class");
          const classes = (cls?.[2] ?? cls?.[3] ?? "").split(/\s+/);
          if (!classes.includes("led")) return false;
          const style = attr(tag, "style");
          return /(?:^|;)\s*(?:width|height)\s*:/.test(
            style?.[2] ?? style?.[3] ?? "",
          );
        });
      expect(inline).toEqual([]);
    });
  }
});
