# SubGlance — Design system

> Internal source of truth for visual and interaction decisions. See
> `ARCHITECTURE.md` for the technical calls.
>
> **Living examples:** open `docs/mockups/index.html` in a browser. Every rule
> below is visible and clickable there. If the code disagrees with this
> document, this document wins — or it's out of date and needs updating in the
> same PR.

---

## 1. Starting point

SubGlance answers one question: **is it still working?** Everything in the
interface earns its place by answering that question faster, or it doesn't
belong.

The obvious comparisons are Uptime Kuma and Better Stack. My bet isn't more
features — it's a dashboard you only need to look at for half a second. That's a
design claim, so the design has to deliver on it.

### The five rules

1. **Status is colour, activity is motion.** The LED tells you *what* is going
   on. Motion tells you *that* something happened. The moment those two blur
   together, neither means anything.
2. **Silence is the default.** A healthy dashboard should be boring. Anything
   that pulses, blinks or glows permanently teaches the viewer to look away —
   exactly when you need their attention.
3. **Absence is loud.** A failed check draws at full height. What *isn't* there
   must never look like a fast response.
4. **One signal per meaning.** As soon as two things say the same thing, the
   signal that matters gets diluted.
5. **Size is for structure, not for emphasis.** Emphasis comes from colour and
   contrast. Components that change size per view stop being reference points.

---

## 2. Tokens

Every value is a CSS custom property. On Tailwind v4 these become
`@theme` tokens; the names stay the same. **Never hardcode a colour or radius in
a component.**

**One exception, and only one: geometry inside an `<svg>`.** An icon's `rx` is
part of a drawing, not part of the interface's shape language — it is expressed
in the icon's own 24x24 viewBox, so a 4px token would mean something different
in every icon and scale wrongly the moment the icon is rendered at another size.
CSS cannot reach those attributes without a rule per shape either. Icon path
data, including `rx`, therefore stays literal in `web/src/shell/icons.tsx`.
Everything a user can click, hover or read still takes its radius from a token.

### 2.1 Colour — dark (default)

```css
--canvas:     oklch(.205 0 0);            /* page background */
--surface:    rgba(255,255,255,.03);      /* card, sidebar, drawer */
--surface-2:  rgba(255,255,255,.05);      /* inputs, hover */
--surface-hi: rgba(255,255,255,.08);      /* active segments, tracks */
--border:     rgba(255,255,255,.05);      /* default border, divider */
--border-hi:  rgba(255,255,255,.10);      /* border on interactive elements */

--ink:        oklch(.97 0 0);             /* primary text */
--ink-2:      oklch(.708 0 0);            /* secondary text */
--ink-3:      oklch(.556 0 0);            /* labels, help text */
--ink-4:      oklch(.439 0 0);            /* placeholders, disabled */
```

**Surfaces are white at low alpha, not lighter greys.** This is the decision
that makes a stack of panels read as one material rather than as separately
painted boxes. An alpha surface inherits whatever sits beneath it, so nesting
stays coherent at any depth: a panel inside a card inside the page is visibly
one step up from its parent without anyone having to choose a third grey. A
panel moved to a different background still belongs there.

**The neutral scale is achromatic — chroma exactly 0.** A grey carrying a hint
of blue reads as a colour decision, and in this product colour belongs to the
data. Greys that are genuinely neutral are what let a single amber or red mean
something.

### 2.2 Colour — light

```css
--canvas:     #fbfbfa;   --surface:    #ffffff;
--surface-2:  #f6f6f5;   --surface-hi: #f0f0ef;
--border:     #e6e6e4;   --border-hi:  #d6d6d3;
--ink:        #16181a;   --ink-2:      #5c6165;
--ink-3:      #8b9196;   --ink-4:      #b6bbbf;
```

Light isn't dark flipped. `--canvas` is a warm grey (`#fbfbfa`), not white;
cards *are* white. That way cards sit in front of the page instead of
disappearing into it. The status colours are darker and more saturated in light
mode, because the dark originals are unreadable on white.

### 2.3 Status

| Status | Dark | Light | Meaning |
|---|---|---|---|
| `--up` | `#34d399` | `#059669` | Last check passed |
| `--warn` | `#fbbf24` | `#b45309` | Slow, or certificate expiring |
| `--down` | `#f43f5e` | `#e11d48` | Last check failed |
| `--idle` | `#4b5257` | `#c2c7cb` | No data yet |

Every status colour has a `-dim` variant for badge and row backgrounds
(`--up-dim`, `--warn-dim`, `--down-dim`).

`--down` additionally has `--down-deep`, one step past `--down-dim`, for a
tinted row under the pointer. **Hover on a row that already means something
must not switch signals.** A neutral highlight arrives as a second kind of
colour on top of the first, and the reader has to work out which of the two
they are being told about; the same red getting louder is the same statement
said closer. A hovered down row therefore deepens its own tint rather than
picking up `--surface-2` like a neutral row does.

There is deliberately no `-deep` for the other three. Down is the only status
with a resting fill to deepen — the others mark themselves with a coloured left
edge and nothing else, so a hover tint would *introduce* a colour rather than
intensify one, which is the same problem in the status palette's clothes. A
token with no caller is a decision nobody made.

**The label on a filled status mark.** A status badge fills itself with its own
status colour, and the ink scale is wrong on that fill: `--ink` measures 1.59:1
on `--up` in dark, so the word naming the status would be the least readable
thing in the row it explains. Each status gets an `--on-*` pair instead,
measured per theme.

| Token | Dark | Light | Measured on its fill |
|---|---|---|---|
| `--on-up` | `#08090a` | `#08090a` | 10.37:1 dark, 5.29:1 light |
| `--on-warn` | `#08090a` | `#ffffff` | 11.94:1 dark, 5.02:1 light |
| `--on-down` | `#08090a` | `#ffffff` | 5.43:1 dark, 4.70:1 light |
| `--on-idle` | `#e8eaec` | `#16181a` | 6.59:1 dark, 10.45:1 light |

These are only for a *solid* status fill. On a `-dim` background the ink scale
is still the right answer — the dim variants sit within 1.2:1 of `--surface`
precisely so ordinary text keeps working on them.

**A zero is data that recedes.** `--ink-zero` is its own step, not a reuse of
`--ink-3`:

| Token | Dark | Light | Measured on `--surface` |
|---|---|---|---|
| `--ink-2` | `#9ba1a6` | `#5c6165` | 6.82:1 dark, 6.26:1 light |
| `--ink-zero` | `#6f767b` | `#7b8186` | 3.86:1 dark, 3.94:1 light |
| `--ink-3` | `#61686d` | `#8b9196` | 3.14:1 dark, 3.19:1 light |

A column of zeros at full strength competes with the measurements beside it for
attention it has not earned: nothing happened, and nothing is what that should
look like. But a zero is still a reading. `0 failures` and `no data for this
window` are different statements, and a screen that renders both at `--ink-3`
can no longer tell them apart — so the zero tone sits one step above the one
that means absent, and the two land within 0.1 of each other across themes so
the distinction survives a theme switch.

**Colour never stands alone.** Roughly 8% of men can't reliably tell red from
green — for a product built around red-versus-green that isn't an edge case. So
status is always carried twice: colour plus position (broken sorts to the top),
colour plus shape (the height of the heartbeat bar), or colour plus text
(`502 Bad Gateway` rather than just red).

### 2.4 Glow

```css
--glow-up:   0 0 0 1px rgba(52,211,153,.35), 0 0 7px -1px rgba(52,211,153,.6);
--glow-warn: 0 0 0 1px rgba(251,191,36,.38), 0 0 7px -1px rgba(251,191,36,.62);
--glow-down: 0 0 0 1px rgba(244,63,94,.4),   0 0 8px -1px rgba(244,63,94,.72);
```

A tight 1px ring plus a short bloom. A wide soft glow reads as an out-of-focus
smudge at this size; the ring keeps the edge of the lamp crisp while still
letting it look lit.

### 2.5 Typography

```css
--font-sans: "InterVariable", ui-sans-serif, system-ui, -apple-system, sans-serif;
--font-mono: "CommitMono", ui-monospace, "SF Mono", Menlo, monospace;
```

**Both faces are shipped by the application, not linked from a CDN.** They are
subsetted woff2 files committed under `web/public/fonts`, regenerated by
`web/scripts/subset-fonts.py`, which records the exact upstream release and its
checksum. This product is installed on private networks with no outbound
access; a typeface fetched from the internet would silently not arrive there,
and a dashboard whose type changes depending on whether the internet is
reachable is worse than one that never had the face.

| Role | Face | Version | Licence | Shipped |
|---|---|---|---|---|
| Sans | InterVariable | 4.1 | SIL OFL 1.1 | 47 kB, variable, weight axis clipped to 400–600 |
| Mono | CommitMono | 1.143 | SIL OFL 1.1 | 21 kB, regular only |

Both licences are redistributed beside the files. The subset keeps Latin-1,
Latin Extended-A, general punctuation and the handful of symbols the interface
uses; anything outside that falls back to a system face, which is the right
trade for a dashboard and not for a text editor.

The mono ships regular only because nothing that resolves to the mono role asks
for more than `--weight-strong` (500), and the `@font-face` weight range says
so, so no browser synthesises a bold that was never drawn.

**Layout features are pinned per role, in `tokens.css`, never at a call site.**
Which features an engine enables by default differs between engines, so a page
that looks right in one renders with different figures in another.

| Token | Value | Why |
|---|---|---|
| `--feat-sans` | `"calt" "ccmp" "locl" "kern" "cv01"` | only what is structurally necessary, plus the alternate figure one, whose foot serif stops a bare `1` reading as an `l` in a monitor name |
| `--feat-mono` | `"ss01"–"ss05"`, `"cv01" "cv05" "cv06" "cv11"` | the contextual sets that draw arrow and comparison sequences as one mark, plus the character variants that keep `a`, `@`, `6`/`9` and `1` apart at 13px |

`tnum` and `zero` are deliberately **off** in the sans: it sets words, and
proportional figures read better in them. The mono needs neither — it is
monospaced, so every figure is already one column wide.

**Loading uses `font-display: block`, not `swap`.** `swap` paints in a fallback
and reflows when the real face arrives. On a page someone opens and closes that
is the right trade; on a dashboard left open all day it is the wrong one twice
over, because the reflow moves a table of numbers under the eye reading it and
the fallback's metrics are not the metrics the layout was built for, so rows
change height as well as width. The two files total 68 kB, are same-origin, and
are preloaded from `index.html`, so the invisible-text period is a frame or two
rather than the 3s the browser would otherwise allow.

`src/styles/fonts.test.ts` guards the wiring — every `@font-face` file exists,
is preloaded under the same name, and is recorded in the manifest with its
upstream checksum; `src/layout/typefaces.browser.test.ts` guards the part only
a browser can see, by measuring rendered text against a family that cannot
exist. Before both existed, `tokens.css` named two faces that were never
loaded, `document.fonts.size` was 0, and every number below was tuned against
metrics nobody was seeing.

Six roles, one scale. A component never spells out a font size, for the same
reason it never spells out a colour (§2): "make everything a step larger" has to
be a single edit, and sixty hand-tuned sizes drift out of proportion the moment
one of them is touched.

**A size and its leading are one decision, so they are one pair.** Every role is
a whole number of pixels paired with a whole-pixel leading, and every rule that
sets the size sets its partner in the same block. Leading is never a ratio: a
ratio times a size lands between device pixels (`12.5 x 1.45 = 18.125`), and what
the engine then does with the fraction depends on the font and the device pixel
ratio. A stated `16px` does not have that question.

Leadings are multiples of 4 so a column of rows keeps the baseline grid. Single
line roles set the leading equal to the size — with no half-leading above and
below, vertical centring is exact, which is what a 30px list row and a 36px
control need.

| Role | Token | Size | Leading | Weight |
|---|---|---|---|---|
| Page title | `--type-page` | `24px` | `--lead-page` `32px` | `--weight-strong` |
| Card title | `--type-card` | `16px` | `--lead-card` `24px` | `--weight-mid` |
| Row title | `--type-row` | `15px` | `--lead-row` `20px` | `--weight-mid` |
| Body / label | `--type-body` | `14px` | `--lead-body` `20px` | `--weight-plain` |
| Helper text | `--type-helper` | `12px` | `--lead-helper` `16px` | `--weight-plain` |
| Section heading | `--type-section` | `12px` | `--lead-section` `12px` | `--weight-strong`, uppercase, mono |

**Helper and section share a size, and separate by face and casing.** An earlier
pass kept helper at 13px specifically to avoid colliding with section, on the
reasoning that two roles at one size would be indistinguishable. Measured
against the reference style that argument does not survive: it puts 39 of 93
elements on 12px and tells those roles apart by face, casing and weight
instead. Section is uppercase, mono and `--weight-strong`; helper is sentence
case, sans and `--weight-plain`. Those are further apart on the page than one
pixel of size ever was, and it takes the scale from four roles inside 3px down
to three — which is the crowding the scale was accused of, removed rather than
argued with.

The leadings stay different on purpose: helper sits on 16px because it is read
as running text, section on 12px because it is a single line whose leading
equals its size so vertical centring is exact.

One leading is opted into rather than inherited:

| Leading | Token | Value | Used for |
|---|---|---|---|
| Prose | `--lead-prose` | `20px` | helper-size text that wraps: empty states, error text, field help |

That is the helper role's running-text partner, not a general ratio. Body size
and up already sit on 20px, so only helper text needs the wider option; five
rules use it and each one wraps to several lines. A sixth token per size would be
the old drift wearing new names.

**Why the helper size changed.** `--type-helper` was `12.5px` — the only
fractional size in the system and the most used role in the product, which put
the engine's glyph rounding on exactly the text with the least room to absorb it.
It is now `13px` rather than `12px`: `--type-section` is already 12px, and
collapsing the two documented roles onto one size to gain a round number trades a
visible distinction for an invisible one. Rounding down would also have shrunk
the most-used text in a product where SUB-67 exists because everything was judged
too small. Page and card titles moved with it (22 to 24, 17 to 18) to keep whole
sizes without flattening the steps between the roles.

`tokens.test.ts` reads the table above and fails when the stylesheet disagrees,
when any size or leading is fractional, when a leading is not a multiple of 4,
and when any rule under `web/src` sets a font size without its paired leading.
The last of those is the one that actually rotted: counted on `bedcde0`, 56 of
the 69 rules that set a size set no leading beside it and inherited whatever sat
above them — Tailwind preflight's 1.5, or one of the old `--lh-*` ratios — so a
single size rendered at three different leadings on one screen. (SUB-74 was
filed against `dc46b9b` and cited 42 of 50; the detail view and the tag filter
landed in between.)

**Nothing is smaller than 12px.** Below that, text stops being readable at a
glance, and reading at a glance is the entire product. The one documented
exception is `--type-nozoom: 16px`, paired with `--lead-nozoom: 20px`, for the
search input and selects on phone widths: iOS Safari zooms the page in when a
focused input renders below 16px and never zooms back out (§13). That is a
platform workaround, not a typographic role, which is why it sits outside the
scale — but it is paired like everything else, because an input that inherits
its leading has the same defect as a label that does.

**The scale grew; the density did not.** Row height stays at 58px and the header
row at 34px, so a laptop still shows the same number of monitors without
scrolling (product principle 1). Larger type inside an unchanged row is paid for
out of the slack that was already there, not out of the viewport.

**Weight is a scale of four, not a dial.** The sans is a variable face whose
weight axis is continuous, so 450 is a real instance rather than a rounding that
snaps to 400 — verified against the shipped file, whose axis runs 400–600, and
against the rendered result in the browser check. That is why the middle step
exists at all: a monitor's name has to separate from the
metadata beside it without becoming a heading, and 500 at 15px is already a
heading. Anything outside these four is drift.

The scale topped out at 500, which is why the page title read flat: at 24px the
same weight that marks a 12px uppercase label carries no more authority than the
row titles under it, and the only thing separating the page title from the rest
of the screen was its size. `--weight-heavy` exists for that one role. It is
deliberately not available to the roles below it — a scale whose top step is
reachable from anywhere is a dial again.

| Role | Token | Value | Used for |
|---|---|---|---|
| Plain | `--weight-plain` | `400` | running text, helper text, table cells |
| Mid | `--weight-mid` | `450` | the name of a thing, set against its own metadata |
| Strong | `--weight-strong` | `500` | section headings, uppercase micro-labels, an emphasised count |
| Heavy | `--weight-heavy` | `600` | the page title, and nothing below it |

**Tracking is an optical correction, never emphasis.** Sans set at its default
spacing looks loose, and uppercase text set at its default looks glued together;
both are fixed by tracking. Widening mixed-case text to make it feel important
is not, and there is no token for it.

**It is decided once, on the body.** `--track-body` is set in the base layer of
`index.css`, so every element inherits the correction and no component has to
remember to ask for it. The other two tokens are exceptions, opted into where
the inherited value is wrong for the face.

| Role | Token | Value | Used for |
|---|---|---|---|
| Body | `--track-body` | `-.02em` | inherited by everything, set once on `body` |

**One value, no exceptions.** Not for uppercase, not for mono, not for badges.
This replaces five tokens — `--track-title`, `--track-name`, `--track-badge`,
`--track-caps` and a short-lived `--track-mono` — each of which was added on
reasoning that sounds right and measures wrong: titles need tightening, mono
sits on a fixed advance, uppercase needs opening up, a mono badge is already
wide.

The first two had to be spelled out per component and therefore reached 12 of
79 visible text elements; the other 67 sat at `normal`, uncorrected. A
correction that has to be remembered is a correction that is mostly absent.

The caps exception was the expensive one. `+.09em` at 12px is `+1.08px` per
letter pair, against the body's `-0.32px` — so every section label sat 1.4px
per pair wider than the rest of the interface and read as spaced-out small caps
instead of as a quiet header. Uppercase mono at 12px is already separated by
its own fixed advance; the conventional advice to letterspace caps is for
proportional faces at display sizes, and applying it here was working against
the tightening the page had already chosen.

If a future face genuinely needs its own value, it arrives with a measurement
attached, not with an argument.

The five spellings that preceded this are worth recording, because they are how
a scale gets away from you: uppercase labels in this product ran at `.02em`,
`.07em`, `.08em`, `.09em` and `.1em` — five versions of one decision, none of
them deliberate. Collapsing them to a single caps token was the right first
move; removing that token in favour of the inherited body value was the second,
and only measuring the difference made it visible.

The raw custom properties are spelled `--track-*` rather than `--tracking-*`
because `--tracking-*` is Tailwind v4's own theme namespace: a token of that
name inside `@theme inline` would have to reference itself. The raw value and
the utility binding therefore carry different names, exactly as `--canvas` and
`--color-canvas` already do.

**Mono is mandatory for anything measurable:** URLs, latency, uptime, intervals,
status codes. Numbers stacked in a column have to line up — otherwise you're not
comparing them, you're reading them.

**A face is a configuration, not a family name.** Naming the family got the
shapes and nothing else, so every property that decides how those shapes render
was left to the browser or repeated per component. Each face is now defined once
as a utility in `index.css` — `face-sans` and `face-mono` — and a component
applies the role rather than the family.

| Role | Ligatures | Numeric | Rendering |
|---|---|---|---|
| Sans | `--ligatures-sans` `common-ligatures contextual` | `--numeric-sans` `normal` | `--render-sans` `optimizeLegibility` |
| Mono | `--ligatures-mono` `none` | `--numeric-mono` `slashed-zero tabular-nums` | `--render-mono` `geometricPrecision` |

Three decisions are worth stating:

- **The mono zero is slashed.** IDs, latencies, timestamps and status codes are
  set in mono precisely so `0` cannot be read as `O`; a mono face that does not
  distinguish them is doing half its job. If the resolved face exposes no
  slashed zero the declaration is inert, which is the correct failure — the text
  renders exactly as it did before.
- **Mono ligatures are off.** `text-rendering: optimizeLegibility` was set on
  `body` and inherited by every mono element, and it enables ligatures. On a
  monospace that fuses `->` or `!=` inside a URL or an error string into a glyph
  that is no longer the characters it stands for, in the one place on screen
  where a character has to be exactly itself.
- **Tabular figures belong to the face, not the call site.** They were spelled
  out at seven places and absent from the other seven that set the mono family,
  so half the measurable text in the product did not line up. If the rule is
  "anything measurable is mono", every mono digit is already meant to be
  tabular.

These are the standard CSS properties rather than raw OpenType feature tags.
Both faces fall back to a system font on most machines, and a feature tag that
one face exposes and the next does not fails silently and differently per
machine.

**The caps legend is a role too.** The small uppercase label over a column, a
panel, a nav group or a form field is one thing, and it was written out by hand
in nine rules across five stylesheets. They drifted exactly as far as
repetition allows: three tones, two weights, sans in every one of them.

```css
@utility caps-legend {
  @apply face-mono;
  font-size: var(--type-section);
  line-height: var(--lead-section);
  font-weight: var(--weight-plain);
  text-transform: uppercase;
  color: var(--ink-2);
}
```

Two decisions inside it are not the ones the old rules made:

- **It is mono, not sans.** A legend is quiet because it is small, monospaced
  and uppercase. That is the whole mechanism, and it costs no contrast.
- **The tone goes up, not down.** Two of the nine rules faded the label to
  `--ink-4`, which measures 1.90:1 against `--surface` in dark and 1.94:1 in
  light — below even the 3:1 floor a non-text edge owes, on text that says what
  the number under it means. `--ink-3` is no better at 3.37 / 3.19. `--ink-2`
  is the first rung that clears AA for text (7.31 / 6.26), and once the face
  and the casing carry the quietness there is nothing left for the greying to
  do. Weight drops from 500 to plain for the same reason: it was compensating.

`tokens.test.ts` guards the role's contents, pins the tone to `--ink-2`, and
fails on any rule under `web/src` outside `index.css` that sets uppercase — a
rule that spells out the casing has opted out of the role, which is where the
next hand-chosen tone comes from.

### 2.6 Shape, space, motion

```css
--r-2xs: 2px;   /* small marks: the lamp, a dot, a tick */
--r-xs: 4px;    /* legacy step, not in the ladder in use */
--r-sm: 6px;    /* anything you click: buttons, inputs, nav items */
--r-md: 8px;    /* panels */
--r-lg: 12px;   /* the card that frames panels, dialogs, drawer */

--ease: cubic-bezier(.4, 0, .2, 1);
--dur:  420ms;  /* theme transition */
```

**The ladder in use is 2 / 6 / 8 / 12.** Four steps, each with a job: a mark, a
control, a panel, the card that frames panels. `--r-xs` (4px) survives for
compatibility but nothing should reach for it — at 4px a control reads as a
mark rather than as something pressable, and the gap from 2 to 6 is what keeps
those two jobs legible as different.

**One duration for everything interactive: 150ms, on `cubic-bezier(.4, 0, .2,
1)`.** A hover, a segment change and a menu opening are the same kind of event
to the person watching; giving each its own timing is what makes an interface
feel assembled from parts rather than designed. 150ms is short enough to feel
immediate and long enough to read as movement rather than as a jump. The 420ms
`--dur` is not an exception to that rule — it is the theme transition, which is
a deliberate, whole-page event rather than a response to a pointer.

Space moves in steps of 4px; §2.7 states the ladder and how it is enforced.

### 2.7 Spacing and radius

The ladder is `--space-1..16` = 4/8/12/16/20/24/32/40/48/64 and the radius
ladder is `--r-2xs/xs/sm/md/lg` = 2/4/6/10/12. Both are guarded by `tokens.test.ts`
the way colour and type already were, because both had drifted: a `7px` and a
`9px` padding, each chosen by hand to reach a rendered height that nothing
stated.

The guard has a **4px floor**. Below it a literal is a hairline, not a spacing
decision — a 1px optical nudge, the `1.5px` inset ring of an unlit lamp, the
`-1px` of a screen-reader clip. There is no token that could say those better.
The lamp's own corner used to be listed here at `2.5px`; it is now `--r-2xs`,
because a radius that shapes the product's brand mark is a decision, and a
fractional one renders differently depending on the device pixel ratio. At 4px and
above the ladder can express the value, so a literal there is either replaced
or written into the allow-list in `tokens.test.ts` with a reason.

**The concentric rule.** Where a component draws a rounded thing inside another
rounded thing, the inner radius is the outer radius minus the padding between
them. Concentric corners stay parallel; equal ones do not, and the gap between a
control and the panel around it visibly pinches at the corners. A segmented
control with `--space-1` padding inside an `--r-sm` shell therefore has an inner
radius of `6 − 4 = 2px`, which is `--r-2xs`. `tokens.test.ts` asserts it wherever
a rule states both an outer radius and its padding, because this is the kind of
rule that is obeyed once and then quietly broken by the next component.

**The exception is a card framing panels, where the outer radius rounds down.**
The monitor list is a 12px card with 6px of padding around 8px panels; strictly
concentric would put the outer corner at 14px. 12px is the better call, because
a 14px corner on a full-width card starts to read as a pill, and a list of
monitors is not a pill. The rule earns its exception here and nowhere else so
far: the pinch the concentric rule prevents is only visible when the gap is
small and the two radii are close, which is the case for a control in a shell
and not for a wide card.

**A frame around panels is padding with an edge on it, not a second panel.**
This is the whole nesting pattern: the card carries the wider radius, a fill one
step quieter than the panels it holds, and — the load-bearing part — padding, so
the panels sit inset from its border instead of flush against it. A frame
without padding puts two edges at the same level and the eye reads them as
competing; that is the double-framing that got this frame removed once already.

**The pattern is a component, not a convention.** `Card` and `Panel` in
`components/Card.tsx` are how a screen gets it, and `tokens.test.ts` asserts the
shape rather than trusting people to remember: every `--r-lg` frame must carry
padding, no box may nest at its container's radius, and no screen may define its
own card-title treatment. The last two each caught a real fault — `.mon-card`
sat at `--r-lg` inside the `--r-lg` card that came to frame it, and three
separate screens had grown their own heading rules that agreed only by luck.

**One step down per level.** Card 12px, panel 8px, anything inside a panel 6px.
Two boxes at the same radius with one inside the other read as a mistake: the
corners run parallel at the wrong offset and the inner box looks like it has
escaped its container.

A height is the intent more often than a padding is. `--control-h: 36px` is the
height a row of interactive chrome settles on; the nav item now states that and
centres its label, instead of encoding it as a padding the next edit would
round to 8px and silently shrink.

One duration for everything interactive: 150ms. A hover, a segment change and a
menu opening are the same kind of event to the person watching, and giving each
its own timing is what makes an interface feel assembled from parts rather than
designed. `--ease` is `cubic-bezier(.4, 0, .2, 1)` —
motion that decays feels mechanical rather than floaty.

### 2.8 The accent

```css
--accent:        oklch(.546 .245 262.881);   /* fill of a control */
--accent-border: oklch(.623 .214 259.815);   /* its 1px edge, one step lighter */
--accent-ink:    #ffffff;                     /* the label sitting on the fill */
--accent-ring:   rgba(43,127,255,.24);        /* focus ring, light surfaces */
--accent-ring-dark: rgba(255,255,255,.30);    /* focus ring, dark surfaces */
```

**Focus is a ring, not an outline.** A ring sits outside the border without
joining it, so a focused control keeps its own shape instead of appearing to
grow a second edge. On dark surfaces a blue ring loses contrast against the
page, so it becomes white at low alpha there — the ring's job is to say where
the keyboard is, and visibility outranks hue.

Until now the product had no accent at all: status green did the job, so an
active segment in the layout switcher and a healthy monitor read as the same
kind of thing. They are not — one is a control you chose, the other is a fact
about the world.

**The rule is a split, and it is enforced.** The accent fills *controls*: a
segment you can pick, the primary button, the focus ring. Status colour marks
*data*: a monitor's state, a heartbeat bar, a badge. Neither crosses over.
`tokens.test.ts` checks both directions, because the split collapses back into
one accent the first time someone reaches for `--up` to make a button look
lively.

The accent is **identical in dark and light.** Everything else in this file is
designed per theme; this one value is not, because an accent that shifts is a
second thing to remember and a second thing to get wrong.

Contrast, measured rather than assumed: white on the `--accent` fill is 5.25:1,
which clears AA for the label on a filled button. `--accent-border` reaches
5.30:1 on the dark canvas and 3.63:1 on the light one, so the edge and the
focus ring clear the 3:1 that WCAG 1.4.11 asks of a non-text UI component in
both themes. The fill alone does not clear 3:1 against the light canvas
(5.07:1 against `--canvas`, but it is the border that has to carry the shape),
which is exactly why the pair exists: the fill is the colour, the border is the
contrast.

Focus uses the accent ring rather than the ink scale. The old
`outline: 2px solid var(--ink-2)` was legible but said nothing — a grey ring
reads as a border that appeared, an accent ring reads as *this is the thing
you are driving*.

### 2.9 Border roles

```css
--border:         /* static: cards, dividers, panels */
--border-control: /* the resting edge of anything clickable */
--border-hi:      /* strongest: hover, active, badges */
```

Three strengths that encode **what a thing is**, not only what state it is in.
The defect this closes: `--border-hi` was used exclusively on `:hover` and
`:active`, so at rest a button carried exactly the same edge as a static card
and only looked clickable once the pointer arrived. An interactive element now
starts one step up and brightens from there.

Border width is **1px everywhere.** The one exception is the 2px status stripe
down the left of a row, which is a signal rather than an edge, and it should
stay the only one.

### 2.10 Depth is a ladder of three

```css
--shadow-flat:   none;   /* cards, rows */
--shadow-raised: /* panels, charts inside a card */
--shadow-float:  /* tooltips, menus, drawers */
```

Each rung is **two layers** — a tight contact shadow plus a wider ambient one,
both with negative spread. A single hard layer at this darkness reads as a seam
rather than as height, which is what the old single `--shadow-card` did: on a
near-black canvas, black at 40% is very nearly invisible, so the product paid
for a shadow token and got no depth from it.

`flat` being `none` is the part worth stating. It is a real rung, not an
absence: a card is the ground that panels sit on, so a card claiming its own
depth flattens the distinction it exists to create. Writing it as a token makes
"no shadow here" a decision on the record instead of a line nobody wrote.

Light gets weaker alphas than dark. The same values over white read as dirt
rather than as depth, and on a light ground the border is already doing most of
the separating.

---

## 3. The LED

The product's brand mark. If one component has to be right, it's this one.

```
20 × 7 px   ·   border-radius: --r-2xs (2px)   ·   one size for the scanned signal
```

**Why a pill and not a dot.** A horizontal shape reads as an indicator lamp on
equipment; a circle reads as a bullet in a list. It's a small difference, and it
decides whether the dashboard feels like instrumentation or like a web page.

**Why `--r-2xs` and not fully round.** At `999px` the eye has no straight line
to focus on and the shape goes soft. 2px keeps the pill silhouette but gives the
sides a readable edge. It was drawn at 2.5px until the ladder had a step this
small; the half pixel was an artefact of the missing token, not a decision.

**The highlight.** A gradient from the top, `inset: 1px 1px 3px`, opacity `.5`.
It suggests a curved lens catching light. Without the highlight it's a coloured
rectangle; with too much of it, it's a button.

### 3.1 Lit, unlit and empty

The lamp has five states, and only four of them are a colour.

| State | Looks like | Means |
|---|---|---|
| `up` | filled `--up` + glow | last check passed |
| `warn` | filled `--warn` + glow | pending, slow, or a certificate expiring |
| `down` | filled `--down` + glow | last check failed |
| `idle` | filled `--idle`, no glow, dim highlight | **there is no reading yet** |
| `off` | transparent, 1.5px `--ink-2` inset ring, no highlight | **nobody is taking a reading, on purpose** |

`idle` and `off` used to be the same grey lamp, which broke rule 4 outright: a
monitor somebody paused by accident looked exactly like one that had just been
created and had not run yet. Either misreading is expensive — the first hides an
unwatched service for weeks, the second sends you looking for a bug in the
scheduler.

**Why shape and not a fifth colour.** A new hue would carry no meaning a viewer
could guess, would have to survive both themes at 3:1, and would spend the one
remaining colour the palette has on a *non*-event. Filled-versus-hollow is the
convention for exactly this distinction (Carbon calls it outline-versus-filled;
HubSpot's status tag calls the prop `hollow`), it reads in greyscale, and it
survives a screenshot in a chat window.

**Why the ring is `--ink-2` and not `--idle`.** A ring in the idle grey measures
2.5:1 against the canvas in dark mode and 1.7:1 on a light card, both under the
3:1 floor WCAG 1.4.11 sets for non-text contrast. `--ink-2` clears it in both themes, and it puts the
empty lamp's edge *brighter* than the filled idle lamp — so the two stay apart
in greyscale, not just in hue.

**The ring is an inset `box-shadow`, never a `border`.** A border would grow the
20x7 box and break the one-size rule above.

**Paused also gets a second signal per layout**, because a 20x7 lamp is not
enough on its own once a list is 200 long: rows, cards and compact lines take a
2px **dotted** `--ink-3` leading edge (down is solid `--down`, pending solid
`--warn`), and a wall card switches its border to **dashed**. Solid means "look
at this", dotted and dashed mean "this is deliberate". `--ink-3` and not
`--ink-4` for the same contrast reason: 3.4:1 / 3.2:1 against `--surface`
versus 1.9:1.

**Reserved for maintenance windows (SUB-33).** A monitor inside a maintenance
window is the same hollow socket with a `--warn` ring: still not being measured,
but on a timer rather than indefinitely. Documented here so the state is a
decision rather than an improvisation later; not implemented yet.

### 3.2 The two documented sizes

| Size | Where | Why |
|---|---|---|
| **20 × 7 px**, radius `--r-2xs` | the status lamp in a list, card, wall tile or group header | it is the signal being scanned, and scanning is what sets the size |
| **12 × 5 px**, radius 1.5px | a lamp sitting *inside* a text badge | it is punctuation on a line of 11px text, not a signal in its own right |

These are not two versions of one lamp; they are two different jobs. The 20x7
lamp is the thing your eye lands on when you sweep a list of 200 monitors, so it
has to win against the name beside it. A lamp inside a badge is the opposite
case: the badge already says `Up` in words, and the lamp is there to carry the
colour and the hollow/filled distinction at text scale. Drawn at 20x7 it would
be wider than the word it belongs to and would turn a quiet inline label into a
second alarm.

The badge lamp is the **only** exception, and it exists because the badge's own
type size sets it. Anything else — a lamp that grows in a card and shrinks in a
row — is decoration, and the one-size rule below still bites.

**The lens highlight scales with it.** At 12x5 the 20x7 highlight inset
(`1px 1px 3px`) leaves a one-pixel core and the lamp reads as an empty outline,
so the badge lamp insets `1px 1px 2px` instead. The highlight is meant to
suggest a curved lens; below a visible core it just erases the colour.

**Neither size is an inline style.** Both live in a rule, because a size that
carries meaning has to be changeable in one place — repeating `12px` on every
badge is how a documented decision quietly becomes an accident.

**Rules.**

- One size across the entire app — sidebar, rows, cards, group headers, toasts.
  The inline badge lamp in §3.2 is the single documented exception, and it is a
  rule, never an inline style.
- The LED **never** blinks on a routine check. Only a real status change plays a
  single short transition (`.changed`), and then it's done.
- No pulsing animation at rest. Fourteen pulsing lamps aren't a dashboard,
  they're a screensaver.

---

## 4. The heartbeat bar

It carries two things at once, deliberately:

- **Colour = status** of that check.
- **Height = latency**, relative to that monitor's own range.

So a single glance answers both "is it running" and "is it getting slower". A
failed check draws at **full height** — absence has to be loud, not look like a
fast response.

Newest check sits on the right. The last 28 checks are visible; that's enough to
see a pattern and few enough that individual bars stay distinguishable.

---

## 5. Live checks

The scheduler runs continuously; the SSE stream delivers every result as it
lands. Showing that is the difference between "this page is alive" and "this
page quietly died an hour ago".

**What happens when a check arrives:**

1. The newest heartbeat bar slides in — enters at 35% height, slightly
   overshoots, settles back, with a brief flare (520ms). The oldest bar drops off.
2. The latency figure ticks along with it (320ms), because that's the number the
   check produced.

**What emphatically does not happen:**

- The LED doesn't blink along. See rules 1 and 4: if the LED also reacts to every
  check, blinking stops meaning anything.
- There is **no row-wide flash or sweep**. A tint across the whole row is a
  large, low-frequency change that reads as "this row is doing something" — and
  that's the language reserved for a status change. I tried it and deliberately
  rolled it back.

The effect can be switched off in the layout settings ("Show live checks",
remembered per user) and turns itself off under `prefers-reduced-motion`.

---

## 6. Losing the connection

The dashboard is fed by SSE. When that stream drops, the naive implementation
keeps every LED at its last known colour and the screen quietly asserts "all
good" for as long as the tab stays open. **A monitoring tool that lies
confidently is worse than one that is visibly broken** — the entire promise of
this product is that you can trust the screen.

**The rule: when it stops knowing, it stops asserting.** Colour is a claim about
reality, so colour is what drains.

What happens when the connection is lost (`body[data-conn="stale"]`):

- Every LED and heartbeat bar desaturates to ~18% and loses its glow, over
  600ms. Slow on purpose: this is not an alarm, it is a withdrawal.
- Latency figures and the summary counts drop to `--ink-3`.
- Live check animations stop — they would be fiction.
- A warm banner appears above the toolbar with a live "since" counter and a
  **Reconnect now** button.

**The banner is warm, not red.** Red would be a false alarm: the monitored
services may well be perfectly healthy. It is *the dashboard's* knowledge that
failed, not
their infrastructure. Red is reserved for things that are actually down.

**Nothing is hidden and nothing moves.** The last known state is still useful —
it just stops being presented as current truth. No modal, no overlay, no
skeletons replacing real data.

**Status wall** has no chrome to put a banner in, so the whole canvas carries
the message: a 2px warm border around the viewport and a suffix on the header
line. A wall that cannot be trusted must not look calm — but it must not grow a
toolbar either. The rule above applies to every other claim on that screen too
(SUB-64): the warm card borders drop back to the neutral border, and the down
count is relabelled "N down, last known" in `--ink-3` rather than shouted in
`--down`. From across a room a red edge reads as "that one is broken right
now", which after the stream died is precisely the assertion it can no longer
make — it may have recovered, or nine more may have joined it.

**A first load that is still in flight, or that failed, keeps the wall's own
frame (SUB-64).** The wall renders its header, clock and visible exit and puts
the sentence inside them, instead of falling through to the dashboard's
chrome-less loading or error view. A wall display is usually a machine nobody
is sitting at; a bare sentence with no way back out is the worst state it can
reach.

In production the trigger is SSE `readyState` **plus a watchdog**: if no event
arrives within ~2.5 check intervals the view is stale regardless of what the socket
claims. A TCP connection that is open but silent is the failure mode that fools
naive implementations. Reconnect uses exponential backoff, but the button is
always available — someone staring at a broken dashboard should never have to
wait out the backoff timer.

---

## 7. Layouts

Layout is a **user setting**, not a design decision made on everyone's
behalf. Four views of the same data, picked from the grid icon in the toolbar,
remembered per user.

| Layout | For | Character |
|---|---|---|
| **Rows** | Laptop, daily scanning | One row per monitor, broken sorts to the top |
| **Cards** | Second screen on the desk | Tiles, bigger LEDs, fewer words |
| **Compact** | 100+ monitors | Grouped by customer/environment, one line per monitor |
| **Status wall** | Screen on the wall | LED and name, nothing else |

**Status wall** hides both the sidebar and the topbar entirely. That's the whole
reason it exists. Two things remain: a whispered line with the name and a count,
and a clock. The clock isn't decoration — on a screen that almost never changes,
a ticking second is the only proof you're looking at something alive.

In Status wall, broken cards get a **warm border**, not a coloured fill. A wall
full of coloured cards is noise; a wall of quiet cards with two warm borders is
information.

**Cards chooses its own column count.** At one card per row the layout spent
most of a wide screen on nothing: the heartbeat was pushed against the right
edge with a dashed rule crossing the empty middle. The toolbar offers 1, 2, 3
and *fill the width*, as a segmented control drawn in icons — the options are
shapes, so the button shows the layout it selects rather than naming it. Each
one still carries an accessible name and a tooltip, because a bar chart is only
obvious to someone who already knows what the control does.

The control appears only while Cards is on screen, keyed off the *effective*
layout rather than the stored one: a setting visible while it governs nothing
teaches people it does nothing. The count is stored under its own key so it
survives a trip through Rows and back.

**It lives in the dashboard's tools row, not in the shell's topbar.** The
topbar holds what is true on every screen — add, layout, workbench, theme — and
a second row under it holds what belongs to *this* screen: search, the status
filter, and whatever the current view brings with it. The split is not
tidiness. A view-specific control in the topbar appears and disappears inside a
right-aligned group, which slides everything before it sideways — measured at
121px, including the layout switcher the user had just clicked. Chrome that
moves out from under the cursor is what §10's no-transform rule exists to
prevent, and it applies to the toolbar too.

The page title is visually hidden rather than deleted: the card below already
says "Monitors (2)", so printing the word twice was the duplication this row
was rearranged to remove — but the `h1` stays in the outline, because the
detail view uses `h1` for a monitor's name and a screen reader needs a level-1
landmark on the busier of the two screens.

There is no breakpoint behind it. The grid asks for "at most N columns, never
narrower than 380px", so a narrow window drops to as many as genuinely fit —
one column on a phone because one is what fits, not because a media query
overrode the setting. 380px is measured: below it the card's facts row starts
wrapping, and a grid whose cells wrap internally is worse than one column
fewer.

**Sidebar.** Collapsible in every layout, via the button on the left of the
topbar or `Cmd/Ctrl + B`. Collapsed it becomes a 56px rail with icons only — not
gone, because then your navigation is unreachable. Status wall hides it
completely and remembers your preference separately, so you get the rail back
exactly as you left it.

**The narrow viewport keeps a veto (SUB-64).** Layout is a user setting, but
Rows and Compact both put five facts on one line and that is precisely what does
not fit below 640px (§13). Below the breakpoint both fall back to Cards, which
keeps every fact the row shows; Cards and Status wall are honoured at every
width. The stored preference is *not* rewritten when this happens — opening the
dashboard on a phone must not change what the desktop shows tomorrow — and the
toolbar shows the layout actually on screen rather than the overridden one.

**The counts are the status filter (SUB-65).** At 200 monitors the question is
almost never "show me everything", it is "show me the two that are down" — and
the four counts in the header already say exactly how many of each there are.
Making them the control means the answer and the way to reach it are the same
target, instead of a second filter row that duplicates numbers already on
screen. They are toggles rather than a radio group, so the way back to the full
list is the chip you just pressed. The counts themselves are always computed
from the *whole* list, never the filtered one: chips that vanished as soon as
you used one would erase both the way back and any idea of what else is going
on. Pressed state is carried by a filled surface, `aria-pressed`, and the
sentence under the search box — never by colour alone (§9). The choice is
component state and does not persist: returning to a dashboard that silently
hides 198 of 200 monitors is how an outage gets missed.

**Grouping is opt-in, on one tag key at a time.** A "Group by" control next to
the tag filters puts one headed section per value of the chosen key over the
list, in all three layouts that `Dashboard` renders; without a key the list
stays flat, ordered by the shared `partition()`. Flat is the default because a
permanent set of headings costs vertical space in the layout that exists to
save it, and at this density the broken monitors are already the first lines on
the screen. Compact still drops the heartbeat bar either way — 40 rects x 200
monitors is the cost that layout exists to avoid.

Three decisions grouping could not be built without:

- **The attention section survives grouping and is taken out first.** A down
  monitor sorted into its environment's group would sit wherever that group
  happens to fall, possibly off-screen, and "needs attention" would stop
  meaning the same thing in every layout — the one invariant `partition()`
  exists to hold. The groups below therefore describe the monitors that are
  fine, which is what grouping is actually asked: "how is production doing",
  not "where is the broken one". Every heading carries its own count, so the
  numbers add up to the list and none of them claims to be the whole.
- **Monitors without the key land in a trailing `Untagged` section**, never
  dropped. Grouping is not filtering: everything handed in comes back out. That
  section is last and is the only one whose position is not alphabetical — it
  is a residue, not a value.
- **One key at a time.** "By environment" and "by customer" are two
  arrangements of the same rows, not two that can be layered. The key is local,
  unpersisted state like the status chip, and it falls back to flat when the
  tag disappears from the data.

**The sidebar does not advertise what does not exist (SUB-64).** The four
unbuilt destinations are rendered as plainly unavailable — dimmed, not pressable,
each saying "Soon" in words rather than by colour alone — and the mockup's "2
incidents" badge is gone. A badge claiming open incidents that goes nowhere is
indistinguishable from a real alert.

---

## 8. Components

See `docs/mockups/components.html` for a working version of everything below.

### 7.1 Buttons

| Variant | Use |
|---|---|
| `primary` | The one action the screen is for. One per screen. |
| `secondary` | Side actions of equal weight |
| `ghost` | Cancel, close, anything that takes the user back |
| `danger` | Delete and other irreversible actions |

**Destructive is a red outline, not a red fill.** A bright red button sitting on
screen permanently becomes wallpaper — and then somebody clicks it by accident.
The fill only arrives on hover, once the intent is already there.

Loading state: the button keeps its width and swaps the label for a spinner. That
way the layout doesn't jump the instant you click.

### 7.2 Forms

The **60-second test** is the bar: paste a URL, everything else has a sensible
default. Every required field costs installs.

- Focus is a green border plus a 3px ring at 14% opacity — visible without the
  blue browser glow.
- Errors sit under the field, with an icon, and name the correct format instead
  of just announcing that something is wrong.
- Units (`sec`, `ms`) belong in an addon on the field, not in the label.
- Three or four mutually exclusive options: segmented control, not a dropdown.
  Visible options are cheaper to read than hidden ones.

### 7.3 Switches

A toggle is for something that takes effect immediately (monitor on/off). A
checkbox is for a choice that only counts once you save. The thumb travels, the
track fills — otherwise someone clicks twice because they can't tell it landed.

### 7.4 Inline editing

Renaming a monitor shouldn't be a modal. Click the name, type, `Enter` commits,
`Esc` reverts. A subtle pencil appears on hover; without it nobody knows it's
possible.

### 7.5 Destructive confirmation

Deleting asks you to **retype the name**, not to answer "are you sure?". The
second is a reflex, the first is a decision. The dialog spells out exactly what
disappears, including open incidents.

### 7.6 Empty, loading and error states

This is the difference between finished and nearly finished.

- **The empty dashboard is the onboarding.** It's the first thing a new
  self-hoster sees. It deserves a real design, not a shrug.
- **Skeletons, not spinners.** A spinner announces a wait that isn't happening.
  Skeletons keep the page still.
- **Toasts confirm, they don't inform.** They disappear after 5 seconds and never
  carry information you'll still need.

### 7.7 Command palette

`Cmd/Ctrl + K`. Past 100 monitors, searching beats scrolling. It holds both
monitors and actions, because the user doesn't know which of the two they're
after.


### 7.8 Segmented control

Three or four mutually exclusive options, all of them visible. §7.2 already
chose this over a dropdown for forms; it is the same control in a toolbar.

**One selected state: `--accent` fill, `--accent-border` edge, `--accent-ink`
label.** Every segmented control in the product, regardless of what it governs.

This section used to specify two variants — a neutral `--surface-hi` selection
for controls that change the *view* (layout, density) and the accent for ones
that change *what data* you see (range, filter). That rule is withdrawn. It was
reasoned from first principles rather than measured, and the reference
contradicts it: its own range selector is a view control by exactly that
definition and still fills the active segment with the accent.

What the rule produced was worse than inconsistent. A selected segment drawn as
a grey box reads as *disabled*, not as chosen, and with both variants on one
toolbar the layout switcher and the theme toggle disagreed about what selection
even looks like. A rule that has to be explained before the control can be read
is not a rule worth keeping.

**An inactive segment is fully transparent, its border included.** The border
is declared at rest in `transparent`, so the box already occupies the space the
selected state will need and nothing shifts by a pixel when the selection
moves. A control that only adds a border when pressed nudges every label beside
it each time you press it.

**The bar carries its own 1px edge.** Without it the segments read as loose
buttons sitting in the toolbar rather than as one control with a selection in
it — which is what ours did.

**The inner radius is concentric, not equal.** Per §2.7 an inner radius is the
outer minus the padding. Measured on the reference: `--r-md` (8px) outside, 2px
of padding, `--r-sm` (6px) on the segment — `8 − 2 = 6`, exactly concentric.
This is the case the concentric guard in `tokens.test.ts` was landed for.

### 8.1 The chip family

Five kinds, and one rule holds them apart:

**A dashed edge means the chip is *about* the data; a solid one means the chip
*is* data.** Incomplete, absent, or not yet assigned. The product already had
this signal available and used it nowhere, which is why a bucket with two
readings in it and a bucket with forty looked identical.

| Kind | Drawn | Says |
|---|---|---|
| status | filled with its own status colour, label from `--on-*` | this is up / down / slow |
| count | neutral fill, mono, tabular | how many of the thing beside it |
| meta | label and value split by an internal divider | one named fact |
| state | **dashed** border, no fill | something about the data: partial, missing |
| empty avatar | **dashed** circle | nobody is assigned |

The status badge does not take its label colour from the ink scale — see §2.3.
The metadata chip splits label from value with a 1px divider rather than a
colon, because a row of them is then scanned by finding the same vertical line
in each instead of parsing punctuation.

### 8.2 The icon tile

32x32, radius `--r-sm`, filled neutral, **no border**. It anchors the left of a
card header. The border is omitted deliberately: the tile is a background for a
glyph, and an edge around it competes with the card's own edge two pixels away.

The glyph inside is hidden from assistive technology unless it is the only
thing saying what the row is. A tile beside a monitor's name that announces
"globe" adds a word carrying no information.

---

### 8.3 The readout tooltip

A tooltip here is a small table, not a sentence. The parts, in order:

| Part | Drawn |
|---|---|
| header | mono timestamp or range, with the unit named once beside it |
| divider | full width, padding included — it separates, it does not underline |
| row | status marker, mono caps label, right-aligned mono value |
| total | closing summary, set apart by its own rule |
| partial | dashed `Partial data` chip (§8.1) when the window is incomplete |

**The unit is named once in the header, not on every row.** A tooltip whose
rows all end in `ms` spends a third of its width repeating one word.

**Values are right-aligned in their own grid column.** That is the whole reason
the readout is a grid: numbers line up on their last digit and become
comparable without being read.

**A row with no status keeps the marker column as an empty spacer.** A neutral
dot beside a coloured one reads as a status of its own, so the spacer is
transparent and only the alignment survives.

#### A partial bucket must not look healthy

The heartbeat bar folds many checks into one column. A column holding two of
the forty checks its window should contain was drawn exactly like a full one,
so a gap in the history read as a healthy stretch — the one thing a monitoring
tool must not do. The dashed chip says so, and dashed already means *about the
data* (§8.1), so it needs no legend.

Whether a column is partial is arithmetic, not a flag from the server:

* The cadence is the **median** gap across the whole series. The mean is
  dragged up by exactly the outages this is meant to catch, and a per-bucket
  cadence lets a bucket that lost nine of every ten checks derive a tenfold
  interval and call itself complete.
* It is read from the stored history rather than the monitor's configured
  interval, because a monitor whose interval changed last week still has
  history at the old cadence, and the configured number would mark all of it
  partial.
* A column is partial below **90%** of its expected count. Not 100%: checks do
  not land on the second they are scheduled, so the newest bucket of a live
  series is routinely one check short of the arithmetic, and flagging that
  would make the marker mean nothing.

---

## 9. Accessibility

Not an afterthought — several of the decisions above exist precisely for it.

- **Never colour alone.** See §2.3.
- **Contrast:** `--ink` and `--ink-2` clear AA against their backgrounds.
  `--ink-3` is for labels and helper text, `--ink-4` exclusively for placeholders
  and disabled state — never for text meant to be read.
- **Focus is always visible.** `:focus-visible` gets a ring, including on toggles
  and custom controls. Never `outline: none` without a replacement.
- **`prefers-reduced-motion`** disables live checks, status transitions and every
  transition. The information stays, only the movement goes.
- Keyboard: `Esc` closes the drawer, the dialog and the palette, and exits Status
  wall. Anything clickable has to be reachable with Tab.

### 9.1 The status word in list layouts

`up` shows a lamp and nothing else. Every other status shows its name beside the
lamp, in caps at `--type-section` and `--ink-2`.

The card layout always said the word; rows and compact lines did not, so `down`,
`pending` and `paused` were red, amber and grey at the same 2px leading edge and
the same filled 20x7 pill. A screen reader was fine — the status is in the row's
accessible name — and the reader this rule exists for, the sighted person who
cannot separate those hues, had nothing at all.

`up` stays wordless on purpose: printing "Up" down 190 rows would bury the three
that matter, and *absence* of a word is a non-colour signal too. `--ink-2` rather
than the `--ink-3` §9 reserves for labels, because this word is measured at
2.6:1 in `--ink-3` against the row surface — under the 4.5:1 AA floor. It is not
a caption next to a value; for the reader it was added for, it **is** the status.

### 9.2 The heartbeat bar is an instrument only once per page

In the detail view the bar takes focus, walks its columns with the arrow keys and
carries a `<table>` of every slot for assistive technology. In the list layouts it
is `aria-hidden`, has no `tabindex` and emits no table — the pixels only.

Repeating the interactive version per row put **402 tab stops** in front of the
last row's link at 200 monitors and **32,831 DOM nodes** on the page against the
~11k the decision not to virtualise (§10) is based on. None of that added a fact
the row does not already state in text. Hover still works in lists: pointing at a
column promises nothing to assistive technology.

### 9.3 Navigation says so

A route change sets `document.title` (page first, product name last — a tab is
truncated from the right), and moves focus to `<main>`, which carries
`tabIndex={-1}` for the purpose. Not on first render: on arrival focus belongs
where the browser put it. A visually-hidden skip link is the first focusable
element on every screen, clipped rather than `display: none` so it can still take
focus, and withdrawn while the drawer is open because there is nothing to skip
to.

`jsx-a11y` runs in CI as part of `npm run lint`, which fails on a warning.
`prefer-tag-over-role` is off: it asks for `<output>` where the code has
`role="status"` on a `<div>`, and for `<fieldset>` in place of `role="group"` on
a segmented control, neither of which is an accessibility improvement here.
`no-autofocus` is waived at exactly one call site, the setup screen, which is a
page with one field on it.

---

## 10. What this design does not do

Just as important as the rest, because these are the ones that keep coming back:

- **No pulsing animations at rest.** See rule 2.
- **No off-the-shelf chart library.** The heartbeat bar is the brand mark;
  generic charts look generic.
- **No default shadcn look.** shadcn/ui is the starting point, not the finish
  line. If it looks like every other shadcn dashboard, it failed.
- **No colour fills for status where a border does the job.** See §6.
- **No setting that only works in one view.** I tried an S/M/L density slider
  and removed it: it only did anything in Status wall, and a control that usually
  does nothing teaches people that controls do nothing.
- **No virtualised monitor list.** 200 rows is roughly 11k DOM nodes, which the
  browser handles fine; a window would break `Ctrl-F`, screen-reader row counts
  and table semantics to save render work that memoised rows already save.
  See §12 (Scale).

---

## 11. From mockup to code

The mockups are standalone HTML files with no build step. When implementing in
React + Tailwind v4:

1. **Tokens first.** §2 becomes a single `@theme` block. Not one component
   contains a literal hex value.
2. **The LED becomes a component**, not a set of utility classes. One place where
   size, radius, highlight and glow are pinned down.
3. **Layouts are a user setting** in the database, not a route.
4. **Live checks come from the existing SSE stream.** The simulation in the
   mockup (`landCheck()`) shows what should happen for each incoming result.
5. This document gets updated in the same PR as the deviation. A styleguide that
   lags behind is worse than no styleguide.

---

## 12. Known gaps

An honest list of what the mockups do *not* yet answer. Written down so it stays
a decision instead of an oversight. Roughly in order of how much it would hurt
to discover late.

### Blocking — the design does not survive without these

- **Spacing and radius are not enforced.** `tokens.test.ts` mechanically
  guarantees that `tokens.css` is the only source of colour, font size, line
  height, weight and letter spacing. Spacing and radius have no such guard, and
  three values have already been written by hand to hit a target height:
  `shell.css:93` `padding: 7px`, `:207` `4px 9px`, `:290` `4px 7px`. The ladder
  is 4/8/12/16/20/24; 7 and 9 are not on it, and a measured 8px radius is not on
  the radius ladder (4/6/10/14) either. These are not cosmetic slips — each was
  chosen to reach a specific rendered height, which is reasoning that belongs in
  a token rather than buried in a padding value somebody will later "tidy up".
  The 20x7 LED with its `--r-2xs` radius (§2.4) is the counter-example: outside the
  ladder, argued for in writing, and therefore an exception rather than a leak. A
  guard is what keeps those two apart.
- **Optical correction barely lands.** Four tracking tokens exist but 67 of the
  79 measured elements sit at `normal`. Dense interface type at 12–15px usually
  wants a slight negative tracking as a single decision on the body, with the
  caps token as the one exception on top. Four tokens that rarely apply are not a
  system; they are the appearance of one.
- ~~**Depth is a single value.**~~ Answered in §2.10: `--shadow-flat`,
  `--shadow-raised` and `--shadow-float` are three rungs by role, and
  `tokens.test.ts` rejects any `box-shadow` that is not one of them or a glow.
  The remaining gap is that only two of the three rungs have a component today
  — nothing is on `raised` until panels exist.

- ~~**Mobile.**~~ Answered in §13: below 640px the dashboard renders a card per
  monitor instead of a row, keeping every fact the row shows. The remaining
  mobile gap is the screens that do not exist yet on any width.
- **Scale.** The mockup shows 14 monitors; the target audience runs 10–200.
  Partly answered. The three searchable layouts — rows, cards and the compact
  list — filter in `Dashboard` and only then partition, so "needs attention
  first" still means the same thing while filtering. The status wall is the
  exception: it bypasses `Dashboard` entirely and has no search field, so it
  always shows everything. Virtualisation was considered and rejected on two
  counts. It would cost more than it saves: 200 rows is roughly 11k DOM nodes,
  which the browser handles fine, while a windowed list breaks `Ctrl-F`, breaks
  screen-reader row counts and breaks table semantics (recorded on
  `MonitorRow`; `MonitorTable.test.tsx` holds all 200 rows in the DOM to keep
  those three true). And the render work it would save is work it never does anyway: a
  heartbeat tick re-renders the one memoised row, card or compact line it
  belongs to, not all two hundred. The wall's tiles are not memoised, but a
  tile is an LED and a name. Filtering on tags now works in those same three
  layouts: one native `<select>` per tag key, ANDed across keys, with the
  options derived from the unfiltered list so choosing a value never removes
  the way back. Grouping by a tag key ships alongside it, opt-in, with the
  attention section lifted out above the groups (§7).

### Screens promised but not designed

The sidebar advertises five destinations; one exists.

- **Incidents** — the badge says 2, there is no screen. Needs at least:
  acknowledge, resolve, a cause, and a note.
- **Monitors** — the management list. Bulk actions (pause 20 monitors at once)
  have no design; today that would be 20 individual clicks.
- **Notifications** — channel configuration (Discord, Slack, Telegram, webhook,
  SMTP), a test button, and a per-channel routing rule.
- **Settings** — users, roles, API keys, retention.

### Smaller, but they will come up

- **Maintenance windows** are a v0.1 feature. The visual state
  is decided (§3.1: a hollow lamp with a `--warn` ring) but nothing in the data
  model says a monitor is in a window, so it is not implemented.
- **Error toasts.** Only the success path is designed. A failed save, a rejected
  form, a check that cannot start — none of those have a visual.
- **Tags/groups.** Tags exist end to end now — column, API field, frontend type
  — and the dashboard both filters and groups on them (§7). One piece is still
  open: there is no screen to create, rename or assign a tag outside the
  create/edit form, so a typo in a key is fixed one monitor at a time.
- **Keyboard shortcuts** exist (`⌘K`, `⌘B`, `Esc`) but are undiscoverable. Needs
  a `?` overlay.
- **Onboarding beyond the empty state.** First run, creating the first user,
  what the very first minute after `docker run` looks like.

### Deliberately out of scope for now

Status pages, config-as-code YAML, multi-region and on-call schedules are all
post-v0.1. Not designing them yet is correct — but the
navigation should not promise them either.

---

## 13. The phone layout

The phone is not a narrower desktop. You open SubGlance on it *after* an alert
fired: standing somewhere, one-handed, wanting one monitor's story. The desktop
screen is built for the opposite task — scanning a column across 200 rows — so
the two get two layouts rather than one stretched one.

**Breakpoint: 640px.** A layout decision, not a device one: below it the
five-column row can no longer hold a readable name, a heartbeat and two numbers
at the same time. The number lives in `web/src/layout/useMediaQuery.ts` as
`COMPACT_MAX_WIDTH` and in `web/src/monitors/monitors.css`; a test asserts the
two agree.

**One monitor is one card.** Status word and lamp on top, then the name, the
target, the heartbeat bar full width, and latency plus 24h uptime as a labelled
pair at the foot. Nothing from the row is dropped — dropping the heartbeat and
the uptime is exactly what made the old behaviour "a list of names" (§12).

**Rejected alternatives, and why.**

- *Horizontal scroll with a frozen first column.* Preserves the grid, but asks
  someone on a platform to swipe sideways to find the column holding the answer.
  The wrong trade for a screen you open in a hurry.
- *Hide the low-priority columns.* There are no low-priority columns here.
  Latency and uptime are the product.
- *CSS-only reflow of the same `<table>` (`display: block` on the cells).* The
  popular trick, and it silently drops table semantics in Safari — the same
  hazard `MonitorTable` already warns about. The card list is a real `<ul>`.
- *Render both and hide one with CSS.* Doubles the DOM at 200 monitors, doubles
  every heartbeat bar's `ResizeObserver`, and gives a screen reader each monitor
  twice. React picks one component; only one is ever mounted.

**Labels become visible.** A table cell inherits its noun from the column
header. A card has none, so `120 ms` alone is a number without a meaning — the
card writes `Latency` above it.

**Names wrap, they do not ellipsis.** On the desktop a truncated hostname is
recoverable by widening the column; on a phone it is not, and the name is the
heading of the thing you came to read. Two lines of card is the cheaper cost.

**The search field is 16px.** Not a typographic choice: iOS Safari zooms the
page when a focused input is smaller, and does not zoom back out on blur.

**Ordering is shared with the desktop.** Both layouts call the same
`partition()` — down first, then alphabetical — so "needs attention" means the
same thing on both screens.

**Navigation is a drawer, not a rail.** On a laptop the sidebar collapses to a
56px icon rail, which keeps every destination one click away for almost no
width. At 375px that same rail is 15% of the screen, held permanently, and
spent mostly on the four destinations that do not exist yet (§12). Below the
breakpoint the rail therefore leaves the grid entirely — the grid becomes a
single column — and the same `Sidebar` is rendered over the content as an
overlay drawer, opened by the same topbar button that collapses the rail above
the breakpoint.

That button's accessible name changes with it: on a phone it reads "Open
navigation", not "Collapse sidebar", because there is no sidebar on screen to
collapse. One control, two truthful names.

**The drawer is modal, with the obligations that implies.** The page beneath it
is marked `inert` rather than wrapped in a hand-written focus trap: it is the
platform's own mechanism, it cannot miss a control added later, it also hides
the covered content from assistive technology, and it still lets Tab reach the
browser's own chrome — which a keyboard user is as entitled to as a mouse user.
Focus moves to the close button on open (a way out, announced before five
destinations) and returns to the opener on close. Esc closes it, and takes
priority over Esc's other meanings: the drawer is the newest and most modal
thing on screen. Body scrolling is frozen while it is open, so closing it never
reveals a page that moved while you were not looking.

The drawer is rendered only while open, rather than parked off-screen with a
transform. An off-screen drawer keeps its links focusable and its text readable
to a screen reader, which is the most common way this pattern is got wrong.

It also closes itself when the viewport grows past 640px — rotating a phone to
landscape crosses it — so the scrim never lingers over a layout that has room
for the rail again.

**The detail view fits because the heartbeat measures its own panel.** Every
component that draws a heartbeat takes a `beatWidth`, and that number is only a
fallback for environments with no layout to measure — jsdom and SSR report
every element as 0 wide. It is deliberately not a layout choice: the width a
bar should have depends on the viewport, which the caller does not know. When
it did act as an override, the detail view drew its 720px desktop bar inside a
317px panel and pushed a 375px page out to 746px — the horizontal scrolling
this section exists to prevent, on the screen it matters most. Measured in a
real browser at 320, 375 and 414px: the bar now buckets to 29, 35 and 39
columns and the page scrolls only downward.

**The back link is 24px tall, not 19.** WCAG 2.2 SC 2.5.8 asks 24 by 24 CSS
pixels and exempts targets inside a sentence; this one sits alone on its own
line, so the exemption does not apply. The height is added under the text
rather than as padding around a box, so it keeps reading as a link — the
affordance is the arrow, not a filled rectangle.

**Rejected here too:** a native `<dialog>` with `showModal()`, which would give
the inert background and Esc handling for free. jsdom 30 still does not
implement `showModal`, so every test of the drawer would have to be skipped or
stubbed — the behaviour that most needs to stay honest is the accessibility
behaviour, and buying it at the price of never testing it is the wrong trade.
Revisit when jsdom ships it.

### Size is checked in a real browser

jsdom has no layout engine. Every element reports a width of zero,
`getBoundingClientRect` returns zeros and media queries never evaluate, so no
assertion about *size* can fail there. That is not a gap in the tests but a
property of the environment, and it hides exactly the class of bug this section
is about: the detail view once shipped drawing a 720px heartbeat bar inside a
317px panel, pushing a 375px page out to 746px, while the whole suite stayed
green.

So a second, small suite runs the built bundle in headless Chromium at 320, 375
and 414px — `web/src/layout/phone-layout.browser.test.ts`, behind
`npm run test:browser`. It asserts three things per screen:

* the page does not scroll sideways (`scrollWidth` equals `clientWidth`);
* no element that the user can actually see extends past the viewport;
* every visible control is at least 24 by 24 CSS pixels (WCAG 2.2 SC 2.5.8).

**Screenshot diffing was rejected.** Font rendering differs per platform, so
every legitimate design change turns into a pile of blessed images and the
suite becomes something people re-bless rather than read. A horizontal overflow
is a number that is either bigger than the viewport or is not; it never needs
blessing, and when it fails it names the element.

**Two exemptions, both load-bearing.** Content inside a clipping or scrolling
ancestor does not count as overflow — the heartbeat bar's accessibility table
is 1370px wide inside a clipped container, read by screen readers and never
painted, and counting it would fail every screen for something working as
designed. Visually-hidden inputs are exempt from the target-size rule for the
same reason: the theme control is `sr-only` radios inside labels, where the
label is the target and the input's 1x1 box is its clipping rectangle.

Keeping these out of `npm test` is deliberate. They need a built bundle and a
browser download, and a unit suite that depends on either is one that people
stop running.
