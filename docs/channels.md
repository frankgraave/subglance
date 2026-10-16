# Notification channels

A channel is where an alert goes. Each one has a type and a small set of
settings; the Notifications screen shows the fields each type needs, and the
same settings can be written through `POST /api/v1/channels` (see
[`openapi.yaml`](openapi.yaml)).

| Type | Required | Optional |
|---|---|---|
| `webhook` | `url` | `headers` (one `Name: value` per line), `method` (`POST` or `PUT`), `body` (see [a body of your own](#a-body-of-your-own)) |
| `discord` | `url` (the channel webhook) | — |
| `slack` | `url` (the incoming webhook) | — |
| `telegram` | `bot_token`, `chat_id` | — |
| `email` | `to`, `from`, `host` | `port` (default 587), `username`, `password` |
| `ntfy` | `topic` | `url` (default `https://ntfy.sh`), `token`, or `username` + `password` |
| `gotify` | `url`, `token` (an application token) | `priority_down` (default 8), `priority_up` (default 4) |
| `sms` | `numbers`, `provider`, and per provider: `url`, `username`, `password` (`android-gateway`) or `account_sid`, `auth_token`, `from` (`twilio`) | `country_code`, `hourly_limit` (default 10), `recoveries` (default `true`), `timezone` |

Every value that is not a plain destination is masked when it is read back:
`****` plus the last four characters. A webhook's `method` and `body` are the
exception: they are written and corrected by hand, so they are read back in
full, and a credential belongs in the URL or a header instead. That includes the ntfy `topic`: on a
server without access control the topic name is the credential, since anyone
who knows it can read the alerts and post fake ones. Use a topic nobody would
guess, or an access token.

A masked value that is saved back unchanged keeps what is stored, so editing a
channel's name or recipients does not ask for its credentials again. Changing
where the channel sends does: a new type, URL, SMTP host or port, or SMS
provider. A stored credential is only ever sent to the destination it was
saved for, so the edit is refused until the password, token or headers are
typed again, and the form opens that field for it.

A channel URL is held to the rule an `http` monitor's target is (see
[Using SubGlance](using-subglance.md)): its hostname has to be one the resolver
will look up, and its port, when written, has to be between 1 and 65535.
Either mistake parses as a URL and then fails every delivery before it leaves
the process, so it is refused on `config.url` when the channel is saved,
edited or imported rather than discovered when an alert is lost.

A webhook's `headers` are held to the rules an `http` monitor's request
headers are: each line is `Name: value`, the name is an HTTP token (letters,
digits and ``!#$%&'*+-.^_`|~``, no spaces), the value holds no control
character other than a tab, and no header is set on two lines, since names
ignore case and only one of them would be sent. A line that breaks a rule is
refused on `config.headers` by its line number; the value is never quoted,
because a header is where a credential goes. A channel saved before these
rules keeps its header lines through an edit that leaves them as they are,
such as a rename or switching it off, and through an import of its own
export.

An email channel's `host` is the relay's name or address alone, held to the
rule a `tcp` monitor's host is: a name the resolver will look up, written in
its `xn--` form when it is internationalised, or an IP address (IPv6 without
brackets). A port goes in `port`, so `smtp.example.com:2525` is refused with
a pointer to that field, and so is a URL such as `smtp://smtp.example.com`.
Each of these used to be saved and then fail every delivery as "no such
host"; now it is refused on `config.host`. Spaces around the host are
ignored.

An email channel's `from` is one address, and `to` is one or more separated
by commas. Either may carry a name, as in `SubGlance <sg@example.com>` or
`"Doe, Jane" <jane@example.com>`: the relay is handed the bare addresses and
the message headers keep the names. Without a name in `from`, the message
comes from `SubGlance <address>`. A value that is not an address, such as
`ops@` or `ops@@example.com`, is refused on `config.from` or `config.to`, and
so is a list separated by semicolons or line breaks. Importing from Uptime
Kuma turns its semicolons into commas.

The subject of an alert email is always ASCII. A subject with a character
outside it, such as a monitor named `Bücherei Zürich`, is sent as RFC 2047
encoded words, which every mail client shows as the original text; written
raw, it could be returned as undeliverable by a relay whose next hop does not
accept UTF-8 headers. Display names in `from` and `to` are encoded the same
way. The addresses themselves cannot be: an address with a character outside
ASCII, such as `jörg@example.com`, is written as typed and still needs a relay
that supports SMTPUTF8. A body with such a character says
`Content-Transfer-Encoding: 8bit`. An ASCII subject and body go out as before.

Every channel has a **Send test** button, which delivers a real message and
shows the exact error the far end returned.

Each channel row also has a **Delivery** column, read from the outbox: what
happened to the real alerts sent through it in the last 30 days, the window
the delivery log is kept for.

- **Failed**: the newest alert gave up after its retries, and nothing has
  arrived since. The count and the last error are in the column's tooltip.
- **Retrying**: an alert is queued after a failed attempt.
- **Delivered**: the newest alert arrived.
- **None in 30 days**: no alert went through the channel. That proves nothing
  either way; Send test is the way to find out before an outage does.

The same record is in `GET /api/v1/channels` as each channel's `delivery`.
Its last error has the channel's masked settings replaced by their masks,
and any other URL cut to its host, so it never shows a viewer a credential
the settings hide.

The Delivery column says how a channel is doing; an incident says how its own
alerts went. Open an incident, on the Incidents screen or a monitor's page,
and its **Notifications** list has one line per alert per channel: the alert,
its reminders and the recovery, each marked Delivered, Failed (with the
error), Retrying, Held for quiet hours, Not sent (with the reason, such as
maintenance or quiet hours set to drop), or Merged, when the morning's
quiet-hours digest or the recovery message carried it instead. Send test
messages are not alerts and are not listed. Delivered and skipped alerts are
kept for the same 30 days, so the list on an older incident may be short and
says so. The same list is `GET /api/v1/incidents/{id}/deliveries`.

## A channel that stops delivering

A webhook that is revoked, a bot removed from its group or a mail password
that expired all fail without anyone changing SubGlance, and the next outage
would then be confirmed and told to nobody. So when a real alert gives up on a
channel, refused outright or out of retries, SubGlance says so in three
places:

- **Another channel** gets one message, with `event` `channel_failing`: the
  default channel, or when that is the one failing or is disabled, the oldest
  other channel that is enabled and not failing itself. It names the channel,
  when its alert gave up, and the error with the credentials taken out. It is
  sent within a minute, and once: further failures on the same channel send
  nothing more until an alert or a **Send test** arrives there, after which a
  new failure is news again. Like the backup notice, it waits for the
  channel's quiet hours to end rather than going out inside them.
- **The dashboard** shows a line above the monitors naming the channel and
  whether the message went out, with a link to the Notifications screen.
- **`/metrics`** counts it in `subglance_notification_deliveries_total`; see
  [Metrics](operations.md#metrics).

With only one channel, or with every other one disabled or failing, there is
nowhere to send the message; the dashboard line and the channel's `delivery`
record (`notice` is `no_other_channel`) are then where it is said. A failed
**Send test** never starts any of this: it shows its error to the person who
pressed it, at the moment they did. Nor does a disabled channel.

In `GET /api/v1/channels`, `delivery.failing_since` is when the failure began
and stays set until the channel delivers again, however long ago that was;
`notice` says whether the message went out (`sent`, `waiting`,
`no_other_channel`), and `notice_sent_at` and `notice_channel_id` when and
through which channel.

## Which monitors use a channel

A monitor's own channels are chosen under **Channels** in its add and edit
forms. Its alerts go to those channels plus the channels of every tag routing
rule whose tag it carries; when that comes to none, they go to the default
channel. The form says which applies as the boxes stand, and warns before a
monitor that would alert nobody is saved. Routing rules have no editor yet:
they are set through the API (`/api/v1/routing-rules`) or a configuration
file.

Through the API, the same set is `channel_ids` on `POST /api/v1/monitors`
and `PATCH /api/v1/monitors/{id}`, or the whole of
`PUT /api/v1/monitors/{id}/channels`. On the PATCH it is part of the same
conditional write as the other fields, and changing a monitor's channels by
any route advances its `ETag`.

### A channel for many monitors at once

In **Monitors**, tick the monitors and choose **Channels** in the bar above
the list. Pick **Add** or **Remove** and one channel, preview, then confirm.
The change is one write: every selected monitor gets it or none does. It adds
or removes that one channel and leaves each monitor's other channels and the
tag routing rules alone, so a monitor whose channels differ from the rest
keeps what it had.

The preview counts the monitors that change and says when the change moves
someone's alerts somewhere else: a remove that leaves monitors with no channel
of their own (the matching rules, or else the default, take over), an add for
monitors that alerted through the default (it no longer applies to them), or
a disabled channel (it sends nothing, but still keeps the default out).

Through the API, send `{"action": "add", "monitor_ids": [...], "channel_id": n}`
(or `"remove"`) to `POST /api/v1/monitors/channels/preview`, then the same
body to `POST /api/v1/monitors/channels` with the preview's `ETag` in
`If-Match`. A 412 means a selected monitor's channels, or the channel itself,
changed after the preview: preview again rather than retrying.

## ntfy

Alerts are published with ntfy's JSON API: a `POST` to the server root with the
topic, a title naming the monitor, the cause and duration as the message, and a
tag that puts an emoji in front of the title. An outage is sent at priority 4
(high), a recovery at priority 3 (default).

- **ntfy.sh**: set only `topic`. Subscribe to the same topic in the ntfy app.
- **Self-hosted**: set `url` to the server's base URL, and `token` (an access
  token, `tk_...`) or `username` + `password` if the server requires login.
  The URL is the server root only: a topic path such as
  `https://ntfy.example/alerts` is refused, because the topic goes in `topic`.
  Setting both a token and a password is refused, since only one of them
  would be used.

## Gotify

Alerts are posted to `<url>/message`, so a Gotify served under a sub-path
behind a reverse proxy works as long as `url` includes that path. The
application token is sent in the `X-Gotify-Key` header, never in the URL,
so it does not end up in logs. Priorities run from 0 to 10; the Android app
plays a sound from 4 and shows a pop-up from 8.

## SMS

A text message arrives when the phone has no data, when the chat app is
muted, and when the internet connection of the person on call is the thing
that is down. That is what this channel is for. One channel sends to up to
ten numbers, through one of two providers.

### Numbers

`numbers` takes one or more numbers separated by commas or new lines. They
are sent in international (E.164) form, and can be typed as `+31 6 1234
5678`, `0031 6 1234 5678` or `+31 (0)6 1234 5678`. With `country_code` set
(`+31`), a national number such as `06-12345678` works too: its leading zero
is dropped and the country code put in front. A number that cannot be read is
refused when the channel is saved, with the number in the message.

Phone numbers are personal data. An administrator reads them back as saved;
an editor or viewer reads each one as `+31 6 •••• 5678`. Logs and the
Delivery column only ever show that masked form.

On the Notifications screen the numbers are a list, one per line. An editor
sees the masked list there; saving other changes keeps the numbers as they
are, and changing them means replacing the whole list. The channel list
itself only says how many numbers a channel sends to, never which.

### One message, 160 characters

Every alert is one SMS part: at most 160 characters in the GSM 7-bit
alphabet. One character outside it (an emoji, a curly quote, a "ê") would
switch the whole message to a 70-character encoding and split it into two or
three billed parts. So the text is rewritten into that alphabet first
(letters with accents it lacks lose them, emoji are dropped) and shortened at
a word. The status comes first and is never shortened; a long monitor name
is:

```
DOWN Production API: timeout after 10s (since 14:03)
UP Production API after 12 min
```

Times are in the channel's `timezone` when it has one (`Europe/Amsterdam`),
and otherwise in the server's zone with its name (`14:03 UTC`).

### The hourly limit

A channel sends at most `hourly_limit` alerts (default 10, from 1 to 100) in
any hour. An alert past the limit is not sent and not retried; it is recorded
as withheld, with the reason, and does not count as a failure. The first message after the hour
ends says how many were held back: `(+3 alerts not sent by SMS, see
SubGlance)`. The reason is money and carriers: on Twilio every message is
billed, and a flapping monitor must not produce a bill or get a SIM card
blocked. [Alert grouping](operations.md#alert-grouping) already folds twenty monitors that
fail together into one alert before the limit counts it.

Two channels that send to the same numbers through the same account share
one limit. The count lives in memory, so a restart starts a fresh hour.

`recoveries: false` sends outages only (a recovery that replaces an alert
which never got through is still sent: see
[the webhook payload](#the-webhook-payload)). Quiet hours work as on every other
channel, but think twice: holding an SMS until morning usually defeats the
reason for choosing SMS.

With several numbers, a message that reaches some of them and not the others
is retried only to the ones that failed. Nobody gets the same alert twice.

### SMS Gateway for Android

[SMS Gateway for Android](https://sms-gate.app) (open source, Apache-2.0)
turns an Android phone with a SIM card into an SMS gateway. In *local
server* mode the phone accepts messages over HTTP on your own network, with
no account and no cloud; you pay what your phone plan costs. It keeps working
when the internet connection of the house or office is down, as long as the
phone has signal.

1. Install the app on the phone and allow it to send SMS.
2. Turn on **Local Server** and tap the status button so it reads
   **Online**. The app shows the phone's local address and a username and
   password.
3. Give the phone a fixed address: a DHCP reservation on the router, so the
   address does not change after a reboot.
4. Create the channel with `provider: android-gateway`, `url` set to the
   address the app shows (`http://192.168.1.50:8080`), and the username and
   password.
5. The phone is on a private address, so start SubGlance with
   `--allow-private-targets` (see below).

SubGlance posts to `<url>/messages`, which the app serves from version 1.28.
The app's cloud relay speaks the same API: set `url` to
`https://api.sms-gate.app/3rdparty/v1` and use the cloud credentials instead.

A delivery counts as done when the phone accepts the message (HTTP 202).
Whether the phone then manages to send it is reported by the app later, and
SubGlance does not ask in this version: check the app's log if a message did
not arrive.

### Twilio

Set `provider: twilio`, the `account_sid` (`AC...`) and `auth_token` from the
Twilio console, and `from`: a Twilio number (`+14155550100`), or a sender
name of up to 11 letters and digits such as `SubGlance`. Not every country
accepts a sender name; when Twilio refuses one, its error code and message
are shown in the test button and the Delivery column. A delivery counts as done
when Twilio accepts the message.

The account SID is shown in full when read back, like a username; the auth
token is masked.

Other SMS providers are not built in. Most accept a JSON request, which the
`webhook` channel can send.

## Servers on a private address

A self-hosted ntfy or Gotify, or the phone running SMS Gateway for Android,
usually lives on the local network, at an address like `192.168.1.20`. SubGlance refuses to connect to private, loopback and
link-local addresses by default, for channels as well as for checks: a channel
URL is text someone typed, and without that guard SubGlance would connect
wherever it pointed, including the cloud metadata address.

Channels do not have a switch of their own. The setting that permits checks on
internal addresses permits deliveries too:

```
subglance --allow-private-targets
# or
SUBGLANCE_ALLOW_PRIVATE_TARGETS=true
```

Without it, saving such a channel is refused with a message naming the field,
the address and this setting. A channel saved earlier and delivered after the
setting was turned off fails the same way, in the test button and in the
Delivery column, and is not retried: a refused address stays refused.

## Links back to SubGlance

Started with `--base-url` (or `SUBGLANCE_BASE_URL`) set to the address people
open SubGlance at, every alert about a monitor links to its incident and to
the monitor's page:

```
subglance --base-url https://status.example.com
# behind a proxy that serves it under a path:
SUBGLANCE_BASE_URL=https://example.com/subglance
```

The incident link is `<base>/incidents#incident-<id>`: the incidents screen,
scrolled to that incident with its row open. The monitor link is
`<base>/monitors/<id>`. An alert about several monitors at once (a grouped
alert, or the summary at the end of quiet hours) links to `<base>/incidents`,
where all of them are. A message about SubGlance itself (a failed backup, a
channel that stopped delivering, the **Send test** button) has no monitor and
no links.

The address has to be an absolute `http://` or `https://` URL. A path prefix
is kept and a trailing slash does not matter; a user name, a password, a query
string or a `#fragment` is refused at start-up, because it would be copied
into every link in every message. Without the setting, every message is
exactly what it was before links existed. The settings page shows the address
in use under **Instance**, or that none is set; it cannot be changed there,
because it belongs to the proxy in front of SubGlance rather than to anything
edited in it.

How each channel carries the links:

| Type | Links |
|---|---|
| `webhook` | `incident_url` and `monitor_url` in the payload, and `{{incident_url}}` and `{{monitor_url}}` for a body of your own |
| `slack` | A last line, "Open the incident · Open the monitor" |
| `discord` | The title opens the incident; the description ends with both links |
| `telegram`, `email` | Two lines after the message, `Incident:` and `Monitor:` |
| `ntfy` | Tapping the notification opens the incident; a button for each link |
| `gotify` | Two lines after the message; tapping it in the app opens the incident |
| `sms` | The incident link only, after the text, which is shortened to make room. A link that would leave the alert less than half of the 160 characters, or that has a character outside the SMS alphabet, is left out |

The address is added when the message is sent, not when the alert is queued,
so an alert being retried after the setting changed links to the new address.

## Muting repeat alerts from the alert

With `--base-url` set, an alert about an open incident (the first alert and
every reminder) carries a third link, **Mute repeat alerts**. It does what the
**Mute repeat alerts** button on the incidents screen does, without signing in:
SubGlance sends no more reminders about that incident, and still sends the
alert when the monitor is back up.

Following the link changes nothing by itself. It opens a page that names the
monitor and when it went down, with one button; only pressing the button mutes.
Slack, Teams, mail scanners and chat apps open the links in a message to draw
a preview or check it, and a link that acted when it was opened would mute
the incident before anyone had read the alert.

| Type | Mute link |
|---|---|
| `webhook` | `mute_url` in the payload, and `{{mute_url}}` for a body of your own |
| `slack`, `discord` | After the other links, "Mute repeat alerts" |
| `telegram`, `email`, `gotify` | A third line after the message, `Mute repeat alerts:` |
| `ntfy` | A third button, **Mute**, which opens the page |
| `sms` | None: a second link does not fit in one message of 160 characters |

A recovery, a message about SubGlance itself, and the top of a grouped alert or
a quiet-hours summary carry no mute link; each member of a grouped alert has
its own in the webhook payload, and the incidents screen it links to mutes any
of them.

A link works for one incident, from the alert it came in, until the incident
is over or for 24 hours after it was sent, whichever comes first. Following it
again after muting says the alert is already muted and changes nothing. A link
that was cut short or changed, or one sent to a channel that has since been
removed, opens a page saying it does not work; removing a channel is how to
take back a link that reached the wrong chat.

The link is signed, not stored. With `--secret-key` set, the signing key is
derived from it; without one, SubGlance creates `mute-link.key` in the data
directory, readable by its owner only. Changing `--secret-key`, or deleting
that file, stops every mute link already sent from working; nothing else
depends on it. A backup does not include the file, so after a restore onto a
new data directory the links sent before it no longer work either.

## Text that is not markup

Slack and Discord read a message as markup, and much of an alert's text was
written by someone other than the operator: a push monitor's `msg`, the value
a JSON assertion quotes from a response, a keyword, a DNS record. That text is
sent so it reads as typed. In Slack, `&`, `<` and `>` go as `&amp;`, `&lt;`
and `&gt;`, so a message saying `<!channel>` pings nobody and
`<https://…|label>` is no link with a label; the title and body are sent as
`verbatim`, so a plain `@here` or `@channel` is no mention either.
`verbatim` also stops Slack from turning a bare address into a link, so an
`http` or `https` address in the alert's text, the monitor's target among
them, is sent as `<https://…>` and arrives clickable, showing the address
itself. It ends at whitespace, `<`, `>` or `|`, so the text after it cannot
become its label; a host name without a scheme stays plain text. In a
Discord description, markdown characters get a backslash, so
`[label](https://…)` is not a masked link and `*`, `` ` ``, `~` and `|` show
as typed; an address in the text is left whole and stays clickable, and an
underscore inside a word, as in `cert_expiry`, is left alone because Discord
does not act on it. Slack's own emphasis (`*`, `_`,
`~`) has no escape, so a pair of asterisks can still make a word bold there.
The links SubGlance adds itself are the only links with a label in either
message; an address in the alert's own text shows as the address it is.

Telegram, ntfy, Gotify, email and SMS send plain text and need none of this.

## The webhook payload

A `webhook` channel sends one request per alert: a `POST`, or a `PUT` when
its `method` says so. Every request carries a `User-Agent` starting with
`SubGlance/` and any headers set in `headers`. Without a `body` of its own
(see below) it is sent with `Content-Type: application/json` and this body:

```json
{
  "monitor_id": 7,
  "monitor_name": "Checkout API",
  "monitor_type": "http",
  "target": "https://shop.example.com/health",
  "event": "incident_confirmed",
  "incident_id": 312,
  "started_at": "2026-10-02T03:12:40Z",
  "at": "2026-10-02T03:14:10Z",
  "cause": "timeout",
  "last_error": "no response within 10s"
}
```

| Field | Meaning |
|---|---|
| `monitor_id`, `monitor_name`, `monitor_type`, `target` | The monitor, as it was when the alert fired. A retry an hour later still says what was true then |
| `event` | What happened: see below |
| `incident_id` | The incident the alert belongs to; left out when there is none |
| `started_at` | When the outage began: the first failed check, not the confirmation |
| `at` | When this alert fired |
| `cause` | The failure kind: `dns`, `connection`, `tls`, `timeout`, `status`, `keyword`, `assertion`, `cert_expiry`, `push_overdue`, `push_reported`, `local_network`, `local_dns` or `internal` |
| `last_error` | The checker's message for the latest failure |
| `reminder_count` | Which repeat of an unanswered alert this is; left out on the first |
| `grouped_names`, `grouped_cause`, `members` | Set when several alerts were sent as one: see below |
| `digest`, `digest_timezone` | Set on the summary a channel receives when its quiet hours end |
| `replaces_alert` | Set on a recovery whose alert never reached this channel: see below |
| `notice` | Set on an alert about a certificate that expires soon, not an outage: see below |
| `incident_url`, `monitor_url` | Links to the incident and the monitor in SubGlance; left out unless `--base-url` is set (see [links back to SubGlance](#links-back-to-subglance)). A grouped alert or a digest has an `incident_url` to the incidents screen at the top and each member's own links in `members` |
| `mute_url` | A page that mutes this incident's repeat alerts without signing in; on a first alert or a reminder only, and left out unless `--base-url` is set (see [muting repeat alerts from the alert](#muting-repeat-alerts-from-the-alert)). In a grouped alert or a digest, only in `members` |

A monitor's alerts carry `incident_confirmed` (it is down), `incident_reminder`
(it is still down and nobody has acknowledged it) or `incident_resolved` (it is
back up).

A certificate that is still valid but expires inside the monitor's
`ssl_warn_days` is not an outage: the service answers, its checks are stored as
up and its uptime is untouched. It is still worth a message, so it carries the
same three events with `notice` set: `incident_confirmed` when the notice opens
("api: certificate expires soon"), `incident_reminder` while nobody has
acknowledged it, and `incident_resolved` once a check sees a certificate that no
longer expires soon. `cause` is `cert_expiry`, and `last_error` says how many
days are left. A notice is never grouped with outages. An expired certificate is
an outage like any other: `incident_confirmed` without `notice`. Messages about SubGlance itself carry `backup_failed`,
`local_network_restored`, `local_dns_restored` or `channel_failing`, with `monitor_id` 0 and
`monitor_name` `SubGlance`; a quiet-hours summary carries `quiet_hours_digest`. Ignore an
`event` you do not recognise rather than treating it as an outage: a newer
version may add one.

When monitors fail inside one grouping window, the channel receives a single
alert. `grouped_names` lists every monitor, oldest failure first, and
`grouped_cause` is the cause when they all agree. `members` holds each
monitor's own alert in the shape above. Reminders are grouped the same way but
never with a first alert: a new outage and a repeat that fall in one window
arrive as two messages, one titled "web is down" and one "2 monitors are still
down" with `event` `incident_reminder`, so the event at the top always says what
every member is. At the top level, `event`, `at`,
`started_at`, `incident_id`, `monitor_name`, `monitor_type` and `target` repeat
the first member's, and `monitor_id` is 0 since the alert is about more than one
monitor. `cause`, `last_error` and `reminder_count` are not set at the top
level: read each monitor's failure from its entry in `members`. A quiet-hours
digest also lists its alerts in `members`.

Deleting a monitor deletes the messages about it alone, queued or sent. A
grouped message or a digest that also names a monitor that remains is kept:
it is still sent if it was waiting, and it still shows on that monitor's
incident as a channel it reached.

`started_at` is `0001-01-01T00:00:00Z` when there is no outage behind the
message: the **Send test** button, a backup notice and a digest. In a
`channel_failing` message, `target` is the failing channel's name and type,
`started_at` is when its alert gave up and `last_error` is why. The test
message is shaped like a recovery (`event` is `incident_resolved`) with
`monitor_id` 0 and `monitor_name` `SubGlance test`, so a receiver that acts on
recoveries should check the `monitor_id` first.

A `2xx` answer counts as delivered. A `429`, a `5xx` or a network error is
retried, six attempts in all spread over about twenty minutes, before the
alert is marked failed; any other `4xx` fails it at once, because retrying
cannot fix a request the receiver refuses.

A channel hears about an outage in the order it happened, and never hears
"down" for an outage that is already over. When a monitor recovers while its
alert is still queued for a channel (the receiver was away and the alert is
being retried), the recovery replaces the alert: the channel gets one message,
"api was down for 3 minutes, now back up", with `event` `incident_resolved`
and `replaces_alert` set, and the alert is not sent. The merged message takes
over the alert's place in the retry schedule, so it arrives when the alert
would have and gives up when the alert would have. A reminder still queued
for the outage goes the same way. When several monitors were alerted in one
grouped message, only the ones that recovered leave it; the rest are still
reported down. A grouped recovery sets `replaces_alert` at the top only when
every member does; each entry in `members` carries its own.

Each channel keeps its own order: a channel that took the alert gets an
ordinary recovery at once, whatever another channel is still retrying. If the
alert gives up for good, the recovery is sent anyway, without it. An SMS
channel set to `recoveries: false` still sends a recovery with
`replaces_alert`, since it is the only message about that outage.

## A body of your own

Teams, Matrix, Pushover and most chat services do not read the payload above;
each wants its own JSON. Give the webhook a `body` and it sends that instead,
with placeholders filled in from the alert. An empty `body` sends the payload
above, so a webhook without one is unchanged.

| Placeholder | What it holds |
|---|---|
| `{{summary}}` | The one-line headline a phone shows: "api is down", "3 monitors are down", "api was down for 3 minutes, now back up" |
| `{{details}}` | The lines under it: target, cause, error and times; one line per monitor for a grouped alert or a quiet-hours summary |
| `{{status}}` | `down`, `up`, or `expiring` for a certificate that expires soon |
| `{{event}}` | The `event` of the payload above |
| `{{monitor_name}}`, `{{monitor_type}}`, `{{target}}` | The monitor; the first one's in a grouped alert |
| `{{cause}}`, `{{last_error}}` | Why the check failed; empty in a grouped alert |
| `{{started_at}}`, `{{at}}` | When the outage began and when this alert fired, RFC 3339 in UTC; `{{started_at}}` is empty when there is no outage behind the message |
| `{{incident_url}}`, `{{monitor_url}}` | Links to the incident and the monitor in SubGlance; empty unless `--base-url` is set. For a grouped alert or a digest, `{{incident_url}}` is the incidents screen and `{{monitor_url}}` is empty |
| `{{mute_url}}` | The page that mutes this incident's repeat alerts; empty unless `--base-url` is set, on a recovery, and for a grouped alert or a digest |
| `{{txn_id}}` | An id that is the same on every retry of one alert and differs between alerts. The only placeholder the `url` may use |

A placeholder is replaced by its value and nothing else: there are no
conditions, loops or includes, and none will be added, since a template
language that can read files is a way to read them off the server. Spaces
inside the braces are allowed: `{{ summary }}`.

Each value is escaped for the body's `Content-Type`, which is
`application/json` unless `headers` sets another:

- **JSON** (`application/json`, or any `+json` type): a value is inserted as
  the text of a JSON string, quotes and line breaks escaped, so put every
  placeholder between quotes. A name with a `"` in it cannot break the
  document.
- **A form** (`application/x-www-form-urlencoded`): a value is form-encoded,
  so an `&` in a monitor name does not start a new field.
- **Anything else**: as it is.

Saving refuses a body with a placeholder not in the table, a `{{` that is not
closed, or, when the type is JSON, a document that does not parse; the
message names the line and column. **Send test** fills the body in, so it
shows what an alert will look like.

The body is read back in full to everyone who can read channels, and is
written as it is into an [exported configuration
file](configuration-files.md). Keep tokens out of it: in each example below
the credential is in the URL or a header, which are masked.

### Microsoft Teams

In the Teams channel, open **Workflows** and choose the template **Send
webhook alerts to a channel**. Use the URL it gives you as `url`, and this
`body`:

```json
{
  "type": "message",
  "attachments": [
    {
      "contentType": "application/vnd.microsoft.card.adaptive",
      "content": {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "body": [
          { "type": "TextBlock", "text": "{{summary}}", "weight": "Bolder", "size": "Medium", "wrap": true },
          { "type": "FactSet", "facts": [
            { "title": "Target", "value": "{{target}}" },
            { "title": "Error", "value": "{{last_error}}" },
            { "title": "At", "value": "{{at}}" }
          ] }
        ]
      }
    }
  ]
}
```

The workflow answers `202` to anything, and a body without `attachments`
fails inside the flow afterwards, where SubGlance cannot see it. Press **Send
test** and check that the card arrives in Teams, not only that the test
passed.

### Matrix

Create an account for SubGlance on your homeserver, invite it to the room,
and take its access token. Then:

- `url`: `https://matrix.example.org/_matrix/client/v3/rooms/!roomid:example.org/send/m.room.message/{{txn_id}}`
- `method`: `PUT`
- `headers`: `Authorization: Bearer <the access token>`
- `body`:

```json
{ "msgtype": "m.text", "body": "{{summary}}\n{{details}}" }
```

Matrix treats a second request with the same transaction id as a repeat, so
a retried alert appears in the room once.

### Pushover

Pushover reads its application token and user key from the query string as
well as from the body, so they go in the masked URL:

- `url`: `https://api.pushover.net/1/messages.json?token=<application token>&user=<user key>`
- `body`:

```json
{ "title": "{{summary}}", "message": "{{details}}" }
```

Pushover accepts at most 1,024 characters in `message`, which a long
quiet-hours summary can exceed; `{{summary}}` alone always fits.

### Mattermost, Rocket.Chat and Google Chat

Each of these takes a message on an incoming-webhook URL, and the URL is the
credential:

- **Mattermost**: **Integrations**, **Incoming Webhooks**, **Add Incoming
  Webhook**, and pick the channel.
- **Rocket.Chat**: **Administration**, **Integrations**, **New**,
  **Incoming**, and pick the channel.
- **Google Chat**, in a browser: open the space, then **Apps &
  integrations** from the menu by its name, **Add webhooks**, and **Copy
  link** on the new webhook.

Use the URL as `url`, and this `body`:

```json
{ "text": "{{summary}}\n{{details}}" }
```

A message goes to the channel or space the webhook was made for. Mattermost
and Rocket.Chat also take a `"channel"` field: a channel name (`"alerts"` in
Mattermost, `"#alerts"` in Rocket.Chat) or `"@username"` for a direct
message. The webhook has to allow it: in Mattermost it must not be locked to
its channel, and in Rocket.Chat the integration must allow overriding the
destination channel, or the message is refused.

### Home Assistant

Home Assistant shows a message on a phone, or in its own notification list,
through a notify action. Create a token under **User profile**, **Security**,
**Long-lived access tokens**. Then:

- `url`: `http://homeassistant.local:8123/api/services/notify/mobile_app_my_phone`
- `headers`: `Authorization: Bearer <long-lived access token>`
- `body`:

```json
{ "title": "{{summary}}", "message": "{{details}}" }
```

The last part of the URL is the action without `notify.` in front: search
for "notify" in the **Actions** tab of Home Assistant's developer tools. A
phone with the companion app has an action named after it, such as
`mobile_app_my_phone`; `persistent_notification` puts the alert in Home
Assistant's own notification list; and `notify` alone uses the first notify
action Home Assistant finds. A Home Assistant on the local network is
reached only when SubGlance is started with
[`--allow-private-targets`](#servers-on-a-private-address).

### Signal

Signal has no webhook of its own. [signal-cli-rest-api](https://github.com/bbernhard/signal-cli-rest-api)
puts one in front of a Signal number you register or link to it, and runs
beside SubGlance in Docker. Then:

- `url`: `http://signal-api:8080/v2/send`
- `body`:

```json
{ "message": "{{summary}}\n{{details}}", "number": "<Signal number>", "recipients": ["<recipient>"] }
```

`number` is the Signal number the API sends from and each recipient a phone
number, both in international form (`+31612345678`); a group is its id, as
`GET /v1/groups/<number>` lists it, starting with `group.`. Separate several
recipients with commas, each between quotes.

The API has no login of its own, so keep it off the internet: it is reached
on the local network, which SubGlance does only when started with
[`--allow-private-targets`](#servers-on-a-private-address). The numbers sit in
the body, which is read back in full and exported as it is, unlike an SMS
channel's numbers, which are masked; use an SMS channel when the people who
can read channels should not see them.

### Bark

[Bark](https://github.com/Finb/Bark) pushes a notification to an iPhone. The
app shows a server address that ends in the phone's device key: the public
`https://api.day.app/<device key>`, or that of a
[bark-server](https://github.com/Finb/bark-server) you run yourself. The key
is the credential, so it goes in the masked URL:

- `url`: `https://api.day.app/<device key>`
- `body`:

```json
{ "title": "{{summary}}", "body": "{{details}}" }
```

Bark also reads `"group"`, the heading it sorts pushes under in the app, and
`"sound"`, one of the sound names the app lists, such as `"telegraph"`.

### WhatsApp (WAHA)

[WAHA](https://waha.devlike.pro/) puts an HTTP API in front of a WhatsApp
number you link to it by scanning a QR code, and runs beside SubGlance in
Docker. Then:

- `url`: `http://waha:3000/api/sendText`
- `headers`: `X-Api-Key: <api key>`, the key WAHA was started with; leave it
  out when WAHA runs without one
- `body`:

```json
{ "session": "<session>", "chatId": "<chat id>", "text": "{{summary}}\n{{details}}" }
```

`session` is the WAHA session the number is linked in, `default` unless you
made another. `chatId` is a phone number in international form without the
plus, followed by `@c.us` (`31612345678@c.us`), or a group's id, which ends
in `@g.us` and is listed by `GET /api/<session>/groups`.

Keep WAHA off the internet: it is reached on the local network, which
SubGlance does only when started with
[`--allow-private-targets`](#servers-on-a-private-address). The chat sits in
the body, which is read back in full and exported as it is, unlike an SMS
channel's numbers, which are masked; use an SMS channel when the people who
can read channels should not see the number.

### WhatsApp (Evolution API)

[Evolution API](https://doc.evolution-api.com/) also puts an HTTP API in
front of WhatsApp numbers, each linked in an instance of its own, and runs
beside SubGlance in Docker. Then:

- `url`: `http://evolution-api:8080/message/sendText/<instance>`
- `headers`: `apikey: <token>`, the instance's token or the API key
  Evolution API was started with
- `body`:

```json
{ "number": "<number>", "text": "{{summary}}\n{{details}}" }
```

`number` is a phone number in international form without the plus
(`31612345678`), or a group's id, which ends in `@g.us` and is listed by
`GET /group/fetchAllGroups/<instance>?getParticipants=false`.

As with WAHA, the number sits in the body, which is read back in full and
exported as it is; use an SMS channel when the people who can read channels
should not see it. An Evolution API on the local network is reached only
when SubGlance is started with
[`--allow-private-targets`](#servers-on-a-private-address).

### WhatsApp (OpenWA)

[OpenWA](https://docs.open-wa.org/) is a third HTTP API in front of WhatsApp
numbers, each linked in a session of its own, and runs beside SubGlance in
Docker. Then:

- `url`: `http://openwa:2785/api/sessions/<session>/messages/send-text`,
  with the session's id, which `GET /api/sessions` lists
- `headers`: `X-API-Key: <api key>`, a key whose role may send messages
- `body`:

```json
{ "chatId": "<chat id>", "text": "{{summary}}\n{{details}}" }
```

`chatId` is a phone number in international form without the plus,
followed by `@c.us` (`31612345678@c.us`), or a group's id, which ends in
`@g.us`. A webhook posts to one chat; for a second chat, add a second
channel with its own `chatId`.

As with WAHA, the chat sits in the body, which is read back in full and
exported as it is; use an SMS channel when the people who can read channels
should not see it. An OpenWA on the local network is reached only when
SubGlance is started with
[`--allow-private-targets`](#servers-on-a-private-address).

### Apprise

[Apprise](https://github.com/caronc/apprise) sends one message to any of a
hundred-odd services, each named by a URL of its own (`tgram://`,
`lametric://`, `mailto://` and so on). Its HTTP server,
[Apprise API](https://github.com/caronc/apprise-api), runs beside SubGlance
in Docker and keeps those URLs under a key of your choosing, saved in its
web interface or with `POST /add/<key>`. A webhook to that key reaches every
service saved under it:

- `url`: `http://apprise:8000/notify/<key>`
- `body`:

```json
{ "title": "{{summary}}", "body": "{{details}}" }
```

Apprise API answers 424 when a service under the key did not take the
message, so a failed delivery shows in the channel's Delivery column like
any other. With `APPRISE_AUTH_REQUIRED=yes` it wants Basic Auth: put
`Authorization: Basic <base64 of user:password>` in `headers`. The service
URLs stay in Apprise API, so the body holds no credential. An Apprise API on
the local network is reached only when SubGlance is started with
[`--allow-private-targets`](#servers-on-a-private-address).

A service SubGlance has a channel type for, or one this page gives a body
for, needs no Apprise in between.

## Which channels get added

A new channel type is maintenance for as long as the upstream API exists, so
there is a bar for adding one: the service can be **self-hosted**, or the
people who run SubGlance **demonstrably use it**. ntfy, Gotify and SMS Gateway
for Android meet the first; Slack, Discord, Telegram, email and Twilio meet the
second.

For anything else (Teams, Matrix, Pushover, Mattermost and so on), use the `webhook`
channel. It posts the alert as a stable JSON object, described under
[the webhook payload](#the-webhook-payload), or, with
[a body of your own](#a-body-of-your-own), in the shape the service wants;
custom headers cover most authentication schemes. A request for a new type is judged against the bar
above, not against whether another tool has it.
