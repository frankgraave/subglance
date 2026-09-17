/**
 * The style guide is generated, so these checks are about the things
 * generation alone does not give you.
 *
 * A generated page cannot hold a stale *value* -- it reads tokens.css at
 * build time. It can very easily hold a stale *file*: someone edits a token,
 * never runs the generator, and the committed HTML shows last month's
 * palette while claiming to be living. It can also silently stop covering a
 * ladder, which is worse than not having the page, because a reader takes a
 * complete-looking page as complete.
 *
 * So: the committed artefact must match a fresh render, every token must
 * appear, and every ladder the guards enforce must have a section.
 */
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = join(here, "..", "..");
const repoRoot = join(webRoot, "..");
const guidePath = join(repoRoot, "docs", "styleguide", "index.html");
const tokensPath = join(webRoot, "src", "styles", "tokens.css");

function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

/**
 * Every custom property that is a rung of its own.
 *
 * The `@theme inline` block at the end of tokens.css is excluded: those are
 * Tailwind bindings (`--color-ink: var(--ink)`), one alias per token that
 * already appears above. Listing them again would put 30 duplicate rows on
 * the page and teach a reader that there are two names for one decision.
 */
function declaredTokens(): Set<string> {
  const css = stripComments(readFileSync(tokensPath, "utf8"));
  const themeBlock = css.indexOf("@theme inline");
  const own = themeBlock === -1 ? css : css.slice(0, themeBlock);
  const names = new Set<string>();
  for (const match of own.matchAll(/(--[\w-]+)\s*:/g)) {
    names.add(match[1]);
  }
  return names;
}

describe("the living style guide", () => {
  it("is exactly what the generator produces right now", () => {
    /*
     * The committed file is regenerated into a temp location and compared.
     * This is the check that makes "living" true rather than aspirational:
     * without it the page is a screenshot of whatever the tokens were on the
     * day someone last remembered to run the script.
     */
    const committed = readFileSync(guidePath, "utf8");
    execFileSync("node", [join(webRoot, "scripts", "build-styleguide.mjs")], {
      cwd: webRoot,
    });
    const fresh = readFileSync(guidePath, "utf8");
    expect(
      fresh === committed,
      "docs/styleguide/index.html is stale — run `npm run styleguide` in web/ and commit the result",
    ).toBe(true);
  });

  it("shows every token that tokens.css declares", () => {
    /*
     * A token that exists but is not on the page is a token the next person
     * will not know about, and will therefore reinvent two pixels away. That
     * is not hypothetical: --control-icon: 28px was added beside three files
     * already drawing that square at 26px.
     *
     * Theme-only aliases are exempt: they are the same rung re-stated per
     * theme, and the colour table already prints both values on one row.
     */
    const html = readFileSync(guidePath, "utf8");
    const missing = [...declaredTokens()].filter(
      (name) => !html.includes(name),
    );
    expect(missing).toEqual([]);
  });

  it("documents every ladder the guards enforce", () => {
    /*
     * Keyed to the guards rather than to a list someone maintains by hand:
     * if tokens.test.ts grows a rule for a new scale, this fails until the
     * page grows a section for it. A rule nobody can see is a rule the next
     * screen breaks in good faith.
     */
    const html = readFileSync(guidePath, "utf8");
    for (const section of [
      "colour",
      "status",
      "type",
      "space",
      "size",
      "radius",
      "depth",
      "motion",
      "breakpoints",
    ]) {
      expect(html, `no section for ${section}`).toContain(`id="${section}"`);
    }
  });

  it("says plainly that the tests, not the page, are the guarantee", () => {
    /*
     * The page is a reading surface. If it ever reads as the authority,
     * someone will "fix" a disagreement by editing the page, which is the
     * one repair that cannot work -- it is generated.
     */
    const html = readFileSync(guidePath, "utf8");
    expect(html).toContain("tokens.test.ts");
    expect(html).toMatch(/the test wins/i);
  });

  it("carries the reasoning, not just the values", () => {
    /*
     * A table of hex codes is a worse version of the token file. What makes
     * the page worth opening is the prose beside each value, so a render
     * that loses it is a regression even though every number is right --
     * which is exactly what happened when the rationale parser was tightened
     * and silently blanked two thirds of the rows.
     */
    const html = readFileSync(guidePath, "utf8");
    const cells = [...html.matchAll(/<td class="sg-why">([\s\S]*?)<\/td>/g)];
    expect(cells.length).toBeGreaterThan(50);
    const filled = cells.filter(([, body]) => body.trim().length > 0);
    expect(
      filled.length / cells.length,
      "most rows should explain themselves",
    ).toBeGreaterThan(0.4);
  });
});
