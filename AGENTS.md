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

Two bars, and one question decides between them: **is this true on every
screen?**

- **Yes** — the masthead (`web/src/shell/Topbar.tsx`). The sidebar toggle,
  search, the layout switcher, the theme. It is identical on every route, so
  it can be read once instead of re-read per screen.
- **No** — the page toolbar (`web/src/shell/PageToolbar.tsx`), filled through
  a portal slot by whichever view is mounted. It hides itself on screens with
  nothing to put in it rather than sitting there empty.
- **Neither** — an action that operates on one kind of thing belongs in the
  header of the card it acts on. Adding a monitor is a monitors action; it is
  not chrome, because chrome that is also present on Notifications while
  meaning something about monitors is chrome you have to re-read.

`web/src/App.masthead.test.tsx` walks all four routes and compares the bar's
accessible names, so a control added to one screen's masthead fails the build
rather than being noticed in review.

A page whose card already names it does not also get an `<h1>` — but do not
delete what the heading was carrying. When the Monitors title went, its "4
configured, 1 paused" moved to the card's `note` prop, which is what `Card`
grew that prop for.

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
