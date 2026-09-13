import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * SUB-29, the back link's 24px target.
 *
 * DESIGN.md promises the extra height is added *under* the text rather than as
 * padding around a box, so the control keeps reading as a link. An inline-flex
 * box with `align-items: center` splits that height evenly above and below
 * instead, which is the same pixel count and the wrong shape.
 *
 * Asserted against the stylesheet text: jsdom applies no CSS, so a rendered
 * assertion would pass against a stylesheet that had been deleted.
 */

const detailCss = readFileSync(
  join(process.cwd(), "src/monitors/detail.css"),
  "utf8",
);

/** The body of a single rule, by exact selector. */
function ruleBody(sheet: string, selector: string): string {
  const at = sheet.indexOf(`${selector} {`);
  expect(at, `no rule for ${selector}`).toBeGreaterThan(-1);
  const open = sheet.indexOf("{", at);
  const close = sheet.indexOf("}", open);
  return sheet.slice(open + 1, close);
}

describe(".mon-detail-back", () => {
  const body = ruleBody(detailCss, ".mon-detail-back");

  /** Declarations only, with the comments that explain them stripped out. */
  const declarations = body.replace(/\/\*[\s\S]*?\*\//g, "");

  it("reserves the WCAG 2.2 minimum target height", () => {
    // --space-6 is the 24px step; SC 2.5.8 asks for 24 CSS pixels and the
    // inline exception does not cover a control alone on its own line.
    expect(declarations).toMatch(/min-height:\s*var\(--space-6\)/);
  });

  it("adds that height below the text, not around it", () => {
    expect(declarations).toMatch(/align-items:\s*flex-start/);
    expect(declarations).not.toMatch(/align-items:\s*center/);
  });

  it("keeps the link affordance: no box padding, border or background", () => {
    // Padding would grow the same target into a filled rectangle, which is the
    // shape DESIGN.md rejects for a frequent, low-stakes action.
    expect(declarations).toMatch(/padding:\s*0/);
    expect(declarations).toMatch(/border:\s*0/);
    expect(declarations).toMatch(/background:\s*none/);
  });
});
