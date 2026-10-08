# Configuration files

SubGlance can write its configuration to a YAML file and read it back:
monitors, notification channels, routing rules and maintenance windows. Use it
to move a setup to another instance, to keep it in a repository where changes
show up as diffs, or to create forty monitors from a file instead of forty
forms.

It is not a backup. History, users and API tokens are not in the file, and
neither are credentials. For a full copy of an instance, see
[Backup and restore](operations.md#backup-and-restore).

- [Exporting](#exporting)
- [Importing](#importing)
- [What never leaves the instance](#what-never-leaves-the-instance)
- [Keys](#keys)
- [The format](#the-format)
- [Coming from Uptime Kuma](#coming-from-uptime-kuma)

## Exporting

On **Settings → Import & export**, *Download configuration* saves the file as
`subglance-config.yaml`. From a script:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/config/export > subglance.yaml
```

Export needs an editor or an administrator. The file lists every monitor,
channel, routing rule and maintenance window. One-off maintenance windows that
have already ended are left out: they are history, not configuration.

## Importing

On **Settings → Import & export**, choosing a file runs the dry run below and
shows its report: what each object would become, which fields an update
changes, and which objects need a value filled in after the import. A refused
file shows the error at its place in the file. Nothing is written until you
confirm the import.

From a script, look first. A dry run reports what the import would do and writes nothing:

```sh
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/yaml' \
  --data-binary @subglance.yaml \
  'http://localhost:8080/api/v1/config/import?dry_run=true'
```

The report lists every object in the file with its `action` (`create`,
`update` or `unchanged`), the fields an update `changes`, and any
`needs_secrets` (see below). Run it again without `dry_run` to apply it.

Import follows three rules.

- **It never deletes.** A monitor on the instance that the file does not
  mention is left alone. There is no mode in which the file replaces the
  instance.
- **It is all or nothing on validation.** Every object is checked against the
  same rules the API applies before the first write. One bad field refuses the
  whole file, and the error says where: `monitors[3].interval_s: interval_s
  must be between 20 and 86400`.
- **Twice is the same as once.** Monitors and channels are matched by key,
  routing rules by their tag pair, and maintenance windows by all of their
  fields, so importing a file a second time changes nothing.

A field the file leaves out keeps its current value on an existing object, and
gets the same default the API gives a new one. A hand-written file can
therefore rename one monitor without restating everything else about it:

```yaml
version: 1
monitors:
  - key: shop
    name: Webshop
```

Lists work the same way: an omitted `channels` or `tags` leaves the current
ones alone, and an empty one (`channels: []`, `tags: {}`) clears them.

A push monitor created by an import is issued a new push URL. It is shown once,
as `push_url` in the import report, exactly like the response that creates a
push monitor through the API. Update the job that pings it.

## What never leaves the instance

A value that proves the right to send or read something is written as
`<fill in after import>`:

- every channel setting except the ones that say where a message goes (`to`,
  `from`, `chat_id`, `channel`, `username`, `host`, `port`) and a webhook's
  `method` and `body` template, which is the same rule the channel API uses
  when it masks a read;
- the value of every request header an HTTP monitor sends (the header names
  stay, so the file still says which headers are sent);
- an HTTP monitor's request body.

The push URL of a push monitor is not written at all.

On import, a placeholder keeps what the instance already holds for that field,
so exporting and re-importing on the same instance changes nothing. On an
instance that has nothing to keep, the object is created without the value and
**switched off**, and the report lists the field under `needs_secrets`. A
channel without its token, or a check without its authorization header, would
fail every attempt and look like an outage, so it waits for you instead. Fill
the value in, either in the file before importing or in the interface
afterwards, and switch the object on.

## Keys

Every monitor and channel in a file has a `key`: lowercase letters, digits,
dots, dashes and underscores, at most 64 characters. It is how an import finds
the object it created last time.

An object that has never been exported has no key. The first export derives
one from its name (`API (prod)` becomes `api-prod`, with `-2`, `-3` added when
two names collide) and stores it. From then on the key belongs to the object:
renaming the monitor in the interface does not change it, so the next import
still updates the same monitor instead of creating a second one. Deleting the
object deletes its key.

A monitor and a channel may share a key; they are looked up separately.

## The format

```yaml
version: 1
channels:
  - key: ops-slack
    name: Ops Slack
    type: slack
    enabled: true
    config:
      url: <fill in after import>
  - key: on-call
    name: On call
    type: telegram
    enabled: true
    config:
      bot_token: <fill in after import>
      chat_id: "-100200"
    quiet_hours:
      start: "23:00"
      end: "07:00"
      timezone: Europe/Amsterdam
      during: hold
monitors:
  - key: api-prod
    name: API (prod)
    type: http
    target: https://api.example.com/health
    enabled: true
    interval_s: 30
    timeout_s: 10
    retries: 3
    recovery_threshold: 2
    repeat_after_s: 900
    method: GET
    expected_status: 200-299
    keyword: ""
    keyword_mode: absent_ok
    follow_redirects: true
    capture_response: true
    headers:
      Authorization: <fill in after import>
    json_assertion:
      path: checks.db.status
      operator: equals
      expected: up
    ssl_warn_days: 14
    min_tls_version: "1.2"
    tags:
      env: prod
    channels: [ops-slack, on-call]
  - key: www-dns
    name: www address
    type: dns
    target: www.example.com
    enabled: true
    interval_s: 300
    timeout_s: 10
    dns:
      record_type: A
      expected: ["192.0.2.10"]
    tags:
      env: prod
    channels: [ops-slack]
  - key: example-domain
    name: example.com registration
    type: domain
    target: example.com
    enabled: true
    interval_s: 86400
    timeout_s: 10
    retries: 0
    domain_warn_days: 30
    tags: {}
    channels: [ops-slack]
  - key: nightly-backup
    name: Nightly backup
    type: push
    enabled: true
    push_interval_s: 86400
    push_grace_s: 600
    tags: {}
    channels: []
routing_rules:
  - tag_key: env
    tag_value: prod
    channels: [on-call]
    exclude: [nightly-backup]
maintenance:
  - name: Patch night
    tag_key: env
    tag_value: prod
    timezone: Europe/Amsterdam
    weekdays: [2]
    local_time: "02:00"
    duration_minutes: 60
```

Field names and allowed values are the ones the API uses; see
[`openapi.yaml`](openapi.yaml) and [Using SubGlance](using-subglance.md).
A few points specific to the file:

- `version` is required. This release reads and writes version 1 and refuses
  any other, rather than guessing what a newer file means. A change to the
  format raises the number, and a newer SubGlance keeps reading older files.
- Unknown fields are an error, not ignored. `interval: 30` instead of
  `interval_s: 30` would otherwise import cleanly and leave the monitor on its
  old schedule.
- A file holds one YAML document. A second document after `---` is refused
  instead of being silently dropped.
- `json_assertion: null` removes a monitor's assertion; leaving the field out
  keeps it. `expected` is a plain YAML value, and its type matters: `1` is the
  number and `"1"` the string, exactly as in the API.
- `dns` is written for every `dns` monitor and holds `record_type`,
  `expected` and, when one is set, `resolver`. An import that leaves it out of
  an existing dns monitor keeps what the monitor has; a new dns monitor needs
  it.
- `domain_warn_days` is written for every `domain` monitor. A new domain
  monitor without it warns 30 days ahead; an import that leaves it out of an
  existing one keeps what the monitor has.
- `default: true` on a channel makes it the instance default. An import never
  clears the default, so a file that does not mention one leaves it as it is.
- A reference (`channels`, `exclude`, a maintenance window's `monitor`) may name
  an object that is in the file or one that already has that key on the
  instance.
- A maintenance window is identified by all of its fields. Editing a window in
  the file and importing it adds the edited window next to the old one; delete
  the old one in the interface.
- The file is limited to 4 MiB.

## Coming from Uptime Kuma

`subglance import uptime-kuma` reads an Uptime Kuma database and writes a
configuration file in the format above. It does not touch SubGlance: you read
the file, then import it like any other, with the dry run first. Kuma 1.23 and
2.x are supported, as long as Kuma keeps its data in SQLite (`kuma.db`); a
Kuma 2 installation set up with MariaDB has no such file.

The database is opened read-only and nothing is written beside it, so Kuma can
keep running while you convert, and a read-only mount is enough. Give it
`kuma.db` or the data directory that holds it:

```sh
subglance import uptime-kuma /path/to/uptime-kuma/data -o kuma.yaml
```

With Docker, mount Kuma's data into the SubGlance image, read-only. Kuma's own
`docker run` instructions keep it in a volume named `uptime-kuma`; its compose
file uses the `data` directory beside the compose file instead:

```sh
docker run --rm -v uptime-kuma:/kuma:ro ghcr.io/frankgraave/subglance:edge \
  import uptime-kuma /kuma > kuma.yaml

# Kuma started with its compose file
docker run --rm -v "$PWD/data:/kuma:ro" ghcr.io/frankgraave/subglance:edge \
  import uptime-kuma /kuma > kuma.yaml
```

Without `-o` the file goes to standard output and the summary to standard
error. The file ends with two lists, as comments: what was **not imported**,
each with its name and the reason, and what was **imported with a change to
check**. Nothing is left out without being named there.

### What comes over

| Kuma | SubGlance |
|---|---|
| HTTP(s) | `http` |
| HTTP(s) - Keyword, with *Invert Keyword* | `http` with `keyword` and `keyword_mode: must_contain` (`must_not_contain`) |
| HTTP(s) - Json Query | `http` with a `json_assertion`: `==`, `!=`, `<` and `>` on a plain path such as `data.items[0].status` |
| TCP Port | `tcp`, `host:port` |
| Ping | `ping` |
| DNS: A, AAAA, CNAME, MX or TXT | `dns` with the same name, `record_type` and resolver; one *record equals* condition becomes the one `expected` value |
| Push | `push`; Kuma's heartbeat interval becomes `push_interval_s` |
| Heartbeat interval, retries, request timeout | `interval_s`, `retries`, `timeout_s` |
| Accepted status codes, method, *Max. Redirects* | `expected_status`, `method`, `follow_redirects` (off when Kuma allowed none) |
| Request headers and body, basic or bearer auth | `headers` and `body`, auth as an `Authorization` header, all with withheld values |
| Paused | `enabled: false` |
| Tags | `tags`; a tag without a value becomes `yes`, and a monitor inside a group gets the tag `group` with the group's name |
| Notifications: Discord, Slack, Telegram, SMTP, ntfy, Gotify, Webhook | channels of the same type, assigned to the same monitors |
| Notifications: Microsoft Teams, Matrix, Pushover | `webhook` channels with [a body of their own](channels.md#a-body-of-your-own), the one the channel documentation gives for that service |

Credentials stay out of the file, exactly as in an export: webhook URLs, bot
tokens, passwords, ntfy topics, header values and request bodies are written as
`<fill in after import>`. The import creates those channels and monitors
**switched off** and lists the missing values under `needs_secrets`. Fill them
in, in the file or in the interface afterwards, and switch them on. Server
addresses, recipients and chat ids come over as they are.

### What changes on the way

- An interval below 20 seconds becomes 20, and values outside SubGlance's other
  limits (retries up to 10, timeout up to 120 seconds) are moved inside them.
  Each one is listed.
- A push monitor gets a **new push URL**, shown once in the import report.
  Point the job that calls Kuma's push URL at it.
- Kuma repeats an alert every *n* checks; SubGlance repeats after a time, so
  *n* times the interval becomes `repeat_after_s`. Kuma's default of 0 (never
  repeat) is left out, and the monitor gets SubGlance's default of a reminder
  every 15 minutes while an outage is unacknowledged. Set `repeat_after_s: 0`
  if you want none.
- Kuma compares a JSON query's result as text, so `42` matches the number and
  the string. SubGlance compares typed values: an expected value that reads as
  a number, `true`, `false` or `null` is written as that value, and listed, in
  case the field holds a string.
- *Ignore TLS/SSL errors* has no equivalent. SubGlance verifies every
  certificate, so a self-signed or expired one fails the check.
- A DNS monitor without conditions passes on any answer of its record type, as
  in Kuma: `expected: []`. With one *record equals* condition, Kuma passed when
  any record matched; a `dns` monitor wants exactly the expected A, AAAA or MX
  records, so another record the name should have goes in `expected` too, and
  each such monitor is listed. CNAME and TXT mean the same in both.
- Kuma 2 tries a list of resolvers in turn; a `dns` monitor asks one. The first
  is kept, with Kuma's port when it is not 53, and the rest are listed. A
  resolver on a private address, such as a Pi-hole or AdGuard Home on the local
  network, is listed as well: SubGlance asks it only when started with
  [`--allow-private-targets`](operations.md#private-targets). So is a resolver
  given by host name, which may resolve to such an address.
- An email channel's Cc recipients become ordinary recipients. Bcc recipients
  are left out rather than shown to everyone, and listed.
- A webhook channel receives [SubGlance's payload](channels.md#the-webhook-payload),
  not Kuma's. A custom body is not carried over, because Kuma's templates are
  written in another language; give the channel
  [a body of its own](channels.md#a-body-of-your-own).
- Teams, Matrix and Pushover have no channel type of their own, so each
  becomes a webhook built as [the channel documentation](channels.md#a-body-of-your-own)
  describes, and is listed with the value to fill in: the Workflows URL for
  Teams; an `Authorization: Bearer` header with the access token for Matrix,
  whose room address comes over; and for Pushover a URL that carries the
  application token and user key. Pushover's priority, sound, device and
  message lifetime are written into the body. Kuma's card tags, Matrix
  message template, Pushover title and separate recovery sound have no
  counterpart, and each one that was set is listed.

### What does not come over

Monitor types SubGlance has no check for (Docker, gRPC, MQTT, databases, game
servers and the rest), DNS monitors on CAA, NS, PTR, SOA or SRV records, DNS
monitors whose conditions are more than one *record equals* (a *contains* or
an *or* has no counterpart in a list of expected values), DNS monitors with
no resolver (Kuma never ran their check) or with a TXT value that starts or
ends with a space (Kuma compared it exactly), groups themselves,
monitors in *Upside Down Mode* (imported as they are, they would report the
opposite state), JSON queries that
use JSONata beyond a plain path, other authentication methods (NTLM, OAuth2,
mTLS), notification types SubGlance has no channel for, status pages and
maintenance windows. History stays in Kuma.
