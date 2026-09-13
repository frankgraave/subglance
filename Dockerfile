# Multi-stage build for the SubGlance container image.
#
# Three stages, and the split is deliberate:
#
#   web   - builds the dashboard with Node. Nothing from this stage except
#           the built assets reaches the final image, so the hundreds of
#           megabytes of node_modules never cost a byte at runtime.
#   go    - compiles the static binary with that dashboard embedded.
#   final - distroless static, which is a base layer and nothing else: no
#           shell, no package manager, no libc.
#
# The base is `static` rather than `base`: the binary is pure Go with
# CGO_ENABLED=0 (modernc's SQLite driver exists precisely so no C toolchain
# is needed), so there is nothing to dynamically link against. `static` still
# carries the two things a network service cannot do without, CA certificates
# for TLS and tzdata, while staying around 2MB.
#
# Build for this machine:
#   docker build -t subglance .
#
# Build and push both architectures (needs buildx and a registry):
#   docker buildx build --platform linux/amd64,linux/arm64 -t <ref> --push .

# --- Stage 1: the dashboard ---------------------------------------------
#
# Pinned to the Node major the CI frontend job uses. Alpine because this
# stage is thrown away and its size only affects build time.
#
# --platform=$BUILDPLATFORM: this stage emits platform-independent
# JavaScript, so building it once natively beats building it twice, one of
# them under QEMU emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web

WORKDIR /src/web

# Manifests alone first, so the dependency layer stays cached until a
# dependency actually changes. `npm ci` from the lockfile, never `npm
# install`: a container build must not be free to resolve a different tree
# than CI did.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
# The token guard tests read docs/DESIGN.md off disk, and the build runs
# `tsc -b` over the same tree. Keep the document available so a drift fails
# here rather than in CI.
COPY docs/DESIGN.md /src/docs/DESIGN.md
RUN npm run build

# --- Stage 2: the binary -------------------------------------------------
#
# Also native, cross-compiling to the target: Go does that for free and it is
# roughly an order of magnitude faster than emulating arm64.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go

WORKDIR /src

# Same caching logic as the Node stage: modules before sources.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# vite writes into internal/webui/dist, which is exactly where the
# //go:embed directive looks. This copy has to happen before the compile and
# has to overwrite whatever the build context carried in.
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=docker
ARG COMMIT=unknown
ARG DATE=unknown

# -trimpath and -ldflags "-s -w" are what make the binary small enough to
# claim a size on the landing page: they drop absolute build paths, the
# symbol table and the DWARF debug info, together about a third of the file.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/frankgraave/subglance/internal/buildinfo.Version=${VERSION} \
        -X github.com/frankgraave/subglance/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/frankgraave/subglance/internal/buildinfo.Date=${DATE}" \
      -o /out/subglance ./cmd/subglance

# The image has no shell, so it cannot mkdir its own data directory at
# startup. Create it here, in a stage that does have one, and copy it over
# with the right ownership.
RUN mkdir -p /out/data

# --- Stage 3: what ships -------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=docker
ARG COMMIT=unknown
ARG DATE=unknown
LABEL org.opencontainers.image.title="SubGlance" \
      org.opencontainers.image.description="Self-hosted uptime and API monitoring dashboard" \
      org.opencontainers.image.source="https://github.com/frankgraave/subglance" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

COPY --from=go /out/subglance /usr/local/bin/subglance

# /data is the default --data-dir, so `-v subglance:/data` is the whole of
# persistence. The directory ships pre-created and owned by nonroot because
# a named volume inherits the ownership of the image path it covers; without
# it, the first start fails writing the database as an unprivileged user.
COPY --from=go --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]

# 65532:65532, the distroless nonroot user. Running a monitoring container
# as root buys nothing: the process binds an unprivileged port and writes
# only under /data. The one thing to know is that ICMP ping needs
# net.ipv4.ping_group_range to include this GID — Docker's own default
# already does, stricter runtimes may not. See the README.
USER nonroot:nonroot

EXPOSE 8080

# The image has no shell, no curl and no wget, so the usual
# `CMD curl -f http://localhost:8080/health` cannot run here. The binary can
# probe itself instead, and it is already in the image, so this costs no bytes.
#
# Exec form, because there is no shell to parse the string form. No --addr is
# passed: the subcommand reads the same configuration the server does, so
# `docker run <image> --addr :9000` moves the server and the healthcheck
# together. SUBGLANCE_ADDR works the same way.
#
# --start-period covers the first start, where the database is still being
# opened and migrated: failures during it do not count against --retries.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/usr/local/bin/subglance", "healthcheck"]

# ENTRYPOINT, not CMD, so `docker run <image> --addr :9000` appends a flag
# instead of replacing the command. The defaults already match the image
# layout (:8080, /data), so the documented run needs no flags at all.
ENTRYPOINT ["/usr/local/bin/subglance"]
