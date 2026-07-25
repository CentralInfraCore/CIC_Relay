# Source Provenance in the Schema Pipeline

This document describes how `cic.schemas.pipeline` records **which source revision**
a build came from, and how that reaches the signed artifact and the ProofTrace.

## Why it matters

Two questions look similar but are not:

| Question | Answered by |
|---|---|
| "Was the same content compiled?" | `build_hash` |
| "Did it come from repository X at commit Y?" | `verification_root` + `source_ref` |

`build_hash` is `sha256(raw_artifact)` — deliberately a pure content digest. Two
builds of two different commits produce the **same** `build_hash` whenever the
compiled artifact is byte-identical, and that is correct behaviour.

Provenance is carried by the **verification manifest**, whose Merkle root is
`verification_root`. Its `Source` block must describe the repository under build;
its `BuildEnv` and `Validation` blocks describe the relay that performed the
build. Mixing those up makes the attestation unable to distinguish revisions.

## Field flow

```
cic.pipeline.start      resolves HEAD (or checks out `commit`)
                        computes source_tree_digest over the clone
                        -> source_ref, source_tree_digest, builder_image_digest
        |
        |   test -> validate -> release        (propagate via copyMap)
        v
cic.source.assert       validates the signed artifact
                        -> carries the provenance keys through
        |
        v
cic.schema.build        produces raw_artifact
                        -> carries the provenance keys through
        |
        v
cic.artifact.sign       build_hash        = sha256(raw_artifact)
                        verification_root = Merkle root of the manifest,
                                            Source filled from the flow
                        -> echoes source_ref, source_tree_digest
```

`cic.source.assert` and `cic.schema.build` build their results from scratch, so
each hands the provenance keys over explicitly (`carryProvenance`). A key that is
absent stays absent rather than being written as an empty string: the signing
step uses that distinction to decide whether the run carried a repository at all.

## Pinning a commit

```jsonc
POST /v1/schemas/pipeline
{
  "schema":   "postgresql",
  "version":  "v0.18.0",
  "repo_url": "https://github.com/org/repo",
  "commit":   "b0d1287abbb67a3ef79bfff27a5cc6b6a98ad5ee"
}
```

Either `branch` or `commit` must be present.

- **`commit` given** — full clone followed by `git checkout --detach <commit>`.
  A shallow, branch-pinned clone can only reach that branch's tip, so it cannot
  serve a request that names an arbitrary revision.
- **`branch` only** — the cheaper `git clone --branch <branch> --depth 1` is kept.

When `commit` is given it also becomes the ProofTrace's `source_digest`, so the
trace is anchored to the requested revision from the start of the run.

## Fallback: runs without a repository

`POST /v1/schema/compile` runs `assert → build → sign` with no clone. There is no
repository to describe, so the signing step falls back to the relay's own
build-time constants (`BuildContext.SourceRef`, `SourceTreeDigest`, injected via
ldflags). This is the correct answer for that call: the only source involved is
the relay's own.

## Verification

`VerifyProofArtifact` warns when the chain is not anchored to a source revision.
The check covers both an empty `source_digest` **and** the `"unknown"` sentinel
the relay writes when it has no revision to name — they mean the same thing, and
treating only emptiness as unanchored let an unanchored trace verify silently.

The warning does not invalidate the artifact: an unanchored chain can still be
internally consistent. It reports that the chain proves *what* was built without
proving *where it came from*.
