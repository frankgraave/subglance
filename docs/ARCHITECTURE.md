# SubGlance — Architectuur

> Interne bron van waarheid voor technische keuzes.

## 1. Stackkeuze

| Laag | Keuze | Reden |
|---|---|---|
| Backend | Go 1.23+ | Duizenden parallelle checks is precies waar goroutines voor bestaan. Eén statische binary, geen runtime. |
| HTTP | chi of stdlib `net/http` | Licht, geen framework-lock-in |
| Database | SQLite (standaard), Postgres (optioneel) | Nul configuratie is de #1 reden dat self-hosted software daadwerkelijk geïnstalleerd wordt |
| DB-driver | `modernc.org/sqlite` | Pure Go, geen cgo — cross-compileren blijft triviaal |
| Migraties | Eigen, embed.FS + transacties | Zie §3.1: een externe library voegt hier niets toe |
| Frontend | React 19 + Vite + TypeScript | Rijkste ecosysteem voor precies de UI-kwaliteit die dit product nodig heeft |
| Styling | Tailwind CSS v4 | Snel itereren, consistente design tokens |
| Componenten | shadcn/ui (basis, zwaar aangepast) | Startpunt, geen eindpunt — het mag er niet uitzien als standaard shadcn |
| Animatie | Motion (voorheen Framer Motion) | Layout-animaties en gedeelde overgangen |
| Grafieken | Eigen SVG-componenten, eventueel visx | Kant-en-klare chartlibs zien er generiek uit; de heartbeat-balk is ons merkicoon |
| State/data | TanStack Query | Cache, polling en optimistische updates |
| Realtime | Server-Sent Events | Simpeler dan WebSockets en voldoende: het verkeer gaat één kant op |
| Distributie | Frontend via `embed.FS` in de binary | Eén bestand bevat de volledige applicatie |

### Waarom niet fullstack TypeScript

Overwogen en bewust afgewezen. De checker-engine is het hart van het product en
Go is daar meetbaar sterker en zuiniger in. Twee talen kost normaal tempo, maar
het contract ertussen is gewoon OpenAPI en de frontend is een losstaande SPA.

**Doel om te verslaan:** Uptime Kuma gebruikt ~150–250MB RAM. Wij mikken op een
image onder de 30MB en een idle-footprint onder de 40MB bij 100 monitors. Dat
is geen ijdelheid — het is een claim op de landingspagina.

## 2. Componenten

```
┌─────────────────────────────────────────────────────┐
│  subglance (één binary)                             │
│                                                     │
│  ┌───────────────┐   ┌──────────────────────────┐   │
│  │  Scheduler    │──▶│  Worker pool             │   │
│  │  (tijdwiel)   │   │  (begrensde goroutines)  │   │
│  └───────────────┘   └───────────┬──────────────┘   │
│                                  │ resultaten        │
│                                  ▼                   │
│  ┌────────────────────────────────────────────────┐ │
│  │  State engine                                  │ │
│  │  bevestiging · incidenten · flapping · stilte  │ │
│  └───────┬───────────────────────┬────────────────┘ │
│          │                       │                  │
│          ▼                       ▼                  │
│  ┌──────────────┐        ┌──────────────────┐       │
│  │  Opslag      │        │  Notifier        │       │
│  │  SQLite/PG   │        │  (wachtrij+retry)│       │
│  └──────┬───────┘        └──────────────────┘       │
│         │                                            │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐   │
│  │  REST API (/api/v1) + SSE (/api/v1/stream)   │   │
│  └──────────────────┬───────────────────────────┘   │
│                     │                                │
│  ┌──────────────────▼───────────────────────────┐   │
│  │  Ingebedde SPA (embed.FS)                    │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

### Scheduler
Eén tijdwiel dat monitors op hun interval inplant. Geen goroutine per monitor —
een begrensde worker pool voorkomt dat 500 monitors 500 gelijktijdige requests
afvuren. Jitter op het interval voorkomt donderende kuddes.

### Checker
Interface met één methode zodat check-types los uitbreidbaar zijn:

```go
type Checker interface {
    Check(ctx context.Context, m Monitor) Result
}
```

Implementaties v0.1: `http`, `tcp`, `ping`, `ssl`. Elke check heeft een harde
timeout en een gedeelde `http.Client` met connection pooling.

### State engine
Het onderdeel dat SubGlance onderscheidt van "curl in een loop". Bewaakt:

- **Bevestiging:** N opeenvolgende mislukkingen voordat er iets omvalt (standaard 2)
- **Incidentlevenscyclus:** open → bevestigd → hersteld, met oorzaak
- **Flapping-detectie:** snel wisselende status wordt onderdrukt, niet doorgestuurd
- **Onderhoudsvensters:** geplande stilte (post-v0.1)

### Notifier
Wachtrij met exponentiële backoff. Een falend Slack-webhook mag nooit de
checker-loop blokkeren. Elk kanaal implementeert dezelfde interface.

## 3. Datamodel (v0.1)

```
users        id, email, password_hash, role, created_at
monitors     id, name, type, target, interval_s, timeout_s, retries,
             expected_status, keyword, keyword_mode, enabled,
             created_at, updated_at
heartbeats   id, monitor_id, ts, ok, latency_ms, status_code, error
             -- hoge schrijffrequentie; ruwe rijen worden na 7 dagen
             -- samengevoegd tot uurbuckets
incidents    id, monitor_id, started_at, confirmed_at, resolved_at,
             cause, last_error
notif_channels  id, name, type, config_json, enabled
monitor_channels monitor_id, channel_id
settings     key, value
```

**Retentie:** ruwe heartbeats 7 dagen, daarna uur-aggregaten (min/max/avg
latency, up/down-tellingen) voor onbeperkte historie tegen verwaarloosbare
opslag. Een achtergrondtaak draait dit dagelijks.

**SQLite-instellingen:** WAL-modus, `synchronous=NORMAL`, `busy_timeout`. Eén
schrijver, meerdere lezers; schrijfacties gaan via één kanaal.

### 3.1 Twee connectiepools

SQLite staat veel gelijktijdige lezers toe, maar slechts één schrijver. In
plaats van dat via willekeurige `SQLITE_BUSY`-fouten te ontdekken, biedt
`store.DB` twee pools:

- **`Writer`** — exact één verbinding. Alle INSERT/UPDATE/DELETE gaat hierheen,
  zodat schrijfacties in Go netjes in de rij staan in plaats van in de driver
  te vechten.
- **`Reader`** — meerdere verbindingen voor gelijktijdige SELECT's.

In WAL-modus blokkeren lezers de schrijver nooit en andersom ook niet.

Pragma's worden via de DSN gezet, niet met een losse `PRAGMA`-statement na
`Open()`. Dat laatste zou alleen gelden voor de ene verbinding die dat
statement toevallig uitvoerde; via de DSN geldt het voor elke verbinding in de
pool. Bij het openen wordt geverifieerd dat `foreign_keys` daadwerkelijk aan
staat — een stil genegeerde pragma zou maandenlang wezen laten ontstaan zonder
dat iemand het merkt.

**Migraties** zijn eigen code: `.sql`-bestanden in `embed.FS`, op
bestandsnaamvolgorde toegepast, elk in één transactie samen met de regel die
de migratie registreert. Een mislukking halverwege laat dus geen half schema
achter én geen valse registratie van succes. Een externe library voegt hier
niets aan toe.

## 4. API-ontwerp

Basis: `/api/v1`. OpenAPI-spec wordt gegenereerd en meegeleverd.

```
GET    /monitors                lijst met huidige status
POST   /monitors                aanmaken
GET    /monitors/{id}
PATCH  /monitors/{id}
DELETE /monitors/{id}
POST   /monitors/{id}/pause
POST   /monitors/{id}/resume
POST   /monitors/{id}/check     nu direct uitvoeren
GET    /monitors/{id}/heartbeats?range=24h
GET    /monitors/{id}/uptime?range=30d

GET    /incidents               ?status=open|resolved
GET    /incidents/{id}
POST   /incidents/{id}/acknowledge

GET    /channels
POST   /channels
POST   /channels/{id}/test

GET    /stream                  Server-Sent Events, live updates
GET    /health                  liveness van SubGlance zelf
GET    /ready                   readiness: kan de database bereikt worden?
```

**Liveness vs. readiness.** `/health` is bewust dependency-vrij: het moet ook
antwoorden als de database ongelukkig is, want een orchestrator beslist hierop
of het proces herstart moet worden — en herstarten repareert geen zieke
database. `/ready` controleert wél de afhankelijkheden. `/health` 200 met
`/ready` 503 betekent dus: laat dit proces met rust, maar stuur er nog geen
verkeer heen.

**Authenticatie:** sessiecookie voor de UI, `Authorization: Bearer <token>` met
API-tokens voor machines. Beide raken exact dezelfde endpoints — de UI krijgt
geen privileges die de API niet heeft.

## 5. Frontendstructuur

```
web/
  src/
    routes/          dashboard · monitor-detail · incidents · settings
    components/
      heartbeat-bar/    HET merkonderdeel — verdient eigen aandacht
      status-pill/
      latency-chart/
      command-menu/     cmd-K
    lib/
      api.ts            gegenereerde client uit OpenAPI
      stream.ts         SSE-abonnement
    styles/
      tokens.css        design tokens: kleur, spacing, timing
```

**Ontwerpuitgangspunten** (worden in de ontwerpfase samen vastgesteld voordat er
gebouwd wordt):

- De heartbeat-balk is het icoon van het product. Daar mag onevenredig veel tijd in.
- Statusovergangen morphen, ze herladen niet.
- Keyboard-first: cmd-K opent alles.
- Als alles goed is, is het scherm rustig en bijna kleurloos.
- Referentiekader qua smaak: Linear, Vercel, Raycast.

## 6. Beveiliging

- Wachtwoorden met argon2id
- Rate limiting op inloggen
- Alle instelbare URL's SSRF-gefilterd (geen interne netwerken zonder expliciete toestemming)
- Notificatie-configuratie versleuteld in de database
- CSRF-token op cookiegebaseerde requests
- Standaard veilige headers, geen inline scripts

## 7. Bouw en distributie

```
docker run -d -p 8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance
```

Meer is er niet nodig. Multi-arch image (amd64 + arm64, want Raspberry Pi's
zijn een groot deel van deze doelgroep), gebouwd vanaf `scratch` of
`distroless`. Losse binaries per platform bij elke release.

## 8. Projectstructuur

```
cmd/subglance/        main
internal/
  checker/            check-implementaties
  scheduler/          tijdwiel + worker pool
  state/              incidenten, bevestiging, flapping
  notify/             kanalen
  store/              database, migraties, queries
  api/                handlers, middleware, auth
  config/
web/                  frontend (los gebouwd, ingebed)
docs/
```

## 9. Beslissingen die nog open staan

- [ ] chi vs. stdlib router
- [ ] sqlc vs. handgeschreven queries
- [ ] Alert-regels in de database of in code
- [ ] Multi-region protocol voor de cloudversie (agent pull of push)
