Real tool output used as fixtures by `tests/test_tools/test_quality_gate.py`.

* `cargo-mutants-27.1.0-list.json` — `mutants.json` of a real `cargo mutants --workspace --re canonical_number`
  run (5 mutants) on CIC_Relay `7ac6e80`, in `rust:1.96.1-bookworm`. Its entries have the same shape as
  `cargo mutants --list --json`. Unmodified.
* `cargo-mutants-27.1.0-outcomes.json` — the `outcomes.json` of the same run: the baseline and 5 `CaughtMutant`
  outcomes. Only the machine-local `log_path` and the toolchain `argv` are elided.

The other verdicts (missed, timeout, unviable) are made in the tests by changing the `summary` and the counters
of this real file; those are derived cases, not tool output.
