# Contributing to SubGlance

Thanks for your interest. Here is what you need to know before opening a pull
request.

> **Early development.** The architecture is still moving. For anything larger
> than a bug fix, open an issue first — it would be a shame to spend your evening
> on a change that collides with work already in progress.

## Contributor License Agreement (CLA)

SubGlance is released under the AGPL-3.0, and the copyright in the codebase is
held by a single party so that the project can also be licensed commercially to
people for whom the AGPL does not work.

So every contributor is asked to accept the [CLA](CLA.md). You keep the copyright
to your own work, but grant Frank Graave the right to also release your
contribution under other licenses, including commercial ones.

You accept it by confirming in your first pull request:

```
I have read the CLA and I agree.
```

Without that confirmation a pull request cannot be merged.

## Getting set up

Requires Go 1.25 or newer.

```sh
git clone https://github.com/frankgraave/subglance.git
cd subglance
make check          # format, vet and test
make run            # runs locally on :8080 against ./tmp
```

`make help` lists every target.

## Before you open a pull request

Run `make check`. It formats, vets and runs the full test suite with the race
detector. CI runs the same thing, so this saves you a round trip.

If you touched anything concurrent, run the tests a few times — race conditions
are shy.

## Code conventions

**Go.** Standard library first. Every dependency is a thing that can break, needs
updating, and adds to the binary. There is a size target to defend, so a new
dependency needs a reason beyond convenience.

**Comments explain why, not what.** The code already says what it does. A comment
earns its place by explaining a decision, a constraint, or a trap — the thing the
next reader would otherwise have to rediscover.

**Errors get context.** `fmt.Errorf("open database: %w", err)` rather than
returning a bare `err`. Someone will read this in a log at 3am.

**Tests describe the failure.** `t.Errorf("status = %d, want 200", got)` beats
`t.Error("failed")`. When a test breaks in CI a year from now, the message is all
you have.

**Prefer constraints in the schema** over checks in application code where the
database can enforce them. A `CHECK` constraint cannot be forgotten by a future
code path.

## Commits

Conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`,
`chore:`. Scope optional: `feat(store): ...`.

Write the subject in the imperative: "add SSL expiry check", not "added" or
"adds".

## Pull requests

1. Open an issue first for anything substantial, so the approach can be agreed
   before you invest time.
2. Fork the repository and work on a separate branch.
3. Keep a pull request to one subject.
4. Describe what you are changing and why. The why is the part reviewers cannot
   reconstruct from the diff.

## Reporting bugs

Open an issue with:

- what you expected to happen;
- what happened instead;
- the steps to reproduce it;
- your version and environment (`subglance --version`, OS, Docker or binary).

Logs help. `--log-level debug` helps more.

## Security issues

Do **not** report vulnerabilities through a public issue. See
[SECURITY.md](SECURITY.md).

## Project documentation

Design decisions live in `docs/`:

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — stack, components, data model,
  API design

Both are currently written in Dutch, as internal working documents. If you need
them in English to contribute, say so in an issue and they will be translated.
