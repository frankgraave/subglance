# Security Policy

## Reporting a vulnerability

Please do **not** report security issues through a public issue.

Use GitHub's private reporting form instead:
[Security → Report a vulnerability](https://github.com/frankgraave/subglance/security/advisories/new).

You will get an acknowledgement within 72 hours. Once a fix is available it will
be published with credit to the reporter, unless you prefer to stay anonymous.

## Supported versions

While the project is in early development, fixes are released only against the
latest `main`. There are no tagged releases yet.

## Scope

SubGlance fetches URLs that its users supply, stores credentials for notification
channels, and exposes an authenticated API. Reports touching any of the following
are especially welcome:

- **SSRF** — bypassing the private-address guard to reach internal networks.
  Note that `--allow-private-targets` intentionally disables that guard; only
  bypasses of the default configuration are in scope.
- **Authentication and session handling** — privilege escalation, session
  fixation, token leakage.
- **Injection** — SQL injection, header injection into outbound checks, template
  injection in notification payloads.
- **Secret exposure** — notification channel credentials appearing in logs, API
  responses, or error messages.
- **Denial of service** through crafted check targets or responses, for example a
  monitored endpoint that can exhaust memory or wedge the scheduler.

## Out of scope

- Findings that require `--allow-private-targets` to be enabled, since that flag
  exists precisely to permit internal monitoring.
- Attacks requiring an already-compromised host or database file.
- Missing hardening headers on endpoints that serve no sensitive content, absent
  a demonstrated impact.
- Automated scanner output without a working proof of concept.

## Security-relevant design choices

For context when assessing a report:

- Private, loopback and link-local targets are refused by default; enabling them
  is an explicit operator decision.
- Passwords are hashed with argon2id.
- Notification channel configuration is encrypted at rest **only when
  `--secret-key` is set**. Without it, webhook URLs, bot tokens and SMTP
  passwords are stored in plain text. See below.
- The HTTP server runs with bounded read, write and idle timeouts.
- A panic in a request handler is recovered rather than taking down the process —
  a monitoring tool that dies on one bad request is worse than useless.
- A failed HTTP check stores the first 2 KiB of the response body (see below).

## Notification channel configuration at rest

A notification channel holds whatever lets SubGlance post on your behalf: a
Slack or Discord webhook URL, a Telegram bot token, an SMTP username and
password. All of it lives in one column of the SQLite database.

### The default is plain text, and that is worth saying in full

**With no `--secret-key`, that column is plain text.** Anyone who can read
`subglance.db` can read every channel credential: a copy of the data volume, a
backup on a laptop, a decommissioned disk, a misconfigured file share. The API
masks these values on the way out, and that mask does nothing about the file.

That default is deliberate rather than an oversight waiting to be fixed. The
alternative considered was a key file created automatically beside
`subglance.db` on first start, so encryption would be on for everyone. It was
rejected: a key that lives next to the data travels inside every backup and
every `docker cp` of the volume, so it protects nothing against the one
attacker this feature is about — while making the database look protected.
Being explicitly unencrypted is more honest than being automatically and
uselessly encrypted.

### Turning it on

```
--secret-key <32 bytes of key material or a path to a file holding it>
SUBGLANCE_SECRET_KEY=<the same>
```

Generate a key with `openssl rand -hex 32` (or `openssl rand -base64 32`;
both encodings are accepted, as is a trailing newline in a file). A passphrase
is **refused**, not stretched into a key — the error says so. Accepting one
would mean `hunter2` looked like encryption.

**Prefer the file over the environment variable.** An environment variable is
readable in `/proc/<pid>/environ` by anything running as the same user, and
`docker inspect` prints it from the container's stored configuration, where it
outlives the running process and lands in whatever collects that output. A file
mounted read-only is visible only to whoever can read the file:

```yaml
volumes:
  - ./subglance.key:/run/secrets/subglance.key:ro
environment:
  SUBGLANCE_SECRET_KEY: /run/secrets/subglance.key
```

Keep the key somewhere other than the backup of the database it decrypts. A key
stored beside the ciphertext is the design that was rejected above, rebuilt by
hand.

### What it protects, and what it does not

Protects against: someone who obtains the database file or a backup of it
without the key. The values are sealed with AES-256-GCM, a fresh random nonce
per write, and the ciphertext is authenticated — an edited value is refused, not
decrypted into something a notifier would then send to.

Does **not** protect against:

- anyone who can read the key file, or the environment of the running process;
- anyone who can read the process memory, where the key and the decrypted
  configuration both live while SubGlance runs;
- anyone with write access to the database, who can still delete a channel or
  replace one wholesale;
- an authenticated API user, whose access is governed by roles and the mask, not
  by this.

It is encryption at rest against theft of the data, and nothing more. Threat
models that begin "the attacker is already on the host" are unaffected.

### Enabling it on an existing installation

The first start with a key encrypts every existing plaintext row, in one
transaction, before the server accepts a request. Nothing is left half
converted, and no channel needs to be re-entered. The alternative — encrypting
each row only when it is next edited — was rejected because the rows least
likely to be edited are the channels that have worked for a year, and the
operator would have no way to know which half of the database was protected.

A start with a key says so in the log, as does the count of rows it converted.
The count is rows; no value and no key is ever logged, at any level.

### A wrong key is a refusal to start

If the key cannot decrypt the stored rows, SubGlance **exits with an error and
changes nothing**. It does not start with unreadable channels.

That is the safer failure. Starting anyway would mean every alert failing at
delivery time — discovered during the incident the alerts existed to report —
and the operator would be debugging notifications instead of the outage. At the
moment of refusal the database is still intact and correct, so putting the right
key back is a complete recovery.

### Rotating the key

Pass the old key as `--secret-key-previous` and the new one as `--secret-key`
for one start:

```
subglance --secret-key-previous /run/secrets/subglance.key.old \
          --secret-key /run/secrets/subglance.key
```

Every row is re-encrypted under the new key at startup, in one transaction; a
crash halfway leaves everything under the old key rather than a split database.
Remove `--secret-key-previous` afterwards. Leaving it set is inert — the
reconciliation is idempotent — but it keeps a retired key mounted for no reason.

### Turning it off

Removing the key while encrypted rows exist is also a refusal to start, for the
same reason: it must not be a way to lose five channels quietly. To decrypt on
purpose, name the key as `--secret-key-previous` with no `--secret-key`:

```
subglance --secret-key-previous /run/secrets/subglance.key
```

Every row is written back as plain text, the log says so, and subsequent starts
need no key at all.

### Backups

`subglance backup` needs no key. It copies the database as it is, so encrypted
configuration stays encrypted in the snapshot — which also means **a backup is
worthless without the key**, and that restoring one onto an instance with a
different key will refuse to start rather than silently break the channels.

## Stored response snapshots

When an HTTP check fails, SubGlance keeps the beginning of the response that
explains the failure — the upstream timeout, the database error, the
maintenance page. This is content from the monitored service, stored in the
SubGlance database, and it is the only place where data SubGlance did not
produce itself is written to disk. It deserves to be called out explicitly.

What that means for an operator:

- Anyone with read access to the API or the database file can read these
  snapshots. They inherit no separate permission.
- A response can contain more than an error message. A health endpoint behind a
  session may answer with account data; an error page may include a request
  identifier, an internal hostname or a stack trace. If that matters for a
  given target, turn capture off for that monitor with
  `"capture_response": false`.
- No credentials are stored from request or response headers. Only an allowlist
  of response headers is kept (content type, server, date, retry-after, location
  and a few request identifiers); `Set-Cookie`, `Authorization` and everything
  else are dropped rather than filtered, so a header invented tomorrow is not
  stored either. The response body is a different matter: it is stored as it
  arrived, truncated but not redacted, so a body that echoes a token or a
  session identifier keeps it until the snapshot expires.
- Snapshots expire with the heartbeat they belong to. When raw heartbeats are
  rolled up into hourly buckets after the retention window, the snapshots are
  deleted with them by a foreign key, so there is no second retention policy
  that can be forgotten.
- Only the first few failures of a streak are stored, and each is capped at
  2 KiB, so an outage cannot grow the database without bound. That allowance is
  counted against the open incident rather than against the running process, so
  restarting SubGlance during an outage does not start it over. A monitor that
  is flapping stores none at all: it has already written several copies of the
  same failure, and each recovery would otherwise reset its allowance.
