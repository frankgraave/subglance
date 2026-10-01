# AGENTS.md

Instructions for any coding agent working in this repository. Read this before
your first edit; it is short on purpose.

## Before you touch the frontend

**Read `docs/styleguide/index.html`.** It is the living style guide: every
value in it is generated from `web/src/styles/tokens.css`, and it carries the
reasoning beside each token. It is the single source of truth for colour,
type, leading, tracking, spacing, size, radius, depth, motion and breakpoints.

Do not type a pixel value, a colour or a font size until you have looked for
an existing rung. The value you want usually already exists — possibly two
pixels away under a name you did not guess.

### Why this is a rule and not a suggestion

This repository had 1797 lines of design documentation and drifted anyway. An
audit in September 2026 counted **88 loose pixel values across 26
stylesheets**, against **zero** loose colours and **zero** loose font sizes.
The authors were not less careful about size than about colour. Colour had a
test that failed the build; size did not.

The clearest example is worth knowing, because it is the failure mode you are
most likely to repeat: `--control-icon: 28px` was added as a *new token*,
correctly following the "name your dimensions" convention, two pixels away
from three files already drawing that square at 26px. Naming a value does not
prevent drift. Comparing it to what already exists does.

### What will fail your build

`web/src/styles/tokens.test.ts` refuses, anywhere under `web/src`:

- a hex, `rgb()` or `hsl()` literal outside `tokens.css`
- a literal font-size, or a size with no paired leading
- a literal line-height, or a Tailwind `leading-*` preset
- a literal letter-spacing
- a literal spacing or radius value at or above 4px
- a literal width, height or grid track at or above 2px
- an `@media` width that is not a breakpoint rung
- a breakpoint written as `var(--bp-*)` — see below
- a border colour that is not a role token
- a bare `z-index`, or a shadow off the ladder

Each guard has an exception map keyed `path: declaration`, and a companion
test that fails when an exception stops matching live code — so the allow-list
cannot quietly rot into a list of justified-sounding lies.

### Where a control belongs

Two bars, and one question decides between them: **does this control do
something on every screen?**

- **Yes** — the masthead (`web/src/shell/Topbar.tsx`). The sidebar toggle,
  search (which opens the command menu), the workbench, the theme. Because
  each of them works everywhere, the bar is the same on every route,
  including a monitor's detail page — it can be read once instead of re-read
  per screen.
- **No** — the page toolbar (`web/src/shell/PageToolbar.tsx`), filled through
  a portal slot (`ToolbarTools`) by whichever view is mounted. A list's
  filter field, its filters and counts, and the dashboard's layout switcher
  live here. It hides itself on screens with nothing to put in it rather than
  sitting there empty.
- **Neither** — an action that operates on one kind of thing belongs in the
  header of the card it acts on. Adding a monitor is a monitors action; it is
  not chrome, because chrome that is also present on Notifications while
  meaning something about monitors is chrome you have to re-read.

A control is in the masthead only on a screen where it does something. The
layout switcher used to sit there on every route, and on four of them
pressing it changed nothing on screen; it is the dashboard's now. There is
one search entry in the masthead — a second, page-bound field beside it is a
filter, and goes in that page's toolbar.

`web/src/App.masthead.test.tsx` walks every route, the detail page included,
and asserts the masthead holds exactly the global set and that the layout
switcher appears only in the dashboard's toolbar — so a control that lands in
the wrong bar, or goes missing from one screen, fails the build rather than
being noticed in review.

### The page frame: one title, one width rule

Every route is drawn inside one frame, `Page` (`web/src/components/Page.tsx`),
and the frame takes its title and width from one table,
`web/src/shell/pages.ts`. A screen does not choose either for itself.

- **Title.** The frame draws the page's only `h1`, visible, on the page type
  role. It is the same word as the sidebar label and the tab title, because
  all three read `PAGE_TITLES`. A screen's cards are `h2` under it; a screen
  does not render an `h1` of its own, visually hidden or otherwise. The one
  exception is a monitor's page, whose title is the monitor's name: the
  frame gets `title={null}` and the detail view's own `h1` wears
  `page-title`, so it sits at the same level as every other page's. The rail
  lights the section an address belongs to — `/monitors/{id}` lights
  Monitors.
- **Width.** Three page types, one rule: *the column of cards is
  `--size-pane-lg` on every screen but the dashboard.* `full` is the
  dashboard's (an overview that is watched, and offers to fill the width);
  `measure` is a list or a record read top to bottom; `indexed` is `measure`
  with the page's own index standing beside the column (Settings). A feature
  stylesheet does not set a page-level `max-width`: three screens doing so is
  how the product had three right edges.

`layout/page-frame.browser.test.ts` measures every route at two widths and
fails on a second `h1`, a hidden one, a title off the page role or off the
content edge, a title that disagrees with the rail or the tab, or a column
of cards that is not its page type's width. The reasoning is in DESIGN.md
§2.16.

A card on a titled page is named for what it holds, not for the page: the
page's `h1` already says "Incidents", so the card under it says "Open
incidents". When the frame took over the title, do not drop what a card
title was carrying — the Monitors card's "4 configured, 1 paused" lives in
its `note` prop, which is what `Card` grew that prop for.

Every card has an icon tile. `Card`'s `icon` prop is required, so a new card
does not compile until you give it a glyph from `web/src/components/icons.tsx`
— one that names the card's subject, not an action, and that no card about a
different subject on the same screen uses. A `Suspense` fallback for a lazy card takes the same
glyph as the card it stands in for. The reasoning is in DESIGN.md §8.2.

Every piece of visible text sits on one of the six type roles (page 24, card
18, row 15, body 14, helper 13, section 12), each strictly larger than the one
below it. Text with no rule of its own inherits body from the base layer, so a
new element does not fall back to the browser's 16px. `tokens.test.ts` fails
when two roles share a size, and `layout/type-roles.browser.test.ts` fails on
any rendered text whose size and leading are not a pair from the scale. Pick
the role by what the text is, not by how big it should look. The reasoning is
in DESIGN.md §2.5.

A checkbox or radio is `Checkbox` or `Radio` from
`web/src/components/Choice.tsx`, never a hand-written `<input type="checkbox">`
and never `accent-color`: `tokens.test.ts` refuses both outside that component
and its `choice.css`. Pass the label as children. The reasoning is in
DESIGN.md §8.9.

Every row of a list is one height. In the monitors inventory each settings
column is a legend over a fixed `--size-row-sm` value slot, a value is one line
that ends in an ellipsis rather than wrapping, and a wrapped row puts its
columns on equal grid tracks. `layout/inventory-rows.browser.test.ts` fails on
two rows of different height. The reasoning is in DESIGN.md §8.10.

A button that is a glyph plus a word needs an explicit `aria-label`. What a
screen reader makes of an unnamed inline `<svg>` is not fixed — some skip it,
some announce "graphic" — so leaving the name to text content makes it depend
on the reader. Name it in the markup, order the glyph in CSS.

### Three facts that will save you an hour

1. **A CSS custom property does not work inside a media query.** Verified in
   Chromium, not assumed: `@media (max-width: var(--bp-phone))` never matches,
   because media queries are evaluated before custom properties are
   substituted. It fails *silently* — the block is dropped, and the layout is
   simply wrong at one width with nothing to show for it. Breakpoints are
   therefore documented as tokens and enforced as literals.

2. **Some literals are correct.** `web/src/monitors/led.css` writes `20px` and
   `7px` because `ledSizes.test.ts` checks the lamp against DESIGN.md §3. A
   token there would satisfy one guard and break another. It is allow-listed
   with that reason attached.

3. **Anything that floats needs `--surface-float`.** The `--surface*` tokens
   are deliberately translucent and composite whatever sits behind them. A
   tooltip, menu or drawer built on one lets the page read straight through
   it — which looks, in a screenshot, exactly like a stacking-context bug.

## When you add or change a token

The guide is generated, so regenerate and commit it, or the test fails:

```bash
cd web && npm run styleguide && git add ../docs/styleguide
```

Never hand-edit `docs/styleguide/index.html`. It is a build artefact and the
test compares it byte for byte against a fresh render.

## When a guard blocks you

In order of preference:

1. **Use an existing rung.** Usually right.
2. **Add a rung**, with a comment stating what it is for and why no existing
   rung serves. Adding one should feel like a small cost.
3. **Add an exception**, keyed `path: declaration`, with a reason a reviewer
   can disagree with.

Never weaken a guard to make a failure go away. If a test blocks something
legitimate, that is information about the ladder, not an obstacle to route
around.

## Verifying

```bash
cd web
npx vitest run src/styles/     # the guards alone
npx vitest run                 # the full suite
npm run build && node scripts/bundle-budget.mjs
npx vitest run --config vitest.browser.config.ts   # real Chromium
```

Budgets are ceilings, not records of the current measurement. Raising one is
allowed and is meant to be deliberate: state the before and after byte counts
and the argument, in the comment beside the number.

## Go and the rest

`go build ./...`, `go vet ./...`, `go test -race ./internal/...`. The frontend
is embedded via `go:embed all:dist`, so `internal/webui/dist/.gitkeep` must
exist even when the frontend has not been built — deleting it fails every
build job in seconds with `pattern all:dist: no matching files found`.
