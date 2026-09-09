# SubGlance — Architecture

> Internal source of truth for technical decisions.

## 1. Stack choices

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go 1.23+ | Thousands of parallel checks is exactly what goroutines exist for. One static binary, no runtime. |
| HTTP | chi or stdlib `net/http` | Lightweight, no framework lock-in |
| Database | SQLite (default), Postgres (optional) | Zero configuration is the #1 reason self-hosted software actually gets installed |
| DB driver | `modernc.org/sqlite` | Pure Go, no cgo — cross-compiling stays trivial |
| Migrations | Hand-rolled, embed.FS + transactions | See §3.1: an external library adds nothing here |
| Frontend | React 19 + Vite + TypeScript | Richest ecosystem for exactly the UI quality this product needs |
| Styling | Tailwind CSS v4 | Fast iteration, consistent design tokens |
| Components | shadcn/ui (base, heavily customized) | A starting point, not an end point — it must not look like stock shadcn |
| Animation | Motion (formerly Framer Motion) | Layout animations and shared transitions |
| Charts | Custom SVG components, possibly visx | Off-the-shelf chart libs look generic; the heartbeat bar is the brand icon |
| State/data | TanStack Query | Caching, polling and optimistic updates |
| Realtime | Server-Sent Events | Simpler than WebSockets and sufficient: traffic only goes one way |
| Distribution | Frontend via `embed.FS` in the binary | One file contains the entire application |

### Why not fullstack TypeScript

Considered and deliberately rejected. The checker engine is the heart of the
product and Go is measurably stronger and leaner there. Two languages normally
costs velocity, but the contract between them is just OpenAPI and the frontend
is a standalone SPA.

**Target to beat:** Uptime Kuma uses ~150–250MB RAM. The target is an image
under 30MB and an idle footprint under 40MB at 100 monitors — small enough to
sit on a Raspberry Pi alongside everything else already running there.

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
│  │  SQLite/PG   │        │  (queue+retry)   │       │
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

### Checker
An interface with a single method so check types can be extended
independently:

```go
type Checker interface {
    Check(ctx context.Context, m Monitor) Result
}
```

Implementations in v0.1: `http`, `tcp`, `ping`, `ssl`. Every check has a hard
timeout and a shared `http.Client` with connection pooling.

### State engine
The part that separates SubGlance from "curl in a loop". It handles:

- **Confirmation:** N consecutive failures before anything goes down (default 2)
- **Incident lifecycle:** open → confirmed → resolved, with a cause
- **Flapping detection:** rapidly toggling status is suppressed, not forwarded
- **Maintenance windows:** scheduled muting (post-v0.1)

### Notifier
A queue with exponential backoff. A failing Slack webhook must never block the
checker loop. Every channel implements the same interface.

## 3. Data model (v0.1)

```
users        id, email, password_hash, role, created_at
monitors     id, name, type, target, interval_s, timeout_s, retries,
             expected_status, keyword, keyword_mode, enabled,
             created_at, updated_at
heartbeats   id, monitor_id, ts, ok, latency_ms, status_code, error
             -- high write frequency; raw rows are rolled up into
             -- hourly buckets after 7 days
incidents    id, monitor_id, started_at, confirmed_at, resolved_at,
             cause, last_error
notif_channels  id, name, type, config_json, enabled
monitor_channels monitor_id, channel_id
settings     key, value
```

**Retention:** raw heartbeats for 7 days, then hourly aggregates (min/max/avg
latency, up/down counts) for unlimited history at negligible storage cost. A
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

Base: `/api/v1`. The OpenAPI spec is generated and shipped along with it.

```
GET    /monitors                list with current status
POST   /monitors                create
GET    /monitors/{id}
PATCH  /monitors/{id}
DELETE /monitors/{id}
POST   /monitors/{id}/pause
POST   /monitors/{id}/resume
POST   /monitors/{id}/check     run immediately
GET    /monitors/{id}/heartbeats?range=24h
GET    /monitors/{id}/uptime?range=30d

GET    /incidents               ?status=open|resolved
GET    /incidents/{id}
POST   /incidents/{id}/acknowledge

GET    /channels
POST   /channels
POST   /channels/{id}/test

GET    /stream                  Server-Sent Events, live updates
GET    /health                  liveness of SubGlance itself
GET    /ready                   readiness: can the database be reached?
```

**Liveness vs. readiness.** `/health` is deliberately dependency-free: it has
to answer even when the database is unhappy, because an orchestrator uses it to
decide whether the process should be restarted — and restarting doesn't fix a
sick database. `/ready` does check the dependencies. So `/health` 200 with
`/ready` 503 means: leave this process alone, but don't send it traffic yet.

**Authentication:** session cookie for the UI, `Authorization: Bearer <token>`
with API tokens for machines. Both hit exactly the same endpoints — the UI gets
no privileges the API does not have.

## 5. Frontend structure

```
web/
  src/
    routes/          dashboard · monitor-detail · incidents · settings
    components/
      heartbeat-bar/    THE brand component — deserves its own attention
      status-pill/
      latency-chart/
      command-menu/     cmd-K
    lib/
      api.ts            generated client from OpenAPI
      stream.ts         SSE subscription
    styles/
      tokens.css        design tokens: color, spacing, timing
```

**Design principles** (settled together in the design phase before anything
gets built):

- The heartbeat bar is the icon of the product. It deserves disproportionate time.
- Status transitions morph, they don't reload.
- Keyboard-first: cmd-K opens everything.
- When everything is fine, the screen is calm and almost colorless.
- Taste references: Linear, Vercel, Raycast.

## 6. Security

- Passwords with argon2id
- Rate limiting on login
- All user-configurable URLs SSRF-filtered (no internal networks without explicit permission)
- Notification configuration encrypted in the database
- CSRF token on cookie-based requests
- Secure headers by default, no inline scripts

## 7. Build and distribution

```
docker run -d -p 8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance
```

That's all it takes. Multi-arch image (amd64 + arm64, because Raspberry Pis are
a large part of this audience), built from `scratch` or `distroless`. Standalone
binaries per platform with every release.

## 8. Project structure

```
cmd/subglance/        main
internal/
  checker/            check implementations
  scheduler/          time wheel + worker pool
  state/              incidents, confirmation, flapping
  notify/             channels
  store/              database, migrations, queries
  api/                handlers, middleware, auth
  config/
web/                  frontend (built separately, embedded)
docs/
```

## 9. Open decisions

- [ ] chi vs. stdlib router
- [ ] sqlc vs. hand-written queries
- [ ] Alert rules in the database or in code
- [ ] Multi-region protocol for the cloud version (agent pull or push)
