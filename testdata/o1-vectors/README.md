# O-1 shared conformance vectors (PROVISIONAL, TEST KEYS)

`chain-and-interaction-vectors.json` is copied verbatim from the O-1 preparation
package (`cic-factory` job `o1-wp05-prooftrace-v2`, `ref/vectors/`). It is the
**shared** input of two independent implementations in this repository:

| Reader | Package | Role |
|---|---|---|
| producer | `core/cabinet` | computes `CICCHAINv2` chain hashes and the `CICHOSTINTv1` list digest |
| verifier | `pkg/o1verify` | recomputes both from its own, separate implementation |

Both read **these** bytes, so an agreement between them is evidence of
independent reproduction (AC-V2-15), not of shared code.

**Status:** `PROVISIONAL (reference implementation only)`. The keys are public
test constants; nothing here is a release signature. The `wasm_executed` value of
the envelope vectors is explicitly not normative (WP-00); the envelope vectors
are not copied here.
