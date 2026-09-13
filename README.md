# SubGlance

**Your whole stack, in one glance.**

SubGlance is a self-hosted uptime and API monitoring dashboard. It tells you at
a glance whether everything is running, what is broken, and since when.

One binary, one command, no configuration required.

> **Status: early development, not yet released.** The engine works end to end:
> monitors are scheduled, checked, confirmed into incidents, and streamed to a
> live dashboard. What is missing before a first release is listed under
> [Where it stands](#where-it-stands) — most notably, **notifications are stored
> but never sent**. Watch or star the repo if you want to know when v0.1 lands.

## Why another uptime monitor

There are good ones already. SubGlance exists because of a specific gap: the
self-hosted options are either functional but dated, or beautiful but
proprietary. I wanted both, plus the things teams keep asking for and not
getting:

- **A real REST API.** Everything the dashboard can do, a script can do. Not an
  afterthought and not held back.
- **A UI you don't mind staring at.** This is a screen you leave open all day.
  It should be quiet when things are fine and unambiguous when they are not.
- **Alerts you can trust.** Confirmation before alarming, flapping suppression,
  and grouping — a false alert costs more trust than ten missed ones.
- **Light enough to forget about.** A single static binary with SQLite, not a
  stack of services.

## Where it stands

An honest split, because the roadmap below says nothing about what you can run
today. Everything below is judged by whether it works end to end, not by whether
code exists for it.

### Working

- **HTTP(S), TCP, ping and SSL checks**, including keyword matching, response
  time, and certificate expiry warnings
- **Push monitors** for jobs that cannot be reached from outside — a backup, a
  cron script, a queue worker reports in and silence is what raises the alarm
- **Failure classification** — a DNS failure, a refused connection and an expired
  certificate are three different problems, and the API says which one you have
- **Scheduler**: a timing wheel with a bounded worker pool, so 500 monitors do
  not mean 500 goroutines or 500 simultaneous requests
- **State engine**: confirmation before alarming, incident lifecycle, flapping
  suppression
- **REST API v1** with an OpenAPI 3.1 specification, checked against the server's
  own route table on every test run
- **Authentication**: sessions, API tokens, three roles, first-run setup
- **Live dashboard**: rows on a laptop, cards on a phone, heartbeat bars, and an
  SSE stream that warns when it loses the connection
- **One binary**: the dashboard is compiled in with `go:embed`

### Not working yet

- **Sending notifications.** Channels can be created, stored and validated
  (webhook, Discord, Slack, Telegram, email), and the state engine decides
  correctly *when* a human should be told — but nothing delivers the message.
  This is the largest gap between the README and reality, and the reason there
  is no release yet.
- **The stale state.** When the stream drops, a banner says so — but every LED
  keeps its last known colour, so the screen still asserts a status it can no
  longer verify. `docs/DESIGN.md` §6 specifies draining the colour instead; that
  is not implemented yet.
- **Most of the UI beyond the dashboard.** No monitor detail view, no incident
  screen, no settings, no notification configuration. The app shell (sidebar,
  layout switcher, status wall) is designed in `docs/DESIGN.md` but not built.
- **Scale.** The dashboard is tested with a handful of monitors, not the 200 it
  targets: no search, no filtering, no virtualisation.
- **Maintenance windows**, tags, and a paused monitor that looks different from
  one that has no data yet.

## Still planned for v0.1

What the list above does not yet cover, and what has to exist before a first
release:

- Delivering notifications: webhook, Discord, Slack, Telegram, email
- Per-monitor detail view with a latency graph
- Incident, monitor, notification and settings screens
- Maintenance windows

Deliberately **not** in v0.1: status pages, config-as-code, multi-region checks,
on-call schedules, SSO, mobile app, CLI, Postgres. They are on the roadmap; they
are not in the first release.

## Getting started

### With Docker Compose

Copy [`docker-compose.yml`](docker-compose.yml) out of this repository and run:

```sh
docker compose up -d
```

That is the whole installation. The file needs no edits to work: it publishes
8080, keeps the database in a named volume, and every option in it is commented
out with the default it would override. `internal/config` has a test that fails
if an option is added to the binary without reaching that file, or named there
without the binary reading it.

To follow the logs or stop it again:

```sh
docker compose logs -f
docker compose down          # add -v to delete the database too
```

### With Docker

Compose is only a wrapper here; a single `docker run` is equivalent:

```sh
docker run -d -p 8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance:edge
```

That is the whole installation. Open <http://localhost:8080/> and the first
screen asks you to create an administrator; the database is SQLite inside the
volume, so there is nothing else to run alongside it.

There is no tagged release yet, so every tag that exists points at the head of
`develop` and will change under you: `:edge` and `:develop` both track that
branch, and a short-SHA tag is published alongside them for pinning an exact
build. Once v0.1 ships, pin a version instead.

The image is `linux/amd64` and `linux/arm64`, built from
[distroless static](https://github.com/GoogleContainerTools/distroless): no
shell, no package manager, and the process runs as the unprivileged user
`65532:65532`. It measured **23.6MB uncompressed** on amd64 when it was first
published, and CI fails the build if that ever passes 30MB.

Flags go after the image name, because the entrypoint is the binary itself:

```sh
docker run -d -p 9000:9000 -v subglance:/data \
  -e SUBGLANCE_ADDR=:9000 \
  ghcr.io/frankgraave/subglance:edge --log-level debug
```

The listen address is the one setting to pass as an environment variable rather
than a flag. Docker runs the image's `HEALTHCHECK` as a separate process that
inherits the environment but not the entrypoint's flags, so `--addr :9000` would
move the server while the healthcheck kept probing `:8080` and reported the
container unhealthy. `SUBGLANCE_ADDR` reaches both.

One consequence of the unprivileged user is worth knowing about: **ping checks
need the container to allow unprivileged ICMP.** Measured on Docker 29.1.3, a
container gets `net.ipv4.ping_group_range = 0 2147483647` by default and ping
works as `65532` with no extra flags — but that default belongs to the runtime,
not to this image, and some Kubernetes and Podman setups are stricter. If a ping
monitor reports a permission error, the binary names both fixes; the narrower
one is:

```sh
docker run -d -p 8080:8080 -v subglance:/data \
  --sysctl net.ipv4.ping_group_range="0 2147483647" \
  ghcr.io/frankgraave/subglance:edge
```

`--cap-add=NET_RAW` works too, by making the raw socket available instead. HTTP,
TCP and SSL checks are unaffected either way.

To build the image yourself, `docker build -t subglance .` — the Dockerfile
builds the dashboard and the binary from source, so Go and Node are only needed
inside the build.

### Building from source

Requires Go 1.26 or newer. Node 24 is needed only if you want the dashboard.

```sh
git clone https://github.com/frankgraave/subglance.git
cd subglance
make build          # produces ./bin/subglance
make run            # runs it locally against ./tmp
make check          # format, vet and test — run this before committing
```

Run `make help` to see every target.

#### The dashboard

The dashboard is a single-page app in `web/`. It is compiled into the binary
with `go:embed`, so a release is still one file with nothing to serve
alongside it:

```sh
make web-install    # npm ci, once
make dist           # build the dashboard, then a binary that contains it
./bin/subglance     # the UI is on / and the API on /api/v1
```

`make build` on its own does not need Node and does not need the dashboard: a
binary without one serves the full API and says so at startup, which is the
normal state while working on the Go side. Building the frontend writes into
`internal/webui/dist`, which is gitignored apart from a `.gitkeep` — that file
is what keeps `go build ./...` working on a fresh clone.

### Configuration

Every option has a working default. Flags beat environment variables, which beat
defaults.

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--addr` | `SUBGLANCE_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `SUBGLANCE_DATA_DIR` | `/data` | Database and persistent state |
| `--log-level` | `SUBGLANCE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `SUBGLANCE_LOG_FORMAT` | `text` | `text` or `json` |
| `--shutdown-timeout` | `SUBGLANCE_SHUTDOWN_TIMEOUT` | `15s` | Grace period for in-flight requests |
| `--check-workers` | `SUBGLANCE_CHECK_WORKERS` | `0` (auto) | Maximum concurrent checks |
| `--allow-private-targets` | `SUBGLANCE_ALLOW_PRIVATE_TARGETS` | `false` | Permit monitoring private/loopback addresses |
| `--watchdog-url` | `SUBGLANCE_WATCHDOG_URL` | empty (off) | External dead man's switch to ping while checks are running |
| `--watchdog-interval` | `SUBGLANCE_WATCHDOG_INTERVAL` | `5m` | How often to ping that URL |

`--allow-private-targets` is off by default on purpose. Users supply the URLs to
monitor, and without that guard SubGlance would happily act as an SSRF proxy into
the host network. Turn it on only if you intend to monitor internal services.

### Watching the watcher

SubGlance cannot report its own death. If the process is killed, runs out of
memory or the host goes down, the dashboard does not turn red — it stops
existing, and silence looks exactly like good news. That is the worst failure
this product can have, and no amount of code inside the process can fix it.

So the judgement goes somewhere else. Set `--watchdog-url` to a dead man's
switch — Healthchecks.io, Dead Man's Snitch, the push endpoint of a second
SubGlance — and SubGlance pings it on a schedule. When the pings stop, that
service raises the alarm.

```sh
subglance --watchdog-url https://hc-ping.com/your-uuid --watchdog-interval 5m
```

Two details matter:

- The ping is tied to evidence, not to a timer. It is only sent when at least
  one check has completed since the previous ping, so a process whose check
  pipeline has wedged goes quiet instead of reporting health from a corpse. An
  instance with no monitors scheduled still pings; there, zero checks is the
  correct answer rather than a symptom.
- A clean shutdown sends one final ping marked `stopped`, so a planned restart
  does not page anyone. The marker is the `X-SubGlance-Event` header, and a
  generic provider ignores it: Healthchecks.io and Dead Man's Snitch read that
  final ping as an ordinary check-in, which resets the switch and keeps the
  pager quiet. Only a receiver that reads the header — a second SubGlance, or
  your own endpoint — can tell a clean stop apart from a normal ping.
- Redirects are refused. The URL you configure is trusted; wherever it might
  redirect to is not, so a `3xx` is logged as a rejected ping instead of being
  followed.

Only a ping is sent: the event, the number of monitors scheduled and the number
of checks completed. No monitor names, targets or results leave the instance.
Those counts are still information about the install, and over `http` they go
out in cleartext — use an `https` URL unless the whole network path is trusted.
The feature is off unless you set the URL, because it is the one part of
SubGlance that talks outbound to a third party.

It is not a complete answer. An instance that is running fine but has lost
outbound network stops pinging too, and that reads as an outage at the other
end. Being told about a problem that turns out to be the messenger is still
better than being told nothing.

### First run

There is no default account and no seeded password. On first start SubGlance
logs that setup is pending; you create the first administrator through the API
(and, once it exists, the web interface):

```sh
curl -X POST http://localhost:8080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"a-long-passphrase"}'
```

That endpoint closes permanently once an account exists.

### Your first monitor

Setup returns a session cookie, so with a cookie jar the two calls chain and
the dashboard has something to show within a minute of `docker compose up -d`:

```sh
curl -c jar -X POST http://localhost:8080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"a-long-passphrase"}'

curl -b jar -X POST http://localhost:8080/api/v1/monitors \
  -H 'Content-Type: application/json' \
  -d '{"name":"Public website","type":"http","target":"https://example.com/","interval_s":60}'
```

Reload <http://localhost:8080/> and the row is there, grey until the first
check lands and then green or red. Everything else has a default, and unknown
fields are rejected rather than ignored, so a typo tells you instead of
silently configuring something other than what you asked for.

### Authentication

Two credential types reach the same endpoints with the same rights:

- **Session cookie** — for the browser. HttpOnly, SameSite=Lax, and
  `__Host-`-prefixed when served over HTTPS.
- **API token** — for scripts and CI: `Authorization: Bearer sgp_…`.

```sh
# create a token (requires an existing session)
curl -X POST http://localhost:8080/api/v1/tokens \
  -H 'Content-Type: application/json' -d '{"name":"ci"}'

# use it
curl -H "Authorization: Bearer sgp_…" http://localhost:8080/api/v1/monitors
```

A token's plaintext is shown once and stored only as a hash; it cannot be
recovered, only replaced.

Three roles: **viewer** (read-only), **editor** (manage monitors, acknowledge
incidents) and **admin** (also manages users and tokens).

### Check types

| Type | Target shape | What it verifies |
|---|---|---|
| `http` | `https://example.com/health` | Status code, response time, keyword present or absent, certificate expiry |
| `tcp` | `db.example.com:5432` | A TCP handshake completes within the timeout |
| `ping` | `example.com` or `192.0.2.10` | ICMP echo reply |
| `ssl` | `example.com` (port optional, defaults to 443) | Certificate validity, hostname match, chain of trust, days until expiry |
| `push` | none — the job reports in | That the job reported inside its window |

A TCP check completes the handshake and hangs up without sending a payload —
speaking a protocol badly is a good way to end up in someone's fail2ban rules.

The SSL check verifies the certificate itself rather than letting the handshake
fail, so an expired certificate still reports *when* it expired and by how much.
Set `ssl_warn_days` to fail the check while there is still time to renew, instead
of at the moment the site breaks.

Ping needs either unprivileged ICMP sockets or `CAP_NET_RAW`. SubGlance tries the
unprivileged socket first and falls back to the raw one; when neither is allowed
the error names both fixes. If ICMP is blocked entirely on your network, a TCP
check against a known port answers the same question more reliably.

### Push monitors

The four types above all look inward from outside. Anything without a reachable
port is invisible to them: a nightly backup, an import script, a queue worker on
a laptop behind NAT. Those are exactly the things that fail quietly and are only
discovered when you need them.

A push monitor turns the direction around. It gets a secret URL, the job calls it
when it finishes, and if nothing arrives inside the window the monitor goes down.

```bash
curl -X POST http://localhost:8080/api/v1/monitors \
  -H 'Content-Type: application/json' \
  -d '{"name":"Nightly backup","type":"push","push_interval_s":86400,"push_grace_s":3600}'
```

The response contains `push_url`, **once**. Only a hash is stored, so it cannot
be looked up afterwards — copy it now or recreate the monitor. Then, at the end
of the job:

```bash
restic backup /data && curl -fsS "$PUSH_URL"
```

Or report the outcome either way, so a failing run says so immediately instead
of waiting out the window:

```bash
restic backup /data
curl -fsS "$PUSH_URL?status=$?&msg=restic+backup"
```

`push_interval_s` is how often the job is expected to report; `push_grace_s` is
how late it may be before that silence counts as failure. Grace is *added* to the
interval, so an hourly job with five minutes of grace is late at 65 minutes. It
defaults to 60 seconds rather than 0, because cron drifts and a zero tolerance
turns ordinary jitter into a 3am alert.

**What a push monitor does and does not tell you.** It proves your script reached
the line with the `curl` on it. It does not prove the work succeeded. A backup
that writes an empty archive and then pings stays green, and that is a worse
outcome than no monitoring at all, because it is monitoring that reassures you.
Pick one of two strategies and stick to it: ping at the very end and only on
success, so silence is the failure signal; or always ping and pass `?status=$?`,
so a failing run reports itself immediately instead of waiting out the window.
Mixing them — pinging unconditionally without a status — reports every run as a
success. If the job can check its own output — a non-zero file size, a row
count — check it before pinging.

The URL is the credential. It carries no session and no API token, because a cron
line cannot hold either, and handing a backup script an API token would give it
authority over every monitor you have. Anyone holding a push URL can report on
that one monitor and nothing else. Treat it as a secret anyway: a leaked one lets
someone tell you a job ran when it did not.

The push URL uses whatever scheme you reached the API on, so on a plain-HTTP
instance it is an `http://` URL — a bearer credential in cleartext. That is fine
on a host or a LAN you trust, and it is why HTTP is not refused. It is not fine
across the internet: anyone on the path can read the URL and then report a job as
healthy forever. If a job pings from outside the network the instance lives on,
put the instance behind TLS first.

### How a failure becomes an alert

A monitor does not go down because one check failed. Each monitor has a failure
threshold (`retries`, default 2), and the state engine walks it through four
states:

| State | Meaning | Alerts? |
|---|---|---|
| `up` | Last check passed | — |
| `pending` | Failing, threshold not yet reached | No |
| `down` | Threshold reached, incident confirmed | Yes, once |
| `up` again | Recovered | Only if it was confirmed |

An incident record is opened on the **first** failure, so its start time is when
the outage actually began — not when the system became sure of it. The gap
between `started_at` and `confirmed_at` is the confirmation delay, and it is
visible in the API.

A blip that recovers before the threshold is recorded but never notified, in
either direction. A monitor that oscillates rapidly is marked as flapping, and
further notifications are held back until it settles; suppressed transitions are
flagged rather than silently dropped. Suppression hides the alert, never the
record — incidents are written to the database throughout, so history stays
accurate.

Checks cut short by a shutdown are discarded rather than recorded as failures.
Otherwise every restart would manufacture an outage on healthy monitors.

Monitors that are deleted or paused have their in-memory state dropped. A
monitor resumed an hour later starts clean rather than resuming a failure
streak from before the pause.

### Tests

```sh
make check         # format, vet and the hermetic suite — run before committing
make test          # the same suite with the race detector
go test ./...      # everything, including tests that need the network
```

The suite is split in two. Most tests are hermetic: they parse strings or talk
to a loopback server the test starts itself, and they run everywhere. A few need
a real resolver, an outbound socket or an ICMP-capable kernel; those skip under
`go test -short`, which is what CI runs on every push.

That split is deliberate. A CI runner's network is not the internet — DNS may be
filtered, ICMP is usually blocked, and a third-party host having a bad morning
must never turn this repository red. The network-dependent tests still run daily
in a separate workflow, where a failure means "go look at it" rather than "your
pull request is broken".

Set `SUBGLANCE_TEST_NETWORK=1` to force them on locally.

### API

```
GET    /api/v1/monitors                    list monitors with current status
POST   /api/v1/monitors                    create a monitor
GET    /api/v1/monitors/{id}               one monitor
PATCH  /api/v1/monitors/{id}               edit a monitor (partial; keeps history)
DELETE /api/v1/monitors/{id}               delete a monitor
POST   /api/v1/monitors/{id}/check         run one check now, return the result
POST   /api/v1/monitors/{id}/pause         stop checking
POST   /api/v1/monitors/{id}/resume        start checking again
GET    /api/v1/monitors/{id}/heartbeats    recent check results
GET    /api/v1/monitors/{id}/incidents     incident history

GET    /api/v1/incidents                   every unresolved incident
POST   /api/v1/incidents/{id}/ack          acknowledge — seen, being worked on

GET    /api/v1/stream                      live check results and status changes (SSE)
```

`check` exists so a fix can be verified without waiting out the interval. For
an enabled monitor the result is recorded like any other check, so the
dashboard turns green immediately; for a paused monitor it is returned but not
recorded, because writing into a period the monitor promised not to watch
would misrepresent it. The response says which happened in its `recorded` field.
Manual checks are limited to one per monitor per five seconds.

Acknowledging is not resolving: it stops repeat notifications without claiming
the problem is fixed.

That listing is the monitor and incident surface only. The complete API —
authentication, users, API tokens, notification channels, uptime windows and
the live event stream — is specified in [`docs/openapi.yaml`](docs/openapi.yaml),
an OpenAPI 3.1 document you can feed to a client generator or an editor such as
Swagger UI. It is checked against the server's own route table on every test
run, so a route cannot be added, removed or change privilege level without the
specification following it.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first — every
contribution requires signing the [CLA](CLA.md).

Work happens on `develop`; `main` only receives releases. Every change arrives
through a pull request against `develop`, where CI and an automated review run
before it can be merged. `develop` is protected: direct pushes are rejected.

## License

[GNU AGPL-3.0](LICENSE) © Frank Graave

If you run SubGlance as a network service, the AGPL requires you to make your
modifications available to its users.

Monitoring your own or client sites does not trigger that: the sites being
checked are not users of SubGlance. If the AGPL does not work for your
situation, get in touch.

---

*Know it's up. At a glance.*
