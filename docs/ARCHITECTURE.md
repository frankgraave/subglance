# SubGlance — Architecture

> Internal source of truth for technical decisions.

## 1. Stack choices

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go 1.26+ | Thousands of parallel checks is exactly what goroutines exist for. One static binary, no runtime. The minimum tracks `go.mod`, and CONTRIBUTING.md states the same version |
| HTTP | stdlib `net/http` | Decided: the stdlib router does everything the API needs since method patterns landed, and `go.mod` carries no router dependency. No framework lock-in because there is no framework |
| Database | SQLite | Zero configuration is the #1 reason self-hosted software actually gets installed. SQLite is the only supported database: there is no Postgres driver in `go.mod` and Postgres is explicitly outside v0.1 |
| DB driver | `modernc.org/sqlite` | Pure Go, no cgo — cross-compiling stays trivial |
| Migrations | Hand-rolled, embed.FS + transactions | See §3.1: an external library adds nothing here |
| Frontend | React 19 + Vite + TypeScript | Richest ecosystem for exactly the UI quality this product needs |
| Styling | Tailwind CSS v4 | Fast iteration, consistent design tokens |
| Components | Own components in `web/src/components`, no component library | Every control is drawn from the design tokens in `web/src/styles/tokens.css`, and the build fails on a value off the scale (`AGENTS.md`); a library's defaults would be one more source of values to override |
| Animation | CSS transitions + the browser's View Transitions API | No animation library. Opening a monitor morphs its name into the page title through `document.startViewTransition` (`web/src/shell/viewTransition.ts`); browsers without it, and readers who ask for reduced motion, get the instant swap |
| Charts | Custom SVG components, no chart library | Off-the-shelf chart libs look generic; the heartbeat bar is the brand icon |
| State/data | TanStack Query | Caching, polling and optimistic updates |
| Realtime | Server-Sent Events | Simpler than WebSockets and sufficient: traffic only goes one way |
| Distribution | Frontend via `embed.FS` in the binary | One file contains the entire application |

### Why not fullstack TypeScript

Considered and deliberately rejected. The checker engine is the heart of the
product and Go is measurably stronger and leaner there. Two languages normally
costs velocity, but the contract between them is just OpenAPI and the frontend
is a standalone SPA.

**Target to beat:** Uptime Kuma, measured on the same host with 50 HTTP
monitors, holds 105–134 MiB resident at the median and up to 192 MiB
([Compared with Uptime Kuma](operations.md#compared-with-uptime-kuma)). The
target is an image under 30MB and an idle footprint under 40MB at 100
monitors — small enough to sit on a Raspberry Pi alongside everything else
already running there.

## 2. Components

```
┌─────────────────────────────────────────────────────┐
│  subglance (one binary)                             │
│                                                     │
│  ┌───────────────┐   ┌──────────────────────────┐   │
│  │  Scheduler    │──▶│  Worker pool             │   │
│  │  (time wheel) │   │  (bounded goroutines)    │   │
│  └───────────────┘   └───────────┬──────────────┘   │
│                                  │ results           │
│                                  ▼                   │
│  ┌────────────────────────────────────────────────┐ │
│  │  State engine                                  │ │
│  │  confirmation · incidents · flapping · muting  │ │
│  └───────┬───────────────────────┬────────────────┘ │
│          │                       │                  │
│          ▼                       ▼                  │
│  ┌──────────────┐        ┌──────────────────┐       │
│  │  Storage     │        │  Notifier        │       │
│  │  SQLite      │        │  (outbox+retry)  │       │
│  └──────┬───────┘        └──────────────────┘       │
│         │                                            │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐   │
│  │  REST API (/api/v1) + SSE (/api/v1/stream)   │   │
│  └──────────────────┬───────────────────────────┘   │
│                     │                                │
│  ┌──────────────────▼───────────────────────────┐   │
│  │  Embedded SPA (embed.FS)                     │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

### Scheduler
One time wheel that schedules monitors on their interval. No goroutine per
monitor — a bounded worker pool stops 500 monitors from firing 500 concurrent
requests. Jitter on the interval prevents thundering herds.

The pool is sized from CPU count *and* from the number of scheduled monitors,
recomputed on every reload and clamped to [8, 128]. The monitor count belongs
in that figure because the pool only binds when checks stop returning: healthy
checks take milliseconds, but during a broad outage every check holds its
worker for its full timeout, and a pool sized for a 2-vCPU box would then push
detection latency for the still-healthy monitors out to minutes. The ceiling
keeps that from becoming an unbounded-goroutine failure on a small VPS. An
explicit `--check-workers` is taken literally and never adjusted.

The pool only ever grows, and only on the scheduling loop's own goroutine —
`sync.WaitGroup.Add` racing the `Wait` in the shutdown path is a panic, and
`Reload` is reachable from an API request.

### Checker
An interface with a single method so check types can be extended
independently:

```go
type Checker interface {
    Check(ctx context.Context, m Monitor) Result
}
```

Implementations in v0.1: `http`, `tcp`, `ping`, `ssl`. Every check has a hard
timeout. HTTP monitors share one `http.Client`, but not its connections:
keep-alive is off, so every check dials, resolves and handshakes afresh, the
way a new visitor does. A reused connection keeps answering after the server
stops accepting new ones, and keeps the certificate it was opened with.

### State engine
The part that separates SubGlance from "curl in a loop". It handles:

- **Confirmation:** N consecutive failures before anything goes down (default 2)
- **Incident lifecycle:** open → confirmed → resolved, with a cause
- **Flapping detection:** rapidly toggling status is suppressed, not forwarded
- **Maintenance windows:** persisted one-off and weekly schedules, evaluated in the runner and before notification delivery; see [maintenance](maintenance.md)

### Notifier
An outbox in the database, drained with exponential backoff and jitter, and a
dead letter after a fixed number of attempts. A failing Slack webhook must never
block the checker loop. Every channel implements the same interface; the
implementations live in `internal/notifier`. Alerts that land inside one window
are grouped so a single outage sends a single message per channel.

Who hears an alert is the union of the monitor's own channels and the channels
of every routing rule whose tag the monitor carries, minus the rules it is
explicitly excluded from. Rules add up rather than first-match-wins, so an
over-broad rule cannot swallow alerts meant for a narrower one. The instance
default channel applies only when that union is empty.

## 3. Data model (v0.1)

```
users        id, email, password_hash, role, created_at
monitors     id, name, type, target, interval_s, timeout_s, retries,
             expected_status, keyword, keyword_mode, enabled,
             created_at, updated_at
heartbeats   id, monitor_id, ts, ok, latency_ms, status_code, error
             -- high write frequency; raw rows are rolled up into
             -- hourly buckets after 30 days by default
incidents    id, monitor_id, started_at, confirmed_at, resolved_at,
             cause, last_error
notif_channels  id, name, type, config_json, enabled
monitor_channels monitor_id, channel_id
routing_rules    id, tag_key, tag_value      -- + rule channels, exclusions
status_pages     id, slug, title, selection, enabled
                 -- + entries: monitor_id, public_key, display_name;
                 -- see docs/design/status-page.md
settings     key, value
```

**Retention:** raw heartbeats for 30 days by default, then hourly aggregates
(min/max/avg latency, up/down counts), kept forever by default at negligible
storage cost. Both windows are set on the settings page or pinned by flag. A
background job runs this daily.

**SQLite settings:** WAL mode, `synchronous=NORMAL`, `busy_timeout`. One
writer, many readers; writes go through a single channel.

### 3.1 Two connection pools

SQLite allows many concurrent readers, but only one writer. Rather than
discovering that through random `SQLITE_BUSY` errors, `store.DB` exposes two
pools:

- **`Writer`** — exactly one connection. Every INSERT/UPDATE/DELETE goes here,
  so writes queue up neatly in Go instead of fighting it out in the driver.
- **`Reader`** — multiple connections for concurrent SELECTs.

In WAL mode readers never block the writer, and vice versa.

Closing those pools is therefore the last thing shutdown does, and it waits on
the check pipeline without a deadline to get there. `--shutdown-timeout` bounds
HTTP requests, but a per-monitor check timeout goes up to 120s, so a shared
budget expires mid-check and closes the pools underneath a worker still calling
`RecordHeartbeat`. The check pipeline gets its own budget derived from the
slowest scheduled monitor timeout; if even that expires, SubGlance logs that
checks are still in flight and keeps the database open until they finish. A
check cannot outlive its own timeout, so the wait is bounded by the monitor
set rather than open-ended.

Pragmas are set through the DSN, not with a separate `PRAGMA` statement after
`Open()`. The latter would only apply to the one connection that happened to
run that statement; via the DSN it applies to every connection in the pool. On
open it verifies that `foreign_keys` is actually on — a silently ignored pragma
would spawn orphans for months without anyone noticing.

**Migrations** are hand-written: `.sql` files in `embed.FS`, applied in
filename order, each inside a single transaction together with the row that
records the migration. A failure halfway through therefore leaves neither a
half-applied schema nor a false record of success. An external library adds
nothing here.

## 4. API design

Base: `/api/v1`; the liveness probe is also served at the root, and `/metrics`
only there. The OpenAPI spec, [`openapi.yaml`](openapi.yaml), is written by
hand; a test compares it with the server's route table on every run, so a route
missing from the spec, or a documented route that does not exist, fails the
build. The list below is the core, and a test holds every line of it to the
same route table; the spec has the rest (users, tokens, maintenance, routing
rules, status pages, settings, backups, configuration files).

```
GET    /api/v1/monitors                  list with current status
POST   /api/v1/monitors                  create
GET    /api/v1/monitors/{id}
PATCH  /api/v1/monitors/{id}
DELETE /api/v1/monitors/{id}
POST   /api/v1/monitors/{id}/pause
POST   /api/v1/monitors/{id}/resume
POST   /api/v1/monitors/{id}/check       run immediately
GET    /api/v1/monitors/{id}/heartbeats  ?limit=100
GET    /api/v1/monitors/{id}/uptime      ?window=30d
GET    /api/v1/monitors/{id}/latency     ?window=7d, stepped series for the chart

GET    /api/v1/incidents                 open incidents
GET    /api/v1/incidents/resolved        resolved incidents, paged
POST   /api/v1/incidents/{id}/ack

GET    /api/v1/channels
POST   /api/v1/channels
POST   /api/v1/channels/{id}/test

GET    /api/v1/stream                    Server-Sent Events, live updates
GET    /health                           liveness of SubGlance itself
GET    /api/v1/ready                     readiness: can the database be reached?
GET    /metrics                          operational counters, Prometheus text format
```

**Liveness vs. readiness.** `/health` is deliberately dependency-free: it has
to answer even when the database is unhappy, because an orchestrator uses it to
decide whether the process should be restarted — and restarting doesn't fix a
sick database. `/api/v1/ready` does check the dependencies. So `/health` 200
with `/api/v1/ready` 503 means: leave this process alone, but don't send it
traffic yet.

**Why `/metrics` is authenticated when the two probes are not.** Neither probe
distinguishes the failure that matters most: when the disk fills, `/health`
answers 200 because the process is alive and `/api/v1/ready` answers 200 because a
read-only SQLite database still answers a ping, while every heartbeat write
fails. `/metrics` is where that becomes a number
(`subglance_heartbeat_write_failures_total`) — but it also publishes how many
monitors an instance watches, how much of its check budget it is using and when
its writes are failing. A probe discloses one bit about this process; a metrics
endpoint is a fleet inventory plus a live map of when the operator is least able
to notice anything. It sits behind the same auth as the rest of the API, which
costs a scrape config one bearer token, and the counters carry no per-monitor
labels so names and targets never appear there at all.

The format is hand-written text, not a client library: it is six lines per
counter, there are no histograms or dynamic labels, and the distribution
promise is one static binary.

**Authentication:** session cookie for the UI, `Authorization: Bearer <token>`
with API tokens for machines. Both hit exactly the same endpoints — the UI gets
no privileges the API does not have.

## 5. Frontend structure

**How it is served.** `npm run build` writes into `internal/webui/dist`, which
that package embeds with `//go:embed all:dist`. One binary, no static directory
to deploy next to it. The handler is mounted on the catch-all route `/`, so it
also receives everything no API pattern matched, and the two cases get opposite
answers: an unknown page path is client-side routing and gets `index.html`,
while an unknown path under `/api` stays a JSON 404 — HTML arriving in a JSON
client's decoder is a bug that surfaces far from its cause. Hashed assets are
cached immutably; `index.html` never is, because it names those hashes. The
shell carries its own Content-Security-Policy, which allows the pre-paint theme
script by SHA-256 hash rather than by `unsafe-inline`.

The embed tolerates a missing build: `webui.Available()` reports false, the
server logs a warning, `/` returns a 503 explaining how to build the dashboard,
and the API is untouched. That is what lets `go build ./...` succeed on a
checkout where Node was never installed.

```
web/
  src/
    App.tsx           routes, drawn inside one page frame (components/Page.tsx)
    shell/            masthead, sidebar, page toolbar, route table (pages.ts)
    components/       shared building blocks: Card, Drawer, Menu, Chart, Choice…
    heartbeat/        THE brand component — deserves its own attention
    live/             the dashboard's data: first fetch, then the SSE stream
    monitors/ incidents/ notifications/ settings/ statuspages/ …
                      one folder per screen or settings card, each with its
                      own api.ts of hand-written fetch helpers
    commands/         the command menu (cmd-K)
    api/http.ts       the one place a request leaves the app
    styles/tokens.css design tokens: colour, type, spacing, size, motion
    layout/           browser tests that measure the real build
```

**Design principles** (settled together in the design phase before anything
gets built):

- The heartbeat bar is the icon of the product. It deserves disproportionate time.
- Status transitions morph, they don't reload.
- Keyboard-first: cmd-K opens everything.
- When everything is fine, the screen is calm and almost colorless.
- The full design system, including the measured tokens and the component rules, lives in `docs/DESIGN.md`.

## 6. Security

- Passwords with argon2id
- Rate limiting on login
- All user-configurable URLs SSRF-filtered (no internal networks without explicit permission). The guard runs when a check or a notification is sent, and also when a notification channel is saved, so a channel pointed at a blocked address is refused on the form rather than at the first alert
- Notification configuration is masked in every API response. It is stored as plain JSON unless `--secret-key` is set, which encrypts it at rest with AES-256-GCM; see [encrypting channel configuration](operations.md#encrypting-channel-configuration) and [SECURITY.md](../SECURITY.md)
- CSRF token on cookie-based requests
- Secure headers by default, no inline scripts

## 7. Build and distribution

```
docker run -d -p 127.0.0.1:8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance:edge
```

That's all it takes. Multi-arch image (amd64 + arm64, because Raspberry Pis are
a large part of this audience), built from distroless static. There is no
`:latest` yet: `:edge` follows `develop`, and a release publishes its own
version tag. Standalone binaries per platform, with signed checksums, with every
release. [Installing SubGlance](installation.md) has the details.

## 8. Project structure

```
cmd/
  subglance/          main
  seed/               fills a database with a demo estate (docs/seeding.md)
  checkdemo/ statedemo/ loadtest/   development tools, not shipped
internal/
  checker/            check implementations
  scheduler/          time wheel + worker pool
  state/              incidents, confirmation, flapping
  monitor/            wires the store to the scheduler and the state engine
  connectivity/       is the host itself offline?
  notifier/           channels, outbox, grouping
  store/              database, migrations, queries
  api/                handlers, middleware, route table
  auth/               password hashing, sessions, API tokens
  statuspage/         what a public status page may show
  configfile/         YAML export and import
  backup/             scheduled snapshots to S3-compatible storage
  housekeeping/       the daily retention pass
  watchdog/           pings an external dead man's switch
  events/             in-process fan-out to the SSE stream
  webui/              the embedded dashboard
  config/ logging/ buildinfo/ datalock/ trustedproxy/
web/                  frontend (built separately, embedded)
docs/
```

## 9. Decisions that were open

- [x] sqlc vs. hand-written queries: hand-written. `go.mod` has no sqlc, and
  the queries live beside the types they fill in `internal/store`.
- [x] Alert rules in the database or in code: in the database, as routing
  rules (tag to channels, with per-monitor exclusions), editable over the API
  and exported with the configuration file.
