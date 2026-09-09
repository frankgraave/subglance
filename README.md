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
