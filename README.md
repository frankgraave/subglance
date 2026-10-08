<div align="center">

# SubGlance

**Your whole stack, in one glance.**

Self-hosted uptime and API monitoring. It tells you at a glance whether
everything is running, what is broken, and since when.

*One binary, one command, no configuration required.*

[![CI](https://github.com/frankgraave/subglance/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/frankgraave/subglance/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/frankgraave/subglance?include_prereleases&sort=semver)](https://github.com/frankgraave/subglance/releases)
[![Licence: AGPL-3.0](https://img.shields.io/badge/licence-AGPL--3.0-blue)](LICENSE)

</div>

---

> [!NOTE]
> **Status: early development.** The engine works end to end — monitors are
> scheduled, checked, confirmed into incidents, streamed to a live dashboard and
> delivered to a human over eight notification channels. The latest release is
> **`v0.1.0-rc5`**, a signed release candidate with binaries for five platforms;
> the `:edge` image carries everything merged since, listed under
> [newer than the latest release](#newer-than-the-latest-release).

## Quick start

```sh
docker run -d -p 127.0.0.1:8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance:edge
```

Open <http://localhost:8080/>, create an administrator on the first screen, and
add a monitor. That is the whole installation — the database is SQLite inside
the volume, and the dashboard is compiled into the binary, so there is nothing
to run alongside it.

> [!IMPORTANT]
> The port is published on loopback on purpose. Until an account exists,
> whoever reaches the dashboard first becomes the administrator — so binding to
> every interface hands that to anyone who can route to the host. Reach it from
> elsewhere over a reverse proxy or an SSH tunnel, or drop the `127.0.0.1:`
> prefix deliberately once an administrator exists.

Prefer Compose, a downloaded binary, or a build from source?
See **[Installing SubGlance](docs/installation.md)**.

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
  and grouping — a false alert costs more trust than ten missed ones. When the
  host itself loses its connection, that is recorded as one outage of the host
  rather than one of every monitor.
- **Light enough to forget about.** A single static binary with SQLite, not a
  stack of services. Measured for 24 hours beside Uptime Kuma on one host, with
  50 HTTP monitors checked every 30 seconds: about 20 MiB resident against
  Kuma's 105 MiB (1.23) and 134 MiB (2.5), a fifth to a quarter of its CPU
  time per check, and an 8 MiB image download against 146 and 174 MiB. The
  memory a sign-in takes is handed back within seconds; the setup, the caveats
  and what a sign-in costs are in [Compared with Uptime Kuma](docs/operations.md#compared-with-uptime-kuma)
  and [Memory](docs/operations.md#memory).

## Self-monitoring

SubGlance cannot report its own outage without an external watchdog. Configure
[the opt-in outbound watchdog](docs/operations.md#watching-the-watcher) to let
another service notice when pings stop. **Settings → Self-monitoring** shows
whether it is configured, the last successful ping and the latest result.
A successful ping does not prove that the receiving service will raise an alarm.

Before it confirms an outage on a network error, SubGlance checks whether the
host can reach anything at all, by dialling two public DNS resolvers
(`1.1.1.1:53` and `9.9.9.9:53`) over TCP. That is outbound traffic, sent only at
that moment; [operations](docs/operations.md#when-this-host-loses-its-own-connection)
explains it and how to choose other targets or turn it off.

## Documentation

| Guide | What is in it |
|---|---|
| **[Keyboard commands](docs/keyboard.md)** | Command menu, monitor search and actions, navigation, themes and focus behavior |
| **[Installing SubGlance](docs/installation.md)** | Docker Compose, Docker, a downloaded binary with signature verification, building from source, running the tests |
| **[Using SubGlance](docs/using-subglance.md)** | First run, your first monitor, authentication, the six check types, push monitors, how a failure becomes an alert, repeat alerts, the API |
| **[Notification channels](docs/channels.md)** | The settings each of the eight channel types needs, the Delivery column, SMS limits, private addresses, the webhook payload, and webhook bodies for Teams, Matrix and Pushover |
| **[Configuration files](docs/configuration-files.md)** | Export and import monitors, channels, routing rules and maintenance windows as YAML, without credentials; convert an Uptime Kuma database |
| **[Managing tags](docs/tags.md)** | Bulk assignment/removal, instance-wide renames, collision policy and conditional API writes |
| **[Running SubGlance](docs/operations.md)** | The configuration table, worker sizing, metrics, shutdown, the dead man's switch, backup and restore |
| [Architecture](docs/ARCHITECTURE.md) · [Design](docs/DESIGN.md) · [Style guide](docs/styleguide/index.html) | How it is built, and the tokens the interface is drawn from |
| [`docs/openapi.yaml`](docs/openapi.yaml) | The complete API as an OpenAPI 3.1 document, checked against the server's own route table on every test run |

## Where it stands

An honest split, because a roadmap says nothing about what you can run today.
Everything below is judged by whether it works end to end, not by whether code
exists for it. It describes `develop`, which is what the `:edge` image runs;
what has not reached a release yet is listed under
[newer than the latest release](#newer-than-the-latest-release).

| Area | State |
|---|---|
| Checks — HTTP(S), TCP, ping, SSL, DNS, domain expiry, push | ✅ Working |
| Scheduler, state engine, flapping suppression | ✅ Working |
| Notifications — webhook, Discord, Slack, Telegram, email, ntfy, Gotify, SMS | ✅ Working |
| REST API v1 + OpenAPI 3.1 specification | ✅ Working |
| Authentication — sessions, API tokens, three roles | ✅ Working |
| Dashboard, monitor detail, incidents, monitors, notifications screens | ✅ Working |
| Signed multi-platform release builds | ✅ Working (`v0.1.0-rc5`) |
| Maintenance windows — one-off and weekly, by monitor or tag | ✅ Working |
| Public status pages | ✅ Working |
| Configuration files — YAML export and import | ✅ Working |
| Moving from Uptime Kuma — `subglance import uptime-kuma` | ✅ Working |
| Scheduled backups to S3-compatible storage, and restore | ✅ Working |
| Latency chart on the detail view — 24h, 7d and 30d | ✅ Working |
| Settings — account, display, users, status pages, self-monitoring, retention, backups, import and export, API tokens, instance diagnostics, reset | ✅ Working |

<details>
<summary><strong>What "working" covers, in detail</strong></summary>

- **HTTP(S), TCP, ping and SSL checks**, including keyword matching, response
  time, and certificate expiry warnings — a certificate that expires soon
  alerts as *expiring* without counting as downtime; an expired one is down
- **Push monitors** for jobs that cannot be reached from outside — a backup, a
  cron script, a queue worker reports in and silence is what raises the alarm
- **Failure classification** — a DNS failure, a refused connection and an expired
  certificate are three different problems, and the API says which one you have
- **The response behind a failure** — when an HTTP check fails, the first 2 KiB
  of the response is kept, so the answer that arrived at 03:00 is still there in
  the morning. Capped, limited to the first few failures of an outage, and
  switchable off per monitor. Only an allowlist of response headers is stored,
  so no credential headers are kept; the body is stored as it arrived and can
  hold sensitive data, so see [SECURITY.md](SECURITY.md) before enabling it on a
  target that answers with more than an error message
- **Scheduler**: a timing wheel with a bounded worker pool, so 500 monitors do
  not mean 500 goroutines or 500 simultaneous requests
- **State engine**: confirmation before alarming, incident lifecycle, flapping
  suppression
- **REST API v1** with an OpenAPI 3.1 specification, checked against the server's
  own route table on every test run
- **Authentication**: sessions, API tokens, three roles, first-run setup
- **Delivering the alert**: webhook, Discord, Slack, Telegram, email, the
  self-hosted push services ntfy and Gotify, and SMS through an Android phone
  or Twilio, one 160-character message per alert with an hourly limit
  ([channels](docs/channels.md)), sent from an outbox that retries with
  exponential backoff and jitter and dead-letters a delivery that keeps
  failing, so a Slack outage never blocks the checker loop. `POST /api/v1/channels/{id}/test` sends a real message through a
  channel, so a misconfigured webhook is found when it is saved rather than
  during the first outage
- **Alert grouping**: twenty monitors failing on one dead uplink send one
  message per channel naming all twenty, with the waiting window configurable
  down to zero
- **Repeat alerts** on a growing schedule until someone acknowledges the
  incident, with the schedule stored so a restart mid-outage continues it
- **Live dashboard**: rows on a laptop, cards on a phone, heartbeat bars, and an
  SSE stream that warns when it loses the connection
- **Search, tag filtering and grouping** on the dashboard, with a render budget
  test that fails if one arriving heartbeat re-renders more than the one monitor
  it belongs to at 200 monitors
- **The stale state**: when the stream drops, the LEDs and heartbeat bars drain
  their colour, so the screen stops asserting a status it can no longer verify
- **Monitor detail view**: heartbeat over a longer window than the row shows,
  uptime over real windows, a latency chart over 24h, 7d or 30d, and the
  incident history for that monitor
- **Incident, monitor and notification screens**: acknowledging an incident,
  managing monitors and configuring channels all have a screen, not only an
  endpoint
- **The app shell**: sidebar, layout switcher, mobile nav drawer, status wall,
  keyboard shortcuts, and a crash boundary
- **Tags and pausing**: tags are key/value pairs the dashboard turns into
  filters, and a paused monitor reads differently from one that has no data yet
- **Backups**: `subglance backup` takes a consistent snapshot on demand, and
  with a bucket configured a compressed copy goes to AWS S3 or any
  S3-compatible storage on a schedule, pruned to a set number and restored with
  `subglance restore`
- **One binary**: the dashboard is compiled in with `go:embed`

</details>

### Newer than the latest release

These work on the `:edge` image, which follows `develop`, but arrived after
`v0.1.0-rc5` was tagged: the release archives and the `:0.1.0-rc5` image do not
have them.

- **`subglance reset-password`**: an owner locked out of their own instance
  sets a new password from the data directory, with the server stopped; see
  [Locked out](docs/operations.md#locked-out)
- **DNS record checks**: a `dns` monitor compares the A, AAAA, CNAME, MX or
  TXT records of a name with the values it expects, through the host's
  resolver or one set on the monitor, and `subglance import uptime-kuma`
  converts Kuma's DNS monitors to them; see
  [Checking a DNS record](docs/using-subglance.md#checking-a-dns-record)
- **Domain expiry checks**: a `domain` monitor reads a domain's registration
  expiry date over RDAP and warns a set number of days ahead; see
  [Checking when a domain expires](docs/using-subglance.md#checking-when-a-domain-expires)

### Not working yet

Everything planned for v0.1 is listed as working above.

Deliberately **not** in v0.1: multi-region checks, on-call schedules, SSO,
mobile app, CLI, Postgres. They are on the roadmap; they are not in the first
release.

### Known sharp edges

Things that work, but not the way they eventually should:

- **Channel configuration is stored as plain text unless you set a key.**
  `notif_channels.config_json` holds webhook URLs, bot tokens and SMTP
  passwords, and without `--secret-key` they are readable by anyone who can
  read the database file or any backup of it. The API masks them on the way
  out; the file does not. Setting a key encrypts them at rest — see
  [encrypting channel configuration](docs/operations.md#encrypting-channel-configuration).

## Releases

Tagged releases publish one archive per platform — Linux and macOS on amd64 and
arm64, Windows on amd64 — with a `SHA256SUMS` file signed keylessly with
[cosign](https://docs.sigstore.dev/) against the release workflow's own
identity, so the signature can be checked without trusting a key I could lose.
[Verification instructions are in the install guide](docs/installation.md#with-a-downloaded-binary).

The container image is built on every push to `develop`, so `:edge` and
`:develop` move under you; a short-SHA tag is published alongside them for
pinning an exact build. A release tag publishes its version as an image tag
too, such as `:0.1.0-rc4`.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first — every
contribution requires signing the [CLA](CLA.md).

Work happens on `develop`; `main` only receives releases. Every change arrives
through a pull request against `develop`, where CI and an automated review run
before it can be merged. `develop` is protected: direct pushes are rejected.

Working on the frontend? [AGENTS.md](AGENTS.md) is the short version of the
house rules, and the [style guide](docs/styleguide/index.html) is the source of
truth for every colour, size and spacing value.

## Warning and uptime

An unconfirmed failed check is **Warning**: visible in the monitor, heartbeat
and failure history, with its error and captured response when available. It
sends no failure or recovery alert. The configured consecutive-failure threshold
promotes the monitor to Down and confirms its incident.

**Only confirmed downtime counts toward uptime.** The percentage is successful
assessed samples divided by successful plus confirmed-down samples. Warnings
are excluded from both counts, including when a later check confirms the
incident; confirmation never rewrites earlier checks. This is a sample ratio,
not elapsed time. Checks while Down run every `min(configured interval, 60s)`
with normal jitter and worker limits, so sampling is more frequent during a
long-interval monitor's outage. Recovery restores the configured interval.

History recorded before assessment was introduced keeps its raw results and
rollups, but is excluded as legacy: those records cannot prove confirmation.
The uptime detail shows warning and legacy counts. With no eligible samples,
uptime is unknown (`null` in the API), never an invented 0% or 100%. A pending
monitor has no check result yet; a paused monitor is not being measured.

## Scheduled maintenance

Schedule one-off or weekly maintenance for a monitor or tag group from the
Monitors page. Measurements continue; alerts are suppressed and maintenance
samples are excluded from uptime. An empty denominator remains unknown. See
[scheduled maintenance](docs/maintenance.md) for API examples, timezone and DST
rules, overlap, pause and delivery semantics.

## Licence

[GNU AGPL-3.0](LICENSE) © Frank Graave

If you run SubGlance as a network service, the AGPL requires you to make your
modifications available to its users.

Monitoring your own or client sites does not trigger that: the sites being
checked are not users of SubGlance. If the AGPL does not work for your
situation, get in touch.

---

<div align="center">

*Know it's up. At a glance.*

</div>
