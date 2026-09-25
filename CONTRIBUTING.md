# Contributing to SubGlance

Thanks for your interest. Here is what you need to know before opening a pull
request.

> **Early development.** The architecture is still moving. For anything larger
> than a bug fix, open an issue first — it would be a shame to spend your evening
> on a change that collides with work already in progress.

## Contributor License Agreement (CLA)

SubGlance is released under the AGPL-3.0 and the copyright in the codebase is
held by a single party. That keeps the licence changeable later without having
to track down every past contributor for permission — a project that cannot
relicense is stuck with whatever it picked on day one.

So every contributor is asked to accept the [CLA](CLA.md). You keep the
copyright to your own work, but grant Frank Graave the right to also release
your contribution under other licence terms.

You accept it by confirming in your first pull request:

```
I have read the CLA and I agree.
```

Contributing on behalf of a company or other legal entity? Use the entity
wording from section 9 of the CLA, which names the entity and confirms you
may bind it.

If a later version of the CLA changes your rights or obligations, your next
pull request asks you to accept it by naming that version:

```
I have read version <version> of the CLA and I agree.
```

Without that confirmation a pull request cannot be merged.

## Getting set up

Requires Go 1.26 or newer. Node 24 is needed only for the dashboard in `web/`;
the Go side builds and tests without it.

```sh
git clone https://github.com/frankgraave/subglance.git
cd subglance
make check          # format, vet and test
make run            # runs locally on :8080 against ./tmp
```

Working on the dashboard as well:

```sh
make web-install    # npm ci, once
make dist           # build the dashboard, then a binary containing it
```

`make help` lists every target.

## Before you open a pull request

Run `make check`. It formats, vets and runs the full test suite with the race
detector. CI runs the same thing, so this saves you a round trip.

If you touched anything concurrent, run the tests a few times — race conditions
are shy.

### The badssl acceptance suite

The TLS diagnosis in `internal/checker` has a second suite behind the `badssl`
build tag. It runs the SSL and HTTP checkers against badssl.com's failure
subdomains and asserts, per endpoint, the `FailureKind` and the sentence the
user is shown. It is the ground truth the hermetic TLS tests are abstractions
of.

```bash
make test-badssl    # run it (needs outbound internet)
make vet-badssl     # compile it without running it
```

It is out of `make test` and out of CI on purpose: it depends on the public
internet and on the state of somebody else's certificates, and a third-party
host having a bad day must not turn this repository red.

Two things to know before changing it. It **fails rather than skips** when
badssl is unreachable — a network test that skips into green reports a pass for
a run that proved nothing, and nobody reads a skip. And many badssl subdomains
have themselves expired (`sha1-intermediate`, `no-common-name`, `superfish`,
`extended-validation`, `10000-sans`), so they fail on expiry rather than on the
thing they are named for; asserting on those would be asserting on badssl's
renewal schedule. Only endpoints that hold for their stated reason belong in
the table.

Because it is not compiled by the default build, run `make vet-badssl` after
touching anything in `internal/checker` — otherwise the tag rots quietly and
nobody finds out until the next person needs it.

## Code conventions

**Everything in this repository is written in English** — code, comments,
documentation, commit messages, branch names, test names, log lines and error
strings. Contributors should not need to know the maintainer's first language to
read the history.

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

Nothing is generated from these prefixes — release notes come from pull request
titles instead (see below), because a squashed pull request's own title is what
survives on `develop` and is what someone reads a year later. The prefixes are a
convention for reviewers, not an input to a tool, so a commit that reads better
as a sentence is not a problem.

## Pull requests

1. Open an issue first for anything substantial, so the approach can be agreed
   before you invest time.
2. Fork the repository and work on a separate branch.
3. Target `develop`, not `main`. `main` only receives releases, and `develop` is
   protected — every change goes through a pull request, all CI checks must pass,
   and review threads must be resolved before it can be merged.
4. Keep a pull request to one subject.
5. Describe what you are changing and why. The why is the part reviewers cannot
   reconstruct from the diff.
6. **If your pull request closes a gap listed in the README's "Where it stands"
   section, update that section in the same pull request.** Move the bullet from
   "Not working yet" to "Working", or delete it, in the diff that made it
   untrue. That section is the first thing a stranger reads to decide whether
   this is worth running, and nothing but this rule keeps it tied to the code.
7. **Write the title as the line you would want to read in a changelog.** A
   release's notes are generated from the titles of the pull requests it
   contains, so the title is not scaffolding that disappears on merge — it is
   the sentence a stranger reads months later. A label (`feature`, `bug`,
   `security`, `documentation`) sorts it into a section; without one it still
   appears, just under "Everything else".

   That fallback is deliberate, but it stops being a fallback once nothing is
   labelled: the first generated notes put *every* entry under "Everything
   else", which is a flat list with the structure switched off. The labels
   that select a section are `feature` or `enhancement` (Added), `bug` or
   `fix` (Fixed), `security` (Security), and `documentation`
   (Documentation) — any other label leaves the entry in "Everything else",
   and `dependencies` or `skip-changelog` keep it out of the notes
   altogether. Applying one of those six takes a few seconds, and it is the
   only thing standing between a release page and a wall of
   undifferentiated lines.

## Releases

A release is cut by pushing a tag, and by nothing else:

```sh
git tag v0.1.0
git push origin v0.1.0
```

That starts two independent workflows. `release.yml` builds the dashboard, then
five binaries — Linux and macOS on amd64 and arm64, Windows on amd64 — archives
them, writes `SHA256SUMS`, signs that file with cosign, and publishes the lot as
a GitHub release. `docker.yml` separately pushes the container image for the same
tag. They are deliberately not one workflow: a registry failure should not cost
the binaries, and either can be re-run alone.

The build configuration lives in [`.goreleaser.yaml`](.goreleaser.yaml), and it
is testable without spending a version number:

```sh
make release-check      # validate the configuration
make release-local      # build for this machine only — fast, use while editing
make release-snapshot   # the full five-platform run into dist/, publishing nothing
```

`make release-snapshot` needs Node, because the dashboard is compiled into every
binary. Running the `Release` workflow manually from the Actions tab does the
same thing on a runner and leaves the archives as a downloadable artefact.

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
- [`docs/DESIGN.md`](docs/DESIGN.md) — design system: tokens, the LED, layouts,
  components, and the reasoning behind them. Read this before touching UI.

Working mockups live in [`docs/mockups/`](docs/mockups/) — open `index.html` in
a browser. No build step, no dependencies. They are the reference implementation
of `DESIGN.md`: if the two disagree, `DESIGN.md` wins.
