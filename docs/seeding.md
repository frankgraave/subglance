# Seeding a demo database

SubGlance cannot be seen without history. A fresh instance is a correct empty
screen: no beat bars, no uptime percentages, no incidents to acknowledge, no
channel that has ever delivered anything. Everything the product is *for*
appears only after days of real checks against real services, which makes it
awkward to demonstrate, hard to judge a design change against, and slow to
reproduce a bug that needs a year of data behind it.

`cmd/seed` writes that history in a couple of seconds.

```bash
make seed            # fills ./tmp with a demo estate
make run             # look at it
```

Or directly, which is what you want when the data directory is somewhere else:

```bash
go run ./cmd/seed --data-dir /var/lib/subglance-demo --reset
go run ./cmd/subglance --data-dir /var/lib/subglance-demo
```

Sign in as `admin@example.com`, password `demo-password-123`. Two more
accounts exist — `editor@example.com` and `viewer@example.com`, same password —
because the role someone has changes what the interface offers them, and a
demo that only ever signs in as an administrator never shows that.

## What it writes

The estate is a fictional company's infrastructure, sized so that every screen
has something to show and every branch a screen can take is taken by at least
one row.

- **Twenty-six monitors** covering all five types: `http`, `tcp`, `ping`,
  `ssl` and `push`. Among them a POST with a JSON body and custom headers, all
  three keyword modes including the inverse one that catches an error page
  returning 200, a monitor with redirects deliberately not followed, response
  capture both on and off, a paused monitor, a monitor no channel is attached
  to, and one that has never been checked at all.
- **Months of heartbeats**, as individual beats for the recent window and as
  hourly rollup buckets before that — which is the shape a long-running
  instance actually has, because that is what `RollupHeartbeats` leaves behind.
- **Incidents in every state**: resolved, open and confirmed, open but not yet
  confirmed, acknowledged and still broken, and one escalating through repeat
  reminders because nobody has answered it.
- **Eight notification channels**, one of each type plus a webhook that is
  failing, one with a backlog, and one switched off, so the channel health
  badges have something other than a row of green to show.
- **Outbox rows** for every alert those incidents produced — delivered, failed
  and pending — which is where the notifications screen gets its per-channel
  health from.
- **Users and API tokens** in the states the token list renders: live,
  expiring, and revoked.
- **Tags** on every monitor: `env`, `team`, `tier`, `customer`, `region`,
  `job`. The grouped and filtered views need more than one dimension to be
  worth looking at.

## Flags

| Flag | Default | What it does |
| --- | --- | --- |
| `--data-dir` | `./tmp` | The directory the server will be given |
| `--db` | — | The database file, overriding `--data-dir` |
| `--reset` | off | Delete the database first |
| `--seed` | `1` | Random seed |
| `--history` | `120 days` | How far back the history reaches |
| `--raw-window` | `7 days` | How much of it is individual beats rather than rollups |
| `--password` | `demo-password-123` | Password for the seeded accounts |
| `--secret-key` | — | Encrypt channel config at rest, as the server's flag does |

`--seed` is worth knowing about. The same seed produces the same estate, so a
screenshot taken last week can be compared to one taken today and the
difference is the change you made rather than the dice.

`--history` and `--raw-window` decide how much is written. The defaults produce
roughly 150,000 heartbeats and 60,000 hourly buckets in about nine megabytes.
Raising `--raw-window` to the full history gives every monitor full resolution
throughout, at a proportional cost in rows.

## What it refuses to do

Seeding a database that already holds monitors or users fails, and says to
pass `--reset`. Pointing this at a production data directory by mistake should
cost nothing, and a fictional estate quietly merged into real data would be
worse than either one alone.

Every target is under `example.com`, `example.net` or `example.invalid` —
reserved by RFC 2606 and unable to belong to anyone. A test enforces it. A
seeder shipped with a real hostname in it would turn every demo into an
unannounced load test against a stranger.

## Running the server against a seeded database

The server starts checking immediately, and none of the seeded targets
resolve, so within a minute or two every monitor accumulates real failures on
top of the invented history. That is fine for a look around and wrong for a
screenshot session. Two ways to avoid it:

- Take the screenshots first. The first check of a 60-second monitor is a
  minute away, and the data is untouched until then.
- Re-seed with `--reset` between passes, which is a two-second operation.

The database is an ordinary one in every other respect: the rows go in through
the store, under the same schema, the same constraints and the same
relationships the running product writes. There is no demo mode, and nothing
in the server knows the data was seeded.

## Encryption at rest

A seeded database works with and without `--secret-key`. Pass the same key to
both commands and the channel configuration is encrypted exactly as it would
be had the channels been created through the interface:

```bash
key=$(openssl rand -hex 32)
go run ./cmd/seed       --data-dir ./tmp --reset --secret-key "$key"
go run ./cmd/subglance  --data-dir ./tmp        --secret-key "$key"
```

Omit it from both and the configuration is stored in plain text, which is the
default and what most instances run with.

## Push monitors

Three of the monitors are push monitors, and their URL tokens exist in
plaintext exactly once — at the moment they are created. The seeder prints
them, because demonstrating a dead man's switch needs a URL to curl:

```bash
curl -fsS http://localhost:8080/api/v1/push/sgu_...
```

Only the hashes are stored, so a token missed on the way past is gone; re-seed
to get a new set.
