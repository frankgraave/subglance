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
--canvas:     #08090a;   /* page background */
--surface:    #0e1011;   /* card, sidebar, drawer */
--surface-2:  #141719;   /* inputs, hover */
--surface-hi: #1a1e20;   /* active segments, tracks */
--border:     #1e2224;   /* default border, divider */
--border-hi:  #2a2f32;   /* border on interactive elements */

--ink:        #e8eaec;   /* primary text */
--ink-2:      #9ba1a6;   /* secondary text */
--ink-3:      #61686d;   /* labels, help text */
--ink-4:      #3d4347;   /* placeholders, disabled */
```

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
--font-sans: "Geist", ui-sans-serif, system-ui, -apple-system, sans-serif;
--font-mono: "Geist Mono", ui-monospace, "SF Mono", Menlo, monospace;
```

Six roles, one scale. A component never spells out a font size, for the same
reason it never spells out a colour (§2): "make everything a step larger" has to
be a single edit, and sixty hand-tuned sizes drift out of proportion the moment
one of them is touched.

| Role | Token | Size | Weight |
|---|---|---|---|
| Page title | `--type-page` | `22px` | 500, `letter-spacing: -.02em` |
| Card title | `--type-card` | `17px` | 500 |
| Row title | `--type-row` | `15px` | 450 |
| Body / label | `--type-body` | `14px` | 400–450 |
| Helper text | `--type-helper` | `12.5px` | 400 |
| Section heading | `--type-section` | `12px` | 500, uppercase, `letter-spacing: .09em` |

| Line height | Token | Value | Used for |
|---|---|---|---|
| Tight | `--lh-tight` | `1.25` | headings, single-line labels |
| Body | `--lh-body` | `1.45` | running text in a row or card |
| Prose | `--lh-prose` | `1.6` | paragraphs — empty states, explanations |

**Nothing is smaller than 12px.** Below that, text stops being readable at a
glance, and reading at a glance is the entire product. The one documented
exception is `--type-nozoom: 16px` for the search input on phone widths: iOS
Safari zooms the page in when a focused input renders below 16px and never zooms
back out (§13). That is a platform workaround, not a typographic role, which is
why it sits outside the scale.

**The scale grew; the density did not.** Row height stays at 58px and the header
row at 34px, so a laptop still shows the same number of monitors without
scrolling (product principle 1). Larger type inside an unchanged row is paid for
out of the slack that was already there, not out of the viewport.

**Mono is mandatory for anything measurable:** URLs, latency, uptime, intervals,
status codes. Numbers stacked in a column have to line up — otherwise you're not
comparing them, you're reading them.

### 2.6 Shape, space, motion

```css
--r-xs: 4px;    /* chips, segmented buttons, badges */
--r-sm: 6px;    /* buttons, inputs, small controls */
--r-md: 10px;   /* rows, list items */
--r-lg: 14px;   /* cards, dialogs, drawer */

--ease: cubic-bezier(.32, .72, 0, 1);
--dur:  420ms;  /* theme transition */
```

Space moves in steps of 4px. Duration follows role: 140ms for hover, 200–280ms
for panels, 320–520ms for anything asking for your attention. `--ease` starts
fast and settles softly — motion that decays feels mechanical rather than
floaty.

---

## 3. The LED

The product's brand mark. If one component has to be right, it's this one.

```
20 × 7 px   ·   border-radius: 2.5px   ·   one size, everywhere
```

**Why a pill and not a dot.** A horizontal shape reads as an indicator lamp on
equipment; a circle reads as a bullet in a list. It's a small difference, and it
decides whether the dashboard feels like instrumentation or like a web page.

**Why 2.5px and not fully round.** At `999px` the eye has no straight line to
focus on and the shape goes soft. 2.5px keeps the pill silhouette but gives the
sides a readable edge.

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

**Rules.**

- One size across the entire app — sidebar, rows, cards, group headers, toasts.
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

**Compact ships without grouping, for now (SUB-64).** The table above describes
it as grouped by customer or environment; §12 records that tags have no screen
to create or assign them, so grouping today would mean inventing a taxonomy in
the frontend. It ships as one dense line per monitor, ordered by the same
`partition()` as every other layout, with the heartbeat bar dropped — 40 rects
x 200 monitors is the cost this layout exists to avoid. Grouping returns with
tags.

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

- ~~**Mobile.**~~ Answered in §13: below 640px the dashboard renders a card per
  monitor instead of a row, keeping every fact the row shows. The remaining
  mobile gap is the screens that do not exist yet on any width.
- **Scale.** The mockup shows 14 monitors; the target audience runs 10–200. No
  search, no filter, no grouping in the row views, no virtualisation, no
  pagination. Compact was designed for density but not tested at 200 rows.

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
- **Tags/groups** are used in the Compact layout but there is no screen to
  create, rename or assign them.
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

**Rejected here too:** a native `<dialog>` with `showModal()`, which would give
the inert background and Esc handling for free. jsdom 30 still does not
implement `showModal`, so every test of the drawer would have to be skipped or
stubbed — the behaviour that most needs to stay honest is the accessibility
behaviour, and buying it at the price of never testing it is the wrong trade.
Revisit when jsdom ships it.
