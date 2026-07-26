#!/usr/bin/env bash
# relay-ci.sh — drive one CI build through a running relay.
#
# Turns "here is a repo and a commit" into a signed artifact, and prints the
# values that identify it. Usually invoked as `make relay.ci`.
#
# Two modes, selected by which variables are set:
#
#   generic repository (POST /v1/repo/ci)
#     REPO_URL=… COMMIT=<sha> ARTIFACT_OUTPUT=dist/app.bin [MAKE_TARGET=build]
#
#   CIC-Schemas pipeline (POST /v1/schemas/pipeline)
#     REPO_URL=… COMMIT=<sha> SCHEMA=postgresql VERSION=v0.18.0
#
# Exit 0 only when the pipeline returned a signed artifact.
set -uo pipefail

RELAY_URL="${RELAY_URL:-http://127.0.0.1:8080}"
REPO_URL="${REPO_URL:-}"
COMMIT="${COMMIT:-}"
BRANCH="${BRANCH:-}"
SCHEMA="${SCHEMA:-}"
VERSION="${VERSION:-}"
MAKE_TARGET="${MAKE_TARGET:-}"
ARTIFACT_OUTPUT="${ARTIFACT_OUTPUT:-}"
BUILD_DIR="${BUILD_DIR:-}"
BUILDER_CONTAINER="${BUILDER_CONTAINER:-}"

command -v jq >/dev/null || { echo "FATAL: jq is required" >&2; exit 1; }

# ARTIFACT_OUTPUT selects the generic route: the caller names the file make
# produces instead of the pipeline assuming source/<schema>.yaml.
if [[ -n "$ARTIFACT_OUTPUT" ]]; then
    MODE=repo
    ENDPOINT="/v1/repo/ci"
else
    MODE=schema
    ENDPOINT="/v1/schemas/pipeline"
fi

missing=()
[[ -n "$REPO_URL" ]] || missing+=(REPO_URL)
if [[ "$MODE" == "schema" ]]; then
    [[ -n "$SCHEMA"  ]] || missing+=(SCHEMA)
    [[ -n "$VERSION" ]] || missing+=(VERSION)
fi
if [[ ${#missing[@]} -gt 0 ]]; then
    echo "FATAL: missing required variables: ${missing[*]}" >&2
    echo "  generic repo : REPO_URL=… ARTIFACT_OUTPUT=… [MAKE_TARGET=…] [COMMIT=… | BRANCH=…] $0" >&2
    echo "  schema repo  : REPO_URL=… SCHEMA=… VERSION=… [COMMIT=… | BRANCH=…] $0" >&2
    exit 1
fi
if [[ -z "$COMMIT" && -z "$BRANCH" ]]; then
    echo "FATAL: set COMMIT (pins an exact revision) or BRANCH" >&2
    exit 1
fi

if ! curl -fsS -o /dev/null --max-time 10 "$RELAY_URL/readyz"; then
    echo "FATAL: relay not ready at $RELAY_URL — start it with: make relay.up" >&2
    exit 1
fi

req=$(jq -n \
  --arg mode "$MODE" \
  --arg schema "$SCHEMA" --arg version "$VERSION" \
  --arg repo "$REPO_URL" --arg branch "$BRANCH" --arg commit "$COMMIT" \
  --arg mt "$MAKE_TARGET" --arg ao "$ARTIFACT_OUTPUT" \
  --arg bd "$BUILD_DIR" --arg bc "$BUILDER_CONTAINER" '
  {repo_url: $repo}
  + (if $mode == "schema" then {schema: $schema, version: $version}
                          else {artifact_output: $ao} end)
  + (if $mt     != "" then {make_target: $mt} else {} end)
  + (if $branch != "" then {branch: $branch} else {} end)
  + (if $commit != "" then {commit: $commit} else {} end)
  + (if $bd     != "" then {build_dir: $bd} else {} end)
  + (if $bc     != "" then {builder_container: $bc} else {} end)')

echo "--- Running $MODE pipeline (${COMMIT:-$BRANCH}) ---"
resp=$(mktemp); trap 'rm -f "$resp"' EXIT
code=$(curl -s -o "$resp" -w '%{http_code}' --max-time 3600 \
  -X POST -H 'Content-Type: application/json' -d "$req" \
  "$RELAY_URL$ENDPOINT")

if [[ "$code" != "200" ]]; then
    echo "FAILED (HTTP $code):" >&2
    jq -r '.error // .' "$resp" >&2 2>/dev/null || head -c 2000 "$resp" >&2
    exit 1
fi

jq -r '
  .artifact as $a | .proof_trace as $t |
  "build_hash         : \($a.build_hash // "-")",
  "verification_root  : \($a.verification_root // "-")",
  "source_ref         : \($a.source_ref // "-")",
  "source_tree_digest : \($a.source_tree_digest // "-")",
  "cic_sign           : \($a.cic_sign // "-")",
  "chain_hash         : \($t.chain_hash // "-")",
  "source_digest      : \($t.source_digest // "-")",
  "steps              : \($t.steps | length)"
' "$resp"

# build_hash is sha256(raw_artifact) — a pure content digest, so it is expected
# to repeat across revisions whose compiled artifact is unchanged. The value that
# identifies WHICH revision was built is verification_root. See
# docs/en/pipeline/source-provenance.md.
sd=$(jq -r '.proof_trace.source_digest // ""' "$resp")
if [[ -z "$sd" || "$sd" == "unknown" ]]; then
    echo ""
    echo "WARNING: source_digest is '${sd:-empty}' — this trace is not anchored to a"
    echo "         source revision. Pass COMMIT to pin one."
fi
