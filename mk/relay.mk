# =============================================================================
# Relay server container — build, run, and drive a CI build through it.
#
# Everything here replaces a hand-assembled `docker run`. See
# docs/en/pipeline/relay-container.md for what each knob is for.
# =============================================================================

RELAY_COMPOSE   ?= docker compose --profile relay
CIC_RELAY_PORT  ?= 8080
RELAY_URL       ?= http://127.0.0.1:$(CIC_RELAY_PORT)

# relay.ci parameters. Declared with defaults so the Makefile stays clean under
# --warn-undefined-variables; relay-ci.sh does the real validation.
REPO_URL          ?=
COMMIT            ?=
BRANCH            ?=
SCHEMA            ?=
VERSION           ?=
BUILD_DIR         ?=
BUILDER_CONTAINER ?=

.PHONY: relay.build relay.up relay.down relay.logs relay.status relay.ci relay.help

relay.build: ## Build the relay server image
	@echo "--- Building relay image ---"
	@$(RELAY_COMPOSE) build \
	  --build-arg COMMIT_HASH=$$(git rev-parse HEAD 2>/dev/null || echo unknown) \
	  --build-arg SOURCE_TREE_HASH=$$(go run ./tools/sourcehash . 2>/dev/null || echo unknown) \
	  relay

relay.up: ## Start the relay server (profile: relay)
	@echo "--- Starting relay ---"
	@$(RELAY_COMPOSE) up -d relay
	@echo "--- Waiting for /readyz ---"
	@for i in $$(seq 1 30); do \
	  if docker compose exec -T relay true 2>/dev/null && \
	     curl -fsS -o /dev/null "$(RELAY_URL)/readyz" 2>/dev/null; then \
	    echo "relay ready at $(RELAY_URL)"; exit 0; \
	  fi; sleep 1; \
	done; \
	echo "relay did not become ready — check: make relay.logs"; exit 1

relay.down: ## Stop the relay server
	@$(RELAY_COMPOSE) rm -sf relay

relay.logs: ## Tail relay logs
	@$(RELAY_COMPOSE) logs -f relay

relay.status: ## Show relay health and whether ProofTrace recording is on
	@curl -fsS "$(RELAY_URL)/healthz" >/dev/null 2>&1 \
	  && echo "healthz: ok" || echo "healthz: UNREACHABLE at $(RELAY_URL)"
	@docker logs cic-relay 2>&1 | grep -q 'ProofTrace recording enabled' \
	  && echo "prooftrace: enabled" \
	  || echo "prooftrace: DISABLED (VAULT_TOKEN unset, or not authorised for CIC_VAULT_KEY)"

# Drive one CI build through the running relay.
#
#   make relay.ci REPO_URL=https://github.com/org/repo COMMIT=<sha> \
#                 SCHEMA=postgresql VERSION=v0.18.0
#
# Prints the signed artifact's build_hash and verification_root. COMMIT is
# optional; without it BRANCH is used and the build is pinned only to that
# branch's tip.
relay.ci: ## Run a CI build (REPO_URL, SCHEMA, VERSION, COMMIT|BRANCH)
	@RELAY_URL="$(RELAY_URL)" REPO_URL="$(REPO_URL)" COMMIT="$(COMMIT)" \
	 BRANCH="$(BRANCH)" SCHEMA="$(SCHEMA)" VERSION="$(VERSION)" \
	 BUILD_DIR="$(BUILD_DIR)" BUILDER_CONTAINER="$(BUILDER_CONTAINER)" \
	 ./tools/relay-ci.sh

relay.help:
	@echo ""
	@echo "--- Relay Server Container (from relay.mk) ---"
	@echo "Lifecycle:"
	@echo "  relay.build         Build the relay server image (Dockerfile.relay)."
	@echo "  relay.up            Start the relay and wait for /readyz."
	@echo "  relay.down          Stop and remove the relay container."
	@echo "  relay.logs          Tail relay logs."
	@echo "  relay.status        Show health and whether ProofTrace recording is on."
	@echo ""
	@echo "CI build:"
	@echo "  relay.ci            Run one pipeline build through the relay."
	@echo "                      REPO_URL=<url> SCHEMA=<name> VERSION=<vX.Y.Z>"
	@echo "                      COMMIT=<sha>   pin an exact revision (recommended)"
	@echo "                      BRANCH=<name>  build that branch's tip instead"
	@echo ""
	@echo "Notes:"
	@echo "  Without VAULT_TOKEN the relay starts but records no signed ProofTrace."
	@echo "  CIC_BUILD_DIR must be the same host path the builder container mounts."
