# Running SubGlance

Configuration, sizing, metrics, shutdown behaviour, the dead man's switch, and
how to take a backup that actually restores.

- [Configuration](#configuration)
- [Which build is this](#which-build-is-this)
- [Worker sizing](#worker-sizing)
- [Metrics](#metrics)
- [Shutdown](#shutdown)
- [Watching the watcher](#watching-the-watcher)
- [Upgrading](#upgrading)
- [Backup and restore](#backup-and-restore)

## Configuration

Every option has a working default. Flags beat environment variables, which beat
defaults.

A malformed environment variable is a startup error, not a fallback:
`SUBGLANCE_WATCHDOG_INTERVAL=300` (no unit) or
`SUBGLANCE_ALLOW_PRIVATE_TARGETS=yes` (not a boolean) refuse to start and name
the variable and the value they could not read. An invalid flag already
behaved that way; silently running on the default meant believing you had a
five-minute dead man's switch, or LAN monitoring, and having neither.

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--addr` | `SUBGLANCE_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `SUBGLANCE_DATA_DIR` | `/data` | Database and persistent state |
| `--log-level` | `SUBGLANCE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `SUBGLANCE_LOG_FORMAT` | `text` | `text` or `json` |
| `--shutdown-timeout` | `SUBGLANCE_SHUTDOWN_TIMEOUT` | `15s` | Grace period for in-flight HTTP requests |
| `--check-workers` | `SUBGLANCE_CHECK_WORKERS` | `0` (auto) | Maximum concurrent checks |
| `--allow-private-targets` | `SUBGLANCE_ALLOW_PRIVATE_TARGETS` | `false` | Permit monitoring private/loopback addresses |
| `--trusted-proxies` | `SUBGLANCE_TRUSTED_PROXIES` | empty (none) | Addresses or CIDR blocks whose `X-Forwarded-For` may be believed |
| `--alert-group-window` | `SUBGLANCE_ALERT_GROUP_WINDOW` | `90s` | How long an alert waits for others so one outage sends one message (`0` = send immediately) |
| `--watchdog-url` | `SUBGLANCE_WATCHDOG_URL` | empty (off) | External dead man's switch to ping while checks are running |
| `--watchdog-interval` | `SUBGLANCE_WATCHDOG_INTERVAL` | `5m` | How often to ping that URL |
| `--raw-retention` | `SUBGLANCE_RAW_RETENTION` | `168h` (7d) | How long raw heartbeats are kept before being rolled up into hourly buckets |
| `--rollup-retention` | `SUBGLANCE_ROLLUP_RETENTION` | `8760h` (1y) | How long hourly buckets and resolved incidents are kept (`0` = forever) |
| `--secret-key` | `SUBGLANCE_SECRET_KEY` | empty (off) | 32 bytes of key material, or a path to a file holding it, to encrypt notification channel configuration at rest. Empty means **no encryption** |
| `--secret-key-previous` | `SUBGLANCE_SECRET_KEY_PREVIOUS` | empty | The key the stored configuration is currently under, for one start: rotates to `--secret-key`, or decrypts back to plain text when `--secret-key` is empty |

### Retention

Retention runs once a day. It folds raw heartbeats older than `--raw-retention`
into hourly buckets, then drops hourly buckets and resolved incidents older than
`--rollup-retention`. To make those deletes visible on disk, SubGlance puts the
database into SQLite's incremental auto-vacuum mode at startup; on an existing
database that requires one rebuild, which is logged when it happens and skipped
with a warning if the file is large enough that the pause would hurt.

### Alert grouping

`--alert-group-window` is why twenty monitors failing on one dead uplink send
one message instead of twenty. An alert waits that long for company, and the
batch is delivered as a single notification per channel naming everything in it.
The cost is that a lone failure is also delayed by up to that window, so set it
to `0` to deliver every alert the instant it happens — sensible when you watch a
handful of services and there is nothing to group. Recoveries are never held
back: a grouped outage is summarised the moment its last member comes back.

The default follows the check schedule rather than taste. Monitors on a
60-second interval do not fail in the same second; they fail across the minute
that follows, as each one's turn comes round. 90s covers a full check cycle plus
the confirmation delay after it, which is what decides whether two failures
belong to the same event. Raise it if your checks run less often than that.

### Private targets

`--allow-private-targets` is off by default on purpose. Users supply the URLs to
monitor, and without that guard SubGlance would happily act as an SSRF proxy into
the host network. Turn it on only if you intend to monitor internal services.

### Encrypting channel configuration

Notification channel configuration — webhook URLs, bot tokens, SMTP passwords —
is stored in the SQLite database. **Without `--secret-key` it is stored in plain
text**, so anyone who can read the database file, or any backup of it, can read
those credentials. The API masks them on the way out; the file does not. That is
the default, and it is stated here rather than left to be discovered in the code.

Set a key to encrypt it:

```
openssl rand -hex 32 > /etc/subglance/secret.key
chmod 600 /etc/subglance/secret.key
subglance --secret-key /etc/subglance/secret.key
```

**Prefer the file form.** A key passed as an environment variable is readable in
`/proc/<pid>/environ` and is printed by `docker inspect` from the stored
container configuration, where it outlives the process.

A key file that appeared automatically beside the database was deliberately
rejected: it would travel with every backup and every `docker cp` of the data
volume, so it would encrypt nothing against the attacker who takes the data,
while creating the impression of protection.

On the first start with a key, existing plaintext rows are encrypted in one
transaction before SubGlance serves anything. Turning it on is therefore a
restart, not a migration you run separately.

**A wrong key, or a removed key while encrypted rows exist, is a refusal to
start.** Not a warning: starting anyway would mean every channel failing at
delivery, during exactly the incident the alerts exist to report, and refusing
leaves the database correct so restoring the right key is complete recovery.

To rotate, pass both for one start — rows already under the new key are skipped,
so a retried rotation is safe:

```
subglance --secret-key-previous OLD --secret-key NEW
```

To turn encryption off on purpose, pass the current key as `--secret-key-previous`
with no `--secret-key`; every row is written back as plain text.

See [SECURITY.md](../SECURITY.md) for the threat model.

### Trusted proxies

`--trusted-proxies` is empty by default, which means `X-Forwarded-For` and
`X-Real-Ip` are ignored and the peer address is used instead. Those headers are
set by whoever sends them, so honouring one from an unknown caller lets that
caller name its own address — and the login rate limiter keys on that address,
so a fresh value per request is credential guessing with the limit switched off.

If SubGlance runs behind nginx, Caddy, Traefik or a load balancer, name it here
and real client addresses reappear in the rate limiter and the session list:

```
--trusted-proxies 127.0.0.1,172.16.0.0/12
```

Getting this wrong fails safe. An unset or too-narrow value means everyone
behind the proxy shares one bucket, which is inconvenient; a value that is too
wide hands the limiter back to the attacker.

## Which build is this

```
$ subglance --version
subglance 0.4.1 (a1b2c3d, go1.26.8)
built 2026-09-18T09:14:02Z
```

It answers before any configuration is read, so it works on a binary you have
only just downloaded: no data directory, no free port, no valid settings. The
first line is exactly what the startup log and `GET /api/v1/health` report, so
a bug report and a health response do not have to be read differently. `version`
without dashes does the same thing.

A build made without the release pipeline says `dev` and omits the build date,
rather than inventing either.

## Worker sizing

`--check-workers` caps how many checks run at once. Left at `0` it is derived,
and recomputed every time the monitor set is reloaded, from two numbers: four
per CPU, and one per four scheduled monitors, clamped to between 8 and 128.

The monitor count is in there because the pool only matters when checks stop
returning. Healthy checks finish in milliseconds and one worker serves
hundreds of monitors; during a broad outage every check instead holds its
worker for its full timeout. 200 monitors on a 2-vCPU box used to get 8
workers, which clears roughly 48 checks a minute against 200 due — the queue
backs up, `skipping check, previous run still active` starts appearing, and
detection latency for the monitors that are *still fine* grows to minutes.
That is the tool degrading worst exactly when it is needed most.

The ceiling of 128 is a real limit, not a formality: every running check holds
an open connection, and SubGlance is meant to run on a small VPS.

Setting `--check-workers` yourself opts out of all of that — the number you
name is used as-is and never adjusted, because the reason to set it is usually
a resource limit the scheduler cannot see. The sizing rule if you are picking
one by hand is `monitors × timeout ÷ interval`: that is how many checks are in
flight at once when everything you watch is down. Watch
`subglance_checks_skipped_total` and `subglance_check_queue_depth` on
`/metrics` to see whether the pool is keeping up.

## Metrics

`GET /metrics` serves operational counters in Prometheus text format. It needs
credentials — a bearer API token in the scrape config — unlike `/health` and
`/api/v1/ready`, which are public: the counters say how many monitors an
instance watches and when its writes are failing, which is a fleet inventory
and a live signal of when the operator is least able to notice anything.

```
subglance_checks_recorded_total            checks whose heartbeat reached the database
subglance_heartbeat_write_failures_total   heartbeats that could not be written
subglance_rollup_failures_total            retention passes that failed
subglance_checks_skipped_total             checks dropped because the previous run was still going
subglance_check_queue_depth                dispatched checks waiting for a worker
subglance_check_workers                    current worker pool size
subglance_monitors_scheduled               monitors on the schedule
```

> [!IMPORTANT]
> The one to alert on is `subglance_heartbeat_write_failures_total`. When the
> disk fills, every heartbeat write fails — but `/health` still answers 200
> because the process is alive, and `/ready` still answers 200 because a
> read-only SQLite database still answers a ping. Without this counter, an
> instance can record nothing for hours while looking perfectly healthy to
> every probe it exposes.

Read it beside `subglance_checks_recorded_total`: both climbing is healthy,
write failures climbing while recorded checks are flat is a full or read-only
disk, and both flat is a wedged scheduler.

## Shutdown

`--shutdown-timeout` bounds in-flight HTTP requests. Running checks are not
covered by it and do not need to be: a per-monitor timeout goes up to 120
seconds, so a single number covering both would have to be sized for the
slowest monitor or it would expire mid-check and close the database underneath
a worker still writing its heartbeat — an error burst and a lost beat on every
restart for anyone with a slow monitor, since Docker's default `--stop-timeout`
is 10 seconds.

So the check pipeline gets its own budget, derived from the slowest scheduled
monitor timeout rather than configured separately. If it expires SubGlance logs
that checks are still in flight and keeps the database open until they finish,
because a check cannot outlive its own timeout and returning early would only
trade a slow stop for a corrupted one.

## Watching the watcher

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
  one check has been **recorded** since the previous ping — completed *and*
  written to the database — so a process whose check pipeline has wedged, or
  whose disk has filled, goes quiet instead of reporting health from a corpse.
  Counting the check rather than the write was the old behaviour and it was
  wrong in exactly the case the switch exists for: on a full disk every write
  failed, the counter kept climbing and the switch kept being reset. An
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

## Upgrading

Take a [backup](#backup-and-restore) before starting a newer binary. Migrations
run during database `Open`, before the service starts serving requests. An
older binary refuses a database with migration-ledger entries it does not
recognise; reverting the binary may therefore require restoring the pre-upgrade
backup rather than reusing the upgraded database.

Migration `0011_heartbeat_capture_reason.sql` adds a `CHECK`-constrained column
to `heartbeats`. SQLite validates that constraint against the existing rows,
so the first start after this upgrade scans the raw heartbeat table. A large
table, especially with a long `--raw-retention`, can delay startup. Allow for
that pause in the upgrade window and any supervisor startup timeout; subsequent
starts skip the already-applied migration. Existing rows keep an unknown capture
reason rather than being classified from today's monitor settings.

## Backup and restore

> [!WARNING]
> **Copying the database file while SubGlance is running is not a backup.**
> SubGlance runs SQLite in WAL mode, which means `subglance.db` on its own is
> only current up to the last checkpoint — recent writes live in the
> `subglance.db-wal` file beside it. A `docker cp`, a `tar` over the volume or
> a filesystem snapshot taken while the container runs gives you a file that is
> either weeks stale or refuses to open, and you find that out on the day you
> need it.

Use the `backup` subcommand instead. It uses SQLite's `VACUUM INTO`, which runs
inside a read transaction and writes one self-contained file holding everything
committed at the moment it started. It is safe to run while SubGlance is
serving traffic and recording checks.

```sh
docker compose exec subglance subglance backup /data/subglance-backup.db
docker compose cp subglance:/data/subglance-backup.db ./subglance-backup.db
```

Or, running from source:

```sh
subglance backup ~/backups/subglance-$(date +%F).db --data-dir /var/lib/subglance
```

The command refuses to overwrite an existing file. That is deliberate: a backup
command that silently truncates its destination will eventually destroy the one
good copy you had, most likely when a nightly job reuses a fixed filename and
that run fails halfway. Give each run a new name — `$(date +%F)` above — and
prune old ones yourself.

The subcommand exists because the shipped image is distroless: there is no
shell and no `sqlite3` binary in it, so the only thing that can take a backup
inside the container is the binary that is already there.

To restore, stop SubGlance and put the file back as `subglance.db` in the data
directory:

```sh
docker compose down
# the SubGlance image has no shell, so borrow one to write into the volume
# (`docker volume ls` shows the real name; Compose prefixes it with the project)
docker run --rm -v subglance-data:/data -v "$PWD:/restore" alpine sh -c \
  'rm -f /data/subglance.db-wal /data/subglance.db-shm && \
   cp /restore/subglance-backup.db /data/subglance.db'
docker compose up -d
```

Two things to check while restoring:

- Remove any leftover `subglance.db-wal` and `subglance.db-shm` next to it, as
  the command above does. They belong to the old database, and SQLite would
  try to replay them over the restored one.
- Restore into a version of SubGlance that is the same as, or newer than, the
  one that took the backup. An older binary refuses to start against a newer
  schema rather than writing rows against a table shape it does not
  understand; the error names the migration it does not recognise.
