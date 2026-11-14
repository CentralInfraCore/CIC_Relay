#!/usr/bin/env python3
"""Plan the diff-mode mutation gate of a pull request.

Discovers, from the files a pull request changes, what has to be mutated:

* Go: every package of the root module that has non-test source and whose files changed.
  A changed or deleted test, and a changed testdata/fixture file, selects the package it
  tests: a test change can change the mutation result (a weaker test lets a mutant
  survive), so it is never treated as "nothing to mutate". Packages are discovered from
  the diff, never listed by hand, so new code is gated by default.
* Rust: whether any Rust source or manifest changed. The mutants themselves are
  selected by `cargo mutants --in-diff` in the workflow, unless the plan escalates.

Escalation to FULL (the diff cannot be proven to narrow the work safely): a change to the
dependencies, the build or the mutation configuration, the gate logic itself, or a Rust
test file plans every Go package (go_full) and/or every Rust mutant (rust_full). A
deleted file is part of the change set (it is not silently dropped).

The Go packages are spread over at most --go-max-jobs jobs (round-robin), so the wall
time stays bounded while the code base grows.

With --all the diff is ignored and everything at --head is planned (the weekly FULL
and the first nightly, which has no earlier run to compare with).

Output (GITHUB_OUTPUT, or stdout when it is unset):
  go       JSON list of {"id": n, "pkgs": "./a ./b"}   ("[]" when nothing to do)
  go_pkgs  the same packages as one space-separated line
  rust     "true" | "false"
  go_full  "true" when every Go package is planned because of an escalation (or --all)
  rust_full "true" when the Rust mutants are NOT narrowed by --in-diff
  full_reasons  the escalation reasons, "; "-separated ("" when none)
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path


def changed_files(base: str, head: str) -> list[str]:
    """Every path the change touches: added, copied, modified, renamed AND deleted.

    Renames are reported as a delete plus an add (--no-renames), so the old path is not lost.
    """
    out = subprocess.run(
        [
            "git",
            "diff",
            "--name-only",
            "--no-renames",
            "--diff-filter=ACMRD",
            f"{base}...{head}",
        ],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [line for line in out.splitlines() if line]


# Paths whose change can alter the Go mutation result of ANY package (dependencies, mutation
# configuration, gate logic): a narrowed plan cannot be proven safe, so the whole root module
# is planned.
GO_FULL_PATHS = {
    "go.mod",
    "go.sum",
    "go.work",
    "go.work.sum",
    ".gremlins.yaml",
}
# The same for the Rust mutants (listed per file name, in any directory).
RUST_FULL_BASENAMES = {"Cargo.toml", "Cargo.lock", "rust-toolchain.toml"}
RUST_FULL_PREFIXES = (".cargo/",)
# The gate logic itself: a change here can change what every gate accepts.
GATE_LOGIC_PATHS = {
    "scripts/mutation_plan.py",
    "scripts/gremlins_gate.py",
    "scripts/baseline_guard.py",
    "scripts/count_mutants.py",
    ".mutation-baseline.json",
    ".github/workflows/mutation.yml",
    ".github/workflows/mutation-pr.yml",
    ".github/workflows/nightly.yml",
}


def is_rust_test_file(path: str) -> bool:
    """A Rust file cargo-mutants never mutates, but whose change can change the result."""
    parts = path.split("/")
    name = parts[-1]
    return path.endswith(".rs") and (
        "tests" in parts[:-1] or "benches" in parts[:-1] or name.endswith("_test.rs")
    )


def full_reasons(files: list[str]) -> tuple[bool, bool, list[str]]:
    """(go_full, rust_full, reasons): whether the change forces a non-narrowed plan."""
    go_full = rust_full = False
    reasons: list[str] = []
    for f in sorted(set(files)):
        base = f.rsplit("/", 1)[-1]
        if f in GATE_LOGIC_PATHS:
            go_full = rust_full = True
            reasons.append(f"{f}: gate logic changed")
        elif f in GO_FULL_PATHS:
            go_full = True
            reasons.append(f"{f}: Go dependency/mutation configuration changed")
        elif base in RUST_FULL_BASENAMES or f.startswith(RUST_FULL_PREFIXES):
            rust_full = True
            reasons.append(f"{f}: Rust dependency/build/mutation configuration changed")
        elif is_rust_test_file(f):
            rust_full = True
            reasons.append(f"{f}: Rust test file changed")
    return go_full, rust_full, reasons


def nearest_go_mod_dir(path_dir: str, module_dirs: set[str]) -> str | None:
    """The directory of the closest go.mod at or above path_dir ('' is the root)."""
    d = path_dir
    while True:
        if d in module_dirs:
            return d
        if d == "":
            return None
        d = d.rsplit("/", 1)[0] if "/" in d else ""


def go_source_dirs(tracked: list[str], module_dirs: set[str]) -> set[str]:
    """Directories of the root module that hold non-test, non-testdata Go source at head."""
    dirs = set()
    for f in tracked:
        parts = f.split("/")
        if not f.endswith(".go") or f.endswith("_test.go"):
            continue
        if "testdata" in parts or "vendor" in parts:
            continue
        d = f.rsplit("/", 1)[0] if "/" in f else ""
        if nearest_go_mod_dir(d, module_dirs) == "":
            dirs.add(d)
    return dirs


def go_packages(
    files: list[str], module_dirs: set[str], source_dirs: set[str] | None = None
) -> list[str]:
    """The packages to mutate for the changed files.

    A non-test .go file selects its own package. A `_test.go` file selects the package it
    tests, and a file under `testdata/` the package that owns the testdata (a test change is
    a change of the mutation result). A deleted file selects its package when the package
    still has source at head. source_dirs (from go_source_dirs) says which directories have
    non-test source; None means "assume every directory has".
    """
    pkgs = set()
    for f in files:
        parts = f.split("/")
        if "vendor" in parts:
            continue
        if "testdata" in parts:
            d = "/".join(parts[: parts.index("testdata")])
        elif f.endswith(".go"):
            d = f.rsplit("/", 1)[0] if "/" in f else ""
        else:
            continue
        if nearest_go_mod_dir(d, module_dirs) != "":
            continue  # a nested module (e.g. tools/goastdump) is not the root module
        if source_dirs is not None and d not in source_dirs:
            continue  # nothing to mutate there (a package that only has tests, or is gone)
        pkgs.add("." if d == "" else "./" + d)
    return sorted(pkgs)


def load_costs(path: str) -> tuple[dict[str, float], float] | None:
    """(seconds per package, default seconds) from a cost-hint file, None if absent."""
    try:
        doc = json.loads(Path(path).read_text())
        pkgs = {str(k): float(v) for k, v in doc["packages"].items()}
        return pkgs, float(doc.get("default_seconds", 10))
    except (OSError, ValueError, KeyError, TypeError):
        return None


def chunk(
    pkgs: list[str],
    max_jobs: int,
    costs: tuple[dict[str, float], float] | None = None,
) -> list[dict]:
    """Spread the packages over at most max_jobs jobs.

    Without cost hints: round-robin. With them: the most expensive package first, each
    into the least loaded job (so the heavy packages do not share a job). A wrong hint
    only makes the jobs uneven; every package is in exactly one job either way.
    """
    if not pkgs:
        return []
    n = min(max_jobs, len(pkgs))
    groups: list[list[str]] = [[] for _ in range(n)]
    if costs is None:
        for i, p in enumerate(pkgs):
            groups[i % n].append(p)
    else:
        per, default = costs
        load = [0.0] * n
        for p in sorted(pkgs, key=lambda x: (-per.get(x, default), x)):
            i = min(range(n), key=lambda j: (load[j], j))
            groups[i].append(p)
            load[i] += per.get(p, default)
        groups = [sorted(g) for g in groups]
    return [{"id": i, "pkgs": " ".join(g)} for i, g in enumerate(groups)]


def rust_changed(files: list[str]) -> bool:
    for f in files:
        base = f.rsplit("/", 1)[-1]
        if (
            f.endswith(".rs")
            or base in RUST_FULL_BASENAMES
            or f.startswith(RUST_FULL_PREFIXES)
        ):
            return True
    return False


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base")
    ap.add_argument("--head", required=True)
    ap.add_argument("--go-max-jobs", type=int, default=6)
    ap.add_argument(
        "--costs",
        default=".mutation-cost.json",
        help="cost hints used to spread the Go packages (round-robin if absent)",
    )
    ap.add_argument("--all", action="store_true", help="plan everything at --head")
    a = ap.parse_args()
    if not a.all and not a.base:
        ap.error("--base is required unless --all is given")

    tracked = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", a.head],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.splitlines()
    module_dirs = {
        (p.rsplit("/", 1)[0] if "/" in p else "")
        for p in tracked
        if p == "go.mod" or p.endswith("/go.mod")
    }

    files = tracked if a.all else changed_files(a.base, a.head)
    source_dirs = go_source_dirs(tracked, module_dirs)
    go_full, rust_full, reasons = full_reasons(files)
    if a.all:
        go_full = rust_full = True
        reasons = ["--all: everything is planned"]
    # An escalated Go plan covers every package of the root module that has source.
    pkgs = go_packages(tracked if go_full else files, module_dirs, source_dirs)
    go = chunk(pkgs, a.go_max_jobs, load_costs(a.costs))
    result = {
        "go": json.dumps(go),
        "go_pkgs": " ".join(pkgs),
        "rust": "true" if (rust_full or rust_changed(files)) else "false",
        "go_full": "true" if go_full else "false",
        "rust_full": "true" if rust_full else "false",
        "full_reasons": "; ".join(reasons),
    }

    print(f"changed files: {len(files)}", file=sys.stderr)
    print(f"go: {result['go']}", file=sys.stderr)
    print(f"rust: {result['rust']}", file=sys.stderr)
    print(
        f"go_full={result['go_full']} rust_full={result['rust_full']}", file=sys.stderr
    )
    for r in reasons:
        print(f"full: {r}", file=sys.stderr)

    out = os.environ.get("GITHUB_OUTPUT")
    lines = "".join(f"{k}={v}\n" for k, v in result.items())
    if out:
        with open(out, "a", encoding="utf-8") as fh:
            fh.write(lines)
    else:
        sys.stdout.write(lines)
    return 0


if __name__ == "__main__":
    sys.exit(main())
