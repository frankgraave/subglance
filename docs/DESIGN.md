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
| `--idle` | `#4b5257` | `#c2c7cb` | Paused, or no data yet |

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

| Role | Size | Weight |
|---|---|---|
| Page title | 20–22px | 500, `letter-spacing: -.02em` |
| Card title | 15px | 500 |
| Row title | 13.5px | 450 |
| Body / label | 12.5–13px | 400–450 |
| Helper text | 11.5px | 400 |
| Section heading | 12px | 500, uppercase, `letter-spacing: .09em` |

**Mono is mandatory for anything measurable:** URLs, latency, uptime, intervals,
status codes. Numbers stacked in a column have to line up — otherwise you're not
comparing them, you're reading them.

### 2.6 Shape, space, motion

```css
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
toolbar either.

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

- **Mobile.** Below 900px the sidebar disappears and the row collapses to
  name + interval: the heartbeat, latency and uptime all drop out. What remains
  is a list of names, which is not what this product is for. The phone is where
  you look *after* the alert fires, so it is arguably the most important screen
  we have not designed. Needs its own decision, not a narrower desktop.
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

- **Maintenance windows** are a v0.1 feature. A monitor that is
  intentionally down needs its own visual state, distinct from `--idle`. Today
  it would read as broken.
- **Error toasts.** Only the success path is designed. A failed save, a rejected
  form, a check that cannot start — none of those have a visual.
- **Tags/groups** are used in the Compact layout but there is no screen to
  create, rename or assign them.
- **A paused monitor** shows `--idle`, which is the same colour as "no data
  yet". Two very different meanings sharing one signal — a direct violation of
  rule 4.
- **Keyboard shortcuts** exist (`⌘K`, `⌘B`, `Esc`) but are undiscoverable. Needs
  a `?` overlay.
- **Onboarding beyond the empty state.** First run, creating the first user,
  what the very first minute after `docker run` looks like.

### Deliberately out of scope for now

Status pages, config-as-code YAML, multi-region and on-call schedules are all
post-v0.1. Not designing them yet is correct — but the
navigation should not promise them either.
