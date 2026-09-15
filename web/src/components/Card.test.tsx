// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { Card, Panel } from "./Card";

afterEach(cleanup);

/**
 * The card is the pattern that makes a screen belong to the product, so most
 * of what is worth asserting is in CSS rather than in the DOM. jsdom applies
 * no stylesheets, which is why the structural rules are read off the file on
 * disk: a computed-style check here would pass no matter what card.css says.
 */
const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "card.css"),
  "utf8",
);

/**
 * The body of one rule, by selector.
 *
 * Takes a plain selector (".card") and escapes it here, rather than asking
 * every call site to pre-escape — a pre-escaped argument gets escaped twice
 * and silently matches nothing, which is a test that cannot fail.
 */
const rule = (selector: string): string => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(css);
  expect(match, `missing the ${selector} rule`).not.toBeNull();
  return match?.[1] ?? "";
};

describe("Card", () => {
  it("renders the title as a real heading at the level it is given", () => {
    render(
      <Card title="Uptime" headingLevel={3}>
        <Panel>body</Panel>
      </Card>,
    );
    const heading = screen.getByRole("heading", { name: "Uptime" });
    expect(heading.tagName).toBe("H3");
  });

  it("defaults to h2 but does not hard-code it", () => {
    // A card's title is a real entry in the document outline, so the level has
    // to follow where the card sits. A component that always emits h2 forces
    // every page into one shape and quietly breaks the outline on any page
    // that nests cards.
    render(
      <Card title="Incidents">
        <Panel>body</Panel>
      </Card>,
    );
    expect(screen.getByRole("heading", { name: "Incidents" }).tagName).toBe(
      "H2",
    );
  });

  it("hides the header glyph from assistive technology", () => {
    // "Alert triangle, Incidents" is one word longer and no more informative.
    render(
      <Card title="Incidents" icon={<svg data-testid="glyph" />}>
        <Panel>body</Panel>
      </Card>,
    );
    const tile = document.querySelector(".icon-tile");
    expect(tile?.getAttribute("aria-hidden")).toBe("true");
  });

  it("puts the action in the header, not in the body", () => {
    render(
      <Card title="Uptime" action={<button type="button">Refresh</button>}>
        <Panel>body</Panel>
      </Card>,
    );
    const head = document.querySelector(".card-head")!;
    expect(within(head as HTMLElement).getByRole("button")).toBeTruthy();
  });

  it("omits the tile and the action slot entirely when unused", () => {
    // An empty flex child still takes its gap, so a card with no icon would
    // sit 10px further right than its neighbours.
    render(
      <Card title="Plain">
        <Panel>body</Panel>
      </Card>,
    );
    expect(document.querySelector(".icon-tile")).toBeNull();
    expect(document.querySelector(".card-head-action")).toBeNull();
  });
});

describe("the nesting pattern", () => {
  it("makes the card a frame with padding, not a second panel", () => {
    // The padding is the load-bearing declaration: without it the card's
    // border sits flush on the panel's border and the eye reads two competing
    // edges rather than one surface holding another.
    const card = rule(".card");
    expect(card, "the card needs its own edge").toMatch(/border:\s*1px/);
    expect(card, "the card takes the wider radius").toMatch(
      /border-radius:\s*var\(--r-lg\)/,
    );
    expect(card, "without padding the frame is a second edge").toMatch(
      /padding:\s*var\(--space-1h\)/,
    );
    expect(card, "the card is the surface the panels rest on").toMatch(
      /background:\s*var\(--surface\)/,
    );
  });

  it("keeps the panel one step tighter and one step louder than the card", () => {
    const panel = rule(".panel");
    expect(panel, "the panel takes the tighter radius").toMatch(
      /border-radius:\s*var\(--r-md\)/,
    );
    // The fill is what carries the nesting: a panel on the same surface as its
    // card is invisible, and one that is quieter inverts the hierarchy.
    expect(panel, "the panel sits above the card").toMatch(
      /background:\s*var\(--surface-2\)/,
    );
  });

  it("matches the gap between panels to the card's own padding", () => {
    // A panel should be the same distance from its neighbour as from the
    // card's edge. Unequal values are what make a card look assembled rather
    // than laid out, and it is the kind of thing nobody can name on sight.
    const card = rule(".card");
    const gap = /gap:\s*var\((--[a-z0-9-]+)\)/.exec(card)?.[1];
    const padding = /padding:\s*var\((--[a-z0-9-]+)\)/.exec(card)?.[1];
    expect(gap, "card gap and card padding must be the same token").toBe(
      padding,
    );
  });

  it("gives the header the asymmetric padding that seats it on the panel", () => {
    // Eight around, six at the bottom. The first panel is already six from the
    // card's edge, so an eight-pixel gap under the title reads as looser than
    // the one beside it.
    const head = rule(".card-head");
    expect(head).toMatch(
      /padding:\s*var\(--space-2\)\s+var\(--space-2\)\s+var\(--space-1h\)/,
    );
  });

  it("titles the card in the text face, not the label face", () => {
    // The distinction this protects: mono-caps is the register for labels
    // *inside* a panel. A card title is a heading. Using caps-legend for both
    // is what made every screen read as a stack of shouted abbreviations with
    // no hierarchy between them.
    const title = rule(".card-title");
    expect(title).toMatch(/@apply face-sans/);
    expect(title).not.toMatch(/caps-legend/);
    expect(title).not.toMatch(/text-transform:\s*uppercase/);
    expect(title).toMatch(/font-size:\s*var\(--type-card\)/);
  });

  it("keeps the label role mono-caps, inside the panel where it belongs", () => {
    const label = rule(".panel-label");
    expect(label).toMatch(/@apply caps-legend/);
  });
});
