BINARY      := subglance
CMD         := ./cmd/subglance
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG         := github.com/frankgraave/subglance/internal/buildinfo
LDFLAGS     := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

GO          ?= go
GOBIN       := $(shell $(GO) env GOPATH)/bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: dist
dist: web-build build ## Build the dashboard and then a binary with it embedded

.PHONY: build
build: ## Build the binary into ./bin (embeds the dashboard if it was built)
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)
	@echo "built bin/$(BINARY) ($$(du -h bin/$(BINARY) | cut -f1))"

.PHONY: run
run: ## Run locally with a ./tmp data dir
	@mkdir -p tmp
	$(GO) run $(CMD) --data-dir ./tmp --log-level debug

.PHONY: test
test: ## Run the hermetic suite with the race detector (what CI runs)
	$(GO) test -race -short -count=1 ./...

.PHONY: test-network
test-network: ## Run everything, including tests that need real network access
	SUBGLANCE_TEST_NETWORK=1 $(GO) test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and open a coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (installs it if missing)
	@command -v $(GOBIN)/golangci-lint >/dev/null 2>&1 || \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
	$(GOBIN)/golangci-lint run

.PHONY: fmt
fmt: ## Format all Go code
	$(GO) fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: check
check: fmt vet test ## Format, vet and test — run before every commit

# --- Frontend -----------------------------------------------------------
# The dashboard lives in web/ and is built separately from the Go binary.
# `npm ci` needs a lockfile, so these targets assume `web/node_modules` is
# either present or installable; CI runs them in their own job.

.PHONY: web-install
web-install: ## Install frontend dependencies (npm ci)
	cd web && npm ci

.PHONY: web-build
web-build: ## Build the dashboard into internal/webui/dist (embedded by `make build`)
	@rm -rf internal/webui/dist/assets
	cd web && npm run build

.PHONY: web-test
web-test: ## Run the frontend suite (token and theme guards)
	cd web && npm test

.PHONY: web-lint
web-lint: ## Lint the frontend
	cd web && npm run lint

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin tmp coverage.out internal/webui/dist/assets internal/webui/dist/index.html
