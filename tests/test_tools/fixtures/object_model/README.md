Fixtures of `tests/test_tools/test_object_model.py`, cut from the REAL tool outputs (nothing is invented).

* `goastdump-inventory.json` — the real output of `tools/objinventory` on `tools/goastdump/main.go` (15 functions). Unmodified.
* `goastdump-cover.out` — the real `go test -covermode=set -coverprofile` of the `goastdump` module. Unmodified.
* `goastdump-main.go` — the source those two describe (CIC_Relay 852594a).
* `llvm-cov-cic-canonical.json` — the entries of `cic-canonical/src/lib.rs` of the real `cargo llvm-cov --json` export (cargo-llvm-cov 0.9.1, rust 1.96.1).
* `demangled-cic-canonical.json` — the real `c++filt` (GNU Binutils 2.40) names of those symbols.
* `mutants-cic-canonical.json`, `outcomes-cic-canonical.json` — the real cargo-mutants 27.1.0 listing and outcomes restricted to that file (11 mutants; the `diff` text and the machine-local log paths removed).
* `cic-canonical-lib.rs` — the source of that file.

Other verdicts (missed, timeout, unviable), other block layouts and tampered records are DERIVED in the tests from these; derived cases are marked in the tests.
