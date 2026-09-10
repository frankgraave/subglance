# SubGlance dashboard

React 19 + TypeScript + Vite 8, styled with Tailwind v4.

## Tokens

`src/styles/tokens.css` is the **only** file in this tree allowed to contain a
literal colour. Everything else uses a token, either as a Tailwind utility
(`bg-surface`, `text-ink-2`) or as a custom property (`var(--glow-up)`).

The values come from `docs/DESIGN.md` §2, which is the source of truth. Two
guards in `src/styles/tokens.test.ts` keep that honest:

- every colour, radius and easing value here must match the design document;
- no other file under `src/` may contain a hex, `rgb()` or `hsl()` literal.

Both failures are otherwise invisible: a hardcoded green looks perfectly
correct until someone switches to light mode.

## Theming

Tokens hang off `[data-theme="dark"|"light"]` on `<html>`. The Tailwind
bindings live in an `@theme inline` block — `inline` is what makes utilities
emit `var(--canvas)` instead of freezing one theme's value at build time.

The preference (`light` / `dark` / `system`, default `system`) is stored in
`localStorage` and applied by a small script in `index.html` *before first
paint*. React cannot do this: it mounts after the first frame, so a light-mode
user would see a black flash on every load. That script duplicates the logic in
`src/theme/theme.ts` on purpose; keep the two in step.

## Commands

| Command | What it does |
|---|---|
| `npm install` | install dependencies |
| `npm test` | token and theme guards |
| `npm run lint` | oxlint |
| `npm run build` | typecheck + production build into `dist/` |

From the repository root: `make web-test`, `make web-lint`, `make web-build`.
