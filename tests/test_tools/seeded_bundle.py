"""The honest bundle of the seeded-defect harness, built from the REAL PR-04 fixtures (not a test module).

Go: the real objinventory output and the real cover profile of the goastdump module (15 objects).
Rust: the real cargo-llvm-cov / c++filt / cargo-mutants output of cic-canonical/src/lib.rs.
The checkout is a throw-away git repository that holds the real goastdump source, so that L2 (identity) and the
independent Go enumeration run for real."""

import json
import os
import shutil
import subprocess
import sys

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import object_model as om  # noqa: E402
import quality_gate as qg  # noqa: E402
import seeded_defects as sd  # noqa: E402

FIX = os.path.join(os.path.dirname(__file__), "fixtures/object_model")
TOOLS = {
    "go": "go1.26.10",
    "rustc": "1.96.1",
    "cargo-mutants": "27.1.0",
    "cargo-llvm-cov": "0.9.1",
    "c++filt": "2.40",
}
SCOPE = json.load(
    open(os.path.join(os.path.dirname(__file__), "../../.measurement-scope.json"))
)


def J(name):
    with open(os.path.join(FIX, name)) as fh:
        return json.load(fh)


def fixture_bundle(
    tmp: str, repeats: int = 0, runs_identical: bool = True
) -> sd.Bundle:
    """The honest bundle. `repeats`: how many further identical Go profiles the Go coverage comes from."""
    # the checkout: a git repository with the real goastdump source and the real Rust source
    co = os.path.join(tmp, "checkout")
    os.makedirs(os.path.join(co, "cic-canonical/src"))
    shutil.copy(os.path.join(FIX, "goastdump-main.go.txt"), os.path.join(co, "main.go"))
    shutil.copy(
        os.path.join(FIX, "cic-canonical-lib.rs.txt"),
        os.path.join(co, "cic-canonical/src/lib.rs"),
    )
    env = {
        **os.environ,
        "GIT_AUTHOR_NAME": "t",
        "GIT_AUTHOR_EMAIL": "t@x",
        "GIT_COMMITTER_NAME": "t",
        "GIT_COMMITTER_EMAIL": "t@x",
    }
    for a in (
        ["init", "-q"],
        ["add", "-A"],
        [
            "-c",
            "commit.gpgsign=false",
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "-q",
            "-m",
            "x",
        ],
    ):
        subprocess.run(["git", "-C", co, *a], check=True, env=env)

    def g(*a: str) -> str:
        return subprocess.run(
            ["git", "-C", co, *a], capture_output=True, text=True, check=True
        ).stdout.strip()

    commit, tree = g("rev-parse", "HEAD"), g("rev-parse", "HEAD^{tree}")

    # the input files (copies of the real outputs) and their digests
    inp_dir = os.path.join(tmp, "inputs")
    os.makedirs(inp_dir)
    files = {
        "go-inventory:goastdump": "goastdump-inventory.json",
        "go-coverprofile:goastdump": "goastdump-cover.out",
        "llvm-cov-json": "llvm-cov-cic-canonical.json",
        "rust-mutant-list": "mutants-cic-canonical.json",
    }
    inputs, digests = {}, {}
    for name, f in files.items():
        shutil.copy(os.path.join(FIX, f), os.path.join(inp_dir, f))
        inputs[name] = os.path.join(inp_dir, f)
        d, size = om.sha256_file(inputs[name])
        digests[name] = {"sha256": d, "bytes": size}

    blocks = om.parse_coverprofile(
        open(os.path.join(FIX, "goastdump-cover.out")).read()
    )
    reps = [blocks] * repeats if runs_identical else None
    go, _ = om.go_objects(
        "goastdump",
        "goastdump",
        J("goastdump-inventory.json"),
        blocks,
        repeats=reps or None,
        detail=True,
    )
    demangled, llvm = J("demangled-cic-canonical.json"), J(
        "llvm-cov-cic-canonical.json"
    )
    verdicts = qg.rust_verdicts(J("outcomes-cic-canonical.json"), "fixture")
    mutants = J("mutants-cic-canonical.json")
    rs, _ = om.rust_objects(llvm, demangled, co, mutants, verdicts)
    rec = om.build_record(go + rs, commit, tree, dict(TOOLS), digests, SCOPE, {})

    derived = om.derive_go_coverage(
        "goastdump", "goastdump", J("goastdump-inventory.json"), blocks, reps or None
    )
    derived.update(om.derive_rust_coverage(llvm, demangled, co))
    rust_ids, unmapped = om.independent_rust_free_functions(
        open(os.path.join(co, "cic-canonical/src/lib.rs")).read(), "cic_canonical"
    )
    return sd.Bundle(
        record=rec,
        root=co,
        inputs=inputs,
        derived=derived,
        go_independent={
            "goastdump": om.independent_go_ids(co, "goastdump", ".", ["main.go"])
        },
        rust_independent=rust_ids if unmapped == 0 else None,
        required_units={"goastdump", "rust"},
        mutants=mutants,
        tool_pins={
            "tools": dict(rec["tools"]),
            "surfaces": {"object-evidence": sorted(rec["tools"])},
        },
    )


HONEST = {
    "L1": "INTEGRITY_OK",
    "L2": "BOUND_TO_INPUTS",
    "L3": "COMPLETE_FOR_SCOPE",
    "tools": "TOOLS_PINNED",
}
