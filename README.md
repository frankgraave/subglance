# SubGlance

**Your whole stack, in one glance.**

SubGlance is a self-hosted uptime and API monitoring dashboard. It tells you at
a glance whether everything is running, what is broken, and since when.

One binary, one command, no configuration required.

> **Status: early development.** The check engine is being built. Not usable yet
> — watch or star the repo if you want to know when it is.

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

## Planned for v0.1

- HTTP(S) checks: status code, response time, keyword present/absent
- TCP port and ping checks
- SSL certificate expiry warnings
- Per-monitor intervals, with retries before an incident is declared
- Uptime over 24h / 7d / 30d and incident history
- Dashboard with heartbeat timeline, plus per-monitor detail and latency graph
- Notifications: webhook, Discord, Slack, Telegram, email
- Multiple users, sessions, and API tokens
- REST API with an OpenAPI specification
- Docker image, SQLite by default — no external database needed

Deliberately **not** in v0.1: status pages, config-as-code, multi-region checks,
on-call schedules, SSO, mobile app, CLI, Postgres. They are on the roadmap; they
are not in the first release.

## Getting started

Not yet — there is no release to install. Once there is, it will be this:

```sh
docker run -d -p 8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance
```

### Building from source

Requires Go 1.25 or newer.

```sh
git clone https://github.com/frankgraave/subglance.git
cd subglance
make build          # produces ./bin/subglance
make run            # runs it locally against ./tmp
make check          # format, vet and test — run this before committing
```

Run `make help` to see every target.

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

`--allow-private-targets` is off by default on purpose. Users supply the URLs to
monitor, and without that guard SubGlance would happily act as an SSRF proxy into
the host network. Turn it on only if you intend to monitor internal services.

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

## License

[GNU AGPL-3.0](LICENSE) © Frank Graave

If you run SubGlance as a network service, the AGPL requires you to make your
modifications available to its users. To use SubGlance inside a closed product or
service, contact us for a commercial license.

---

*Know it's up. At a glance.*
