import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * SUB-47, the half the lamp cannot carry on its own.
 *
 * A 20x7 lamp is a fine signal when you are looking at one monitor and a weak
 * one when you are scanning 200. So every layout gives "paused" a second,
 * larger mark on the same leading edge that already carries down and pending
 * (DESIGN.md §3.1) — dotted where trouble is solid, because the line style,
 * not the colour, is what says "this is deliberate".
 *
 * Asserted against the stylesheet text: jsdom applies no CSS, so a rendered
 * assertion would be satisfied by a stylesheet that had been deleted.
 */

const css = (path: string) => readFileSync(join(process.cwd(), path), "utf8");
const monitorsCss = css("src/monitors/monitors.css");
const wallCss = css("src/wall/wall.css");

/** The body of the `[data-status="…"]` rule for one block class. */
function statusRule(sheet: string, block: string, status: string): string {
  // The trailing ` {` matters: `.wall-card[data-status="paused"]` is also the
  // prefix of a descendant selector, and matching that would read the wrong
  // rule body and quietly assert nothing.
  const selector = `.${block}[data-status="${status}"] {`;
  const at = sheet.indexOf(selector);
  expect(at, `missing ${selector}`).toBeGreaterThan(-1);
  return sheet.slice(at, sheet.indexOf("}", at));
}

describe("paused carries a second signal in every list layout", () => {
  for (const block of ["mon-row", "mon-card", "mon-line"]) {
    it(`marks ${block} with a dotted leading edge`, () => {
      expect(statusRule(monitorsCss, block, "paused")).toContain("2px dotted");
    });

    it(`keeps ${block}'s trouble edges solid, so the two never read alike`, () => {
      expect(statusRule(monitorsCss, block, "down")).toContain("2px solid");
      expect(statusRule(monitorsCss, block, "pending")).toContain("2px solid");
    });

    it(`draws ${block}'s paused edge in --ink-3, which clears 3:1`, () => {
      // --ink-4 measures 1.9:1 against --surface in both themes; a signal at
      // that contrast is decoration. See DESIGN.md §3.1.
      expect(statusRule(monitorsCss, block, "paused")).toContain("var(--ink-3)");
    });
    it(`keeps ${block}'s paused edge out of the faded content`, () => {
      // `opacity` on the element composites its own border, so fading the
      // block would take the dotted edge down with it — from 3.4:1 to roughly
      // 2:1, under the 3:1 floor the edge exists to clear. The fade belongs to
      // the children. Caught by CodeRabbit on PR #11.
      expect(statusRule(monitorsCss, block, "paused")).not.toContain("opacity");
      expect(monitorsCss).toContain(`.${block}[data-status="paused"] > * { opacity:`);
    });
  }

  it("dashes the wall card, where the lamp is too small to be read across a room", () => {
    expect(statusRule(wallCss, "wall-card", "paused")).toContain("border-style: dashed");
  });
});
