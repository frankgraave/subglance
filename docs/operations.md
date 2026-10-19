# Running SubGlance

Configuration, sizing, metrics, shutdown behaviour, the dead man's switch, and
how to take a backup that actually restores.

- [Configuration](#configuration)
- [Which build is this](#which-build-is-this)
- [Worker sizing](#worker-sizing)
- [Memory](#memory)
- [Metrics](#metrics)
- [Shutdown](#shutdown)
- [Watching the watcher](#watching-the-watcher)
- [Upgrading](#upgrading)
- [Backup and restore](#backup-and-restore), including
  [scheduled backups to S3](#scheduled-backups-to-s3-compatible-storage)
- [Locked out](#locked-out)
- [Public status pages](#public-status-pages), including
  [a page on its own subdomain](#a-page-on-its-own-subdomain)

## Configuration

Every option has a working default. Flags beat environment variables, which beat
defaults.

A malformed environment variable is a startup error, not a fallback:
`SUBGLANCE_WATCHDOG_INTERVAL=300` (no unit) or
`SUBGLANCE_ALLOW_PRIVATE_TARGETS=yes` (not a boolean) refuse to start and name
the variable and the value they could not read. An invalid flag already
behaved that way; silently running on the default meant believing you had a
five-minute dead man's switch, or LAN monitoring, and having neither.

A flag takes its value as `--name value` or `--name=value`. A true/false flag
takes it only after an equals sign: `--connectivity-check=false`, while a bare
`--allow-private-targets` means true. A word on the command line that is not a
flag is refused at startup, naming the word. Go's flag parser stops reading at
such a word, so `--connectivity-check false --addr :9000` used to leave the
check on and listen on `:8080` without a message; the same holds for the
subcommands, so `subglance restore latest --data-dir /srv/subglance` is refused
rather than restoring into `/data`.

`--help`, and every flag that does not parse, prints the flags with the value
each one has after the environment is read, so the list shows what this start
would run with. The exceptions are the values that can hold a credential:
`--secret-key` and `--secret-key-previous` say only that a key is set,
`--watchdog-url` and `--backup-endpoint` are shown as their scheme and host,
as in the log, and `--base-url` and `--backup-target` are shown as set except
for a user name and password, which read `***` (startup refuses both with
one).

A subcommand's `--help` (`subglance restore --help`, `backup`, `healthcheck`,
`import`, `reset-password`) prints that subcommand's usage line and its own
flags, such as `restore --from`, and exits without touching the data
directory. The settings a subcommand reads the way the server does, such as
`--data-dir`, are listed by `subglance --help`. A flag a subcommand cannot
parse, whether its own or one of the server's (a typo, a flag that does not
exist, a value left off), is refused in one line on stderr that names the flag
and points at that subcommand's `--help`, with exit code 1 and nothing
touched; the server's list of flags is printed only by the server itself.

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--addr` | `SUBGLANCE_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `SUBGLANCE_DATA_DIR` | `/data` | Database and persistent state |
| `--log-level` | `SUBGLANCE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `SUBGLANCE_LOG_FORMAT` | `text` | `text` or `json` |
| `--shutdown-timeout` | `SUBGLANCE_SHUTDOWN_TIMEOUT` | `15s` | Grace period for in-flight HTTP requests |
| `--check-workers` | `SUBGLANCE_CHECK_WORKERS` | `0` (auto) | Maximum concurrent checks |
| `--allow-private-targets` | `SUBGLANCE_ALLOW_PRIVATE_TARGETS` | `false` | Permit monitoring private/loopback addresses |
| `--extra-ca-file` | `SUBGLANCE_EXTRA_CA_FILE` | empty (none) | PEM file of certificate authorities that `http` and `ssl` checks trust besides the system trust store, for services behind a private CA. Read at start; a file that cannot be read or holds no certificate stops the start. See [A self-signed certificate, or a CA of your own](using-subglance.md#a-self-signed-certificate-or-a-ca-of-your-own) |
| `--base-url` | `SUBGLANCE_BASE_URL` | empty (no links) | The address SubGlance is reached at, such as `https://status.example.com` or `https://example.com/subglance`, so every alert links to its incident and its monitor, and a new push monitor's URL points at it. An absolute `http(s)` address; a path prefix is kept and a trailing slash does not matter. Shown read-only under Instance on the settings page. See [Links back to SubGlance](channels.md#links-back-to-subglance) |
| `--trusted-proxies` | `SUBGLANCE_TRUSTED_PROXIES` | empty (none) | Addresses or CIDR blocks whose `X-Forwarded-For` may be believed |
| `--alert-group-window` | `SUBGLANCE_ALERT_GROUP_WINDOW` | `90s` | How long an alert waits for others so one outage sends one message (`0` = send immediately) |
| `--watchdog-url` | `SUBGLANCE_WATCHDOG_URL` | empty (off) | External dead man's switch to ping while checks are running |
| `--watchdog-interval` | `SUBGLANCE_WATCHDOG_INTERVAL` | `5m` | How often to ping that URL |
| `--connectivity-check` | `SUBGLANCE_CONNECTIVITY_CHECK` | `true` | Before confirming an outage on a network error, check whether this host can reach anything at all. See [When this host loses its own connection](#when-this-host-loses-its-own-connection) |
| `--connectivity-targets` | `SUBGLANCE_CONNECTIVITY_TARGETS` | `1.1.1.1:53,9.9.9.9:53` | `host:port` addresses that check dials over TCP; the host counts as offline only when all of them fail |
| `--connectivity-names` | `SUBGLANCE_CONNECTIVITY_NAMES` | `one.one.one.one,dns.quad9.net` | Host names that check looks up through this host's resolver after a check's own lookup got no answer; the resolver counts as down only when none of them gets one. Empty looks up none. See [When the resolver stops answering](#when-the-resolver-stops-answering) |
| `--raw-retention` | `SUBGLANCE_RAW_RETENTION` | unset: `720h` (30d), or the settings page | How long raw heartbeats are kept before being rolled up into hourly buckets. Minimum `24h`; `0` = forever. Setting it locks the settings page field |
| `--rollup-retention` | `SUBGLANCE_ROLLUP_RETENTION` | unset: `0` (forever), or the settings page | How long hourly buckets and resolved incidents are kept (`0` = forever). Setting it locks the settings page field |
| `--retention-run-at` | `SUBGLANCE_RETENTION_RUN_AT` | unset: `03:30`, or the settings page | Time of day, `HH:MM` in the server's time zone, at which the daily retention pass runs. A pass missed while the server was down runs at the next start. Setting it locks the settings page field |
| `--max-database-size` | `SUBGLANCE_MAX_DATABASE_SIZE` | unset: no limit, or the settings page | Size the database may reach, such as `2GB` or `500MiB` (minimum `32MiB`; `0` = no limit), before the daily pass removes history beyond the two windows to stay under it. See [A size limit](#a-size-limit). Setting it locks the settings page field |
| `--secret-key` | `SUBGLANCE_SECRET_KEY` | empty (off) | 32 bytes of key material, or a path to a file holding it, to encrypt notification channel configuration at rest. Empty means **no encryption** |
| `--secret-key-previous` | `SUBGLANCE_SECRET_KEY_PREVIOUS` | empty | The key the stored configuration is currently under, for one start: rotates to `--secret-key`, or decrypts back to plain text when `--secret-key` is empty |
| `--backup-target` | `SUBGLANCE_BACKUP_TARGET` | empty (off) | [Scheduled backups](#scheduled-backups-to-s3-compatible-storage) to `s3://bucket` or `s3://bucket/prefix` |
| `--backup-endpoint` | `SUBGLANCE_BACKUP_ENDPOINT` | empty (AWS S3) | Service URL for any S3-compatible storage that is not AWS |
| `--backup-region` | `SUBGLANCE_BACKUP_REGION` | `us-east-1` | Signing region of the bucket |
| `--backup-interval` | `SUBGLANCE_BACKUP_INTERVAL` | `24h` | Time between backups. Minimum `1h` |
| `--backup-keep` | `SUBGLANCE_BACKUP_KEEP` | `14` | How many backups stay in the bucket; older ones are deleted after each upload |
| (environment only) | `SUBGLANCE_BACKUP_ACCESS_KEY_ID` | empty | Access key ID for the bucket |
| (environment only) | `SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE` | empty | Path to a file holding the secret access key. Preferred over the next row |
| (environment only) | `SUBGLANCE_BACKUP_SECRET_ACCESS_KEY` | empty | The secret access key itself |

### Retention

Retention runs once a day, at 03:30 in the server's time zone unless
`--retention-run-at` says otherwise. It folds raw heartbeats older than the raw window
into hourly buckets, then drops hourly buckets and resolved incidents older than
the rollup window. By default raw heartbeats are kept 30 days and everything
summarised is kept forever: on a measured database a raw heartbeat costs about
75 bytes and an hourly bucket about 24, so fifty monitors on a 60-second interval
hold about 160 MB of raw history and add about 10 MB of summaries a year.

Both windows are set on the **Settings** page, under *Retention & storage*,
which shows each table's size and daily growth beside the field and says how
many rows a shorter window would remove before it is saved. A change applies
from the next pass, without a restart. Nothing is capped: either window may be
forever. Two rules protect the data and are enforced — raw heartbeats are kept
at least a day, because the 24-hour charts read them, and summaries at least as
long as raw heartbeats.

`--raw-retention` and `--rollup-retention` (or their environment variables)
still work, and win: a window set that way is read-only on the page, which names
the flag or variable that set it, so a restart can never silently undo a choice
made on the page or the other way round. If a pinned window makes the saved
partner window invalid, the saved one is set aside for the nearest valid value,
lengthened where possible, and the page says so. To make those deletes visible on disk, SubGlance puts the
database into SQLite's incremental auto-vacuum mode at startup; on an existing
database that requires one rebuild, which is logged when it happens and skipped
with a warning if the file is large enough that the pause would hurt.

A pass that was due while the server was down, or that failed, runs as soon as
the server starts again, so an instance that is restarted every night still
gets its housekeeping. Two passes never run at once. Changing the time does not
start a pass by itself: the next one runs when the new time next comes round.
Each pass is recorded in the database — when it started, how long it took, what
it removed per table, how much space it freed, and the error if it failed — so
the record survives a restart. The server's time zone is the container's `TZ`
variable; without one it is UTC.

#### A size limit

The windows are measured in time, but a small disk runs out in bytes. With
`--max-database-size` set, the daily pass checks the size of the data after the
windows have done their work, and if it is still over the limit it keeps going,
in the order that costs least:

1. It folds the oldest raw heartbeats into hourly buckets, oldest first, as
   the raw window does. Per-check detail and stored failure responses go; the
   history does not, because the hourly buckets keep the counts and latencies.
   It never folds the last 24 hours, which the 24-hour charts read.
2. Only once raw heartbeats are down to that last day does it delete hourly
   buckets, oldest first, and never the ones for the last day.
3. It never deletes an incident. They are small, and they are what anyone
   comes back to look for.

A limit it cannot meet without breaking those rules stops at them rather than
removing anything else. Either way it is not silent: a pass that went past the
windows logs a warning that says what it removed and from when, adds one to
`subglance_size_limit_passes_total` on `/metrics`, and is recorded with the
pass. One that stopped at the floor says so in its warning and sets
`subglance_size_limit_unmet` to 1 until a pass meets the limit again.

The size measured is the space the data occupies, not the file. With
incremental auto-vacuum on (the default, see above) the file shrinks to match in
the same pass. On a database too large to have been switched over at startup,
the freed space is reused instead, so the file stops growing at the limit
rather than shrinking to it.

No limit is set by default, because removing history the windows would have
kept should be a choice. The minimum is `32MiB`: a smaller limit is almost
certainly a unit left off, and it would fold nearly everything on the first
pass. A number without a unit is refused for the same reason.

#### Running a pass now, and compacting

The time of day and the size limit are set on the **Settings** page too, in the
same *Retention & storage* card as the windows and saved together with them.
Below the form the card shows the last pass: when it ran, how long it took, what
it removed, the space it freed, and, when the size limit went past the windows,
how far it shortened raw data ("Shortened to 12 days of raw data to stay under
2.0 GB") or that it could not get under. The size is typed in megabytes (MB,
1,000,000 bytes); a limit set in MiB by a flag or the API is shown rounded and
kept exact unless it is edited.

An administrator can start the pass without waiting for its time of day, with
**Run now** on that card, which first counts what the windows in force would
remove, or with `POST /api/v1/settings/retention/run`. It runs in the background with the
settings in force and is recorded like a scheduled pass, with trigger
`manual`; `GET /api/v1/settings/retention` shows `running` while it works and
the outcome as `last_pass` afterwards, along with the time of day and the size
limit, which a `PUT` to the same address changes. A pass that is already
running, scheduled or not, makes the request fail with `409`.

A database too large to be switched to incremental auto-vacuum at startup never
shrinks, however much a pass deletes. `POST /api/v1/settings/retention/compact`
rewrites it once with a full `VACUUM` and leaves it in incremental mode, so the
file shrinks now and later passes keep it that way. The shipped image has no
`sqlite3` binary, so this is the way to do it without stopping the server. While
it runs, every write waits for it: checks keep running and their results are
recorded when it finishes. `compact` in the settings response says whether it
would help, roughly how long it takes and whether the disk has room; the copy
it writes can need up to twice the database size, and without that much free
space it is refused (`507`) before anything is written. The card offers
**Compact database** only when it would help, says how long writes will wait,
and disables the button when the disk cannot hold the copy.

The same pass clears the notification delivery log: a notification that was
delivered, or suppressed by maintenance or folded into a quiet-hours digest, is
removed 30 days after its last update, whatever the two windows say. Failed
deliveries are kept, because they may be the only record that an alert never
arrived.

Failure-response snapshots expire with their raw heartbeats; hourly rollups do
not retain response bodies or headers. Capture keeps up to three stored responses
per outage and 2048 body bytes per response, plus allowed headers. **There is no
per-monitor or global snapshot-byte cap.** Repeated settled outages, unconfirmed
failures and large headers can cost much more than one outage's allowance.
See [response snapshot sizing](response-snapshot-sizing.md) for the measured
synthetic workloads, limit decisions, reproduction harness and storage caveats.
The monitor's **Failure responses** history explains recorded suppression.

### Alert grouping

`--alert-group-window` is why twenty monitors failing on one dead uplink send
one message instead of twenty. An alert waits that long for company, and the
batch is delivered as a single notification per channel naming everything in it.
The cost is that a lone failure is also delayed by up to that window, so set it
to `0` to deliver every alert the instant it happens — sensible when you watch a
handful of services and there is nothing to group. Recoveries are never held
back: a grouped outage is summarised the moment its last member comes back.
Reminders for outages that are still unanswered are grouped too, into one
"3 monitors are still down", but kept apart from first alerts, so a new outage
never arrives worded as a repeat.

The default follows the check schedule rather than taste. Monitors on a
60-second interval do not fail in the same second; they fail across the minute
that follows, as each one's turn comes round. 90s covers a full check cycle plus
the confirmation delay after it, which is what decides whether two failures
belong to the same event. Raise it if your checks run less often than that.

### Private targets

`--allow-private-targets` is off by default on purpose. Users supply the URLs to
monitor, and without that guard SubGlance would happily act as an SSRF proxy into
the host network. Turn it on only if you intend to monitor internal services.

The same setting governs notification channels. A self-hosted ntfy or Gotify,
or a phone running SMS Gateway for Android, on the local network needs it; see [channels](channels.md#servers-on-a-private-address).

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

The file has to belong to the user SubGlance runs as. In the container image
that is `65532:65532`, and a file mounted from the host keeps its host owner,
so give it to that user before the first start
(`sudo chown 65532:65532 subglance.key`); the
[Compose file](../docker-compose.yml) shows the mount. A key file SubGlance
cannot read is refused at start with its owner and that command.

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

A delivery that fails does not undo this. The error it leaves in the log and in
the database has the channel's credentials taken out first; a URL in it keeps
its host, so "connection refused" still says which server. Errors stored by an
older version are rewritten at the next start, and every start checks again;
to clear the old text from the file's free pages as well, compact the database
afterwards (see [compacting](#running-a-pass-now-and-compacting)).

See [SECURITY.md](../SECURITY.md) for the threat model.

### Trusted proxies

`--trusted-proxies` is empty by default, which means `X-Forwarded-For` and
`X-Real-Ip` are ignored and the peer address is used instead. Those headers are
set by whoever sends them, so honouring one from an unknown caller lets that
caller name its own address — and the login rate limiter keys on that address,
so a fresh value per request is credential guessing with the limit switched off.

If SubGlance runs behind nginx, Caddy, Traefik or a load balancer, name it here
and real client addresses reappear in the rate limiters, in the session list
under Settings → Account, and in the log lines that name a caller:

```
--trusted-proxies 127.0.0.1,172.16.0.0/12
```

Name every proxy in the chain: a CDN in front of a load balancer in front of
nginx is three entries (or the ranges they connect from). SubGlance reads
`X-Forwarded-For` from the right, skipping each address in this list, and
takes the first one that is not in it as the client. A proxy that appends to
the header (nginx's `$proxy_add_x_forwarded_for`, HAProxy's `option
forwardfor`, most load balancers) and one that replaces it both work, because
whatever a visitor wrote into the header stands to the left of the address the
proxy added and is never reached. An entry that is not an IP address stops the
walk, and so does nothing to the left of it.

`X-Real-Ip` is read when a proxy sends no `X-Forwarded-For`. When it sends
both, they must agree: `X-Real-Ip` has to name the client the chain resolved,
or one of the listed proxies the walk passed on the way. If they name different
clients, SubGlance uses the proxy's own address. That case is a proxy that sets
one of the two headers and passes on whatever the visitor wrote in the other:
nginx with only `proxy_set_header X-Real-IP $remote_addr;` forwards a visitor's
`X-Forwarded-For` untouched, and Caddy forwards a visitor's `X-Real-Ip`. Nothing
in the request says which of the two the proxy wrote, so neither is believed,
and a visitor who sends a header of their own shares the proxy's bucket instead
of choosing one. Visitors who send nothing keep their own address. Setting
`X-Forwarded-For` as shown in the nginx examples below avoids the question.

Getting the list wrong fails safe. An unset or too-narrow value means everyone
behind the proxy shares one bucket, which is inconvenient; a value that is too
wide hands the limiter back to the attacker.

Too wide includes an address shared with something that forwards without
writing `X-Forwarded-For`: a TCP tunnel, a layer-4 load balancer or an SSH
forward that connects to nginx from `127.0.0.1` while `127.0.0.1` is listed.
Every visitor then reaches nginx from a listed address, so a chain a visitor
sends ends in a listed hop (nginx appends it, or the visitor writes it), the
walk skips that hop, and the visitor's own entry is read as the client. An
`X-Real-Ip` naming that address does not catch it, because it names a listed
proxy. Every address in the list has to belong to a proxy that writes the
header; one that does not belongs in front of a proxy that does, or out of the
list.

### Changes refused as cross-origin

A change made from the dashboard, such as saving a monitor, must come from a
page at SubGlance's own address; a page on another site cannot act with your
session. Browsers say where a request came from in `Sec-Fetch-Site`, but only
to an HTTPS or loopback address. Over plain HTTP at a LAN address or a host
name, SubGlance compares the page's `Origin` with the `Host` the request
arrived with, and with the address [`--base-url`](#configuration) names.

A proxy that rewrites `Host` breaks the first comparison: nginx sends its
upstream address (`127.0.0.1:8080`) unless told otherwise, and so does Apache
unless `ProxyPreserveHost` is on. Each change is then refused with
`403 cross-origin request refused`, and the dashboard shows that message
instead of saving. Either fix works:

- set `--base-url` to the address in the browser's address bar, or
- have the proxy pass the browser's `Host` on (`proxy_set_header Host
  $http_host;` in nginx, which keeps a port that `$host` drops,
  `ProxyPreserveHost On` in Apache; Caddy and Traefik do by default).

### The sign-in cookie over HTTPS

When the dashboard is opened over HTTPS, the sign-in cookie is `Secure` and
named `__Host-subglance_session`, so the browser never sends it over plain
HTTP. A proxy that terminates TLS passes the request on as plain HTTP, so
SubGlance takes the page's scheme from any one of: a TLS connection,
`X-Forwarded-Proto: https`, or the `Origin` the browser sends with the
sign-in, which names the `https://` page. The last one is what covers nginx,
whose `proxy_pass` sends no `X-Forwarded-Proto` unless it is configured to.
The same rule picks the scheme of a push monitor's URL when `--base-url` is
not set.

Over plain HTTP the cookie is `subglance_session`, without `Secure`, because a
browser refuses a `Secure` cookie there. A browser that still holds that one
from before its HTTPS page was recognised has it replaced at the next sign-in,
and signing out removes both.

### Under a path

SubGlance can share a host with other applications, at a path such as
`https://example.com/subglance/`. The proxy in front strips the path before
passing the request on, so SubGlance itself is still served from `/` and is
never told the path: the dashboard works it out from the address its own
scripts were loaded from, and every request, link and the live stream stay
under it. Set [`--base-url`](#configuration) to the same address, path
included, so alerts and push URLs point there too.

Caddy:

```
example.com {
	redir /subglance /subglance/
	handle_path /subglance/* {
		reverse_proxy 127.0.0.1:8080
	}
}
```

nginx (the trailing slashes on both lines are what strip the path):

```
location /subglance/ {
    proxy_pass http://127.0.0.1:8080/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
```

Open the address with its trailing slash. Both examples redirect
`/subglance` to `/subglance/`; a proxy that serves the dashboard at the bare
path instead makes the browser look for its files one level up, outside the
path. [Public status pages](#public-status-pages) work under the path as
well, at `/subglance/status/<slug>`.

The sign-in cookie applies to the whole host, and every application on that
host shares an origin with the dashboard. Put SubGlance under a path only beside applications you trust;
otherwise give it a subdomain of its own.

### When this host loses its own connection

If the machine SubGlance runs on loses its uplink, router or DNS resolver,
every check fails at once. Without a way to tell that apart from twenty real
outages, every monitor would confirm an incident, every uptime figure would
drop, and the alert — arriving once the connection is back — would be false.

So before a failure on a network error (DNS, connection refused, timeout)
confirms an incident, SubGlance dials the connectivity targets over TCP:
by default `1.1.1.1:53` and `9.9.9.9:53`, two public resolvers run by
independent operators, reached by address so a broken local resolver cannot
decide the answer. **This is outbound traffic you should know about.** It is
sent at that moment, and once when SubGlance starts or the targets are
changed, to learn whether they answer at all (see
[When the targets cannot see out](#when-the-targets-cannot-see-out)) — never
on a timer while everything is healthy. One round is shared by every monitor
that asks within 30 seconds. Only a TCP handshake is made; nothing is sent
over the connection.

When **every** target fails too, the failure is the host's, not the monitor's:

- the check is kept as a heartbeat with its real error, filed under the cause
  `local_network`, and shown as a warning;
- no incident is confirmed and no alert is sent;
- it does not count against the monitor's uptime, the same as any warning.

While the host is offline the targets are re-dialled every 30 seconds, and
when one answers, one notice goes to the [default channel](channels.md) saying
how long the host was offline. A monitor whose target is still down once the
host is back confirms on its next failure, as it would have otherwise.

While the host is offline the dashboard says so once, above the list — "No
outbound connection since 03:12" — and the status wall adds the same words to
its header line. The monitors themselves stay as they are, amber with their
real error, so nothing on screen claims they are fine. The same state is
available to scripts as `GET /api/v1/connectivity` (any signed-in role; see
the [API reference](openapi.yaml)). Reading it never dials anything: it
reports what the last round found.

The check errs towards reporting. A status code, a missing keyword, a failed
JSON assertion or a certificate problem proves the target answered, and is
never suppressed. If even one target answers, nothing is suppressed. An
incident that was already confirmed stays confirmed.

#### When the targets cannot see out

"Every target failed" only means "this host is offline" for targets this
host can reach while it is online. Two common setups break that, and in both
the host is fine:

- a network that may only see the LAN — an `internal: true` Docker network,
  a VLAN without internet — where the targets never answer;
- a Pi-hole or AdGuard setup whose firewall drops outbound port 53 for
  everything but the resolver, a standard way of making every device use it,
  where `1.1.1.1:53` and `9.9.9.9:53` stop answering.

A check that believed those targets would hold back every network-error
outage on every monitor, for good. So two rules, both towards reporting:

- **Targets that have never answered prove nothing.** Until one of them
  answers once — since SubGlance started, or since the targets were changed —
  nothing is held back: a monitor on a closed port confirms after its retries
  and alerts, as it would with the check off. The dashboard says "Connectivity
  targets have not answered since 03:34", the *Connectivity check* card under
  Settings says what to do about it, and the log has one warning.
- **A check that reaches the internet ends an offline spell.** While the host
  is believed offline, a check that connects to a public address — whatever
  it then finds, a 200, a 503 or a bad certificate — proves it is not. The
  targets are dialled once more: if one answers, the spell ends as usual,
  with its notice; if none does, it ends without one, and the targets count
  as never answered again until one does. The dashboard and the card then say
  the targets failed while other checks reached the internet, so they may be
  blocked from this host. A check on a private address is not evidence: when
  the uplink fails, the LAN keeps working.

Neither state sends anything to a channel: the outages they would have held
back are reported again, which is what matters. `GET /api/v1/connectivity`
reports them as `targets_unusable` (`unanswered` or `contradicted`). The fix
is the same for both: set targets this host can always reach, such as your
gateway, below.

A host that starts without a connection — after a power cut, before the
router is up — has targets that have not answered yet, so its first failures
are reported rather than held back. That is the direction this check is
allowed to err in.

To pick your own targets — your gateway, an address you run elsewhere, or
anything else this host should always be able to reach — set
`SUBGLANCE_CONNECTIVITY_TARGETS=gateway:443,other-host:22`. Choose targets
that do not share a failure with your monitors. To turn the check off, set
`SUBGLANCE_CONNECTIVITY_CHECK=false`; then an outage of this host is reported
as an outage of every monitor, as it was before this check existed.

A target's host has to be an address or a name a resolver will look up — the
same rule as a monitor's target (see [Using SubGlance](using-subglance.md)):
no empty label (`exa..mple.com`), no label that starts or ends with a hyphen,
no digits and dots that are not an IPv4 address (`999.1.1.1`), no `.onion`
name, and an internationalised name in its `xn--` form. A target that breaks
it fails every dial without sending anything, so a list made only of such
targets would call this host offline in every round and hold back every real
outage. The flag, the variable, the Settings page and the API all refuse one
and say why. A list saved on the Settings page before this rule still loads,
so an upgrade does not stop the server; the startup log names the target that
cannot be looked up.

An administrator can make the same two changes on the **Settings** page,
under *Connectivity check* (one `host:port` per line, with a button that puts
the default addresses back), or over the API with
`PUT /api/v1/settings/connectivity` (see the [API reference](openapi.yaml)).
They are stored in the database, apply to the running check at once without a
restart, and survive one. A change of targets forgets what the old ones said:
an ongoing "no outbound connection" spell ends without a notice, and the next
suspected outage is judged against the new list. A flag or environment
variable wins over what is saved there, and the API refuses to change a
setting one of them fixes, naming it — so leave both unset if you want to
manage the check from Settings or the API. The card shows a fixed setting
read-only, with the flag or variable that fixes it.

#### When the resolver stops answering

The targets are dialled by address, so they answer even when the resolver
this host uses — often a Pi-hole or AdGuard box for the whole network — has
stopped answering. Then every monitor on a host name fails on its lookup while
the targets look fine. To catch that case too, a failure whose own lookup got
**no answer** (silence, `SERVFAIL` or a refusal from this host's resolver) gets
one more question before it confirms an incident: SubGlance looks up the
reference names, by default `one.one.one.one` and `dns.quad9.net`, through the
same resolver. **This is outbound traffic too:** a lookup of each name, sent to
your resolver and by it to whatever it asks upstream, only at that moment and
then every 30 seconds while the resolver stays silent.

When the targets answer and **none** of the names does, the failure is the
resolver's, not the monitor's: it is kept as a warning with the cause
`local_dns`, no incident is confirmed, and it does not count against uptime.
The dashboard and the status wall say "SubGlance's DNS resolver has not
answered since 03:12", `GET /api/v1/connectivity` reports `resolver_down`, and
when the resolver answers again one notice goes to the default channel saying
for how long it did not.

What it does not catch, on purpose:

- A name that does not exist is an answer. A monitor whose domain has expired
  gets `NXDOMAIN` and confirms its incident as before; so does one whose name
  a filtering resolver blocks.
- A failure on a monitor's own resolver (a DNS monitor with a resolver set)
  is that monitor's outage, and is never put down to this host's.
- If even one reference name gets an answer, nothing is held back. A
  resolver that blocks the reference names answers them (with `NXDOMAIN` or a
  sinkhole address), so it cannot make a real outage look like the
  resolver's; pick names your resolver does not block, and that no monitor
  depends on.
- A monitor that reaches its target by address never looks anything up, and
  is measured as usual while the resolver is down.

To pick your own names, set `SUBGLANCE_CONNECTIVITY_NAMES=router.lan,example.org`
(host names, not addresses: an address is never sent to the resolver; and,
as for the targets, names a resolver will look up — `x.onion` or `999.1.1.1`
would never be asked about, so the round could not tell anything). To keep
the targets but look up no names, set it empty:
`SUBGLANCE_CONNECTIVITY_NAMES=`. The names follow the connectivity check's
switch, including the one under Settings; they are not set from Settings
themselves.

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

## Warning, confirmation and recovery cadence

Unconfirmed failures are Warning. They retain their raw result, failure kind
and optional response snapshot, but send no alert, including on recovery.
Warning/up oscillation does not count as confirmed flapping. Pausing closes any
open incident without a recovery notification and clears the failure streak;
resuming starts a new streak. Restarting an active warning restores its streak.

Confirmed Down monitors use `min(interval_s, 60s)` with the existing jitter (up
to 10% later) and worker queue. This also applies on restart. It is a target cadence, not a
latency guarantee: check duration and worker saturation can delay it. Push
monitors retain their configured reporting deadline; nothing polls a push job.

Uptime is a sample ratio: assessed up / (assessed up + confirmed down). Warning
samples are excluded, never backdated into downtime. The first failed sample
still starts the incident's diagnostic duration, so incident duration and uptime
measure different things. Missing checks and paused periods supply no samples.
Latency averages continue to cover all recorded timings, including warnings.

Migration `0012_heartbeat_assessment.sql` preserves old raw results and hourly
counts. It adds immutable per-check assessment and separate assessed rollup
counts. Legacy samples remain unclassified and are excluded from the new
percentage; the API and detail screen report their excluded count. All-legacy
or all-warning windows return null uptime. Existing assessments survive rollup
and merging late samples. The migration validates the raw-table CHECK constraint,
which may scan a large table at first startup; allow for that upgrade pause.

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
`/metrics` to see whether the pool is keeping up. The same readings, plus how
many workers are busy right now, are on the instance card at
`/settings#instance` for an administrator.

## Memory

Measured on Linux amd64 with the dashboard built in, 20 HTTP monitors checked
every 30 seconds and no one signed in, SubGlance holds about 22 MB resident.

Signing in costs more for a moment. Passwords are hashed with argon2id at
64 MiB a hash, which is what makes a stolen hash expensive to crack. One
sign-in briefly takes the process to about 90 MB; forty at once, which queue
four at a time, took it to between 450 and 590 MB in the same setup. When the
last hash in flight finishes, that memory goes back to the operating system
within seconds instead of the minutes the Go runtime would otherwise take: a
single sign-in is back at 22 MB within a second, and setup followed straight
away by the first sign-in within five. The hand-back runs at most once every
two seconds, so a stream of sign-ins does not buy a full garbage collection
each.

To measure it yourself, read `VmRSS` from `/proc/<pid>/status` (on Linux):
that is the resident figure the numbers above are. The memory column of
`docker stats` is a different metric, the container's cgroup memory usage
with the page cache taken off, so it will not match `VmRSS` exactly. It shows
the same rise and fall around a sign-in, and is fine for watching that, but
compare `VmRSS` with the figures here.

### Compared with Uptime Kuma

Measured on 5 and 6 October 2026 with
[`scripts/benchmark/run.py`](../scripts/benchmark/README.md): SubGlance, Uptime
Kuma 1.23.16 and Uptime Kuma 2.5.5 (`-slim`, SQLite) running at the same time on
one host, each in its own container with its own copy of the same 50 HTTP
monitors, checked every 30 seconds with a 10-second timeout against one shared
target. The figures cover 24 hours after a 10-minute warm-up; nobody signed in
during them. Memory is the summed `VmRSS` of the container's processes, the
same figure as above.

| | SubGlance | Kuma 1.23.16 | Kuma 2.5.5 |
|---|---|---|---|
| Resident memory, median | 20.1 MiB | 105.0 MiB | 134.1 MiB |
| Resident memory, 95th percentile | 26.9 MiB | 153.9 MiB | 191.4 MiB |
| Resident memory, highest | 27.8 MiB | 157.0 MiB | 192.2 MiB |
| Swapped out, median | 1.3 MiB | 25.7 MiB | 28.8 MiB |
| `docker stats` memory, median | 16.6 MiB | 109.2 MiB | 123.1 MiB |
| CPU, mean share of one core | 0.33% | 1.61% | 1.32% |
| CPU time per check | 2.1 ms | 9.7 ms | 7.9 ms |
| Checks made | 137,153 | 143,950 | 144,000 |
| Data directory after 24 hours | 11.2 MiB | 24.9 MiB | 26.5 MiB |
| Image download, linux/amd64 | 7.9 MiB | 146.3 MiB | 173.6 MiB |
| Image unpacked | 22.8 MiB | 531.3 MiB | 636.4 MiB |

How to read it:

- **SubGlance made 4.8% fewer checks.** Each next run is spread by up to 10%
  of the interval so that monitors added together do not stay in lockstep,
  which makes the average gap about 31.5 seconds instead of 30. The CPU time
  per check takes that out: it is total CPU time divided by the checks the
  target counted, and SubGlance's is a fifth of Kuma 1.23's and about a quarter
  of Kuma 2.5's.
- **The host swapped some of each out.** This run's containers had no memory
  limit and no swap cap: the script's 1 GiB, no-swap limit landed after the run
  had started. The host has 2 cores and 3.7 GiB of memory and was doing other
  work. Resident memory leaves out what is in swap; counting it back in, the
  medians are 21.3, 131.2 and 162.9 MiB, so the gap gets wider, not narrower.
  The swap figures are medians over every sample in the window.
- **The image figures are for linux/amd64.** The download is the compressed
  image a pull fetches; unpacked is the root filesystem of a container created
  from the image.
- **This is a comparison, not a forecast.** The three ran side by side so that
  the host's noise fell on all of them; compare them with each other, not with
  a figure from another machine. The target answered plain HTTP on the same
  host, so the cost measured is the monitor's own, not the network's.

Images: SubGlance `ghcr.io/frankgraave/subglance@sha256:2fa19abc511947a625940d013c8dfb23f16a128919d80b96bd245159912c95a8`
(the `develop` build of 5 October 2026), Kuma 1.23.16
`louislam/uptime-kuma@sha256:431fee3be822b04861cf0e35daf4beef6b7cb37391c5f26c3ad6e12ce280fe18`,
Kuma 2.5.5-slim
`louislam/uptime-kuma@sha256:9c56a772a7df53f444a404c579e87a27bd0c201375d00d6745a75bf8138f4342`.
Docker 29.1.3 with the containerd image store, Linux 7.0.

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
subglance_size_limit_passes_total          retention passes that removed history beyond the windows to stay under --max-database-size
subglance_size_limit_unmet                 1 when the last pass ended over that limit with nothing left it may remove
subglance_checks_skipped_total             checks dropped because the previous run was still going
subglance_check_queue_depth                dispatched checks waiting for a worker
subglance_check_workers                    current worker pool size
subglance_check_workers_busy               workers inside a check at the moment of the scrape
subglance_monitors_scheduled               monitors on the schedule
subglance_notification_deliveries_total    alert deliveries, labelled channel_type and outcome (delivered, failed, retried)
```

With [scheduled backups](#scheduled-backups-to-s3-compatible-storage) on,
three `subglance_backup_*` series join them.

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

`subglance_notification_deliveries_total{outcome="failed"}` counts alerts that
gave up on their channel, refused outright or out of retries, and so reached
nobody through it. Every channel type in the build has all three series from
start, at zero, so `increase(subglance_notification_deliveries_total{outcome="failed"}[15m]) > 0`
fires on the first failure. `retried` is an attempt that failed with another
scheduled; it is normal in small numbers. Test sends and the messages
SubGlance sends about itself are not counted. SubGlance also reports a failing
channel without Prometheus: see
[a channel that stops delivering](channels.md#a-channel-that-stops-delivering).

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

A clean stop loses no alert. One still waiting in its
[grouping window](#alert-grouping) is written to the outbox when the stop
begins, and one raised after that (by a check that finishes during the stop, a
push report the server is still answering or a reminder already due) is written
straight away rather than grouped. Whatever has not been delivered when
SubGlance exits is sent on the next start.

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

The URL may point at any host, private addresses included. SubGlance refuses
to start only on one no ping could reach: not http or https, no host, a port
of 0 or above 65535, or a hostname the resolver refuses without asking (two
dots in a row, or a character other than a letter, digit, hyphen, underscore
or dot).

The URL is the switch's credential: whoever knows it can reset the switch and
keep it quiet while this instance is down. So the log and that start-up
refusal show it as its scheme and host only, such as `https://hc-ping.com/…`
for the example above, with no user name, password, path or query. The host
is enough to tell which receiver a failed ping was meant for.

The read-only **Settings → Self-monitoring** card shows watchdog history for
this process. The dashboard explains the limitation once when no watchdog is
configured. Unknown or unavailable diagnostics do not mean it is switched off.
Search hides settings sections without discarding an unsaved password form.

`GET /api/v1/watchdog` exposes the same private, non-cacheable diagnostics to any
authenticated role. Last success records receipt of 2xx headers and survives
later rejection, network failure or liveness suppression. The snapshot never
contains the destination URL, outbound payload, response body or raw error.
History resets on restart; the first ping waits a full interval. Suppressed,
in-flight and overdue are separate from historical success. An unwired source
returns 503; a deliberately disabled watchdog reports `configured=false` with
null history. These observations do not guarantee the receiver will alert.

Three details matter:

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

Coming from `v0.1.0-rc4` or earlier, expect a step in every HTTP monitor's
latency chart at the moment of the upgrade. Those releases reused one
connection between checks; HTTP checks now open a fresh one every time, so the
recorded latency includes the DNS lookup and the TCP and TLS handshakes it used
to skip — usually two or three extra round trips to the server. Nothing got
slower: the chart now measures what a new visitor waits for. Latency recorded
before and after the upgrade is not directly comparable.

Coming from `v0.1.0-rc6` or earlier, a monitor with an interval over 60
seconds alerts sooner. A failure that is not confirmed yet is now rechecked
every minute instead of at the monitor's interval, so a five-minute monitor
with `retries: 3` alerts about two minutes after its first failed check rather
than ten. A brief blip that used to fall between two checks can now reach the
threshold; raise `retries` (up to 10) on a monitor that should wait longer.
See [How a failure becomes an alert](using-subglance.md#how-a-failure-becomes-an-alert).

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

The backup file is created readable by its owner only (mode 0600), as are
`subglance.db` and its `-wal` and `-shm` files: they hold password hashes and,
without `--secret-key`, every channel credential. A database an older version
created readable by others is narrowed to 0600 when the server starts, and the
log names the files it changed. On Windows the modes are left to the folder's
permissions.

The command refuses to overwrite an existing file. That is deliberate: a backup
command that silently truncates its destination will eventually destroy the one
good copy you had, most likely when a nightly job reuses a fixed filename and
that run fails halfway. Give each run a new name — `$(date +%F)` above — and
prune old ones yourself.

It also refuses a data directory that holds no `subglance.db`, and names the
path it looked for. A mistyped `--data-dir`, or a `SUBGLANCE_DATA_DIR` that is
not set where the server has it, is the wrong directory rather than an empty
instance: backing it up would create a database there and report a successful
copy of nothing, every night, until the day the copy is needed.

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
   cp /restore/subglance-backup.db /data/subglance.db && \
   chown -R 65532:65532 /data'
docker compose up -d
```

Three things to check while restoring:

- Remove any leftover `subglance.db-wal` and `subglance.db-shm` next to it, as
  the command above does. They belong to the old database, and SQLite would
  try to replay them over the restored one.
- Give the data directory back to the user SubGlance runs as, `65532:65532`
  in the image, as the `chown` above does. If the container runs with
  `--user` (`user:` in Compose), put that user's `uid:gid` in the `chown`
  instead. A file copied in by a borrowed root shell belongs to root, and a
  backup is written with mode `0600`, so SubGlance may not write the copy:
  the next start refuses it, naming the file, its owner and the `chown` that
  fixes it. The same goes for a fresh volume, which the borrowed container
  creates as a directory owned by root that only root may write.
- Restore into a version of SubGlance that is the same as, or newer than, the
  one that took the backup. An older binary refuses to start against a newer
  schema rather than writing rows against a table shape it does not
  understand; the error names the migration it does not recognise.

### Scheduled backups to S3-compatible storage

A backup on the same disk as the database does not survive the disk. Set a
bucket and SubGlance takes the same `VACUUM INTO` snapshot on a timer,
compresses it and uploads it, then deletes the oldest backups beyond
`--backup-keep`. It works with AWS S3 and with anything that speaks the S3 API:
Backblaze B2, Cloudflare R2, MinIO, Wasabi.

What it does, and what it deliberately does not:

- **One full, gzip-compressed copy per run**, named
  `subglance-<UTC time>.db.gz` under the prefix. Pruning only ever touches
  objects with exactly that name shape, so anything else you keep under the
  same prefix is left alone.
- **A failed upload never removes anything.** Old backups are pruned only
  after the new one is safely in the bucket.
- **Never two at once.** A run that is still going when the next is due is
  not doubled up.
- **The first backup follows the newest one already in the bucket**, not the
  moment the process started, so an instance that is redeployed every night
  still backs up once a day rather than never — or on every start.
- **A run is abandoned after an hour** and counted as a failed run, so an
  upload that stalls without an error cannot stop the schedule.
- **A failed run is retried within the hour**, and sends one notice to the
  default channel per failing streak, then again every day it keeps failing.
  With no default channel set, the failure is only in the log, on `/metrics`
  and in `GET /api/v1/backup`. During the default channel's quiet hours the
  notice waits for the next retry after the window.
- **No client-side encryption.** Use the storage provider's server-side
  encryption. Channel secrets are only encrypted in the backup if
  [`--secret-key`](#encrypting-channel-configuration) is set.

The credentials are environment variables only, never flags: a flag is
visible to every user on the host in `ps`. Prefer
`SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE` pointing at a mounted secret over
putting the key itself in the environment, for the same reason as
`--secret-key`. Give the key only what it needs on that one bucket: list, get,
put and delete objects. The mounted file has to be readable by the user the
image runs as, so keep it owner-only and give it to that user:
`chmod 600 s3.secret && sudo chown 65532:65532 s3.secret`.

AWS S3, with the bucket in `eu-west-1`:

```yaml
    environment:
      SUBGLANCE_BACKUP_TARGET: s3://my-subglance-backups/prod
      SUBGLANCE_BACKUP_REGION: eu-west-1
      SUBGLANCE_BACKUP_ACCESS_KEY_ID: your-access-key-id
      SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE: /run/secrets/s3.secret
    volumes:
      - ./s3.secret:/run/secrets/s3.secret:ro
```

Backblaze B2, where the endpoint and region come from the bucket's details
page and the key is an application key limited to that bucket:

```yaml
      SUBGLANCE_BACKUP_TARGET: s3://my-subglance-backups/prod
      SUBGLANCE_BACKUP_ENDPOINT: https://s3.eu-central-003.backblazeb2.com
      SUBGLANCE_BACKUP_REGION: eu-central-003
```

The endpoint is the service URL alone, with no path; one with a port of 0 or
above 65535, or a hostname the resolver refuses without asking, stops
SubGlance at startup rather than failing every upload. So does a user name or
password in the endpoint or the target (`https://key:secret@host`): requests
are signed with the access key and secret above, never with credentials in
the URL, and the refusal does not repeat them. The same goes for the bucket
and the region:

- The bucket takes letters, digits, dots, hyphens and underscores. A host and
  port in its place (`s3://minio.local:9000/backups`) is refused with a
  pointer to the endpoint, which is where the service URL goes.
- A region with a `/` or `,` in it, or a control character, is refused for
  every service: it breaks the request signature. Any other region name is
  passed on as written, since an S3-compatible service may name its regions
  as it likes.
- With no endpoint, requests go to `<bucket>.s3.<region>.amazonaws.com` (or
  `s3.<region>.amazonaws.com/<bucket>` for a bucket with a dot in its name),
  so the bucket and the region are held to the hostname rule: a bucket that
  starts or ends with a hyphen, or a region with a space, is refused.
Cloudflare R2 uses `https://<account id>.r2.cloudflarestorage.com` as the
endpoint and `auto` as the region. MinIO uses its own URL and accepts any
region. With a custom endpoint, requests use path-style URLs
(`endpoint/bucket/key`), which every S3-compatible service accepts.

Whether it is working is visible in three places: the log line
`backup uploaded` after every run, `GET /api/v1/backup` (administrators), and
these series on `/metrics`, present only when a target is set:

```
subglance_backup_last_success_timestamp_seconds   when the last backup reached the bucket
subglance_backup_last_size_bytes                  its compressed size
subglance_backup_failures_total                   failed runs since start
```

Alert on `time() - subglance_backup_last_success_timestamp_seconds` rather
than on failures: it also catches a backup that stopped being attempted.

### Restoring from S3

`subglance restore` downloads the newest backup from the configured target
and puts it in place of the database. It reads the same settings as the
server, so in Compose it is one command with nothing else to type:

```sh
docker compose stop subglance
docker compose run --rm subglance restore
docker compose start subglance
```

`--from subglance-20260925T030000Z.db.gz` picks an older backup instead; an
unknown name is refused with the name of the newest one.

It is careful in four ways:

- **It refuses while a server holds the data directory.** Replacing the file
  under a running server loses whatever the server writes next, so stopping
  SubGlance first is enforced, not just advised. The server holds an
  exclusive lock on `subglance.lock` in the data directory for as long as it
  runs, and the restore takes the same lock and keeps it until the new
  database is in place. The lock belongs to the operating system (`flock` on
  Linux and macOS, `LockFileEx` on Windows), so it works across containers
  sharing one volume, and a server that crashed leaves nothing behind to
  clean up. The same lock stops a second server from starting on a data
  directory that is already in use. Leave the file where it is; deleting it
  does not release a lock that is held.
- **It also refuses while something answers on `--addr`**, as a second guard.
  `--force` skips only this check, for when the thing answering is not
  SubGlance; it never overrides the lock.
- **It checks the download before touching anything.** A backup that does not
  decompress or fails SQLite's `quick_check` leaves the current database where
  it is.
- **It deletes nothing.** The database it replaces, and its `-wal` and `-shm`
  files, are renamed with a `.before-restore-<time>` suffix, so restoring the
  wrong backup can be undone. Remove them once the restored instance looks
  right.

## Signed-in sessions

Settings → Account lists every browser signed in to your account: the browser
and platform its User-Agent header names, the address it signed in from, when
it signed in and when it was last used. "Sign out" beside a session ends that
one on its next request; "Sign out everywhere else" ends all but the browser
you are using. Neither touches API tokens, which are revoked on their own card.

An administrator also sees each account's sessions under Users, with
addresses, and can sign an account out everywhere. The account keeps its
password, role and tokens; that is for a lost or shared device. Removing the
account is what takes its access away. Other roles see only their own
sessions.

Two things to read with care:

- **The address is the one SubGlance saw.** Behind a reverse proxy that is the
  proxy's, unless it is named in [`--trusted-proxies`](#trusted-proxies).
- **The browser name is a claim.** A User-Agent header is whatever the client
  sends. It is there to tell your laptop from your phone, not to prove which
  device a session is on. "Last seen" moves at most once an hour, so it names
  the hour, not the minute.

Changing the password, or `subglance reset-password` below, still signs every
other session out at once.

## Locked out

Forgot the password, and nobody else with an administrator account can sign
in? `subglance reset-password` sets a new one from the data directory:

```sh
docker compose stop subglance
docker compose run --rm subglance reset-password --email you@example.com
docker compose start subglance
```

Or, running from source:

```sh
subglance reset-password --email you@example.com --data-dir /var/lib/subglance
```

It asks for the new password twice without showing it, and applies the same
rule as the sign-in page: at least 12 characters. There is deliberately no flag
or environment variable to pass the password in, because both end up in the
shell history or the process list. Where there is no terminal to ask on — a
script, or `docker run` without `-it` — `--generate` makes up a password,
sets it and prints it once. Copy it, sign in, and change it in Settings.

What else it does:

- **It signs the account out everywhere.** Every session of that account
  ends, as it does after a password change in Settings, so a reset after a
  suspected compromise also locks out whoever else was signed in. The
  account's API tokens keep working; revoke them in Settings if they are part
  of the problem.
- **It lifts the sign-in limit for that address.** Ten failed attempts within
  15 minutes block an address for a while, and someone who has forgotten the
  password has usually tripped that. The count for the reset address is
  cleared; every other address and every client IP keeps its own.
- **It touches one account.** An unknown address is refused, and the refusal
  does not list the accounts that do exist.

It refuses while SubGlance runs, the same way [`subglance
restore`](#restoring-from-s3) does: the server holds the lock on
`subglance.lock` in the data directory, and `--force` never overrides it. It
also refuses while something answers on `--addr`, and `--force` skips only
that check. It refuses a data directory without a `subglance.db` in it rather
than creating an empty database there, since that almost always means the
wrong `--data-dir`.

Anyone who can run this can already read every monitor and account in the
data directory, so the command asks for nothing more than that access. Keep
the data directory as private as the passwords in it. Channel secrets are
readable there too unless they are [encrypted at
rest](#encrypting-channel-configuration); with a `--secret-key` set, reading
them still takes that key, and a reset does not change that. The command
does not need `--secret-key`: it never reads a channel's configuration.

## Public status pages

An administrator creates status pages under **Settings → Status pages**. A
page is off until it is switched on; once on, anyone who can reach the server
can open it, without an account:

- `/status/<slug>` is the page itself: one HTML document, its stylesheet
  inline, no JavaScript beyond a one-line theme choice.
- `/api/v1/status-pages/<slug>` is the same answer as JSON.

A visitor sees the public names given to the monitors on the page, their
state, 90 days of history and uptime, announced maintenance and recent
outages. Internal names, targets, tags, error text and incident causes are
never part of either answer.

What to expect once a page is public:

- **Unknown and switched-off pages look the same.** Both are the same 404, so
  a page that is off does not confirm that its address exists. A slug is not a
  secret, though: anyone who guesses it can read an enabled page. There is no
  password or private-link mode.
- **Answers are reused for 30 seconds.** A page is built at most once per 30
  seconds and sent with `Cache-Control: public` and an `ETag`, so a proxy or
  CDN in front can serve it too. A change, including switching a page off,
  can take that long to show to visitors: this server forgets its copy when
  the page is saved, but a cache in front keeps its own until it expires.
- **Rate limits.** 5 requests a second per client address (bursts of 20) and
  50 a second in total, checked before the database is read; past them the
  answer is `429` with `Retry-After`. Answers served from the 30-second cache
  do not count, so a page shared widely during an outage keeps answering.
  Behind a reverse proxy, set [`--trusted-proxies`](#trusted-proxies), or
  every visitor shares the proxy's single allowance.
- **Search engines are asked not to index a page** (`X-Robots-Tag: noindex`)
  unless its settings allow it.
- **No cookies.** The routes never read or set one, so a shared cache cannot
  hand one visitor's session to another, and a signed-in operator sees exactly
  what the public sees.
- **The page cannot be framed** by another site.

### A page of your own

Each page's settings also decide how it looks to the people you give it to:

- **Language.** English or Dutch, for every fixed text on the page: the status
  words, headings, dates and the decimal separator.
- **Accent colour.** An optional `#rrggbb` colour for the page title. The page
  follows each visitor's light or dark theme, so the colour has to reach a
  contrast of 3:1 against both; one that does not is refused on save, with
  both measured ratios. The status lamps and bars keep their own colours.
- **Footer credit.** "Monitored with SubGlance" is shown unless you switch it
  off. The time zone on the same line stays.
- **Logo.** A PNG, JPEG or WebP image of at most 256 KB and 2048 pixels on a
  side, shown above the title. It is stored in the database and served from
  this server beside the page, so the page still loads nothing from anywhere
  else. The type is read from the file itself, not its name, and SVG is
  refused because it can carry script. Pick a logo that reads on both a light
  and a dark background. Each upload gets a new random address, so a cache in
  front never shows an old logo; a page that is off answers 404 for its logo
  too.

### A page on its own subdomain

A page loads nothing but its two fonts and its logo, and asks for them
relative to itself, so a reverse proxy can serve it at the root of a domain of
its own. Map `/` to `/status/<slug>/` (with the trailing slash), `/fonts/` to
`/status/fonts/` and `/logos/` to `/status/logos/`.
Everything else on that domain can stay unanswered: the dashboard and the rest
of the API do not need to be reachable there.

Caddy, for a page with the slug `acme` and SubGlance on port 8080:

```
status.example.com {
	handle / {
		rewrite * /status/acme/
		reverse_proxy 127.0.0.1:8080
	}
	@assets path /fonts/* /logos/*
	handle @assets {
		rewrite * /status{path}
		reverse_proxy 127.0.0.1:8080
	}
	handle {
		respond 404
	}
}
```

nginx, the same mapping:

```
server {
    server_name status.example.com;
    # listen and TLS settings as for any other site

    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

    location = / {
        proxy_pass http://127.0.0.1:8080/status/acme/;
    }
    location /fonts/ {
        proxy_pass http://127.0.0.1:8080/status/fonts/;
    }
    location /logos/ {
        proxy_pass http://127.0.0.1:8080/status/logos/;
    }
    location / {
        return 404;
    }
}
```

Name the proxy in `--trusted-proxies` so the rate limit sees each visitor's
own address. Caddy sets `X-Forwarded-For` by default; nginx needs the line
above. Appending to the header, as that line does, is safe: SubGlance reads
the chain from the right (see [trusted proxies](#trusted-proxies)).
