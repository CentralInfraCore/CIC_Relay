SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
MAKEFLAGS += --no-builtin-rules --warn-undefined-variables

-include mk/infra.mk

# ---- Phony ----
.PHONY: all help infra-init prepare fmt fmt-go fmt-check lint lint-go vet quality deadcode \
	test test-go test-py coverage coverage-threshold coverage-profile coverage-html \
	build build-relay verify manifest-src \
	tdd cache-populate mq-publish verify-auto verify-debug \
	manifest-verify manifest-update verify-full clean shell \
	symbols check-symbols test-api deps \
	dev-vault dev-vault-stop \
	release release-relay release-verify sourcehash validate \
	up down repo.init typecheck check build-crt-parser build-canonicalize fetch-pki fetch-pki-release \
	infra-down infra.help \
	mutate mutate-all \
	fmt-rust fmt-rust-check lint-rust test-rust coverage-rust deny-rust release-rust rust mutate-rust \
	ffi-staticlib test-ffi demo demo-trustflow demo-oci-sign

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

# BUILDER_IMAGE_DIGEST: golang builder image digest, derived at make-time from the
# single source of truth — the &golang_image anchor in docker-compose.yml that every
# golang service (*golang_image) references. This cannot drift: bump the anchor and the
# digest baked into the binary (-X main.BuilderImageDigest) follows automatically.
# Override for hermetic/off-tree builds: make ... BUILDER_IMAGE_DIGEST=sha256:...
BUILDER_IMAGE_DIGEST ?= $(shell grep '^x-golang-version:' docker-compose.yml | grep -oE 'sha256:[a-f0-9]{64}')

# RUST_IMAGE_DIGEST: same single-source discipline for the rust-builder image —
# derived from the &rust_image anchor in docker-compose.yml so it cannot drift.
RUST_IMAGE_DIGEST ?= $(shell grep '^x-rust-version:' docker-compose.yml | grep -oE 'sha256:[a-f0-9]{64}')

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
	docker compose exec -T go-builder sh -eu -o pipefail -c 'cd /git-source && $(1)'
endef
define GO_FIXER
	docker compose exec -T go-fixer sh -eu -o pipefail -c 'cd /git-source && $(1)'
endef
# RUST_EXEC: run a cargo command in the rust-builder. Ensures rustfmt/clippy are
# present (idempotent, cheap once installed) so every rust target is self-sufficient.
define RUST_EXEC
	docker compose exec -T rust-builder bash -eu -o pipefail -c 'cd /git-source && rustup component add rustfmt clippy >/dev/null 2>&1 || true; $(1)'
endef

# =============================================================================
# Container Lifecycle
# =============================================================================
up: infra.up ## Start the development environment
down: infra.down ## Stop and remove the development environment
build: infra.build ## Build Docker images

shell: ## Open an interactive shell into the running Python builder container
	docker compose exec py-builder bash

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
	@echo "  quality             Go quality gate (fmt-check + lint-go + vet + vuln + deadcode)."
	@echo ""
	@echo "Coverage:"
	@echo "  coverage            Run tests with coverage (profile + HTML)."
	@echo "  coverage-threshold  Fail if coverage < COVERAGE_MIN%."
	@echo ""
	@echo "Mutation Testing:"
	@echo "  mutate              Run gremlins on MUTATE_PKG (default: ./core/cabinet, MUTATE_EFFICACY=90)."
	@echo "  mutate-all          Run gremlins on all critical packages; fail if efficacy < 90%."
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
	@docker compose exec py-builder python -m tools.compiler validate $(COMPILER_CLI_ARGS)

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
		py-builder python -m tools.compiler release --version $(VERSION) $(COMPILER_CLI_ARGS)

# =============================================================================
# Symbol generation
# =============================================================================
symbols: ## Generate symbols documentation
	@echo "Generating symbols..."
	@$(call GO_FIXER, go run ./tools/symbolsgen context/SYMBOLS.ALLOWPKGS context/SYMBOLS.md)

check-symbols: ## Check if symbols documentation is up-to-date
	@echo "Checking for symbol drift..."
	@$(call GO_FIXER, go run ./tools/symbolsgen context/SYMBOLS.ALLOWPKGS context/SYMBOLS.md && (git diff --exit-code context/SYMBOLS.md))

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
		command -v staticcheck >/dev/null 2>&1 || GOFLAGS="" go install honnef.co/go/tools/cmd/staticcheck@v0.6.1; \
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

deadcode: ## Scan for functions unreachable from any main entrypoint (catches exported dead code staticcheck misses)
	mkdir -p $(BUILD_DIR) && $(call GO_EXEC, \
		set -euo pipefail; \
		command -v deadcode >/dev/null 2>&1 || GOFLAGS="" go install golang.org/x/tools/cmd/deadcode@v0.44.0; \
		echo "deadcode ./..."; \
		GO111MODULE=on GOFLAGS="$(GOFLAGS)" deadcode ./... \
	)

quality: fmt-check lint-go vet vuln deadcode ## Go quality gate: all checks must pass

# =============================================================================
# Rust build/lint/test (rust-builder container; workspace at repo root)
# =============================================================================
fmt-rust: ## Apply rustfmt across the Rust workspace
	@echo "Formatting Rust..."
	$(call RUST_EXEC, cargo fmt --all)

fmt-rust-check: ## Fail if Rust formatting differs
	$(call RUST_EXEC, cargo fmt --all --check)

lint-rust: ## Run clippy as a hard gate (-D warnings)
	@echo "Clippy (Rust)..."
	$(call RUST_EXEC, cargo clippy --all-targets --all-features --locked -- -D warnings)

test-rust: ## Run Rust unit/doc tests (locked dependencies)
	@echo "Testing Rust..."
	$(call RUST_EXEC, cargo test --all --locked)

# Line-coverage floor, to ratchet up (never down) — parity with the Go per-package
# thresholds and the Python --cov-fail-under gate. connections currently measures 100%.
RUST_COV_MIN ?= 90
coverage-rust: ## Rust line-coverage gate (cargo-llvm-cov, fail under RUST_COV_MIN%)
	@echo "Rust coverage (min $(RUST_COV_MIN)%)..."
	$(call RUST_EXEC, export PATH="/cargo/bin:$$PATH"; \
		rustup component add llvm-tools-preview >/dev/null 2>&1 || true; \
		command -v cargo-llvm-cov >/dev/null 2>&1 || cargo install cargo-llvm-cov --locked >/dev/null 2>&1; \
		cargo llvm-cov --all --locked --fail-under-lines $(RUST_COV_MIN))

deny-rust: ## Rust supply-chain gate (cargo-deny: advisories/licenses/bans/sources)
	@echo "cargo-deny (Rust)..."
	$(call RUST_EXEC, export PATH="/cargo/bin:$$PATH"; \
		command -v cargo-deny >/dev/null 2>&1 || cargo install cargo-deny --locked >/dev/null 2>&1; \
		cargo deny check)

release-rust: ## Build reproducible release artifacts (--locked) + sha256 into dist/
	$(call RUST_EXEC, \
		mkdir -p /git-source/dist 2>/dev/null || true; \
		RUSTFLAGS="--remap-path-prefix=/git-source=." \
			cargo build --release --locked --workspace; \
		echo "rust builder image: $(RUST_IMAGE_DIGEST)")
	@echo "Rust release built (target/release) with digest $(RUST_IMAGE_DIGEST)"

rust: fmt-rust-check lint-rust coverage-rust deny-rust ## Rust quality gate: fmt + clippy + coverage + supply-chain

# =============================================================================
# Rust ↔ Go FFI bridge (cic-ffi staticlib → Go cgo). Opt-in: CGO_ENABLED=1 +
# the `ffi_cgo` build tag, so the default pure-static Go build is untouched.
# See features/feature-008/ffi-boundary.md.
# =============================================================================
FFI_LIB_DIR ?= /output/lib

ffi-staticlib: ## Build the cic-ffi staticlib and stage it for the Go cgo link
	@echo "Building cic-ffi staticlib..."
	$(call RUST_EXEC, cargo build -p cic-ffi --release --locked)
	$(call RUST_EXEC, mkdir -p $(FFI_LIB_DIR) && cp /target/release/libcic_ffi.a $(FFI_LIB_DIR)/libcic_ffi.a)
	@echo "staticlib staged at $(FFI_LIB_DIR)/libcic_ffi.a"

test-ffi: ffi-staticlib ## Test the live Go→Rust→Go bridge (cgo, ffi_cgo tag)
	@echo "Testing FFI bridge (cgo)..."
	$(call GO_EXEC, \
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
		CGO_ENABLED=1 CGO_LDFLAGS="-L$(FFI_LIB_DIR)" \
			go test -tags ffi_cgo -count=1 -v ./pkg/cicffi/...; \
		echo "Testing http.request capability deploy wiring (cgo relay)..."; \
		CGO_ENABLED=1 CGO_LDFLAGS="-L$(FFI_LIB_DIR)" \
			go test -tags ffi_cgo -count=1 ./cmd/relay/...)

# Opt-in live-Vault end-to-end test of the full feature-008 trust-flow chain
# (countersign → mTLS cert-auth login → read_sealed → open). Spins a throwaway
# `-dev-tls` OpenBao inside the rust-builder (needs the `vault`/openbao binary on
# the host, mounted in) — no admin token, never touches a real Vault. Not a CI
# gate; the chain is otherwise unverifiable without a live server.
test-vault-live: ## Live-Vault e2e trust-flow chain (throwaway dev-tls server; needs the vault/openbao binary)
	@command -v vault >/dev/null 2>&1 || { echo "vault/openbao binary not found on host — install it to run the live-Vault test"; exit 1; }
	@echo "Running live-Vault trust-flow chain (throwaway dev-tls server)..."
	docker compose run --rm --entrypoint bash \
		-v "$$(command -v vault):/usr/local/bin/vault:ro" \
		rust-builder scripts/vault-live-e2e.sh

# Live FFI trust-flow: Go → Rust (cic-ffi built with --features live-vault) → real
# Vault (countersign / mTLS cert-auth login / read_sealed / open) → HTTP actuation.
# Builds the live staticlib (rust-builder), then runs the cgo Go test against a
# throwaway dev-tls server in the go-builder (with the vault binary mounted).
# Opt-in, not a CI gate.
test-ffi-live: ## Live FFI trust-flow (Go → Rust → live Vault → HTTP; needs the vault/openbao binary)
	@command -v vault >/dev/null 2>&1 || { echo "vault/openbao binary not found on host — install it to run the live FFI test"; exit 1; }
	@echo "Building live-vault cic-ffi staticlib..."
	$(call RUST_EXEC, cargo build -p cic-ffi --release --locked --features live-vault)
	$(call RUST_EXEC, mkdir -p $(FFI_LIB_DIR) && cp /target/release/libcic_ffi.a $(FFI_LIB_DIR)/libcic_ffi.a)
	@echo "Running live FFI trust-flow (throwaway dev-tls server)..."
	docker compose run --rm --entrypoint bash \
		-v "$$(command -v vault):/usr/local/bin/vault:ro" \
		go-builder /git-source/scripts/ffi-live-e2e.sh

# Live demo of the feature-010 signed state chain: spins a throwaway dev-tls
# OpenBao with a Transit signing key, records signed state transitions, verifies
# the chain, and proves it is tamper-evident. Opt-in; not a CI gate.
demo: ## Live signed-state-chain demo (throwaway dev-tls Vault; needs the vault/openbao binary)
	@command -v vault >/dev/null 2>&1 || { echo "vault/openbao binary not found on host — install it to run the demo"; exit 1; }
	@echo "Running the signed state-chain demo (throwaway dev-tls server)..."
	docker compose run --rm --entrypoint bash \
		-v "$$(command -v vault):/usr/local/bin/vault:ro" \
		go-builder -c 'cd /git-source && scripts/statechain-demo.sh'

# Live demo of the feature-008/009 FFI trust-flow: Go → Rust (cic-ffi live-vault)
# → real Vault (module CSR → PKI countersign → mTLS cert-auth login → read_sealed
# → open) → HTTP actuation. Builds the live staticlib (rust-builder), then runs
# the demo in the go-builder against a throwaway dev-tls server. Opt-in; not a CI gate.
demo-trustflow: ## Live FFI trust-flow demo (Go → Rust → live Vault → HTTP; needs the vault/openbao binary)
	@command -v vault >/dev/null 2>&1 || { echo "vault/openbao binary not found on host — install it to run the demo"; exit 1; }
	@echo "Building live-vault cic-ffi staticlib..."
	$(call RUST_EXEC, cargo build -p cic-ffi --release --locked --features live-vault)
	$(call RUST_EXEC, mkdir -p $(FFI_LIB_DIR) && cp /target/release/libcic_ffi.a $(FFI_LIB_DIR)/libcic_ffi.a)
	@echo "Running the live FFI trust-flow demo (throwaway dev-tls server)..."
	docker compose run --rm --entrypoint bash \
		-v "$$(command -v vault):/usr/local/bin/vault:ro" \
		go-builder /git-source/scripts/trustflow-demo.sh

# Proves the feature-011 OCI sign-then-send signing profile (draft-cavage
# canonical string + RSA-SHA256 PKCS#1 v1.5) end to end, in-process — no relay,
# no Vault, no cgo. A fake OCI endpoint verifies the signature as OCI would, then
# a tampered body is rejected. Opt-in; not a CI gate.
demo-oci-sign: ## OCI sign-then-send signing-profile proof (pure Go, no Vault)
	@echo "Proving the OCI sign-then-send signing profile (feature-011)..."
	$(call GO_EXEC, go run ./tools/oci-sign-demo)

# =============================================================================
# Code Quality — combined (Go + Python + Rust)
# =============================================================================
fmt: fmt-go fmt-rust infra.fmt ## Format all code (Go gofmt + Rust rustfmt + Python black/isort)

lint: lint-go lint-rust infra.lint ## Lint all code (Go staticcheck + Rust clippy + Python ruff + yamllint)

typecheck: infra.typecheck ## Run static type checking (Python MyPy)

check: infra.check quality rust ## Run all code quality checks (Go + Python + Rust)

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

test: test-go test-py test-rust ## Run all tests (Go + Python + Rust)

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
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
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

# =============================================================================
# Mutation Testing (gremlins)
# =============================================================================
# MUTATE_PKG:      single-package target (gremlins limit)
# MUTATE_EFFICACY: minimum kill-ratio threshold (0–100); default 90
# GREMLINS_WORKERS: parallelism inside the container
MUTATE_PKG      ?= ./core/cabinet
MUTATE_EFFICACY ?= 90
GREMLINS_VERSION ?= v0.5.1
GREMLINS_WORKERS ?= 4

mutate: ## Run mutation testing with gremlins (MUTATE_PKG=./pkg, MUTATE_EFFICACY=90)
	$(call GO_EXEC, \
		set -euo pipefail; \
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
		rm -rf /tmp/gremlins-run && cp -r /git-source /tmp/gremlins-run; \
		cd /tmp/gremlins-run && \
		GOFLAGS="$(GOFLAGS)" gremlins unleash \
			--workers $(GREMLINS_WORKERS) \
			--threshold-efficacy $(MUTATE_EFFICACY) \
			$(MUTATE_PKG); \
		rm -rf /tmp/gremlins-run \
	)

# Per-csomag mutációs küszöbök — aktuális mérések alapján (90% floor).
# Frissítsd, ha szándékosan változtatod a tesztelt csomagok körét.
mutate-all: ## Run mutation testing on all critical packages (efficacy ≥ 90%)
	$(call GO_EXEC, \
		set -euo pipefail; \
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
		rm -rf /tmp/gremlins-run && cp -r /git-source /tmp/gremlins-run; \
		cd /tmp/gremlins-run; \
		echo "--- core/cabinet ---"; \
		GOFLAGS="$(GOFLAGS)" gremlins unleash --workers $(GREMLINS_WORKERS) --threshold-efficacy 90 ./core/cabinet; \
		echo "--- core/modules/schemapipeline ---"; \
		GOFLAGS="$(GOFLAGS)" gremlins unleash --workers $(GREMLINS_WORKERS) --threshold-efficacy 90 ./core/modules/schemapipeline; \
		echo "--- core/modules/schemacompile ---"; \
		GOFLAGS="$(GOFLAGS)" gremlins unleash --workers $(GREMLINS_WORKERS) --threshold-efficacy 90 ./core/modules/schemacompile; \
		echo "--- core/modules/httpcap ---"; \
		GOFLAGS="$(GOFLAGS)" gremlins unleash --workers $(GREMLINS_WORKERS) --threshold-efficacy 90 ./core/modules/httpcap; \
		echo "--- core/nexus/ois ---"; \
		GOFLAGS="$(GOFLAGS)" gremlins unleash --workers $(GREMLINS_WORKERS) --threshold-efficacy 90 ./core/nexus/ois; \
		rm -rf /tmp/gremlins-run \
	)

# Rust mutation testing (cargo-mutants). Like gremlins, it is slow — kept out of
# the per-push `rust` gate and run as a PR-only CI job. cargo-mutants fails if any
# viable mutant survives (0 surviving = pass); connections currently: 6 caught,
# 1 unviable, 0 missed. Output goes to the writable /target volume (source is ro).
mutate-rust: ## Rust mutation testing (cargo-mutants; fails on surviving mutants)
	@echo "cargo-mutants (Rust)..."
	$(call RUST_EXEC, export PATH="/cargo/bin:$$PATH"; \
		command -v cargo-mutants >/dev/null 2>&1 || cargo install cargo-mutants --locked >/dev/null 2>&1; \
		cargo mutants --workspace --output /target/mutants)

coverage-threshold: coverage ## Fail if coverage < $(COVERAGE_MIN)%
	mkdir -p $(BUILD_DIR) && docker compose exec -T go-builder sh -c 'cd /git-source && \
		go tool cover -func=$(COVERAGE_FILE) | \
		awk -v MIN=$(COVERAGE_MIN) '"'"'/^total:/ { gsub("%","",$$3); v=$$3+0 } END { if (v < MIN) { printf "Coverage below %d%% (got %.1f%%)\n", MIN, v; exit 1 } else { printf "Coverage OK: %.1f%% >= %d%%\n", v, MIN } }'"'"''

# Per-csomag coverage küszöbök — aktuális mérések alapján (~1-2 pp tartalékkal).
# Frissítsd, ha szándékosan emeled a lefedettséget.
coverage-check-pkgs: ## Fail if per-package coverage < threshold
	mkdir -p $(BUILD_DIR) && docker compose exec go-builder sh -c 'cd /git-source && \
		set -e; \
		git config --global --add safe.directory /git-source 2>/dev/null || true; \
		for p in $$(go list ./... | grep -v /vendor/); do \
		  case "$$p" in \
		    *"/core/nexus/types") continue ;; \
		    *"/embedded/pki")     continue ;; \
		    *"/tools/sourcehash") continue ;; \
		    *"/tools/statechain-demo") continue ;; \
		    *"/tools/trustflow-demo") continue ;; \
		    *"/tools/oci-sign-demo") continue ;; \
		    *"/pkg/cicffi")       continue ;; \
		  esac; \
		  GO111MODULE=on GOFLAGS="$(GOFLAGS)" go test $$p -coverprofile=/tmp/cover.out >/dev/null 2>&1; \
		  pc=$$(go tool cover -func=/tmp/cover.out 2>/dev/null | awk '"'"'END{print $$3}'"'"' | tr -d "%"); \
		  case "$$p" in \
		    *"/cmd/relay")                   min=80  ;; \
		    *"/core/cabinet")                min=93  ;; \
		    *"/core/modules/certselfsigned") min=99  ;; \
		    *"/core/modules/cibuild")        min=94  ;; \
		    *"/core/modules/schemacompile")  min=90  ;; \
		    *"/core/modules/schemapipeline") min=90  ;; \
		    *"/core/nexus/crypto")           min=93  ;; \
		    *"/core/nexus/git")              min=92  ;; \
		    *"/core/nexus/iac")              min=90  ;; \
		    *"/core/nexus/isolation")        min=90  ;; \
		    *"/core/nexus/operator")         min=90  ;; \
		    *"/core/nexus/recorder")         min=85  ;; \
		    *"/core/nexus/sync")             min=88  ;; \
		    *"/pkg/canonicaljson")           min=96  ;; \
		    *"/pkg/merkle")                  min=97  ;; \
		    *"/pkg/obs")                     min=97  ;; \
		    *"/pkg/sourcedigest")            min=85  ;; \
		    *"/tools/canonicalize")          min=99  ;; \
		    *"/tools/certutils")             min=82  ;; \
		    *"/tools/symbolsgen")            min=90  ;; \
		    *) min=80 ;; \
		  esac; \
		  echo "$$p: $$pc% (min $$min%)"; \
		  awk -v p=$$pc -v m=$$min '"'"'BEGIN{exit (p+0 < m+0)}'"'"'; \
		done'

# =============================================================================
# PKI asset fetch (build-time, requires local Vault)
# =============================================================================
# Vault connection + KV mount and cert key — must match vault-sign-agent.sh KEY_NAME + --cic-dir load.
# VAULT_ADDR is normally supplied via the environment; declared here so
# --warn-undefined-variables stays quiet (the fetch recipes still guard on it).
VAULT_ADDR        ?=
VAULT_KV_MOUNT    ?= cic-my-sign-key
VAULT_SOURCE_CA   ?= CICSourceCA
PKI_CERT_FIELD    ?= bar
PKI_OUT           := embedded/pki/cic-source-ca.pem

# Shared CICSourceCA fetch. $(1) = extra `vault` TLS args, $(2) = mode label.
# The trust root is embedded into the binary (go:embed), so how it is fetched is
# itself a trust-boundary decision: dev tolerates a self-signed Vault; release
# must pull it over a CA-verified TLS channel.
define FETCH_PKI
	@echo "Fetching $(VAULT_SOURCE_CA) from Vault ($(VAULT_ADDR)) [$(2)]..."
	@test -n "$${VAULT_ADDR:-}"  || { echo "VAULT_ADDR not set"; exit 1; }
	@test -n "$${VAULT_TOKEN:-}" || \
		{ VAULT_TOKEN=$$(cat $$XDG_RUNTIME_DIR/vault/sign-token 2>/dev/null) && \
		  test -n "$$VAULT_TOKEN" || { echo "VAULT_TOKEN not set and sign-token not found"; exit 1; }; }
	@VAULT_TOKEN=$${VAULT_TOKEN:-$$(cat $$XDG_RUNTIME_DIR/vault/sign-token)} && \
	 VAULT_CACERT=$${VAULT_CACERT:-$$XDG_RUNTIME_DIR/vault/server.crt} && \
	 vault kv get \
		-address="$$VAULT_ADDR" \
		$(1) \
		-mount=$(VAULT_KV_MOUNT) \
		-field=$(PKI_CERT_FIELD) \
		$(VAULT_SOURCE_CA) > $(PKI_OUT)
	@echo "$(VAULT_SOURCE_CA) written to $(PKI_OUT)"
endef

fetch-pki: ## [dev] Fetch CICSourceCA from local Vault (TLS verification DISABLED)
	@echo "⚠  fetch-pki (dev): -tls-skip-verify is ON — do NOT use this for release builds."
	$(call FETCH_PKI,-tls-skip-verify,dev)

# Release fetch: no -tls-skip-verify, and VAULT_CACERT must be explicitly provided
# (the ephemeral dev server.crt is not a substitute). Fails loudly otherwise.
# NOTE: not yet wired as a hard dependency of release-relay — that gate lands once
# the CICRootCA → Intermediate → leaf chain is bootstrapped (see CLAUDE.md scaffold
# map). Until then, run this manually for release-grade provenance.
fetch-pki-release: ## [release] Fetch CICSourceCA with CA-verified TLS (no skip-verify; requires VAULT_CACERT)
	@test -n "$${VAULT_CACERT:-}" || { echo "VAULT_CACERT must point to the CA that signed the Vault server cert (release fetch will not skip TLS verification)"; exit 1; }
	@test -f "$${VAULT_CACERT:-}"  || { echo "VAULT_CACERT file not found: $${VAULT_CACERT:-}"; exit 1; }
	@case "$${VAULT_CACERT:-}" in "$${XDG_RUNTIME_DIR:-}"/vault/*) echo "Refusing the ephemeral dev Vault cert ($${VAULT_CACERT:-}) for a release fetch"; exit 1 ;; esac
	$(call FETCH_PKI,,release)

# =============================================================================
# Go Relay Build
# =============================================================================
build-relay: fetch-pki prepare quality test-go coverage coverage-check-pkgs ## Build relay binary with quality gates
	@echo "Building $(APP_NAME)..."
	docker compose exec go-builder sh -c ' cd /git-source \
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
ifeq ($(VERSION),)
	$(error VERSION is required for the release-relay command. Usage: make release-relay VERSION=1.0.0)
endif
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
	docker compose exec go-builder sh -c 'cd /git-source/tools/certutils && go build -ldflags "$(LD_FLAGS)" -o /output/$(COMMIT)/crt_parser crt_parser.go'
	@echo "crt_parser built at $(BUILD_DIR)/crt_parser"

build-canonicalize: prepare ## Build canonicalize tool
	@echo "Building canonicalize..."
	docker compose exec go-builder sh -c 'cd /git-source/tools/canonicalize && mkdir -p /output/$(COMMIT) && go build -trimpath -ldflags "$(LD_FLAGS)" -o /output/$(COMMIT)/canonicalize .'
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
	@docker compose exec go-builder sh -c 'test -f MANIFEST.sha256 && sha256sum -c MANIFEST.sha256'

manifest-update: ## Re-generate MANIFEST.sha256
	@echo "--- Updating repository manifest ---"
	@bash scripts/gen_manifest.sh . MANIFEST.sha256
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
		&& echo deadcode && go install golang.org/x/tools/cmd/deadcode@v0.44.0 \
		&& echo gremlins && go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 \
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