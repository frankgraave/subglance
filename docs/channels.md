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
  Setting both a token and a password is refused, since only one of them
  would be used.

## Gotify

Alerts are posted to `<url>/message`, so a Gotify served under a sub-path
behind a reverse proxy works as long as `url` includes that path. The
application token is sent in the `X-Gotify-Key` header, never in the URL,
so it does not end up in logs. Priorities run from 0 to 10; the Android app
plays a sound from 4 and shows a pop-up from 8.

## Servers on a private address

A self-hosted ntfy or Gotify usually lives on the local network, at an address
like `192.168.1.20`. SubGlance refuses to connect to private, loopback and
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
people who run SubGlance **demonstrably use it**. ntfy and Gotify meet the first;
Slack, Discord, Telegram and email meet the second.

For anything else (Teams, Matrix, Pushover and so on), use the `webhook`
channel. It posts the alert as a stable, documented JSON object, and custom
headers cover most authentication schemes. A request for a new type is judged
against the bar above, not against whether another tool has it.
