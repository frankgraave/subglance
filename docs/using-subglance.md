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

An account's address is one line of at most 254 characters, the longest
plain-ASCII address mail can be delivered to, and it cannot be changed later.

Passwords must be at least 12 characters. There is no complexity rule: length
is what makes a password hard to crack, and demanding a digit and a symbol
mostly produces predictable substitutions, so a few ordinary words beat a
short cryptic one. Everything is stored on your own machine.

There is no emailed reset link, and an administrator cannot set another
account's password: a password is its owner's to know. Whoever can stop the
server and reach its data directory can set a new one with
`subglance reset-password`; see [Locked out](operations.md#locked-out).

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
reachable rather than waiting for the first red row. Pressing Save monitor
without testing runs the same check first. If it passes, the monitor is
saved. If it fails, the form shows the result and saves nothing; press Save
monitor again to watch the service anyway, since a target that is down right
now is a fair thing to start monitoring. The dashboard has
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

For an active check, a changed target or request setting is tested before it
is saved. Press **Test it** to see the answer first, or press **Save changes**
and the drawer runs the same check itself: if it passes, the change is saved;
if it fails, the drawer shows the result and saves nothing, and pressing Save
changes again saves it anyway, since a service that is down right now is still
worth watching. The check uses the current draft without recording check
history or sending alerts, and any change to a setting it reads replaces an
earlier answer with a fresh test. A name, tags, the interval, channels or
repeat alerts save without a check. The TLS floor remains in Advanced options;
changing only that floor can be saved directly.

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
  `Secure` with the `__Host-` prefix when the dashboard is opened over HTTPS,
  behind a TLS-terminating proxy included (see
  [the sign-in cookie over HTTPS](operations.md#the-sign-in-cookie-over-https)).
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

Browser sessions are listed at `GET /api/v1/sessions` and under Settings →
Account, where one can be signed out (`DELETE /api/v1/sessions/{id}`) or all
but the current one (`POST /api/v1/sessions/end-others`). Administrators read
and end another account's at `/api/v1/users/{id}/sessions`. See
[Signed-in sessions](operations.md#signed-in-sessions).

Three roles decide what a credential may do to monitors and incidents:
**viewer** (read-only), **editor** (manage monitors, acknowledge incidents) and
**admin** (also manages users). Tokens are scoped a little differently, because
listing and revoking are limited to the caller's own: any role may list its
tokens and revoke one, while minting a new token needs editor or admin.

A token can carry less authority than the account that made it: pass
`"role":"viewer"` when creating one for a dashboard that only reads. A token
never acts above its owner's current role, so demoting an account demotes its
tokens too. Without a role, a token acts with the creator's role.

## Check types

| Type | Target shape | What it verifies |
|---|---|---|
| `http` | `https://example.com/health` | Status code, response time, keyword present or absent, one field of a JSON body, certificate expiry |
| `tcp` | `db.example.com:5432` | A TCP handshake completes within the timeout |
| `ping` | `example.com` or `192.0.2.10` | ICMP echo reply |
| `ssl` | `example.com` (port optional, defaults to 443) | Certificate validity, hostname match, chain of trust, days until expiry, OCSP revocation when available |
| `dns` | `example.com` (a domain name, no scheme or port) | The A, AAAA, CNAME, MX or TXT records hold the expected values |
| `domain` | `example.com` (a domain name, no scheme or port) | The domain's registration has not expired, with a warning a number of days before |
| `push` | none — the job reports in | That the job reported inside its window |

The request settings, `method`, `expected_status`, `keyword`, `headers` and
`body`, are read only by an `http` check, and `ssl_warn_days` and
`min_tls_version` only by an `http` or `ssl` check. Set on another type, they
would sit in the monitor's settings and never change a check: a keyword on a
`tcp` monitor passes whatever the service answers. So the API refuses them on
`field`, and the forms offer each one only where it is read. Changing a
monitor's type resets the settings the new type does not read.

Creating or editing a monitor rejects whitespace inside its hostname, including
Unicode spaces such as a nonbreaking space, with an error on `target`. Internal
spaces are never removed to turn the input into a different hostname. Ping,
TCP and SSL accept surrounding whitespace and trim it when parsing the target;
HTTP parses the input as a URL instead of trimming it: leading whitespace is
invalid, while spaces in paths and queries are URL data and remain allowed.
TCP and SSL ignore a pasted URL's credentials, path, query and fragment; ping
requires a bare hostname or IP address, with no port or URL components.

A hostname has to be one a resolver will look up. SubGlance resolves names
itself rather than through the C library, and that resolver answers "no such
host" for a malformed name without asking any server — the same words it uses
for a name that does not exist. So a monitor saved with one would fail every
check while looking like an outage. Creating, editing, previewing or importing
an `http`, `tcp`, `ssl` or `ping` monitor therefore refuses, on `target`, a
hostname with an empty label (`exa..mple.com`), a character other than a
letter, digit, hyphen, underscore or dot, a label that starts or ends with a
hyphen, a label over 63 characters or a name over 253, a name of digits and
dots that is not a valid IPv4 address (`10.0.1`, `999.1.1.1`), and a `.onion`
name. Single-label names such as `db` or `localhost`, underscores and a
trailing dot are accepted. An internationalised name such as `bücher.de` works
in an `http` URL, which is converted to its `xn--` form before the request;
`tcp`, `ssl` and `ping` need that form written out (`xn--bcher-kva.de`).
A port has to be one a connection can use: an `http` target with port 0 or a
port above 65535 (`https://example.com:99999/`) is refused on `target`, as a
`tcp` target already was. An empty port (`http://example.com:/`) means the
scheme's default. A monitor saved before these rules keeps its target until
it is edited; the check reports it as a DNS or connection failure, as it did
before.

A monitor's free-text settings have ceilings, as its name does: the target,
`keyword`, `expected_status` and a JSON assertion's path and expected value
take 2048 characters each, a request `body` 65,536, and `headers` at most 64
entries with each `Name: value` line at most 8190 bytes. Past them a server
refuses the request on every check (nginx stops a request line at 8 KB, and
Apache httpd a header line at 8190 bytes), and an export of the monitor could
grow past what an import accepts. Creating, editing, previewing and importing
all refuse a longer value on its field. A monitor saved before the limits
keeps its values through any other edit, and through an edit or an import
that sends them back unchanged.

An HTTP monitor's own request headers — an `Authorization` or `X-Api-Key`, for
instance — are sent only to the origin (scheme, host and port) of its target.
When a followed redirect leads to another origin, every header the monitor set
is left off that hop and any after it that stay off the original origin; the
`User-Agent` and `Accept` SubGlance sends itself remain. Otherwise a redirect to
an expired domain, a CDN or a login page would hand your key to that server.
The one exception is the upgrade from `http://host` to `https://host` on the
default ports, which only makes the connection safer. A check that then fails
on its status code, keyword or JSON assertion says which origin went without the headers. If that server needs them, point
the monitor at the final URL instead of the one that redirects to it.

### Checking a field of a JSON response

A health endpoint that answers `200 OK` with `{"status":"degraded","db":"down"}`
passes a status check, and a keyword check for `"ok"` can match the wrong
field. An HTTP monitor can instead assert one field of the JSON body:

```json
"json_assertion": {"path": "checks.db.status", "operator": "equals", "expected": "up"}
```

- **`path`** is dot notation with array indexes: `checks.db.status`,
  `items[0].ok`, or `[0].id` when the body is an array. A key that itself
  contains a dot or a square bracket cannot be addressed in this version.
- **`operator`** is one of `equals`, `not_equals`, `exists`, `less_than` and
  `greater_than`.
- **`expected`** is a JSON value written as itself, and its type counts:
  `"1"` is a string and `1` a number, so `equals` with `"1"` fails on
  `{"count": 1}` and the message names both types. Numbers compare by value,
  so `1` equals `1.0`. `less_than` and `greater_than` take a number. `exists`
  takes no expected value and passes for any value at the path, `null`
  included.

A failed assertion fails the check with the cause `assertion`, and the error
says what was there: `checks.db.status was "down", expected "up"`. A body that
is not JSON, and a path that does not exist, fail the same way with their own
message; the missing-path message says which part of the path was missing.
With response capture on, the body that failed is kept alongside.

The assertion runs after the status and keyword checks, on the same first
1 MiB of the body. A larger body fails the assertion instead of being parsed
in part.

A keyword check and a JSON assertion both read the response body, and a `HEAD`
response has none. A monitor that sends `HEAD` and also checks the body is
refused with an error on `method`: a required keyword would never be found,
a forbidden keyword would never be present, and the assertion would never have
a document to read. Use `GET` for a monitor that checks what the page says.

There is one assertion per monitor, and no boolean logic across several. If
an endpoint needs more judgement than one field can carry, a small health
route that does the judging and answers with one field is the better tool,
and it keeps the logic next to the service that knows what healthy means.

Every HTTP check opens a new connection and closes it afterwards, so it goes
through the same DNS lookup, TCP handshake and TLS handshake as a new visitor.
A check that reused the previous check's connection would stay green while the
server refused new connections — after a firewall change, a DNS record moved
to a dead host or a broken TLS reload — and would keep reporting a certificate
that had already been renewed. The recorded response time therefore includes
setting up the connection, not only the server's answer.

A TCP check completes the handshake and hangs up without sending a payload —
speaking a protocol badly is a good way to end up in someone's fail2ban rules.

The SSL check verifies the certificate itself rather than letting the handshake
fail, so an expired certificate still reports *when* it expired and by how much.
Set `ssl_warn_days` to hear about it while there is still time to renew, instead
of at the moment the site breaks. A certificate that is valid but expires within
that many days puts the monitor in its own state, **expiring** ("Expiring soon"
on the dashboard). It is not an outage: the check passes, it counts as up in
every uptime figure, and a public status page shows the service as up with the
note "Certificate expires soon" and no date. It still alerts, once, as
"api: certificate expires soon", repeats on the monitor's reminder schedule until
someone acknowledges it, and sends "certificate no longer expires soon" on the
first check that sees a renewed certificate.

The warning starts at `ssl_warn_days` or at a quarter of the certificate's own
lifetime before it expires, whichever is later. Certificates do not all last
90 days any more: Let's Encrypt issues ones that last 160 hours (Caddy's
`profile shortlived`, Certbot's `--preferred-profile shortlived`, and every
certificate for an IP address), and with a fixed 14 days such a certificate
would be expiring from the hour it was issued, a notice that never goes away
and that you learn to ignore. A quarter of 160 hours is 40 hours, well after
the point where an ACME client renews, so a certificate still not renewed then
is a renewal that failed. A 90-day certificate's quarter is 22 days, so 14 days
stays 14; a 45-day one warns at 11 days. The cap applies to a value you set as
well, since a warning window longer than the certificate's life can only mean
always warning. When it shortened the window, the notice says so:
"certificate expires in 35 hours (on 2026-10-24); it lasts 6 days, so the
warning starts 40 hours before expiry". With less than two days left, the
notice counts hours rather than days.

The same applies to an `http` monitor with an `https` target, at the same
threshold and with the same sentence. When that check follows
a redirect to another host, as a site behind a single sign-on proxy does to its
login page, the certificate it reports is the one of the first host that
answered over TLS: the host in the URL field for an `https` target, the host it
upgrades to for an `http` one. The login host's certificate is still verified,
so an expired or untrusted one fails the check like any other TLS failure, but
its expiry date is not reported and `ssl_warn_days` does not warn about it; give
that host a monitor of its own. A failure while a monitor is expiring is judged
like any other failure: one is a warning, and reaching the retry threshold
confirms an outage, which is down and is its own incident. An expired
certificate fails the check and is down.

SSL monitors also check the **leaf certificate's OCSP revocation status** after
validating its chain. A fresh, authenticated stapled response takes precedence;
if it is absent, invalid, stale or unknown, SubGlance tries the certificate's
OCSP responders. An authenticated `revoked` answer fails with the TLS cause
`certificate revoked (OCSP)`, retaining the certificate expiry date. Response
signatures, serial and issuer identity, responder authorization and timestamps
are checked before accepting an answer. These checks follow the acceptance
requirements in [RFC 6960 §3.2](https://www.rfc-editor.org/rfc/rfc6960.html#section-3.2).

**Revocation is best effort, with a soft-fail policy.** An unreachable or blocked
responder, an unknown status, an invalid response, or a certificate without an
OCSP responder does not make an otherwise valid endpoint fail. A passing SSL
check therefore does **not prove that its certificate is unrevoked**. The result
does not expose a separate revocation-certainty field. Active lookup has a
shared two-second budget within the monitor's existing timeout and caller
context, at most three responder attempts of at most one second each, a 64 KiB
response-body limit and a 16 KiB response-header limit. Responses expire at
`nextUpdate` and may be at most seven days old; without `nextUpdate`, at most
24 hours old. Future timestamps allow five minutes of clock skew.

Responder URLs must use HTTP or HTTPS and cannot include credentials or
fragments. Redirects and environment proxies are disabled. Resolved addresses
pass the normal private/reserved-address guard at connection time, **even with
`--allow-private-targets` enabled**: permission to monitor an internal service
does not grant a certificate permission to contact arbitrary internal services.
Internal OCSP responders therefore remain unavailable to active lookup; a valid
staple still works. HTTPS responders require a normally trusted TLS certificate.

CRL downloads are deliberately omitted: whole revocation lists can be large and
need their own download, caching and freshness policy. Intermediate CA and
responder-certificate revocation are not recursively queried, and TLS
Must-Staple is not enforced. A directly trusted leaf has no separate verified
issuer to query. HTTP monitors retain their existing TLS checks; use an SSL
monitor for this OCSP check.

Ping needs either unprivileged ICMP sockets or `CAP_NET_RAW`. SubGlance tries the
unprivileged socket first and falls back to the raw one; when neither is allowed
the error names both fixes. If ICMP is blocked entirely on your network, a TCP
check against a known port answers the same question more reliably.

Routers drop and rate-limit ICMP routinely, so one lost packet is not a failed
check. A ping check sends an echo request every second until one is answered
or the monitor's timeout runs out, as `ping -w` does; a late reply to an
earlier request counts too. The response time is that of the request that was
answered, so a lost packet does not show up as a slower host. The check fails
when the timeout passes without a reply, and at once when the network answers
with an ICMP error such as *destination unreachable*.

### Checking a DNS record

A domain move that went wrong, or a zone someone else now controls, usually
shows up only once the site itself fails. A `dns` monitor asks for one record
type of one name and compares the answer with the values it expects:

```json
{"type": "dns", "target": "example.com",
 "dns": {"record_type": "A", "expected": ["192.0.2.10", "192.0.2.11"], "resolver": "1.1.1.1"}}
```

- **`record_type`** is one of `A`, `AAAA`, `CNAME`, `MX` and `TXT`.
- **`expected`** lists the values, written as a DNS tool prints them: an
  address for A and AAAA, a host name for CNAME, `mail.example.com` or
  `10 mail.example.com` for MX (without the number the preference is not
  compared), and the full text for TXT. Names compare without case or
  trailing dot. An empty list passes on any record of the type.
- **`resolver`** is optional: a host or an IP address with an optional port.
  Without it the check asks the resolver of the host SubGlance runs on, the
  one in `/etc/resolv.conf`. Set one, such as `1.1.1.1` or the zone's own name
  server, to see past a local cache. A resolver set on the monitor is reached
  through the same private-address guard as every target; the host's own is
  not, because inside a container it is a loopback address. A resolver given
  by name follows the hostname rule above (an `xn--` form for an
  internationalised name, no `exa..mple.com`, no `999.1.1.1`), and is refused
  on `dns.resolver` otherwise: it is looked up before it is asked, and a name
  the lookup refuses would read as a resolver that never answers.

For A, AAAA, CNAME and MX the answer must hold exactly the expected values, in
any order: an extra address is what a hijacked zone looks like. For TXT each
expected value must be present and other TXT records are allowed, because one
name's TXT records serve unrelated purposes such as SPF and site verification.

A CNAME check compares the record the zone holds, not the address the alias
chain ends at, so `www` pointing at a CDN's name passes even though the CDN
answers with its own internal names.

An answer with other records than expected fails with the cause
`dns_mismatch`, and the error names both sides:
`A records for example.com: expected 192.0.2.10, got 192.0.2.9`. A name that does not
exist (NXDOMAIN), a resolver that answers with an error and a resolver that
cannot be reached fail with the cause `dns`, the same as an HTTP check whose
host cannot be resolved; a resolver that stays silent past the monitor's
timeout fails with `timeout`. An answer too large for one UDP datagram is
asked again over TCP.

A query or its answer lost on the way is not a failed check. When no answer
has come after two seconds the check sends the query again, then waits twice
as long before the next, until an answer arrives or the monitor's timeout runs
out, as the resolver of the host itself does; a late answer to an earlier
query counts too. The response time is that of the query that was answered,
so a lost datagram does not show up as a slower resolver. A resolver that
refuses the query outright is not asked again.

### Checking when a domain expires

A domain whose registration lapses takes a site offline as completely as a
broken server, and none of the other checks see it coming: the site answers
until the day the registry stops publishing the name. A `domain` monitor reads
the registration's expiry date and warns ahead of it:

```json
{"type": "domain", "target": "example.com", "domain_warn_days": 30}
```

- **`target`** is a domain name. A name under a registered domain is checked
  as that domain, so `www.example.co.uk` reads the date of `example.co.uk`.
  An internationalised name is written in its `xn--` form.
- **`domain_warn_days`** is how many days before the expiry date the monitor
  starts warning, 0 to 365. Left out it is 30: renewing a domain can need a
  person with the registrar login and a payment method, which is more lead
  time than a certificate needs. 0 never warns and only fails once the
  registration has expired.

Inside the warning window the monitor is `expiring`: it alerts with the
cause `domain_expiry`, without counting as downtime, the same way a
certificate about to expire does. Once the date has passed the check fails
and the monitor is down.

The date comes from RDAP, the JSON protocol registries publish registration
data in, with the server for each top-level domain taken from IANA's
bootstrap registry. WHOIS is not read: it is free text in a different layout
per registry. Not every country-code registry runs RDAP, and a registry's
server can be down or limiting requests. When the date cannot be read the
monitor is `unknown`, with the reason as its error. That is not an outage:
nothing is recorded in its history, no incident opens and no alert is sent,
and an alert already open stays as it was.

A domain monitor is checked once a day unless its `interval_s` says otherwise,
and never more often than every 6 hours (`interval_s` 21600), also while it
is down: the date moves once a year, and registries limit clients that ask
often. Its first failure confirms, because a date read off the registry is not
a blip; set `retries` to wait for more.

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

## A self-signed certificate, or a CA of your own

SubGlance verifies every certificate against the host's trust store. A router,
a NAS, a hypervisor's web interface or a service behind a private CA presents a
certificate nothing there vouches for, so its `http` and `ssl` checks fail with
`certificate signed by unknown authority` while the service works. There are two
ways to say what to trust instead. There is no switch that turns verification
off: a monitor that accepts any certificate keeps passing when something else
answers on the address, which is the one thing it was set up to notice.

**Trust one certificate on one monitor.** When **Test it** fails on an `ssl`
monitor or an `https` address because nothing vouches for the certificate —
self-signed, or from a CA this host does not know — the form shows the
certificate the server presented: who it is for, who issued it, when it expires
and its SHA-256 fingerprint, with a **Trust this certificate** button. Compare
the fingerprint with the one the service shows on its own certificate page
before pressing it: whatever answered the test is what gets trusted. Pressing it
tests again with the certificate trusted, and saving the monitor stores the pin.
The edit form shows a trusted certificate with the day it was trusted and a
**Stop trusting** button, which tests again without it. From then on:

- the check passes while the server presents exactly that certificate, whatever
  the host name in it and whoever signed it;
- `ssl_warn_days` still applies, so a pinned certificate that is about to expire
  is reported as expiring, and one that has expired fails;
- a different certificate fails the check with `certificate changed since it
  was trusted on 2026-10-11` and its new fingerprint. A reinstall or an update
  that generates a new self-signed certificate does this too: the failed check
  offers the new certificate, to be trusted in turn.

Over the API, a failed preview (`POST /api/v1/monitors/preview`) or manual
check (`POST /api/v1/monitors/{id}/check`) carries the same facts as
`certificate`, and the monitor's `trusted_cert_sha256` takes its `sha256`. The
pin is accepted on an `ssl` monitor or an `http` monitor of an `https` address.
Colons and case are optional, and `""` removes it:

```sh
curl -X PATCH http://localhost:8080/api/v1/monitors/3 \
  -H 'Content-Type: application/json' \
  -d '{"trusted_cert_sha256":"3F:9A:…:C2"}'
```

The pin covers the monitored host only. A redirect to another host is verified
the ordinary way, so trusting a router's certificate does not also trust
whatever it redirects to. A certificate that fails for another reason — a
publicly trusted one for a different host name, or a chain the server sent
without its intermediate — is not offered: every other client trips over those
too, and a pin would only hide them.

**Trust a CA for the whole instance.** A private CA that issues certificates for
a day at a time (smallstep's default) cannot be pinned one certificate at a
time. Point `--extra-ca-file` (`SUBGLANCE_EXTRA_CA_FILE`) at a PEM file holding
the CA certificate, or several; checks trust them beside the system trust store,
so public certificates keep passing. The file is read at start, and a file that
cannot be read or holds no certificate stops the start rather than leaving
every check behind that CA to fail. With the official image, mount it read-only:

```sh
docker run -v ./my-ca.pem:/etc/subglance/ca.pem:ro \
  -e SUBGLANCE_EXTRA_CA_FILE=/etc/subglance/ca.pem …
```

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
be looked up afterwards — copy it now, or replace it later (see below), which
stops the old one working. Then, at the end of the job:

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

A job that runs at a fixed *local* time keeps its local time, not its distance
in hours. On the night the clocks go back at the end of daylight saving time, a
job scheduled for 06:00 runs 25 hours after the previous run (169 for a weekly
job), and the night they go forward, 23 (167). Counted in elapsed time, a daily
job with 60 seconds of grace would go down at about 05:01 that October night and
recover when it reports at 06:00. It happens to every job on a local-time
schedule (cron on a host with a local `TZ`, the Synology, Unraid or Windows task
scheduler), on the last Sunday of October in the EU and the first Sunday of
November in the US.

So tell the monitor which clock the job runs on. With `push_timezone` set to an
IANA zone, an interval of whole days is counted on that clock: the deadline is
the same local time one day (or seven, or however many) after the last report,
plus grace.

```bash
curl -b jar -X POST http://localhost:8080/api/v1/monitors \
  -H 'Content-Type: application/json' \
  -d '{"name":"Nightly backup","type":"push","push_interval_s":86400,"push_grace_s":300,"push_timezone":"Europe/Amsterdam"}'
```

In the form it is *Runs on the clock of*, shown once the interval is in days.
The zone only applies to whole days, and an interval of hours with a zone is
refused: an hourly job keeps no time of day, so elapsed time is already right
for it. A job scheduled in UTC needs no zone either. Two readings a clock skips
or repeats are settled on the late side: a job at 02:30 is on time at either
02:30 the night the clocks go back, and is due at 03:30 the night 02:30 does not
happen.

Two kinds of silence are not held against a job, because SubGlance could not
have heard it:

- **While the monitor was paused.** A report for a paused monitor is accepted
  and not recorded. When you resume it, the window starts again at the resume:
  the job gets one full window (interval plus grace) to report before it counts
  as late.
- **While SubGlance itself was not running.** A job that reported during an
  upgrade or a host reboot got a refused connection. If its deadline fell inside
  that gap, the window starts again when SubGlance starts. A deadline that falls
  after the start is left alone, so a short restart moves nothing, and an
  instance that restarts every day still reports a daily job that has stopped.

Until the job reports, the monitor shows as waiting, with the reason on its
page, rather than as up on the strength of a report from before the gap. The
cost is deliberate: a job that genuinely stopped during the pause is reported
one window later than it otherwise would be.

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

If a push URL leaks (a crontab committed to git, a shared script, a colleague
who left), or it was lost before anyone saved it, replace it rather than
deleting the monitor. On the monitor's page, **More actions → New push URL**
shows a new one, once, like the first; over the API it is
`POST /api/v1/monitors/{id}/push-token`. The old URL stops working at once and
gets the same 404 as one that never existed, so update every job that uses it.
The monitor keeps its heartbeats, incidents and uptime, and its deadline stays
where it was: a new URL is not a report, so a job that is overdue stays overdue
until it reports on the new one.

With [`--base-url`](channels.md#links-back-to-subglance) set, the push URL is
built on that address, path prefix included, because behind a reverse proxy
the request that creates the monitor carries the proxy's upstream address and
not the one a job can reach. Without it, the push URL uses the host and scheme
you reached the API on, so on a plain-HTTP instance it is an `http://` URL — a
bearer credential in cleartext. That is fine on a host or a LAN you trust, and
it is why HTTP is not refused. It is not fine across the internet: anyone on the
path can read the URL and then report a job as healthy forever. If a job pings
from outside the network the instance lives on, put the instance behind TLS
first.

## How a failure becomes an alert

A monitor does not go down because one check failed. Each monitor has a failure
threshold (`retries`, default 2), and the state engine walks it through the
states below (`pending` means no check result yet; paused monitors are not
measured):

| State | Meaning | Alerts? |
|---|---|---|
| `up` | Last check passed | — |
| `warning` | Failing, threshold not yet reached | No |
| `down` | Threshold reached, incident confirmed | Yes, once |
| `recovering` | Confirmed incident, passing checks counting towards the recovery threshold | No |
| `up` again | Recovered | Only if it was confirmed |

Recovery follows the same rule in the other direction. A confirmed incident
closes only after a streak of passing checks: the recovery threshold, stored per
monitor as `recovery_threshold` (default 2, range 1 to 10). A single pass in the
middle of an outage is not a recovery, so a service that fails, passes once and
fails again produces one alert and no false "resolved". A failure during that
streak starts it over; it is the same incident, and nobody is alerted again.
The incident's resolution time is the **first** pass of the streak that closed
it, so the confirmation checks are not counted as downtime. The cost is that
the "resolved" message arrives `recovery_threshold` − 1 check intervals after
the first pass: one interval later at the default of 2, nine at 10. A threshold
of 1 is the old behaviour: the first pass closes the incident.

The streak is not kept across a restart. A monitor that was part-way through
recovering starts counting again, so a restart can delay a "resolved" but never
send one early. Push monitors always recover on one report: a job saying it
succeeded is not a sample. Reminders stop while a streak is in progress,
because "still down" would no longer be true.

An unconfirmed incident (`warning`) still closes on the first pass. Nobody was
told about it, so there is no false all-clear to prevent.

An incident record is opened on the **first** failure, so its start time is when
the outage actually began — not when the system became sure of it. The gap
between `started_at` and `confirmed_at` is the confirmation delay, and it is
visible in the API.

Only confirmed downtime affects uptime: successful assessed checks divided by
successful plus confirmed-down checks. Warning and unclassified legacy samples
are excluded; a window with no eligible checks has unknown uptime. See the
[uptime policy](../README.md#warning-and-uptime) for rollups and sampling details.
A failing monitor is rechecked every `min(interval, 60 s)`, or slower where
one of the floors below applies, from its first failed check until the
incident closes: through `warning`, `down` and
`recovering`, with the existing jitter and worker limits. The threshold is
evidence that a failure is real, not a wait, so the time from the first failed
check to the alert is

```text
max(retries − 1, 0) × recheck
```

where `recheck` is `min(interval, 60 s)` unless a floor below raises it, plus
up to 10% jitter per recheck: two minutes for a five-minute monitor with
`retries: 3`, 40 seconds for a 20-second one, and immediately at `retries` 0
or 1. A pass during `warning` closes the unconfirmed incident and puts the
monitor back on its interval, counted from that pass. The passing checks that
close a confirmed incident come at the same recheck pace.

Two things slow the recheck down. A monitor whose timeout is over 48 seconds
is rechecked no sooner than 1.25 times its timeout, so the next check never
comes due while a hanging one may still be running. A `domain` monitor keeps
its six-hour floor: a registry does not answer faster for an expired domain.
Push monitors are not rechecked at all; they confirm on the first missed
report.

A monitor's timeout may be at most 80% of its interval: 48 seconds at the
default interval of 60, 16 at the shortest of 20. A monitor is never checked
twice at once, so a check that could hang for longer than the gap before the
next one would make the scheduler skip that next one. Creating, editing or
importing a monitor refuses the pair otherwise, naming `timeout_s`, or
`interval_s` when only the interval changed. A monitor stored before this rule
keeps its values until one of the two is edited, and is checked every 1.25
times its timeout in the meantime.

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

Use **Repeat alerts** in the create form or the edit drawer, under **Alerts**.
Choose **Do not repeat**, or set **First repeat after** to anything from
1 minute to 24 hours, in seconds, minutes or hours, as long as it comes to a
whole number of seconds. The form rejects a shorter delay before sending a
request: under one minute, reminders would become too frequent. The default is
15 minutes. This is an escalating base, not a fixed repeat interval.

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
GET    /api/v1/incidents/{id}/deliveries   where its alerts went, per channel

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

To move monitors and channels between instances or keep them in a repository,
use [configuration files](configuration-files.md) rather than scripting these
calls.

That listing is the monitor and incident surface only. The complete API —
authentication, users, API tokens, notification channels, uptime windows and
the live event stream — is specified in [`openapi.yaml`](openapi.yaml),
an OpenAPI 3.1 document you can feed to a client generator or an editor such as
Swagger UI. It is checked against the server's own route table on every test
run, so a route cannot be added, removed or change privilege level without the
specification following it.

## Scheduled maintenance

Use **Monitors → Scheduled maintenance** to manage one-off or weekly windows
for one monitor or an exact tag group. Checks continue while alerts and uptime
contributions are suppressed. See [maintenance](maintenance.md) for details.
