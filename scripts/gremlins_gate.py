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

Traps closed here (all measured):
(1) The Go test cache: gremlins derives the time limit of a mutant from the baseline test
run; a cached baseline takes ~0 s and then nearly every mutant times out (core/nexus/git:
13 killed / 7 lived on the first run, then 9 killed / 0 lived / 11 timed out, which reads
as 100%). GOFLAGS gets -count=1.
(2) Timeouts. A mutant that makes a test hang (a loop condition, pkg/merkle/merkle.go:64)
times out, and as in mutation testing generally that is a detection: killed + timed out is
what counts as detected. That sum is stable (core/nexus/iac: 117+1 or 110+8, always 118,
only the split follows the load of the machine), so it is used. But a run in which the
timeouts dominate (more than max(3, 20% of the mutants)) says nothing about the tests and
is rejected as untrustworthy. gremlins itself leaves timed-out mutants out of its efficacy.
(3) Parallelism: more workers than the machine can serve slow the tests down; the default
is half of the CPUs.

With --report FILE the verdict of every package is also written as a machine-readable
report (gremlins-report/1: one row per package, with killed / lived / not covered / timed
out counted on their own), so that scripts/quality_gate.py can check that the packages that
were planned are exactly the packages that were measured. The thresholds are not affected.

usage: gremlins_gate.py [--baseline FILE] [--workers N]
                        [--report FILE --chunk N --plan-digest SHA256] PKG [PKG ...]
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


def attempt(pkg: str, eff_t: float, mc_t: float, workers: int):
    """One gremlins run; (process, cleaned output, parsed numbers or None)."""
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
    return proc, out, measured(out)


def timeouts_dominate(nums: tuple[int, int, int, int]) -> bool:
    """Too many timed-out mutants for the run to say anything about the tests."""
    k, lived, nc, timed_out = nums
    return timed_out > max(3, 0.2 * (k + lived + nc + timed_out))


def evaluate(
    nums: tuple[int, int, int, int], eff_t: float, mc_t: float
) -> tuple[float, float, list[str]]:
    """(efficacy, mutant coverage, problems) of a gremlins result."""
    k, lived, nc, timed_out = nums
    detected = k + timed_out
    cov = detected + lived
    tot = cov + nc
    eff = 100.0 * detected / cov if cov else 0.0
    mc = 100.0 * cov / tot if tot else 100.0
    problems = []
    if timeouts_dominate(nums):
        problems.append(
            f"{timed_out} of {tot} mutants timed out: the time limit says "
            "nothing about the tests"
        )
    if eff + EPS < eff_t:
        problems.append(f"efficacy {eff:.2f} < {eff_t}")
    if mc + EPS < mc_t:
        problems.append(f"mutant coverage {mc:.2f} < {mc_t}")
    return eff, mc, problems


def row(pkg: str, status: str, nums: tuple[int, int, int, int] | None = None) -> dict:
    """One package verdict of the machine-readable report."""
    k, lived, nc, timed_out = nums or (0, 0, 0, 0)
    return {
        "pkg": pkg,
        "status": status,
        "killed": k,
        "lived": lived,
        "not_covered": nc,
        "timed_out": timed_out,
    }


def run_one(pkg: str, entry: dict | None, workers: int) -> tuple[bool, dict]:
    eff_t, mc_t = thresholds(entry)
    proc, out, nums = attempt(pkg, eff_t, mc_t, workers)
    retried = ""
    if nums is not None and timeouts_dominate(nums) and workers > 1:
        # more workers than the machine serves slow the tests down: once more with one
        proc2, out2, nums2 = attempt(pkg, eff_t, mc_t, 1)
        if nums2 is not None:
            retried = f"; retried with 1 worker (timed out {nums[3]} -> {nums2[3]})"
            proc, out, nums = proc2, out2, nums2
    tag = "baseline" if entry else "default"
    if nums is None and "No results to report" in out:
        print(f"ok   {pkg}: no mutants")
        return True, row(pkg, "no-mutants")
    if nums is None:
        print(f"FAIL {pkg}: gremlins output not understood (exit {proc.returncode})")
        print(out[-2000:])
        return False, row(pkg, "unparseable")
    k, lived, nc, timed_out = nums
    if k + lived + nc + timed_out == 0:
        print(f"ok   {pkg}: no mutants")
        return True, row(pkg, "no-mutants")
    eff, mc, problems = evaluate(nums, eff_t, mc_t)
    if proc.returncode != 0 and not problems:
        problems.append(f"gremlins exit {proc.returncode}")
    line = (
        f"{pkg}: killed {k}, timed out {timed_out}, lived {lived}, not covered {nc}; "
        f"efficacy {eff:.2f} (need {eff_t}), coverage {mc:.2f} (need {mc_t}) [{tag}]{retried}"
    )
    if problems:
        print("FAIL " + line + " -> " + "; ".join(problems))
        return False, row(pkg, "fail", nums)
    print("ok   " + line)
    return True, row(pkg, "ok", nums)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--baseline", default=".mutation-baseline.json")
    ap.add_argument("--workers", type=int, default=max(1, (os.cpu_count() or 2) // 2))
    ap.add_argument(
        "--report", help="write the per-package verdicts here (gremlins-report/1)"
    )
    ap.add_argument("--chunk", type=int, default=0, help="the chunk id, for the report")
    ap.add_argument("--plan-digest", default="", help="the plan the report belongs to")
    ap.add_argument("pkgs", nargs="+")
    a = ap.parse_args()
    baseline = json.loads(Path(a.baseline).read_text())["packages"]
    results = [run_one(p, baseline.get(p), a.workers) for p in a.pkgs]
    if a.report:
        Path(a.report).write_text(
            json.dumps(
                {
                    "schema": "gremlins-report/1",
                    "plan_digest": a.plan_digest,
                    "chunk": a.chunk,
                    "packages": [r for _, r in results],
                },
                indent=1,
                sort_keys=True,
            )
            + "\n",
            encoding="utf-8",
        )
    return 0 if all(ok for ok, _ in results) else 1


if __name__ == "__main__":
    sys.exit(main())
