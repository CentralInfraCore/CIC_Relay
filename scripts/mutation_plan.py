#!/usr/bin/env python3
"""Plan the diff-mode mutation gate of a pull request.

Discovers, from the files a pull request changes, what has to be mutated:

* Go: every package of the root module whose non-test source changed. Packages are
  discovered from the diff, never listed by hand, so new code is gated by default.
* Rust: whether any Rust source or manifest changed. The mutants themselves are
  selected by `cargo mutants --in-diff` in the workflow.

The Go packages are spread over at most --go-max-jobs jobs (round-robin), so the wall
time stays bounded while the code base grows.

With --all the diff is ignored and everything at --head is planned (the weekly FULL
and the first nightly, which has no earlier run to compare with).

Output (GITHUB_OUTPUT, or stdout when it is unset):
  go       JSON list of {"id": n, "pkgs": "./a ./b"}   ("[]" when nothing to do)
  go_pkgs  the same packages as one space-separated line
  rust     "true" | "false"
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path


def changed_files(base: str, head: str) -> list[str]:
    out = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACMR", f"{base}...{head}"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [line for line in out.splitlines() if line]


def nearest_go_mod_dir(path_dir: str, module_dirs: set[str]) -> str | None:
    """The directory of the closest go.mod at or above path_dir ('' is the root)."""
    d = path_dir
    while True:
        if d in module_dirs:
            return d
        if d == "":
            return None
        d = d.rsplit("/", 1)[0] if "/" in d else ""


def go_packages(files: list[str], module_dirs: set[str]) -> list[str]:
    pkgs = set()
    for f in files:
        if not f.endswith(".go") or f.endswith("_test.go"):
            continue
        parts = f.split("/")
        if "testdata" in parts or "vendor" in parts:
            continue
        d = f.rsplit("/", 1)[0] if "/" in f else ""
        if nearest_go_mod_dir(d, module_dirs) != "":
            continue  # a nested module (e.g. tools/goastdump) is not the root module
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
        if f.endswith(".rs") or base in (
            "Cargo.toml",
            "Cargo.lock",
            "rust-toolchain.toml",
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
    pkgs = go_packages(files, module_dirs)
    go = chunk(pkgs, a.go_max_jobs, load_costs(a.costs))
    result = {
        "go": json.dumps(go),
        "go_pkgs": " ".join(pkgs),
        "rust": "true" if rust_changed(files) else "false",
    }

    print(f"changed files: {len(files)}", file=sys.stderr)
    print(f"go: {result['go']}", file=sys.stderr)
    print(f"rust: {result['rust']}", file=sys.stderr)

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
