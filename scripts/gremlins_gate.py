#!/usr/bin/env python3
"""Run gremlins on Go packages with a per-package ratchet threshold.

A package that is not in .mutation-baseline.json must reach efficacy >= 90 and
mutant coverage >= 90. A package that is listed (it was below 90 when the baseline
was taken) must not get worse than its baseline minus the weight of one mutant, so
quality can only go up. A new package is never listed, so it starts at 90.

gremlins v0.6.0 does not enforce thresholds given on the command line (measured:
exit 0 at 70% with --threshold-efficacy 100), so the thresholds are written to a
temporary --config file, and the numbers it prints are checked here as well. Output
that cannot be parsed fails the package (fail-closed). All packages are run; the
exit status is non-zero if any package failed.

usage: gremlins_gate.py [--baseline FILE] [--workers N] PKG [PKG ...]
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

DEFAULT = 90.0
EPS = 1e-9


def thresholds(entry: dict | None) -> tuple[float, float]:
    """(efficacy, mutant coverage) a package has to reach."""
    if entry is None:
        return DEFAULT, DEFAULT
    k, lived, nc = entry["killed"], entry["lived"], entry["not_covered"]
    cov, tot = k + lived, k + lived + nc
    eff_b = 100.0 * k / cov if cov else 0.0
    mc_b = 100.0 * cov / tot if tot else 100.0
    eff = DEFAULT if eff_b >= DEFAULT else max(0.0, eff_b - (100.0 / cov if cov else 0))
    mc = DEFAULT if mc_b >= DEFAULT else max(0.0, mc_b - (100.0 / tot if tot else 0))
    return round(eff, 2), round(mc, 2)


def measured(text: str) -> tuple[int, int, int, int] | None:
    """(killed, lived, not covered, timed out) from the gremlins summary."""
    m = re.search(r"Killed: (\d+), Lived: (\d+), Not covered: (\d+)", text)
    if not m:
        return None
    t = re.search(r"Timed out: (\d+)", text)
    return (
        int(m.group(1)),
        int(m.group(2)),
        int(m.group(3)),
        int(t.group(1)) if t else 0,
    )


def run_one(pkg: str, entry: dict | None, workers: int) -> bool:
    eff_t, mc_t = thresholds(entry)
    with tempfile.TemporaryDirectory() as td:
        cfg = Path(td) / "gremlins.yaml"
        # a hair below, so that the equality case is decided by the check below
        cfg.write_text(
            "unleash:\n  threshold:\n"
            f"    efficacy: {max(0.0, eff_t - 0.01)}\n"
            f"    mutant-coverage: {max(0.0, mc_t - 0.01)}\n"
        )
        proc = subprocess.run(
            [
                "gremlins",
                "unleash",
                "--workers",
                str(workers),
                "--config",
                str(cfg),
                pkg,
            ],
            capture_output=True,
            text=True,
            env={
                **os.environ,
                "GOFLAGS": (os.environ.get("GOFLAGS", "") + " -count=1").strip(),
            },
        )
    out = re.sub(r"\x1b\[[0-9;]*m", "", proc.stdout + proc.stderr)
    nums = measured(out)
    tag = "baseline" if entry else "default"
    if nums is None and "No results to report" in out:
        print(f"ok   {pkg}: no mutants")
        return True
    if nums is None:
        print(f"FAIL {pkg}: gremlins output not understood (exit {proc.returncode})")
        print(out[-2000:])
        return False
    k, lived, nc, timed_out = nums
    lived += timed_out  # a timed-out mutant was not killed by a test
    cov, tot = k + lived, k + lived + nc
    if tot == 0:
        print(f"ok   {pkg}: no mutants")
        return True
    eff = 100.0 * k / cov if cov else 0.0
    mc = 100.0 * cov / tot
    problems = []
    if eff + EPS < eff_t:
        problems.append(f"efficacy {eff:.2f} < {eff_t}")
    if mc + EPS < mc_t:
        problems.append(f"mutant coverage {mc:.2f} < {mc_t}")
    if proc.returncode != 0 and not problems:
        problems.append(f"gremlins exit {proc.returncode}")
    line = (
        f"{pkg}: killed {k}, lived {lived} (of which timed out {timed_out}), "
        f"not covered {nc}; "
        f"efficacy {eff:.2f} (need {eff_t}), coverage {mc:.2f} (need {mc_t}) [{tag}]"
    )
    if problems:
        print("FAIL " + line + " -> " + "; ".join(problems))
        return False
    print("ok   " + line)
    return True


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--baseline", default=".mutation-baseline.json")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("pkgs", nargs="+")
    a = ap.parse_args()
    baseline = json.loads(Path(a.baseline).read_text())["packages"]
    results = [run_one(p, baseline.get(p), a.workers) for p in a.pkgs]
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
