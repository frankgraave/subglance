# Configuration files

SubGlance can write its configuration to a YAML file and read it back:
monitors, notification channels, routing rules, maintenance windows and status
pages. Use it
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
have already ended are left out: they are history, not configuration. Status
pages are administered by administrators only, so they are in the file when an
administrator exports it, and left out when an editor does.

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
  routing rules by their tag pair, maintenance windows by all of their
  fields and status pages by their slug, so importing a file a second time
  changes nothing.

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
status_pages:
  - slug: acme
    title: Acme services
    description: The services customers use.
    timezone: Europe/Amsterdam
    language: en
    accent: ""
    hide_credit: false
    indexable: false
    enabled: true
    selection: monitors
    monitors:
      - monitor: api-prod
        name: Public API
      - monitor: www-dns
        name: Website
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
- A status page is identified by its `slug`, compared without regard to case.
  Changing the slug in the file describes a second page; rename a page's
  address in the interface. `monitors` lists the page's monitors in page
  order, each by its monitor key and under the `name` visitors see; on a page
  with `selection: tag` (and a `tag_key` and `tag_value`) it lists the tagged
  monitors that have a public name. A `selection` given in the file brings its
  tag pair with it, so `selection: monitors` clears the tag.
- A status page that the import creates with `enabled: true` is filled with
  its monitors before it is switched on, so it is never published empty.
- Status pages need an administrator. An editor importing a file with
  `status_pages` is refused whole, with nothing written, rather than having
  the pages silently skipped.
- A status page's logo is not in the file. It is an image, which does not
  belong in a file meant to be read in a diff; upload it again after the
  import.
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
error. The summary counts converted and total monitors, channels, maintenance
windows and status pages. The file ends with two lists, as comments: what was
**not imported**, each with its name and the reason, and what was **imported
with a change to check**. Nothing is left out without being named there.

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
| Heartbeat interval, request timeout | `interval_s`, `timeout_s` |
| Retries | `retries`, one more than in Kuma, so the alert comes on the same failed check (see below); on a push monitor, `push_grace_s` |
| *Certificate Expiry Notification*, with the days under *Settings, Notifications* | `ssl_warn_days`, one more than the largest day listed, so the first notice comes on the same day (see below) |
| Kuma 2's *Domain Name Expiry Notification*, with the days under *Settings, Notifications* | one `domain` monitor per registered domain, with `domain_warn_days` one more than the largest day listed (see below) |
| Accepted status codes, method, *Max. Redirects* | `expected_status`, `method`, `follow_redirects` (off when Kuma allowed none) |
| Request headers and body, basic or bearer auth | `headers` and `body`, auth as an `Authorization` header, all with withheld values |
| *Body Encoding* of a request body: JSON, x-www-form-urlencoded or XML | a `Content-Type` header with the type Kuma sent: `application/json`, `application/x-www-form-urlencoded` or `text/xml; charset=utf-8`, written out rather than withheld; a `Content-Type` among the monitor's own headers wins, as it did in Kuma |
| Kuma 2's *Save HTTP Error Response for Notifications* | `capture_response`, on or off as in Kuma, so a monitor whose responses Kuma did not keep does not start keeping them; a monitor that also kept passing responses, a keyword or JSON query monitor that kept only the response of a failed status code (SubGlance also keeps it when the keyword or the query fails), or a *Response Max Length* of 0, below Kuma's default of 1024 characters or above SubGlance's 2048 bytes, is listed, because SubGlance keeps the first 2048 bytes of a failed check's response only; at Kuma's default it keeps up to twice what Kuma did |
| Paused | `enabled: false` |
| Tags | `tags`; a tag without a value becomes `yes`, and a monitor inside a group gets the tag `group` with the group's name |
| Notifications: Discord, Slack, Telegram, SMTP, ntfy, Gotify, Webhook | channels of the same type, assigned to the same monitors |
| Notifications: Microsoft Teams, Matrix, Pushover, Mattermost, Rocket.Chat, Google Chat, Home Assistant | `webhook` channels with [a body of their own](channels.md#a-body-of-your-own), the one the channel documentation gives for that service |
| Notifications: Twilio | `sms` channels with [`provider: twilio`](channels.md#twilio), the same account SID and sender |
| Status pages | `status_pages`, with their slug, title, description, publication and indexing settings, footer credit visibility and ordered monitor list |
| Maintenance: *Single Maintenance Window* | a one-off window, from and to the same wall-clock times in the window's time zone |
| Maintenance: *Recurring - Day of Week*, *Recurring - Interval* of one day, and a *Cron Expression* that starts at one time of day on chosen weekdays (Kuma's default `30 3 * * *` is one) | a weekly window on the same weekdays, at the same time, for as long, in the same time zone |

Credentials stay out of the file, exactly as in an export: webhook URLs, bot
tokens, passwords, ntfy topics, Twilio auth tokens, header values and request
bodies are written as `<fill in after import>`. The import creates those
channels and monitors **switched off** and lists the missing values under
`needs_secrets`. Fill them in, in the file or in the interface afterwards, and
switch them on. Server addresses, email recipients, chat ids and a Twilio
sender come over as they are, so a sender that is a phone number stays visible
in `from`. Recipient numbers do not: they are personal data, so an SMS
channel's `numbers` is withheld too, and the report shows the number Kuma sent
to in masked form (`+31 6 •••• 5678`).

### Status pages

A page's `published` setting becomes `enabled`, `search_engine_index` becomes
`indexable`, and `show_powered_by` becomes the inverse of `hide_credit`.
Pages use `selection: monitors`. Only memberships in public Kuma status-page
groups are included, flattened in group order and then monitor order; group
headings do not come over. A monitor listed in more than one group appears
only at its first position. Each entry refers to the converted monitor's key
and uses its Kuma monitor name as the public name. Monitors that did not come
over are left out of the page and listed in the conversion report.

The converter validates pages before writing them, so a bad page does not
make the whole configuration file impossible to import. Invalid pages are
skipped with a note, including pages with invalid or reserved slugs (`api`,
`assets`, `fonts` and `logos`). Slugs allow 1–63 lowercase letters, digits or
dashes and must start with a letter or digit. Titles allow 1–120 characters
and descriptions at most 500. Public monitor names must be 1–80 characters,
without line breaks or tabs, and a page may contain at most 200 monitors.
Entries that cannot meet these limits are skipped and listed, rather than
silently renamed or truncated.

**Password-protected pages are explicitly disabled**, even when published in
Kuma. SubGlance's public pages have no password protection, so enabling one
would make its contents public. The password is never written to the file or
its report. Review the page before choosing whether to publish it without a
password. If that slug already names a public page on the destination, it is
switched off before its monitor list is replaced, even if the replacement
fails. Importing a converted file that contains `status_pages` requires an
**administrator**; an editor's import is refused without writing anything.

Custom CSS, footer text, analytics, logos, custom domains, tag and certificate
expiry display settings, incidents and maintenance-to-page links do not come
over. Each unsupported setting or relationship that was present is listed in
the conversion report. A maintenance window's monitor coverage is converted
separately; a status-page link does not become monitor coverage.

### What changes on the way

- An interval below 20 seconds becomes 20, and values outside SubGlance's other
  limits (retries up to 10, timeout up to 120 seconds) are moved inside them.
  Each one is listed.
- Kuma's *Retries* counts the failed checks that stay pending before the next
  one alerts, so 3 alerts on the fourth failure; SubGlance's `retries` counts
  the failures that confirm an incident, so 3 alerts on the third. A monitor
  with *n* retries in Kuma gets `retries: n + 1`, at most 10, and Kuma's
  default of 0 stays 0: both alert on the first failure. Kuma rechecks a
  pending monitor every *Heartbeat Retry Interval*; SubGlance checks again at
  the monitor's interval, so when the two differ the time from the first
  failure to the alert changes, and the monitor is listed with both. A Json
  Query monitor set to *Only retry if status code check fails* is listed too:
  SubGlance waits for its retries whichever part failed.
- A push monitor confirms the first missed report, whatever its `retries`.
  Kuma waited its retries before alerting, so that wait, *n* times the retry
  interval, becomes `push_grace_s` when it is longer than the default of 60
  seconds, and is listed.
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
- A few settings change how Kuma ran a check and have no SubGlance field, so
  the monitor comes over without them and is listed with what that means:
  a *Proxy* (SubGlance connects directly, so a site reachable only through
  the proxy fails), and from Kuma 2 an *IP Family* of IPv4 or IPv6 only
  (SubGlance uses whichever answers), the *cache buster* parameter (a cache
  in front of the site can keep answering while the server behind it is
  down), and a *TCP Port* monitor's *Expected TLS Alert* (a `tcp` monitor
  passes whether or not the server still refuses a client without a
  certificate). The proxy is named by protocol, host and port; its username
  and password are not written.
- Kuma shows a monitor inside a paused group as paused, but pausing a group
  does not stop the monitors in it: Kuma 1.23 and 2.x keep checking them and
  alerting. Such a monitor comes over enabled, as Kuma ran it, and is listed
  with the paused group, so pause it if it should not run.
- Kuma warns before a certificate expires when a monitor's *Certificate Expiry
  Notification* is on, once for each day listed under *Settings,
  Notifications, TLS Certificate Expiry* (7, 14 and 21 unless changed), when
  that many days or fewer are left. SubGlance warns when fewer than
  `ssl_warn_days` are left, so an HTTP monitor gets the largest listed day plus
  one, at most 365, and the first notice comes on the same day. From then on
  SubGlance keeps one notice open and reminds about it, instead of a new alert
  at each listed day, and it judges the server's own certificate, not each one
  in the chain. A monitor with the warning off, with *Ignore TLS/SSL errors* on,
  or with no days listed keeps SubGlance's default of 14: `ssl_warn_days` has
  no setting for never.
- Kuma 2 warns before a domain's registration expires, from every monitor
  whose *Domain Name Expiry Notification* is on, which it is by default. It
  looks up the registered domain of the monitor's address (`example.com` for
  `https://shop.example.com/`) once a day and notifies through that
  monitor's notifications, once for each day listed under *Settings,
  Notifications, Domain Name Expiry* (7, 14 and 21 unless changed). SubGlance
  has a check of its own for this, so each such domain becomes one `domain`
  monitor, named after it (`example.com registration`), checked once a day,
  with `domain_warn_days` the largest listed day plus one, at most 365, and
  the channels of every Kuma monitor on that domain. Each one is listed, with
  the Kuma monitors it stands for, and the summary counts them apart from the
  monitors that came over. A domain only paused monitors asked for comes over
  switched off. An IP address or a local name had no such warning. A name
  under a shared suffix such as `github.io`, for which Kuma looked up the
  suffix's own domain, and an internationalised name, which a `domain` monitor
  takes only in its `xn--` form, are listed instead; add those by hand. When
  no day is listed, Kuma sent no warning and no monitor is added.
- Kuma 2 also warns about the certificate of a *TCP Port* monitor whose *SMTP
  Security* is secure TLS or STARTTLS. A `tcp` monitor reads no certificate,
  so each one is listed: for secure TLS, an `ssl` monitor on the same host and
  port keeps the warning; SubGlance has no STARTTLS check.
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
- A notification of a type SubGlance has no channel for is not imported, so
  the monitors that alerted through it are listed too, each with the
  notifications it lost. A monitor that kept none of them comes over with
  `channels: []`, which in SubGlance means its alerts go to the channels of
  the tag routing rules it matches, or to the default channel when that comes
  to none ([which monitors use a channel](channels.md#which-monitors-use-a-channel)),
  and to nobody on an instance with neither, as a fresh one is. Give it a
  channel before switching Kuma off. A monitor that kept some keeps those
  channels, which alert while they are enabled.
- An email channel's Cc recipients become ordinary recipients. Bcc recipients
  are left out rather than shown to everyone, and listed.
- A Twilio channel signs in with the account SID and its auth token. When Kuma
  signed in with an API key, its auth token field held the key's secret, which
  does not work with the account SID, so fill in the account's own auth token;
  each such channel is listed. A messaging service is not used, and is
  listed. SubGlance sends at most `hourly_limit` alerts an hour (default 10) on
  an SMS channel, [one text each](channels.md#one-message-160-characters). A
  sender Twilio would refuse, such as a sender name of more than 11
  characters, keeps the channel out of the file, with the reason.
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
- A maintenance window covers one monitor or one tag pair, so a Kuma window
  on several monitors becomes one window per monitor, with the same name. A
  window on a group covers the tag `group` its monitors were given, for the
  group and every group inside it. A monitor it covered that did not come
  over is listed.
- A window set to Kuma's *Same as Server Timezone* gets the zone set under
  Kuma's *Settings*, *General*. When none was set there, Kuma ran in its
  host's zone, which its database does not record, so the window is placed
  in UTC and listed: check its times.
- Kuma runs a recurring window only inside its date range; a weekly window
  has none. A window whose range has not ended comes over without it and is
  listed. Its description is not carried over, and is listed too.
- Mattermost, Rocket.Chat and Google Chat become webhooks that post a text
  message, as [the channel documentation](channels.md#mattermost-rocketchat-and-google-chat)
  describes, each listed with its incoming-webhook URL to fill in. A
  Mattermost or Rocket.Chat channel Kuma posted to is written into the body,
  so the message still goes there. Kuma's sender name and icon and its
  Google Chat message template have no counterpart, and each one that was set
  is listed.
- Home Assistant becomes a webhook that calls the same notify action, as
  [the channel documentation](channels.md#home-assistant) describes. Its
  address comes over, and the long-lived access token goes in an
  `Authorization: Bearer` header to fill in. An action typed with `notify.`
  in front, which Kuma's request could not reach, is written without it, and
  listed. A Home Assistant on a private address or under a local name such as
  `homeassistant.local` is listed with
  [`--allow-private-targets`](operations.md#private-targets). Kuma's fixed
  title "Uptime Kuma" and its data fields (monitor name, and status 0 or 1)
  are not sent, so an automation that matched on them has to be changed.

### What does not come over

Maintenance windows switched on and off by hand, paused, or repeating on
days of the month, every few days, or on a cron expression with more than
one start a day; windows whose date range has ended; windows longer than
SubGlance keeps (366 days for one-off, a day for weekly); and windows that
cover none of the imported monitors. Monitor types SubGlance has no check
for (Docker, gRPC, MQTT, databases, game servers and the rest), DNS monitors
on CAA, NS, PTR, SOA or SRV records, DNS
monitors whose conditions are more than one *record equals* (a *contains* or
an *or* has no counterpart in a list of expected values), DNS monitors with
no resolver (Kuma never ran their check) or with a TXT value that starts or
ends with a space (Kuma compared it exactly), groups themselves,
monitors in *Upside Down Mode* (imported as they are, they would report the
opposite state), JSON queries that
use JSONata beyond a plain path, other authentication methods (NTLM, OAuth2,
mTLS), and notification types SubGlance has no channel for. History stays in
Kuma.
