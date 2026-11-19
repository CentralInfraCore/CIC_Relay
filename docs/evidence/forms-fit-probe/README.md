# Forms fit probe (evidence for docs/{en,hu}/concept/primitives-forms-fit-survey.md)

A minimal, non-production probe: a representative `ModuleRelease` schema (`schema.yaml`) compiled with `compile_schema` and ten documents validated with `validate` and `path_of`, by the Forms Rust and Go libraries. The two programs are stored as `probe.go.txt` and `probe.rs.txt` (not `.go`/`.rs`, so that no build of this repository picks them up).

Run against `cic-primitive-forms` `main` `2f02a75`: copy `probe.go.txt` to a `main.go` in a module that `replace`s `github.com/CentralInfraCore/cic-primitive-forms/go` with a checkout, and `probe.rs.txt` to `rust/examples/probe.rs` of the checkout (`cargo run --example probe -- schema.yaml docs`). The Go and Rust outputs were identical, byte for byte, and equal `expected-output.txt`.

The documents are derived from the published `primitives/@v0.2.1` record, with identity and certificate values replaced by placeholders. The grammar pin used by `compile_schema` is `cic-primitives-73ae469-6a188078`, the only profile Forms carries: a development commit, not a release.
