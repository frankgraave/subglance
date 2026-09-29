# Public status page: design before build

This document is the design step for the public status page (SUB-160). It
fixes what the page and its JSON show, the threats that come with the first
read path that needs no login besides `/push`, and the data model. The static
prototype beside it, [`docs/mockups/pages/status.html`](../mockups/pages/status.html),
draws the result on a phone, a tablet and a desktop, in both themes.

Nothing here is built yet. The build ticket (SUB-153) takes its acceptance
criteria from this file.

**Version 1 is small on purpose.** A page shows a chosen set of monitors under
public names, their current state, a 90-day history and the outages behind it,
plus announced maintenance. Writing incident messages ("investigating", "fixed")
is out of scope for version 1: that is an editing feature with its own UI.

---

## 1. Content: an allowlist

The page and its JSON are built from a **dedicated response type**, never from
`store.Monitor`, `store.Incident` or any struct the authenticated API
serialises. A new field on a monitor then cannot reach the public page by
accident: it has to be added to the public type by name, in a diff a reviewer
sees. That is what "allowlist, not blocklist" means in code.

### 1.1 What a page shows

Per page:

| Field | Source | Why it is safe |
|---|---|---|
| `title` | `status_pages.title` | Written for the public by the operator |
| `description` | `status_pages.description` | Same; optional, plain text, no HTML |
| `generated_at` | server clock | Says how fresh the answer is |
| `timezone` | `status_pages.timezone` | Chosen for the audience, not the server's |
| `summary` | counted from the entries | `{ "up": 4, "degraded": 0, "down": 1, "unmonitored": 0 }` |

Per entry, in the page's `entries` list (one per monitor on the page):

| Field | Source | Notes |
|---|---|---|
| `key` | `status_page_entries.public_key` | Random 8-byte id, stable per entry. **Not** the monitor id (see §3.1) |
| `name` | `status_page_entries.display_name` | Required. Never falls back to the internal name |
| `status` | derived, see §1.3 | One of `up`, `degraded`, `down`, `no_data`, `not_monitored` |
| `in_maintenance` | maintenance windows | Boolean only; the window's name is not shown |
| `uptime_90d` | hourly rollup | Percentage, 2 decimals, `null` when there is no data |
| `days` | hourly rollup | 90 entries, oldest first: `{ "date", "state", "down_minutes" }` |

Page-level lists:

| List | Fields | Window |
|---|---|---|
| `maintenance` | `starts_at`, `ends_at`, `keys` (the entries it covers) | Running now, or starting within 7 days |
| `outages` | `key`, `started_at`, `resolved_at` (or `null`), `duration_s` | Confirmed incidents in the last 14 days |

### 1.2 What never appears

Neither in the HTML, nor in the JSON, nor in a response header, nor in an
error body:

- the monitor's **internal name**, **id**, **type** or **target** (URL, host,
  port, push token);
- anything from a check: **failure body**, **response headers**, **status
  code**, **error text**, **latency**, TLS details, the certificate's subject;
- an incident's **cause**, **last error**, **acknowledgement** or anything
  about who was notified;
- **tags** — they are selection criteria, not labels (`customer:acme` is
  exactly the kind of string an operator does not mean to publish);
- the maintenance window's **name**, its schedule or its channels;
- the instance's version, uptime or any setting.

Latency is left out deliberately, not forgotten. A public response time is a
free benchmark of the operator's infrastructure, and it turns a status page
into a monitoring target of its own.

### 1.3 Public status words

The page answers one question — can I rely on it right now — so it uses fewer
states than the dashboard, and only **confirmed** facts:

| Monitor status (dashboard) | Public status | Lamp | Word |
|---|---|---|---|
| up | `up` | up | Up |
| pending (a check failed, not yet confirmed) | `up` | up | Up |
| recovering (passing, incident still open) | `degraded` | warn | Degraded |
| down (confirmed incident) | `down` | down | Down |
| waiting (never checked) | `no_data` | idle | No data yet |
| paused | `not_monitored` | off | Not monitored |

*Pending shows as up.* The dashboard shows a single failed check in amber so
the operator can look early; a public page that did the same would announce
every blip that the retry setting exists to absorb. The public page follows the
state engine's confirmed state, which is the same rule the notifications
follow.

*The rows are named by meaning, not by the API's status field.* The API
reports a failed check that is not yet confirmed as `warning`, and a monitor
that was never checked as `pending`. The build maps by meaning: the first is
the "pending" row above and shows as up, the second is the "waiting" row. No
live state today is amber without a failure, so a live monitor is `degraded`
only while it is recovering. The daily history is different on purpose: a day
with unconfirmed failures is `degraded` (§1.4), because a day is looked back
on, and a blip that is over is a fact rather than an alarm.

*Recovering shows as degraded, not up.* Until the recovery threshold is met the
outage is not over, and the page must not say so before the operator's own
channels do.

### 1.4 The daily history

Each of the 90 days gets one state, from the hourly rollup:

- **down** — the day contains confirmed incident time outside maintenance;
- **degraded** — no downtime, but warning checks;
- **up** — only passing checks;
- **no data** — no checks at all (before the monitor existed, or paused all day).

Checks during maintenance are excluded, exactly as they are from uptime.
`down_minutes` is the confirmed incident time that day, rounded up to whole
minutes, so a visitor can tell a 2-minute blip from a lost afternoon.

In the prototype every daily state has its own **height** as well as its own
colour: down days draw at full height, degraded at three quarters, up at half
and no-data days as a quarter-height stub. The same rule as the heartbeat bar
(DESIGN.md §4: absence has to be loud), and it keeps all four states apart
without relying on colour.

The bars are `aria-hidden`, so each row carries the whole history as text for
assistive technology: the count of up, degraded, down and no-data days over the
90 days, followed by how many days ago each down and each degraded day was. The
14-day outage list below adds times and durations for recent outages; it is not
the only accessible record of older ones.

---

## 2. Design

The prototype is `docs/mockups/pages/status.html`. It opens from disk, links the
live `tokens.css` and `led.css`, and is covered by the same guards as every
other mockup (`mockups.test.ts`, `mockup-contract.browser.test.ts`), plus
`status-page-mockup.browser.test.ts` for its own layout.

Decisions it draws:

- **No product chrome.** No sidebar, no masthead, no search. A visitor has no
  account and nothing to navigate to. One centred column at the detail page's
  reading measure (`--size-pane-lg`).
- **One sentence first.** The summary card states the count, not an adjective:
  "1 of 5 services is down", "All 5 services are up". "Major outage" and
  "partial outage" are judgements the page cannot make on the operator's behalf.
- **The same lamp and word** as the product (DESIGN.md §3, §9.1): a hidden
  lamp and a visible word, so the state is never colour alone.
- **Maintenance above services** when there is any, because an announced window
  explains a down row before the visitor reaches it. Nothing is drawn when
  there is none — no empty card.
- **Phone:** the history shows the last 30 days instead of 90. Ninety bars in
  a 320px column come out under 2px wide, which is no longer a bar. The axis
  label changes with it ("30 days ago"), so the picture never claims a range it
  does not draw. The uptime figure stays the 90-day number and says so.
- **Both themes** come from the tokens. The page follows the visitor's
  `prefers-color-scheme`; there is no toggle on the real page (the prototype
  has one for review only).
- **Times** are shown in the page's configured time zone, named once in the
  footer, never in the server's.

---

## 3. Threat model

The status page is the first read path without a login besides `/push`. Every
point below names the measure the build must carry.

### 3.1 Enumeration

- **Slugs.** A slug is chosen by the operator (`status`, `acme`), so it is
  guessable by design; secrecy is not its job. An unknown slug and a disabled
  page return the **same 404 with the same body**, so probing cannot tell
  "exists but off" from "never existed". There is no index of pages.
- **Monitor ids.** Sequential ids would reveal how many monitors the instance
  has and let a visitor correlate pages. Entries are keyed by a random
  `public_key` instead, generated when the monitor is added to the page.
- **Not a secret link.** Anyone who needs a private page should not publish
  one; version 1 has no password or unguessable-URL mode, and the docs say so.

### 3.2 Rate limiting

- Per client address and globally, with the same lazy token bucket `/push`
  uses (`tokenBucket` / `ipBuckets`), behind `trustedproxy` so a forged
  `X-Forwarded-For` cannot pick its own bucket.
- Both limits run **before** any database read, for the same reason as on
  `/push`: a flood must not reach the reader pool that the dashboard needs.
- A limited request gets `429` with `Retry-After`.
- Starting values: 5 requests per second per address with a burst of 20, and
  50 per second across all addresses. The response is also cached (below), so
  the database sees at most one render per page per cache interval regardless.

### 3.3 Caching behind a reverse proxy

- The server keeps **one rendered response per page and format** for 30
  seconds, keyed by slug and format, so the HTML and JSON routes hold separate
  entries and a JSON request can never be answered with cached HTML or the
  reverse. Uptime and 90 days of rollups are the expensive part; recomputing
  them per request would make the page the cheapest way to load the instance.
- Responses carry `Cache-Control: public, max-age=30` and an `ETag`, so a
  proxy or CDN in front can serve them too. `Vary` is not needed: the response
  does not depend on cookies or headers.
- **No cookies, ever.** The handler never reads or sets a session cookie, so a
  shared cache cannot store one visitor's session and hand it to the next, and
  a logged-in operator viewing their own page gets exactly what the public gets.
- Disabling a page takes effect within the cache interval. The docs state the
  30 seconds rather than promising "immediately".
- Works on its own (sub)domain: the page renders with relative asset paths and
  no absolute self-links, so `status.example.com` proxied to
  `/status/<slug>` needs no extra setting.

### 3.4 A page that is switched off

- `enabled = 0` returns the 404 from §3.1: no "this page is disabled" text,
  which would confirm the slug.
- Removing a monitor from the page, or deleting the monitor, removes its entry
  and its history from the page at the next render. Its `public_key` is not
  reused.
- A page with no entries renders, with the sentence "No services on this page
  yet." — an empty page is a configuration state, not an error to leak.

### 3.5 Timing and error codes

- **Status codes:** only `200`, `304`, `404` and `429`. Any internal failure
  is a `503` with a fixed body; the error goes to the log, never the response.
- **Timing:** the unknown-slug path does one indexed lookup and returns. It
  cannot be made constant-time against a disabled page without a fake render,
  and it does not need to be: a slug is not a secret (§3.1). The cached
  path is what matters, and it makes an enabled page cheaper, not slower, so
  timing leaks nothing that the page does not show anyway.
- **Change timing:** a public "down" appears at the same moment the operator's
  notification goes out, because both follow the confirmed state. The page does
  not reveal failures earlier than the operator learns of them.
- **Headers:** the API's security headers apply unchanged, including
  `frame-ancestors 'none'`. Embedding the page in another site is a later,
  opt-in setting, not a default.
- **Search engines:** `X-Robots-Tag: noindex` by default, with a per-page
  switch to allow indexing. An operator publishing a page for their users has
  not necessarily decided to publish it to search engines.

---

## 4. Data model

Two tables, in a new migration. Both `STRICT`, like the rest of the schema.

### `status_pages`

| Column | Type | Rule |
|---|---|---|
| `id` | INTEGER PRIMARY KEY AUTOINCREMENT | |
| `slug` | TEXT NOT NULL UNIQUE COLLATE NOCASE | `^[a-z0-9][a-z0-9-]{0,62}$`; reserved words refused (`api`, `assets`, `fonts`) |
| `title` | TEXT NOT NULL | 1–120 characters |
| `description` | TEXT NOT NULL DEFAULT '' | ≤ 500 characters, plain text |
| `timezone` | TEXT NOT NULL DEFAULT 'UTC' | IANA name, validated like maintenance windows |
| `selection` | TEXT NOT NULL | `monitors` or `tag` (CHECK) |
| `tag_key`, `tag_value` | TEXT | Both set when `selection = 'tag'`, both NULL otherwise (CHECK) |
| `indexable` | INTEGER NOT NULL DEFAULT 0 | 0/1; drives `X-Robots-Tag` |
| `enabled` | INTEGER NOT NULL DEFAULT 0 | 0/1; **off by default**, so saving a draft publishes nothing |
| `created_at`, `updated_at` | INTEGER NOT NULL | Unix seconds, as elsewhere |

### `status_page_entries`

| Column | Type | Rule |
|---|---|---|
| `page_id` | INTEGER NOT NULL | References `status_pages(id)`; entries go with their page |
| `monitor_id` | INTEGER NOT NULL | References `monitors(id)`; an entry goes with its monitor |
| `public_key` | TEXT NOT NULL UNIQUE | 16 hex characters from `crypto/rand` |
| `display_name` | TEXT NOT NULL | 1–80 characters; required even when the page selects by tag |
| `position` | INTEGER NOT NULL | Order on the page |

Primary key `(page_id, monitor_id)`: a monitor appears once per page, and may
appear on several pages under different public names.

**Selection by tag** fills entries from the monitors that carry the tag, but a
monitor still needs a `display_name` before it shows. A tagged monitor without
one is listed in the editor as "not shown until named", never published under
its internal name. That keeps the one rule that matters — internal names do
not leak — true for both selection modes, at the cost of one extra step when a
tag gains a monitor.

No new tables for maintenance or outages: the page reads the existing
maintenance windows and incidents through the entries.

---

## 5. Acceptance for the build (SUB-153)

1. `GET /status/{slug}` (HTML) and `GET /api/v1/status-pages/{slug}` (JSON) are
   public routes in the route table and in `docs/openapi.yaml`; page
   management (`/api/v1/status-pages`, CRUD) is admin-only.
2. The public JSON is a dedicated type holding exactly the fields in §1.1; a
   test marshals it and fails on any field not on that list.
3. A test seeds a monitor with a recognisable internal name, target URL, push
   token, failure body, response header, error text and tag, puts it on a page,
   and asserts that none of those strings appear in the HTML, the JSON or the
   response headers — for up, degraded and down.
4. Unknown and disabled slugs return byte-identical 404 responses.
5. Per-address and global rate limits run before any database read; tests cover
   `429` + `Retry-After` and the trusted-proxy address.
6. Responses carry `Cache-Control: public, max-age=30` and an `ETag`, never
   `Set-Cookie`, and a second request inside the interval does not hit the
   store.
7. Public states follow §1.3, including pending → up and recovering → degraded.
8. The page matches the prototype on a phone (320/375/414px, no sideways
   scroll, 30-day history), a tablet and a desktop, in both themes, and passes
   the existing axe checks.
9. `docs/operations.md` covers setting up a page on its own subdomain behind a
   reverse proxy.
