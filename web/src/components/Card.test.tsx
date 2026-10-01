// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
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

const controlsCss = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "controls.css"),
  "utf8",
);

/**
 * The body of one rule, by selector.
 *
 * Takes a plain selector (".card") and escapes it here, rather than asking
 * every call site to pre-escape — a pre-escaped argument gets escaped twice
 * and silently matches nothing, which is a test that cannot fail.
 */
const rule = (selector: string, sheet = css): string => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(sheet);
  expect(match, `missing the ${selector} rule`).not.toBeNull();
  return match?.[1] ?? "";
};

describe("Card", () => {
  it("renders the title as a real heading at the level it is given", () => {
    render(
      <Card title="Uptime" icon={<svg />} headingLevel={3}>
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
      <Card title="Incidents" icon={<svg />}>
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
      <Card title="Uptime" icon={<svg />} action={<button type="button">Refresh</button>}>
        <Panel>body</Panel>
      </Card>,
    );
    const head = document.querySelector(".card-head")!;
    expect(within(head as HTMLElement).getByRole("button")).toBeTruthy();
  });

  it("omits the action slot entirely when unused", () => {
    // An empty flex child still takes its gap, so an empty action wrapper
    // would push the header's right edge in on a card that has no action.
    render(
      <Card title="Plain" icon={<svg />}>
        <Panel>body</Panel>
      </Card>,
    );
    expect(document.querySelector(".card-head-action")).toBeNull();
  });

  it("always draws the icon tile, as the first thing in the header", () => {
    // SUB-167: every card has a tile, so the eye scanning down a column of
    // cards finds the same anchor on each. The tile leads the header, before
    // the title, and holds the glyph it was given.
    render(
      <Card title="Backups" icon={<svg data-testid="glyph" />}>
        <Panel>body</Panel>
      </Card>,
    );
    const lead = document.querySelector(".card-head-lead");
    expect(lead?.firstElementChild?.classList.contains("icon-tile")).toBe(true);
    expect(lead?.firstElementChild?.contains(screen.getByTestId("glyph"))).toBe(true);
  });

  it("does not compile without an icon", () => {
    // The rule is enforced by the type, not by review. `tsc -b` (part of
    // `npm run build`) checks this file, and `@ts-expect-error` fails the
    // build if the line below ever compiles, so making `icon` optional again
    // breaks CI rather than quietly letting untiled cards back in.
    const untiled = (
      // @ts-expect-error `icon` is required on every card (SUB-167).
      <Card title="No tile">
        <Panel>body</Panel>
      </Card>
    );
    // Nor does `null` stand in for it: the prop is an element, not a node.
    // @ts-expect-error `null` is not an icon.
    const nulled = <Card title="Null tile" icon={null}><Panel>body</Panel></Card>;
    expect([untiled, nulled]).toHaveLength(2);
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
    /*
     * The fill is what carries the nesting: a panel on the same surface as its
     * card is invisible, and one that is quieter inverts the hierarchy.
     *
     * This asserts `--surface-panel` rather than `--surface-2`, and the
     * distinction is the whole point. These fills are alphas that composite
     * against their *parent*, so "louder" is about what renders, not about
     * which alpha is larger: `--surface-2` (.05) on a card rendered at 41
     * where the reference's panel measures 35, while `--surface-panel` (.02)
     * renders at 34 — still lighter than the card's own 30, because it stacks
     * on top of it. A larger alpha here does not mean a better-nested panel.
     */
    expect(panel, "the panel sits above the card").toMatch(
      /background:\s*var\(--surface-panel\)/,
    );
    // And it rests on the card rather than being painted onto it (§2.10). A
    // flat panel was the thing that made our cards read as one printed sheet.
    expect(panel, "the panel takes the raised rung").toMatch(
      /box-shadow:\s*var\(--shadow-raised\)/,
    );
  });

  it("gives a panel the static edge, not the control edge", () => {
    /*
     * §2.9 reserves `--border-control` for the resting edge of things that are
     * clickable; a panel is a static surface.
     *
     * Not pedantry about roles: `--border-control` is the one opaque, slightly
     * blue value in the set, and on a dark card it measured 38,40,43 against a
     * 41 fill — an edge with no contrast against the thing it encloses, so the
     * panel simply lost its outline. The reference draws a panel's edge
     * *lighter* than the panel's own fill, by 11 greyscale values, which is
     * what `--border` reproduces here.
     */
    expect(rule(".panel"), "a panel is not a control").toMatch(
      /border:\s*1px solid var\(--border\)/,
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

/**
 * Every file under web/src with the given extension, tests excluded: a guard
 * quotes the patterns it refuses.
 */
function sources(dir: string, extension: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...sources(full, extension));
    else if (entry.endsWith(extension) && !entry.includes(".test.")) out.push(full);
  }
  return out;
}

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

/**
 * The attributes of every `<Card` tag in a source file, with the JSX inside
 * them blanked out.
 *
 * Read with a small scanner rather than a regular expression: the tag holds
 * JSX of its own (`icon={<IconKey />}`, an action button with its own class),
 * so a pattern either ends inside the icon or picks up the button's class as
 * the card's.
 */
function cardTags(source: string): string[] {
  const tags: string[] = [];
  for (const start of source.matchAll(/<Card\b/g)) {
    let depth = 0;
    let quote = "";
    let own = "";
    for (let i = start.index + 1; i < source.length; i++) {
      const c = source[i];
      if (quote) { if (c === quote) quote = ""; own += c; continue; }
      if (depth === 0 && c === '"') { quote = c; own += c; continue; }
      if (c === "{") depth++;
      else if (c === "}") depth--;
      else if (c === ">" && depth === 0) { tags.push(own); break; }
      own += depth === 0 && c !== "}" ? c : " ";
    }
  }
  return tags;
}

/** Each class a stylesheet defines, with the directory that stylesheet sits in. */
function classHomes(): Map<string, Set<string>> {
  const homes = new Map<string, Set<string>>();
  for (const file of sources(webSrc, ".css")) {
    const body = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    for (const [, selector] of body.matchAll(/([^{}]+)\{/g)) {
      for (const [, name] of selector.matchAll(/\.([a-zA-Z][\w-]*)/g)) {
        const dirs = homes.get(name) ?? new Set<string>();
        dirs.add(dirname(file));
        homes.set(name, dirs);
      }
    }
  }
  return homes;
}

describe("Panel spacing", () => {
  it("gives a form panel the form's own field gap, and leaves every other panel alone", () => {
    const { container } = render(<><Panel spacing="form">form</Panel><Panel>prose</Panel></>);
    const [form, prose] = container.querySelectorAll(".panel");
    expect(form.className).toBe("panel panel--form");
    expect(prose.className).toBe("panel");
    // The same rung as .stack, the form such a panel holds.
    expect(rule(".panel--form")).toMatch(/gap:\s*var\(--space-4\)/);
    expect(rule(".stack", controlsCss)).toMatch(/gap:\s*var\(--space-4\)/);
  });
});

describe("a card's own class (SUB-171)", () => {
  // Six settings cards used to wear the self-monitoring or retention card's
  // class to get its panel spacing and helper text. A change to either card's
  // layout then moved cards that only borrowed it, and nothing in the markup
  // said so. What cards share lives in Panel's props and in
  // panel-content.css, under names that belong to no card.

  it("is styled beside the card that wears it", () => {
    const homes = classHomes();
    const borrowed: string[] = [];
    let cards = 0;
    let named = 0;
    for (const file of sources(webSrc, ".tsx")) {
      for (const tag of cardTags(readFileSync(file, "utf8"))) {
        cards++;
        const names = /\bclassName="([^"]*)"/.exec(tag)?.[1].split(/\s+/).filter(Boolean) ?? [];
        for (const name of names) {
          named++;
          // A class no stylesheet defines is a hook for a test, not a style
          // borrowed from anywhere.
          const dirs = homes.get(name);
          if (dirs && !dirs.has(dirname(file))) {
            borrowed.push(`${relative(webSrc, file)} wears .${name}, styled in ${[...dirs].map((d) => relative(webSrc, d)).join(", ")}`);
          }
        }
      }
    }
    // A scanner that stops finding cards, or their classes, would pass this
    // test vacuously.
    expect(cards).toBeGreaterThanOrEqual(30);
    expect(named).toBeGreaterThanOrEqual(5);
    expect(borrowed).toEqual([]);
  });

  it("does not reach into the panels it holds", () => {
    // A card that sets its panels' display or gap through `.its-card .panel`
    // makes those panels differ from every other panel, and invites the next
    // card to borrow the class for the same effect. Panel's props are the
    // way to vary a panel; card.css is the only stylesheet that styles one.
    const offenders: string[] = [];
    for (const file of sources(webSrc, ".css")) {
      if (file.endsWith(join("components", "card.css"))) continue;
      const body = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
      for (const [, selector] of body.matchAll(/([^{}]+)\{/g)) {
        if (/\.panel(?![\w-])/.test(selector)) offenders.push(`${relative(webSrc, file)}: ${selector.trim()}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
