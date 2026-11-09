#!/usr/bin/env python3
"""Plan the diff-mode mutation gate of a pull request.

Discovers, from the files a pull request changes, what has to be mutated:

* Go: every package of the root module whose non-test source changed. Packages are
  discovered from the diff, never listed by hand, so new code is gated by default.
* Rust: whether any Rust source or manifest changed. The mutants themselves are
  selected by `cargo mutants --in-diff` in the workflow.

The Go packages are spread over at most --go-max-jobs jobs (round-robin), so the wall
time stays bounded while the code base grows.

Output (GITHUB_OUTPUT, or stdout when it is unset):
  go    JSON list of {"id": n, "pkgs": "./a ./b"}   ("[]" when nothing to do)
  rust  "true" | "false"
"""

import argparse
import json
import os
import subprocess
import sys


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


def chunk(pkgs: list[str], max_jobs: int) -> list[dict]:
    if not pkgs:
        return []
    n = min(max_jobs, len(pkgs))
    groups: list[list[str]] = [[] for _ in range(n)]
    for i, p in enumerate(pkgs):
        groups[i % n].append(p)
    return [{"id": i, "pkgs": " ".join(g)} for i, g in enumerate(groups)]


def rust_changed(files: list[str]) -> bool:
    for f in files:
        base = f.rsplit("/", 1)[-1]
        if f.endswith(".rs") or base in ("Cargo.toml", "Cargo.lock", "rust-toolchain.toml"):
            return True
    return False


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    ap.add_argument("--head", required=True)
    ap.add_argument("--go-max-jobs", type=int, default=6)
    a = ap.parse_args()

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

    files = changed_files(a.base, a.head)
    go = chunk(go_packages(files, module_dirs), a.go_max_jobs)
    result = {"go": json.dumps(go), "rust": "true" if rust_changed(files) else "false"}

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
