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

/**
 * Where each layout draws its leading edge.
 *
 * `.mon-row` paints on its first cell rather than on itself, and that is not
 * a stylistic preference: it is a `<tr>` under `border-collapse: separate`,
 * which paints neither a background nor a border. SUB-139 found the four
 * status rules sitting on the row, live in the computed style and invisible
 * on screen — so this suffix is the thing that keeps them honest. The other
 * two layouts are ordinary elements and carry the edge themselves.
 */
const EDGE_TARGET: Record<string, string> = {
  "mon-row": " > :first-child",
  "mon-card": "",
  "mon-line": "",
};

/** The body of the `[data-status="…"]` rule that draws one block's edge. */
function statusRule(sheet: string, block: string, status: string): string {
  // The trailing ` {` matters: `.wall-card[data-status="paused"]` is also the
  // prefix of a descendant selector, and matching that would read the wrong
  // rule body and quietly assert nothing.
  const selector = `.${block}[data-status="${status}"]${EDGE_TARGET[block] ?? ""} {`;
  const at = sheet.indexOf(selector);
  expect(at, `missing ${selector}`).toBeGreaterThan(-1);
  return sheet.slice(at, sheet.indexOf("}", at));
}

describe("paused carries a second signal in every list layout", () => {
  for (const block of ["mon-row", "mon-card", "mon-line"]) {
    it(`marks ${block} with a dotted leading edge`, () => {
      expect(statusRule(monitorsCss, block, "paused")).toContain("dotted");
    });

    it(`keeps ${block}'s trouble edges solid, so the two never read alike`, () => {
      // Solid is the default the resting rule states, so a status that does
      // not say `dotted` is solid. Asserting the absence rather than the
      // presence of `solid` is what lets `.mon-row` reserve its 2px once and
      // change only the colour per status — reserving the width is why a row
      // does not shift one pixel right the moment a monitor goes down.
      expect(statusRule(monitorsCss, block, "down")).not.toContain("dotted");
      expect(statusRule(monitorsCss, block, "pending")).not.toContain("dotted");
    });

    it(`draws ${block}'s leading edge at the status-rail rung in every status`, () => {
      // The width belongs to the edge, not to the state. `.mon-row` states it
      // once on the resting rule; the other two state it per status because
      // they have no resting edge to reserve it on.
      //
      // Asserted as the TOKEN, not as `2px` (SUB-142). The literal moved into
      // `--size-status-rail` so that one decision has one name — the incidents
      // rail was writing the same 2px as `var(--outline-w)`, a focus-outline
      // token, and this edge was writing it as a literal the size guard could
      // not see. A grep for "2px" here would now pass only by accident if
      // somebody wrote the literal back, which is the drift this test should
      // fail on rather than accept. The rung's *value* is checked against the
      // ladder in tokens.test.ts, which is where a pixel count belongs.
      const resting =
        block === "mon-row"
          ? monitorsCss.slice(
              monitorsCss.indexOf(".mon-row > :first-child {"),
              monitorsCss.indexOf("}", monitorsCss.indexOf(".mon-row > :first-child {")),
            )
          : statusRule(monitorsCss, block, "paused");
      expect(resting).toContain("var(--size-status-rail)");
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
    expect(
      wallCss.slice(
        wallCss.indexOf('.wall-card[data-status="paused"] {'),
        wallCss.indexOf("}", wallCss.indexOf('.wall-card[data-status="paused"] {')),
      ),
    ).toContain("border-style: dashed");
  });
});
