# Clickable mockups

Open `index.html` directly from this checkout (`file://`). No server, build or
network access is required. Data and interactions are simulated; these files
are review examples, not an implementation of the API. Page-proposal annotations
may describe an earlier product state. For current components and the reasoning
behind each token, use [`../styleguide/index.html`](../styleguide/index.html).

## One token source

Every HTML entrypoint links **`web/src/styles/tokens.css` itself** by a relative
path, plus the product's `web/src/monitors/led.css`. Keep the directory structure
when sharing a checkout: copying just an HTML file no longer includes its styles.
Do not paste palettes, typography scales, radii or shadow values into a mockup.
Browsers ignore the Tailwind-only `@theme` block; the ordinary root/theme rules
are the same ones the product consumes.

`common.css` loads the real font files from `web/public/fonts/`. Only their URLs
are adapted for disk access: the production font sheet uses `/fonts/`, which
would resolve to the filesystem root. A guard compares all face descriptors
against the production source. No remote font service or generated font copy is
involved.

## Status markup

Use the same shape as `Led.tsx`: an `aria-hidden="true"` lamp in `.led-wrap`,
followed by a `.sr-only` or visible `.led-label` word. Where a badge already says
the status, make that word the visible label rather than repeating it.

The dashboard's generated markup names its state with `mockupStatusWord`;
changes go through `setMockupLed` so the word changes with the lamp. Disconnects
qualify live claims as “Was up”, “Was down” or “Was warning”; waiting and paused
remain facts. Adding a footnote about simplified accessibility is not a substitute
for the label.

## Checks

From `web/`:

```sh
npx vitest run src/styles/mockups.test.ts --maxWorkers=2
npx vitest run --config vitest.browser.config.ts src/layout/mockup-contract.browser.test.ts --maxWorkers=2
```

The static guard walks **all** HTML, CSS and JavaScript under this directory,
including inline declarations and HTML templates. It rejects token copies,
literal typography, unpaired leading, private colours, off-ladder spacing/radii,
undefined variables and missing LED alternatives. The existing LED-size guard
also preserves the documented 12×5 text-badge exception to the 20×7 signal lamp.

The Chromium test opens every page through `file://` in both themes. It compares
resolved variables to an independent document containing only the live tokens,
checks loaded fonts and all rendered text roles, and measures panel/card depth,
density, nesting and segmented-control geometry. It exercises dashboard layouts,
outages, disconnects, the detail drawer, component toasts and inline editing.
Set `MOCKUP_PROOF_DIR` to save screenshots and a computed-token evidence ledger.
