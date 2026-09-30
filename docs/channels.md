# Notification channels

A channel is where an alert goes. Each one has a type and a small set of
settings; the Notifications screen shows the fields each type needs, and the
same settings can be written through `POST /api/v1/channels` (see
[`openapi.yaml`](openapi.yaml)).

| Type | Required | Optional |
|---|---|---|
| `webhook` | `url` | `headers` (one `Name: value` per line) |
| `discord` | `url` (the channel webhook) | — |
| `slack` | `url` (the incoming webhook) | — |
| `telegram` | `bot_token`, `chat_id` | — |
| `email` | `to` | `from`, `host`, `port`, `username`, `password` |
| `ntfy` | `topic` | `url` (default `https://ntfy.sh`), `token`, or `username` + `password` |
| `gotify` | `url`, `token` (an application token) | `priority_down` (default 8), `priority_up` (default 4) |
| `sms` | `numbers`, `provider`, and per provider: `url`, `username`, `password` (`android-gateway`) or `account_sid`, `auth_token`, `from` (`twilio`) | `country_code`, `hourly_limit` (default 10), `recoveries` (default `true`), `timezone` |

Every value that is not a plain destination is masked when it is read back:
`****` plus the last four characters. That includes the ntfy `topic`: on a
server without access control the topic name is the credential, since anyone
who knows it can read the alerts and post fake ones. Use a topic nobody would
guess, or an access token.

Every channel has a **Send test** button, which delivers a real message and
shows the exact error the far end returned.

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
delivery log only ever show that masked form.

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
any hour. An alert past the limit is not sent; the delivery log shows it as
not sent and why, and it is not retried. The first message after the hour
ends says how many were held back: `(+3 alerts not sent by SMS, see
SubGlance)`. The reason is money and carriers: on Twilio every message is
billed, and a flapping monitor must not produce a bill or get a SIM card
blocked. [Alert grouping](operations.md#alert-grouping) already folds twenty monitors that
fail together into one alert before the limit counts it.

Two channels that send to the same numbers through the same account share
one limit. The count lives in memory, so a restart starts a fresh hour.

`recoveries: false` sends outages only. Quiet hours work as on every other
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
are shown in the test button and the delivery log. A delivery counts as done
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
delivery log, and is not retried: a refused address stays refused.

## Which channels get added

A new channel type is maintenance for as long as the upstream API exists, so
there is a bar for adding one: the service can be **self-hosted**, or the
people who run SubGlance **demonstrably use it**. ntfy, Gotify and SMS Gateway
for Android meet the first; Slack, Discord, Telegram, email and Twilio meet the
second.

For anything else (Teams, Matrix, Pushover and so on), use the `webhook`
channel. It posts the alert as a stable, documented JSON object, and custom
headers cover most authentication schemes. A request for a new type is judged
against the bar above, not against whether another tool has it.
