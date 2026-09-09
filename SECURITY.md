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
- Notification channel configuration is encrypted at rest.
- The HTTP server runs with bounded read, write and idle timeouts.
- A panic in a request handler is recovered rather than taking down the process —
  a monitoring tool that dies on one bad request is worse than useless.
