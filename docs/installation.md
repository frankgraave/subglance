# Installing SubGlance

Four ways to run SubGlance, in rough order of how quickly they get you a
dashboard. All of them produce the same thing: one process, one SQLite
database, nothing to serve alongside it.

- [With Docker Compose](#with-docker-compose)
- [With Docker](#with-docker)
- [With a downloaded binary](#with-a-downloaded-binary)
- [Building from source](#building-from-source)
- [Tests](#tests)

## With Docker Compose

Copy [`docker-compose.yml`](../docker-compose.yml) out of this repository and
run:

```sh
docker compose up -d
```

That is the whole installation. The file needs no edits to work: it publishes
8080, keeps the database in a named volume, and every option in it is commented
out with the default it would override. `internal/config` has a test that fails
if an option is added to the binary without reaching that file, or named there
without the binary reading it.

To follow the logs or stop it again:

```sh
docker compose logs -f
docker compose down          # add -v to delete the database too
```

## With Docker

Compose is only a wrapper here; a single `docker run` is equivalent:

```sh
docker run -d -p 8080:8080 -v subglance:/data ghcr.io/frankgraave/subglance:edge
```

That is the whole installation. Open <http://localhost:8080/> and the first
screen asks you to create an administrator; the database is SQLite inside the
volume, so there is nothing else to run alongside it.

The image tags track branches rather than releases. `:edge` and `:develop` both
follow the head of `develop` and will change under you; a short-SHA tag is
published alongside them for pinning an exact build. Released versions are
published as archives on the [releases page](https://github.com/frankgraave/subglance/releases)
rather than as image tags, so pin the SHA if you need a fixed container.

The image is `linux/amd64` and `linux/arm64`, built from
[distroless static](https://github.com/GoogleContainerTools/distroless): no
shell, no package manager, and the process runs as the unprivileged user
`65532:65532`. It measured **23.6MB uncompressed** on amd64 when it was first
published, and CI fails the build if that ever passes 30MB.

Flags go after the image name, because the entrypoint is the binary itself:

```sh
docker run -d -p 9000:9000 -v subglance:/data \
  -e SUBGLANCE_ADDR=:9000 \
  ghcr.io/frankgraave/subglance:edge --log-level debug
```

> [!IMPORTANT]
> The listen address is the one setting to pass as an environment variable
> rather than a flag. Docker runs the image's `HEALTHCHECK` as a separate
> process that inherits the environment but not the entrypoint's flags, so
> `--addr :9000` would move the server while the healthcheck kept probing
> `:8080` and reported the container unhealthy. `SUBGLANCE_ADDR` reaches both.

One consequence of the unprivileged user is worth knowing about: **ping checks
need the container to allow unprivileged ICMP.** Measured on Docker 29.1.3, a
container gets `net.ipv4.ping_group_range = 0 2147483647` by default and ping
works as `65532` with no extra flags — but that default belongs to the runtime,
not to this image, and some Kubernetes and Podman setups are stricter. If a ping
monitor reports a permission error, the binary names both fixes; the narrower
one is:

```sh
docker run -d -p 8080:8080 -v subglance:/data \
  --sysctl net.ipv4.ping_group_range="0 2147483647" \
  ghcr.io/frankgraave/subglance:edge
```

`--cap-add=NET_RAW` works too, by making the raw socket available instead. HTTP,
TCP and SSL checks are unaffected either way.

To build the image yourself, `docker build -t subglance .` — the Dockerfile
builds the dashboard and the binary from source, so Go and Node are only needed
inside the build.

## With a downloaded binary

Every tagged release publishes one archive per platform on the
[releases page](https://github.com/frankgraave/subglance/releases): Linux and
macOS on both amd64 and arm64, and Windows on amd64. The archive contains a
single executable with the dashboard already inside it, so there is nothing to
install and nothing to serve alongside it.

```sh
# replace VERSION and the platform with the ones you want
curl -fsSLO https://github.com/frankgraave/subglance/releases/download/vVERSION/subglance_VERSION_linux_amd64.tar.gz
curl -fsSLO https://github.com/frankgraave/subglance/releases/download/vVERSION/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing   # macOS: shasum -a 256 -c SHA256SUMS --ignore-missing

tar xzf subglance_VERSION_linux_amd64.tar.gz
./subglance --data-dir ./data
```

On Windows, PowerShell prints the hash and you compare it against the line for
your archive in `SHA256SUMS` — there is no `-c` equivalent that checks the file
for you:

```powershell
(Get-FileHash subglance_VERSION_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
Select-String subglance_VERSION_windows_amd64.zip SHA256SUMS
```

The checksum file is itself signed with [cosign](https://docs.sigstore.dev/),
keylessly, against the release workflow's own identity — so the signature can be
checked without trusting a key I could lose:

```sh
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github\.com/frankgraave/subglance/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

> [!NOTE]
> The newest published version is `v0.1.0-rc3`, a release candidate. It is a
> real, signed build with archives for all five platforms — but a candidate,
> not v0.1: pin it deliberately rather than treating it as stable.

## Building from source

Requires Go 1.26 or newer. Node 24 is needed only if you want the dashboard.

```sh
git clone https://github.com/frankgraave/subglance.git
cd subglance
make build          # produces ./bin/subglance
make run            # runs it locally against ./tmp
make check          # format, vet and test — run this before committing
```

Run `make help` to see every target.

### The dashboard

The dashboard is a single-page app in `web/`. It is compiled into the binary
with `go:embed`, so a release is still one file with nothing to serve
alongside it:

```sh
make web-install    # npm ci, once
make dist           # build the dashboard, then a binary that contains it
./bin/subglance     # the UI is on / and the API on /api/v1
```

`make build` on its own does not need Node and does not need the dashboard: a
binary without one serves the full API and says so at startup, which is the
normal state while working on the Go side. Building the frontend writes into
`internal/webui/dist`, which is gitignored apart from a `.gitkeep` — that file
is what keeps `go build ./...` working on a fresh clone.

## Tests

```sh
make check         # format, vet and the hermetic suite — run before committing
make test          # the same suite with the race detector
go test ./...      # everything, including tests that need the network
```

The suite is split in two. Most tests are hermetic: they parse strings or talk
to a loopback server the test starts itself, and they run everywhere. A few need
a real resolver, an outbound socket or an ICMP-capable kernel; those skip under
`go test -short`, which is what CI runs on every push.

That split is deliberate. A CI runner's network is not the internet — DNS may be
filtered, ICMP is usually blocked, and a third-party host having a bad morning
must never turn this repository red. The network-dependent tests still run daily
in a separate workflow, where a failure means "go look at it" rather than "your
pull request is broken".

Set `SUBGLANCE_TEST_NETWORK=1` to force them on locally.
