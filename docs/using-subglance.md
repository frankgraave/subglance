# Using SubGlance

From the first administrator account to the API that does everything the
dashboard does.

- [First run](#first-run)
- [Your first monitor](#your-first-monitor)
- [Editing a monitor](#editing-a-monitor)
- [Authentication](#authentication)
- [Check types](#check-types)
- [Choosing a TLS floor](#choosing-a-tls-floor)
- [Push monitors](#push-monitors)
- [How a failure becomes an alert](#how-a-failure-becomes-an-alert)
- [Repeating an alert nobody answered](#repeating-an-alert-nobody-answered)
- [API](#api)

## First run

There is no default account and no seeded password. Open
<http://localhost:8080/> and the interface asks for an email address and a
password, creates the first administrator from them and signs you in. That
screen closes permanently once an account exists; from then on the same
address shows a sign-in form, and Sign out sits at the bottom of the sidebar.

Passwords must be at least 12 characters. There is no complexity rule: length
is what makes a password hard to crack, and demanding a digit and a symbol
mostly produces predictable substitutions, so a few ordinary words beat a
short cryptic one. Everything is stored on your own machine.

The same thing can be done from a terminal, which is what an unattended
install wants:

```sh
curl -X POST http://localhost:8080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"a-long-passphrase"}'
```

## Your first monitor

Press Add a monitor, paste an address and press Test it: the check runs
before anything is saved, so you find out immediately whether the target is
reachable rather than waiting for the first red row. The dashboard has
something to show within a minute of `docker compose up -d`, and neither
setup nor the first monitor needs a terminal.

Setup also returns a session cookie, so with a cookie jar the two calls chain:

```sh
curl -c jar -X POST http://localhost:8080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"a-long-passphrase"}'

curl -b jar -X POST http://localhost:8080/api/v1/monitors \
  -H 'Content-Type: application/json' \
  -d '{"name":"Public website","type":"http","target":"https://example.com/","interval_s":60}'
```

Either way the row is there, grey until the first check lands and then green
or red. Everything else has a default, and unknown fields are rejected rather
than ignored, so a typo tells you instead of silently configuring something
other than what you asked for.

## Editing a monitor

Open a monitor and choose **Edit monitor**, or use its edit action in the
Monitors list. Editors and administrators can rename it and change its
settings. The check type stays fixed; a push monitor keeps its identity and
secret reporting URL, and offers its report interval and grace instead of a
target to probe.

For an active check, changing its target or request settings requires **Test
it** before saving. The preview uses the current draft without creating a
monitor, recording check history or sending alerts. A failed probe is still a
valid preview: you can intentionally save a service that is currently down.
Changing the draft invalidates an earlier preview. The TLS floor remains in
Advanced options; changing only that floor can be saved directly.

The drawer reads settings and their version together. It sends only changed
fields with that version. If another edit wins first, the stale save is
refused: your draft stays visible, nothing is overwritten, and **Reload latest
settings** explicitly replaces the draft with the newer values. The drawer
also asks before discarding unsaved changes when you close it or navigate
away. Request headers and bodies remain in the form's memory, not browser
storage.

## Authentication

Two credential types reach the same endpoints with the same rights:

- **Session cookie** — for the browser. HttpOnly, SameSite=Lax, and
  `__Host-`-prefixed when served over HTTPS.
- **API token** — for scripts and CI, sent as an `Authorization: Bearer` header.

```sh
# create a token (requires an existing session — `-b jar` from the setup call above)
curl -b jar -X POST http://localhost:8080/api/v1/tokens \
  -H 'Content-Type: application/json' -d '{"name":"ci"}'

# use it
curl -H "Authorization: Bearer $SUBGLANCE_TOKEN" http://localhost:8080/api/v1/monitors
```

A token's plaintext is shown once and stored only as a hash; it cannot be
recovered, only replaced.

Three roles decide what a credential may do to monitors and incidents:
**viewer** (read-only), **editor** (manage monitors, acknowledge incidents) and
**admin** (also manages users). Tokens are scoped a little differently, because
listing and revoking are limited to the caller's own: any role may list its
tokens and revoke one, while minting a new token needs editor or admin.

## Check types

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

## Choosing a TLS floor

`min_tls_version` is the lowest TLS version a monitor will negotiate, written
`1.0`, `1.1`, `1.2` or `1.3`. Leave it out and SubGlance uses TLS 1.2. It
applies to `http` and `ssl` checks.

```sh
curl -X PATCH http://localhost:8080/api/v1/monitors/3 \
  -H 'Content-Type: application/json' \
  -d '{"min_tls_version":"1.0"}'
```

It reads like a client setting, and that is the thing worth unlearning, because
it is used in two opposite ways and only one of them is about being permissive.

**Lowering it makes an old endpoint watchable at all.** The appliance in the
cupboard that only speaks TLS 1.0 is exactly the sort of thing a self-hoster
needs to know about, and with the default floor SubGlance cannot complete a
handshake with it — so it reports a permanent outage that is really a refusal
to connect. `1.0` there is the difference between monitoring it and not.

**Raising it is an assertion about the server, and making the check fail is the
entire point.** Set `1.3` on an endpoint that is supposed to have retired
everything older, and the check goes red the day it starts offering TLS 1.2
again. That is not the monitor breaking; it is the monitor reporting what you
asked it to watch for.

Because the floor is ours rather than the server's, a failure names ours: the
peer refuses a too-low `ClientHello` with a handshake alert rather than telling
us which versions it would have accepted, so the message says what SubGlance
insisted on, not what the server offers. Send `""` in a PATCH to take the floor
back off and return to the default.

## Push monitors

The four types above all look inward from outside. Anything without a reachable
port is invisible to them: a nightly backup, an import script, a queue worker on
a laptop behind NAT. Those are exactly the things that fail quietly and are only
discovered when you need them.

A push monitor turns the direction around. It gets a secret URL, the job calls it
when it finishes, and if nothing arrives inside the window the monitor goes down.

```bash
curl -b jar -X POST http://localhost:8080/api/v1/monitors \
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

> [!WARNING]
> **What a push monitor does and does not tell you.** It proves your script
> reached the line with the `curl` on it. It does not prove the work succeeded.
> A backup that writes an empty archive and then pings stays green, and that is
> a worse outcome than no monitoring at all, because it is monitoring that
> reassures you.

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

## How a failure becomes an alert

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

## Repeating an alert nobody answered

One alert at 02:40 that you sleep through leaves the outage unattended until
morning, which is the scenario a monitor is run for. A confirmed incident that
nobody has acknowledged is therefore alerted about again, on a schedule that
grows: after 15 minutes, then an hour, then four hours, then sixteen, then once
a day. Each reminder says how long the monitor has been down, not only that it
still is.

The interval is `repeat_after_s` per monitor. It is the delay before the *first*
reminder; the gaps after it grow from there. Set it to `0` to switch reminders
off for a monitor.

Use **Repeat alerts** in the create form or the edit drawer.
Choose **Do not repeat**, or enter any whole-number base from 60 to 86400
seconds. The form rejects smaller nonzero values before sending a request:
under one minute, reminders would become too frequent. The default is 900
seconds. This is an escalating base, not a fixed repeat interval.

The detail view shows the next reminder's due time and the number already
issued for each open incident. Those values come from the server's persisted
clock, not an estimate based on how long the page has been open. “Issued” does
not promise successful delivery to every notification channel. Paused,
disabled, unconfirmed, acknowledged and flapping incidents explain why no
reminder is due; their existing reminder count is retained.

The gaps grow rather than staying flat on purpose. A monitor that repeats at a
fixed short interval trains its owner to mute it, and a muted monitor misses the
next outage too.

Acknowledging an incident (`POST /api/v1/incidents/{id}/ack`) stops the
reminders without claiming the problem is solved. Resolving, pausing the
monitor, or the monitor recovering all stop them too, and a flapping monitor
never gets them — suppression for oscillation is the stronger rule. The schedule
is stored with the incident, so restarting SubGlance during a long outage
continues it instead of alerting again from the beginning.

Checks cut short by a shutdown are discarded rather than recorded as failures.
Otherwise every restart would manufacture an outage on healthy monitors.

Monitors that are deleted or paused have their in-memory state dropped. A
monitor resumed an hour later starts clean rather than resuming a failure
streak from before the pause.

## API

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
Manual checks are limited to one per monitor per five seconds. The detail
view offers **Check now** in the Recent checks card and reports both probe
results and request failures there. Push monitors have no check control:
SubGlance receives their reports rather than probing them.

The list includes attached channel IDs and names in `channels`, without
channel credentials. An empty array means no attachments; an omitted or
unreadable value means **not loaded**, not **none**. There is no per-monitor
channel fetch or forty-monitor limit in the inventory.

`GET /api/v1/monitors/{id}` also returns stored check settings for editing,
paired with the response's `ETag`. Send that validator as `If-Match` with only
the fields being changed. Request `headers` and `body` are returned only to
editors and administrators, and are omitted from the list and live stream.
The detail and inventory edit drawers use this paired read for target, timing,
request settings, tags, TLS floor and repeat-alert settings. A 412 response
requires an explicit reload rather than retrying the same draft with a new
validator.

Acknowledging is not resolving: it stops repeat notifications without claiming
the problem is fixed.

That listing is the monitor and incident surface only. The complete API —
authentication, users, API tokens, notification channels, uptime windows and
the live event stream — is specified in [`openapi.yaml`](openapi.yaml),
an OpenAPI 3.1 document you can feed to a client generator or an editor such as
Swagger UI. It is checked against the server's own route table on every test
run, so a route cannot be added, removed or change privilege level without the
specification following it.
