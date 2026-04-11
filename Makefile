SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
MAKEFLAGS += --no-builtin-rules --warn-undefined-variables

-include mk/infra.mk

# ---- Phony ----
.PHONY: all help infra-init prepare fmt fmt-go fmt-check lint lint-go vet quality \
	test test-go test-py coverage coverage-threshold coverage-profile coverage-html \
	build build-relay verify manifest-src \
	tdd cache-populate mq-publish verify-auto verify-debug \
	manifest-verify manifest-update verify-full clean shell \
	symbols check-symbols test-api deps \
	dev-vault dev-vault-stop \
	release release-relay release-verify sourcehash validate \
	up down repo.init typecheck check build-crt-parser build-canonicalize fetch-pki \
	infra-down infra.help

# Default to showing help
all: help

# =============================================================================
# Variables — Go runtime
# =============================================================================
MAKE := make
APP_NAME ?= cic-relay
COMMIT   ?= $(shell git rev-parse --short HEAD)
BUILD_DIR ?= ./output/$(COMMIT)

COVERAGE_FILE ?= /output/$(COMMIT)/coverage.out
COVERAGE_HTML ?= /output/$(COMMIT)/coverage.html

# BUILDER_IMAGE_DIGEST: pinned golang builder image digest from docker-compose.yaml.
# Re-pin: grep -m1 'sha256:' docker-compose.yaml | grep -o 'sha256:[a-f0-9]*'
BUILDER_IMAGE_DIGEST ?= sha256:d6d07e5326853f71a30decb4d777467428e85c087433b3d76c8b74933abd48b1

# RELEASE_LD_FLAGS: timestamp-free — produces byte-identical binaries from the same source.
# SourceTreeHash is injected inline in release/build recipes via:
#   SOURCE_HASH=$$(go run ./tools/sourcehash .) && ... -X main.SourceTreeHash=$$SOURCE_HASH
RELEASE_LD_FLAGS ?= -X main.BuildID=$(VERSION)-$(COMMIT) \
                    -X main.CommitHash=$(COMMIT) \
                    -X main.BuilderImageDigest=$(BUILDER_IMAGE_DIGEST)
# LD_FLAGS: includes Timestamp for dev/debug builds (not used in `make release-relay`).
LD_FLAGS ?= $(RELEASE_LD_FLAGS) \
            -X main.Timestamp=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GOFLAGS  ?= -mod=readonly -trimpath
GCFLAGS  ?= all=-dwarf=false
LDFLAGS  ?= $(LD_FLAGS) -s -w

# Kapcsolható race detektor: dev/CI ON, release OFF (RACE=0)
RACE ?= 1
ifeq ($(RACE),1)
  GO_RACE := -race
else
  GO_RACE :=
endif

# =============================================================================
# Variables — Python compiler
# =============================================================================
VERSION  ?=
VERBOSE  ?=
DEBUG    ?=
DRY_RUN  ?=
GIT_TIMEOUT   ?= 60
VAULT_TIMEOUT ?= 10
TEST_FILE ?=
TEST_NAME ?=

COMPILER_CLI_ARGS =
ifeq ($(VERBOSE),1)
    COMPILER_CLI_ARGS += --verbose
endif
ifeq ($(DEBUG),1)
    COMPILER_CLI_ARGS += --debug
endif
ifeq ($(DRY_RUN),1)
    COMPILER_CLI_ARGS += --dry-run
endif
COMPILER_CLI_ARGS += --git-timeout $(GIT_TIMEOUT)
COMPILER_CLI_ARGS += --vault-timeout $(VAULT_TIMEOUT)

PYTEST_ARGS =
ifeq ($(TEST_FILE),)
    PYTEST_ARGS += tests/
else
    PYTEST_ARGS += $(TEST_FILE)
endif
ifneq ($(TEST_NAME),)
    PYTEST_ARGS += -k "$(TEST_NAME)"
endif

# =============================================================================
# Macros — Go container exec
# =============================================================================
define GO_EXEC
	docker compose exec -T builder sh -eu -o pipefail -c 'cd /git-source && $(1)'
endef
define GO_FIXER
	docker compose exec -T fixer sh -eu -o pipefail -c 'cd /git-source && $(1)'
endef

# =============================================================================
# Container Lifecycle
# =============================================================================
up: infra.up ## Start the development environment
down: infra.down ## Stop and remove the development environment
build: infra.build ## Build Docker images

shell: ## Open an interactive shell into the running builder container
	docker compose exec builder bash

# =============================================================================
# Help
# =============================================================================
help: ## Show available make targets
	@echo "Usage: make [target] [OPTIONS]"
	@echo ""
	@echo "Development Environment:"
	@echo "  up                  Start the development environment."
	@echo "  down                Stop and remove the development environment."
	@echo "  shell               Open an interactive shell into the running container."
	@echo "  build               Build Docker images."
	@echo ""
	@echo "Python Compiler Tasks:"
	@echo "  validate            Run fast, offline validation of all schemas."
	@echo "  release             Sign and release all non-dev schemas (requires Vault + VERSION=X.Y.Z)."
	@echo "  test                Run all tests (Go + Python)."
	@echo "  test-py             Run Python (pytest) tests only."
	@echo ""
	@echo "Go Relay Build:"
	@echo "  test-go             Run Go unit tests."
	@echo "  build-relay         Build relay binary (with quality gates)."
	@echo "  release-relay       Build reproducible static binary + sha256 + SBOM into dist/."
	@echo "  release-verify      Verify dist/relay checksum."
	@echo "  sourcehash          Print deterministic source tree hash."
	@echo ""
	@echo "Code Quality:"
	@echo "  fmt                 Format all code (Go + Python)."
	@echo "  fmt-check           Fail if Go formatting differs."
	@echo "  lint                Lint all code (Go staticcheck + Python ruff + yamllint)."
	@echo "  vet                 Run go vet."
	@echo "  typecheck           Run static type checking (Python MyPy)."
	@echo "  check               Run all code quality checks."
	@echo "  vuln                Run Go vulnerability scan."
	@echo "  quality             Go quality gate (fmt-check + lint-go + vet + vuln)."
	@echo ""
	@echo "Coverage:"
	@echo "  coverage            Run tests with coverage (profile + HTML)."
	@echo "  coverage-threshold  Fail if coverage < COVERAGE_MIN%."
	@echo ""
	@echo "Manifest:"
	@echo "  manifest-verify     Verify the integrity of the repository."
	@echo "  manifest-update     Re-generate the MANIFEST.sha256 file."
	@echo ""
	@echo "Options for validate/release:"
	@echo "  VERBOSE=1           Enable verbose output."
	@echo "  DEBUG=1             Enable debug output (most verbose)."
	@echo "  DRY_RUN=1           Perform a trial run without making any changes."
	@echo "  VERSION=X.Y.Z       Semantic version (required for 'release')."
	@echo "  GIT_TIMEOUT=N       Git command timeout in seconds (default: 60)."
	@echo "  VAULT_TIMEOUT=N     Vault API call timeout in seconds (default: 10)."
	@echo ""
	@echo "Options for test:"
	@echo "  TEST_FILE=path/to/file.py  Specify a single test file."
	@echo "  TEST_NAME=test_function    Specify a single test function."
	@$(MAKE) infra.help

# =============================================================================
# Infra / Prepare (Go build dirs)
# =============================================================================
infra-init: ## Initialize dockerized infra and dirs
	@mkdir -p output tmp/{build,cache,gomodcache,bin}
	@{ command -v sudo >/dev/null 2>&1 && sudo chown -R $(shell id -u):$(shell id -g) ./output; } || true
	docker compose up -d

prepare: infra-init ## Prepare local/dev environment
	@mkdir -p $(BUILD_DIR)
	@sudo chown -R $(shell id -u):$(shell id -g) ./output

infra-down: ## Stop and clean infra
	docker compose down

# Stop output for this commit
clean: ## Remove build output for current commit
	rm -rf $(BUILD_DIR)
	@echo "Cleaned build output for commit $(COMMIT)"

# =============================================================================
# Dependency Management
# =============================================================================
deps: ## Tidy go module files
	@echo "Tidying go module files..."
	@$(call GO_FIXER, go mod tidy)

# =============================================================================
# Python Compiler Tasks
# =============================================================================
validate: ## Run fast, offline validation of all schemas
	@echo "--- Validating all schemas against the meta-schema ---"
	@docker compose exec builder python -m tools.compiler validate $(COMPILER_CLI_ARGS)

release: ## Build, checksum, and sign all non-dev schemas (requires Vault + VERSION=X.Y.Z)
ifeq ($(VERSION),)
	$(error VERSION is required for the release command. Usage: make release VERSION=1.0.0)
endif
	@echo "--- Building and signing release schemas ---"
	@docker compose exec \
		-e GIT_AUTHOR_NAME="$(shell git config user.name)" \
		-e GIT_AUTHOR_EMAIL="$(shell git config user.email)" \
		-e GIT_COMMITTER_NAME="$(shell git config user.name)" \
		-e GIT_COMMITTER_EMAIL="$(shell git config user.email)" \
		builder python -m tools.compiler release --version $(VERSION) $(COMPILER_CLI_ARGS)

# =============================================================================
# Symbol generation
# =============================================================================
symbols: ## Generate symbols documentation
	@echo "Generating symbols..."
	@$(call GO_FIXER, go run ./tools/symbolsgen)

check-symbols: ## Check if symbols documentation is up-to-date
	@echo "Checking for symbol drift..."
	@$(call GO_FIXER, go run ./tools/symbolsgen && (git diff --exit-code context/SYMBOLS.md))

# =============================================================================
# Code Quality — Go
# =============================================================================
fmt-go: ## Apply gofmt -s (and goimports if available)
	$(call GO_FIXER, git config --global --add safe.directory /git-source && \
		git ls-files -z -- "*.go" | xargs -0 gofmt -s -w ; \
		if command -v goimports >/dev/null 2>&1; then \
			git ls-files -z -- "*.go" | xargs -0 goimports -w ; \
		fi )

fmt-check: ## Fail if Go formatting differs
	$(call GO_EXEC, M="$$(git ls-files -z -- "*.go" | xargs -0 gofmt -s -l)"; \
		test -z "$$M" || { printf "%s\n" "$$M"; echo "Code not formatted. Run make fmt"; exit 1; })

lint-go: ## Run Go static linters (staticcheck, ineffassign)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to lint."; \
			exit 0; \
		fi; \
		echo "Staticcheck on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" staticcheck $$PKGS \
	)

vet: ## Run go vet
	$(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to vet."; \
			exit 0; \
		fi; \
		echo "Vet on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" go vet $$PKGS \
	)

vuln: ## Run Go vulnerability scan (govulncheck)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to scan."; \
			exit 0; \
		fi; \
		echo "govulncheck on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" govulncheck $$PKGS \
	)

quality: fmt-check lint-go vet vuln ## Go quality gate: all checks must pass

# =============================================================================
# Code Quality — combined (Go + Python)
# =============================================================================
fmt: fmt-go infra.fmt ## Format all code (Go gofmt + Python black/isort)

lint: lint-go infra.lint ## Lint all code (Go staticcheck + Python ruff + yamllint)

typecheck: infra.typecheck ## Run static type checking (Python MyPy)

check: infra.check quality ## Run all code quality checks (Go + Python)

repo.init: infra.repo.init ## Set up Git hooks for this repository

# =============================================================================
# Tests
# =============================================================================
test-go: ## Run Go unit tests (verbose, race)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to test."; \
			exit 0; \
		fi; \
		echo "Test on: $$PKGS"; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" go test $(GO_RACE) -v $$PKGS \
	)

test-py: ## Run Python (pytest) tests
	@$(MAKE) infra.test

test: test-go test-py ## Run all tests (Go + Python)

test-api: ## Run E2E smoke tests against a running relay (RELAY_URL=http://localhost:8080)
	@echo "Running E2E smoke tests..."
	@bash ./api_tests/smoke.sh

# =============================================================================
# Coverage
# =============================================================================
coverage: coverage-profile coverage-html ## Run tests with coverage (profile + HTML)
	@echo "Coverage HTML: $(COVERAGE_HTML)"

coverage-profile: ## Run tests with coverage profile
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		PKGS="$$(go list ./... | grep -v /vendor/)"; \
		if [ -z "$$PKGS" ]; then \
			echo "No Go packages to test."; \
			exit 0; \
		fi; \
		mkdir -p $(BUILD_DIR) \
		&& GOFLAGS="$(GOFLAGS)" go test $(GO_RACE) -covermode=atomic -coverprofile=$(COVERAGE_FILE) $$PKGS \
	) && cp $(BUILD_DIR)/* ./CurrentTest/

coverage-html: ## Generate HTML coverage report
	$(call GO_EXEC, mkdir -p $(BUILD_DIR) \
		&& go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)) && cp $(BUILD_DIR)/* ./CurrentTest/

COVERAGE_MIN ?= 85

coverage-threshold: coverage ## Fail if coverage < $(COVERAGE_MIN)%
	mkdir -p $(BUILD_DIR) && docker compose exec -T builder sh -c 'cd /git-source && \
		go tool cover -func=$(COVERAGE_FILE) | \
		awk -v MIN=$(COVERAGE_MIN) '"'"'/^total:/ { gsub("%","",$$3); v=$$3+0 } END { if (v < MIN) { printf "Coverage below %d%% (got %.1f%%)\n", MIN, v; exit 1 } else { printf "Coverage OK: %.1f%% >= %d%%\n", v, MIN } }'"'"''

# Per-csomag küszöbök — igazítsd igény szerint:
# cabinet: 95, canonicalize: 85, certutils: 70, cmd/relay: 80, egyebek: 75
coverage-check-pkgs: ## Fail if per-package coverage < threshold
	mkdir -p $(BUILD_DIR) && docker compose exec builder sh -c 'cd /git-source && \
		set -e; \
		for p in $$(go list ./... | grep -v /vendor/); do \
		  if [ "$$p" = "centralrelay/core/nexus/types" ]; then continue; fi; \
		  GO111MODULE=on GOFLAGS="$(GOFLAGS)" go test $$p -coverprofile=/tmp/cover.out >/dev/null; \
		  pc=$$(go tool cover -func=/tmp/cover.out | awk '\''END{print $$3}'\'' | tr -d "%"); \
		  case "$$p" in \
		    *"/core/cabinet") min=95 ;; \
		    *"/tools/canonicalize") min=85 ;; \
		    *"/tools/certutils") min=70 ;; \
		    *"/cmd/relay") min=80 ;; \
		    *) min=75 ;; \
		  esac; \
		  echo "$$p: $$pc% (min $$min%)"; \
		  awk -v p=$$pc -v m=$$min '\''BEGIN{exit (p+0 < m+0)}'\''; \
		done'

# =============================================================================
# PKI asset fetch (build-time, requires local Vault)
# =============================================================================
# Vault KV mount and cert key — must match vault-sign-agent.sh KEY_NAME + --cic-dir load
VAULT_KV_MOUNT    ?= cic-my-sign-key
VAULT_SOURCE_CA   ?= CICSourceCA
PKI_CERT_FIELD    ?= bar
PKI_OUT           := embedded/pki/cic-source-ca.pem

fetch-pki: ## Fetch CICSourceCA cert from local Vault → embedded/pki/cic-source-ca.pem
	@echo "Fetching CICSourceCA from Vault ($(VAULT_ADDR))..."
	@test -n "$$VAULT_ADDR"  || { echo "VAULT_ADDR not set"; exit 1; }
	@test -n "$$VAULT_TOKEN" || \
		{ VAULT_TOKEN=$$(cat $$XDG_RUNTIME_DIR/vault/sign-token 2>/dev/null) && \
		  test -n "$$VAULT_TOKEN" || { echo "VAULT_TOKEN not set and sign-token not found"; exit 1; }; }
	@VAULT_TOKEN=$${VAULT_TOKEN:-$$(cat $$XDG_RUNTIME_DIR/vault/sign-token)} && \
	 VAULT_CACERT=$${VAULT_CACERT:-$$XDG_RUNTIME_DIR/vault/server.crt} && \
	 vault kv get \
		-address="$$VAULT_ADDR" \
		-tls-skip-verify \
		-mount=$(VAULT_KV_MOUNT) \
		-field=$(PKI_CERT_FIELD) \
		$(VAULT_SOURCE_CA) > $(PKI_OUT)
	@echo "CICSourceCA written to $(PKI_OUT)"

# =============================================================================
# Go Relay Build
# =============================================================================
build-relay: fetch-pki prepare quality test-go coverage coverage-check-pkgs ## Build relay binary with quality gates
	@echo "Building $(APP_NAME)..."
	docker compose exec builder sh -c ' cd /git-source \
		&& SOURCE_HASH=$$(go run ./tools/sourcehash .) \
		&& go build $(GO_RACE) $(GOFLAGS) -gcflags="$(GCFLAGS)" \
		   -ldflags "$(LDFLAGS) -X main.SourceTreeHash=$$SOURCE_HASH" \
		   -o /output/$(COMMIT)/$(APP_NAME) ./cmd/relay'
	@echo "Build complete at $(BUILD_DIR)/$(APP_NAME)"
	@$(MAKE) manifest-src
	@if [ -z "$(NO_PUBLISH)" ]; then $(MAKE) mq-publish; fi

# =============================================================================
# Release artifacts (Go relay binary)
# =============================================================================
CYCLONEDX_GOMOD_VERSION ?= v1.8.0

release-relay: ## Build reproducible static binary + sha256 + CycloneDX SBOM into dist/
	$(call GO_EXEC, \
		mkdir -p /git-source/dist && \
		SOURCE_HASH=$$(go run ./tools/sourcehash .) && \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
			-trimpath \
			-ldflags "$(RELEASE_LD_FLAGS) -X main.SourceTreeHash=$$SOURCE_HASH -s -w" \
			-o /git-source/dist/relay \
			./cmd/relay && \
		sha256sum /git-source/dist/relay > /git-source/dist/relay.sha256 && \
		GOFLAGS="" go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION) \
			mod -output /git-source/dist/sbom.json -json \
	)
	@echo "Release artifacts: dist/relay  dist/relay.sha256  dist/sbom.json"

release-verify: ## Verify dist/relay checksum matches dist/relay.sha256
	$(call GO_EXEC, cd /git-source && sha256sum -c dist/relay.sha256)
	@echo "Release checksum verified"

sourcehash: ## Print deterministic source tree hash (pkg/sourcedigest)
	$(call GO_EXEC, go run ./tools/sourcehash .)

build-crt-parser: prepare ## Build crt_parser tool
	@echo "Building crt_parser..."
	docker compose exec builder sh -c 'cd /git-source/tools/certutils && go build -ldflags "$(LD_FLAGS)" -o /output/$(COMMIT)/crt_parser crt_parser.go'
	@echo "crt_parser built at $(BUILD_DIR)/crt_parser"

build-canonicalize: prepare ## Build canonicalize tool
	@echo "Building canonicalize..."
	docker compose exec builder sh -c 'cd /git-source/tools/canonicalize && mkdir -p /output/$(COMMIT) && go build -trimpath -ldflags "$(LD_FLAGS)" -o /output/$(COMMIT)/canonicalize .'
	@echo "canonicalize built at $(BUILD_DIR)/canonicalize"

# =============================================================================
# Verification
# =============================================================================
verify: ## Run golden file verification (BINARY=path or auto-detected)
	@bash scripts/ai_verify.sh

verify-auto: ## Auto-build canonicalize then verify
	@set -e; \
	BIN=$$(ls -1t output/*/canonicalize 2>/dev/null | head -n1 || true); \
	if [ -z "$$BIN" ]; then \
	  echo "Building canonicalize..."; \
	  $(MAKE) build-canonicalize; \
	  BIN=$$(ls -1t output/*/canonicalize | head -n1); \
	fi; \
	echo "Using BINARY=$$BIN"; \
	$(MAKE) verify BINARY="$$BIN"

verify-debug: ## Show what verify-auto sees
	@set -x; ls -l output/*/canonicalize || true; env | grep -E 'BINARY|MAKE|PATH' || true

verify-full: ## Run full verification (quality + coverage + manifest)
	$(MAKE) quality
	$(MAKE) coverage-check-pkgs
	$(MAKE) manifest-verify

# =============================================================================
# Manifest
# =============================================================================
manifest-src: ## Snapshot of tracked sources (audit)
	$(call GO_FIXER, git ls-files -z | xargs -0 sha256sum > /output/$(COMMIT)/SOURCE.MANIFEST.sha256)
	@echo "Source manifest: /output/$(COMMIT)/SOURCE.MANIFEST.sha256"

manifest-verify: ## Verify repository manifest
	@echo "--- Verifying repository manifest ---"
	@docker compose exec builder sh -c 'test -f MANIFEST.sha256 && sha256sum -c MANIFEST.sha256'

manifest-update: ## Re-generate MANIFEST.sha256
	@echo "--- Updating repository manifest ---"
	@docker compose exec builder sh -c 'git ls-files -z \
		| xargs -0 sha256sum' | grep -v "MANIFEST.sha256" | LC_ALL=C sort > MANIFEST.sha256
	@echo "MANIFEST.sha256 updated"

# =============================================================================
# Dev Vault
# =============================================================================
dev-vault: ## Start local in-memory Vault for development
	@bash scripts/dev-vault.sh

dev-vault-stop: ## Stop local dev Vault
	@bash scripts/dev-vault.sh --stop

# =============================================================================
# Optional: TDD loop
# =============================================================================
tdd: ## TDD loop with reflex (Go)
	$(call GO_EXEC, \
		command -v reflex >/dev/null 2>&1 || go install github.com/cespare/reflex@latest; \
		reflex -r "(\.go|go\.mod|go\.sum)$$" -- sh -c "GOFLAGS=-mod=readonly\ -trimpath go test -race -count=1 ./..." \
	)

cache-populate: ## Pre-populate Go module and tool cache
	docker compose run --rm --entrypoint bash mod-cache-loader -c " \
		GOFLAGS=-buildvcs=false GOBIN=/go/bin \
		&& go mod download \
		&& go mod download gopkg.in/yaml.v3 \
		&& echo staticcheck && go install honnef.co/go/tools/cmd/staticcheck@v0.6.1 \
		&& echo ineffassign && go install github.com/gordonklaus/ineffassign@v0.1.0 \
		&& echo reflex && go install github.com/cespare/reflex@v0.3.1 \
		&& echo govulncheck && go install golang.org/x/vuln/cmd/govulncheck@v1.1.4 \
		"

# =============================================================================
# Optional: MQ publish (guarded by NO_PUBLISH)
# =============================================================================
mq-publish: ## Example publish step (override as needed)
	docker compose exec nats-cli sh -c ' \
		FILE="/output/$(COMMIT)/$(APP_NAME)" && \
		if [ -f $$FILE ]; then \
			nats pub build.result.$(COMMIT) "Build ready: $$FILE"; \
		else \
			nats pub build.result.$(COMMIT) "Build failed or missing output."; \
		fi '