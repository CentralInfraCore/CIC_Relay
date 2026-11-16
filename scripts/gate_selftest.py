#!/usr/bin/env python3
"""Self-test of the REAL quality gates (report-only): does the gate that guards the repository turn red when it must?

PR-06 probes 1 and 2 of the #169 self-testing list:

  P1  a deliberately weakened test suite lowers the coverage and the coverage gate turns red
      (Go: the per-package threshold step of verify.yml; Rust: `cargo llvm-cov --fail-under-lines`)
  P2  a deliberately surviving mutant turns the mutation gate red
      (Go: scripts/gremlins_gate.py, the per-package ratchet; Rust: `cargo mutants --in-diff`, as the PR gate runs it)

What makes this more than a seeded exit code:
  - the gate is the REAL one. The Go coverage gate is the `run:` script of the workflow step, extracted from the
    workflow file and executed unchanged (its thresholds are the ones in the file; nothing here sets a threshold);
    the other three are the very commands of the workflows, run with the same tools.
  - every defect runs in an ISOLATED COPY of the checkout; the checkout itself is never written (its `git status`
    is recorded before and after).
  - every defect has a POSITIVE CONTROL next to it: the same gate on the unmodified copy (P1) or on the same planted
    code WITH an asserting test (P2) must be green, so a gate that is red for another reason is not counted.
  - the expected failure must be the right one (a marker in the gate's own output), not just a non-zero exit.

Not covered here: the plan/completeness aggregation of the PR workflow (mutation-pr.yml `gate`): the P2 Rust case runs
the same `cargo mutants --in-diff` command as the shard job, not the whole pipeline. Said in the report.

Usage: gate_selftest.py --src CHECKOUT [--work DIR] [--only go-cov,rust-cov,go-mut,rust-mut]
"""

import argparse
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

import yaml  # type: ignore[import-untyped]

IGNORE = shutil.ignore_patterns(
    ".git", "target", "node_modules", "__pycache__", "*.pyc", ".venv"
)


class SelfTestError(Exception):
    pass


def extract_step(workflow: Path, job: str, step: str, expr: dict[str, str]) -> str:
    """The `run:` script of one step of a workflow, byte for byte, with the `${{ ... }}` expressions of `expr`
    filled in. Any other expression is an error: a script the harness cannot run unchanged is not run.
    """
    doc = yaml.safe_load(workflow.read_text(encoding="utf-8"))
    steps = (doc.get("jobs", {}).get(job) or {}).get("steps") or []
    hits = [s for s in steps if s.get("name") == step]
    if len(hits) != 1 or "run" not in hits[0]:
        raise SelfTestError(
            f"{workflow.name}: job {job!r} has {len(hits)} step(s) named {step!r} with a run script"
        )
    script = hits[0]["run"]

    def fill(m: re.Match) -> str:
        key = m.group(1).strip()
        if key not in expr:
            raise SelfTestError(
                f"{workflow.name}/{job}/{step}: the expression {key!r} cannot be filled in"
            )
        return expr[key]

    return re.sub(r"\$\{\{(.*?)\}\}", fill, script)


def copy_tree(src: Path, dst: Path) -> None:
    shutil.copytree(src, dst, ignore=IGNORE, symlinks=True)


def run(cmd: list[str], cwd: Path, env: dict | None = None) -> tuple[int, str]:
    import os

    e = dict(os.environ)
    e.update(env or {})
    p = subprocess.run(cmd, cwd=cwd, env=e, capture_output=True, text=True, check=False)
    return p.returncode, (p.stdout or "") + (p.stderr or "")


def status(src: Path) -> str:
    return subprocess.run(
        ["git", "-C", str(src), "status", "--porcelain", "--untracked-files=no"],
        capture_output=True,
        text=True,
        check=False,
    ).stdout


@dataclass
class Result:
    case: str
    title: str
    control_ok: bool
    control_got: str
    defect_ok: bool
    defect_got: str

    @property
    def ok(self) -> bool:
        return self.control_ok and self.defect_ok

    def lines(self) -> list[str]:
        return [
            f"{'OK  ' if self.control_ok else 'FAIL'} {self.case:<8} control: {self.control_got}",
            f"{'OK  ' if self.defect_ok else 'FAIL'} {self.case:<8} {self.title}: {self.defect_got}",
        ]


# ---------------------------------------------------------------------------------------------- the planted code

GO_PLANTED = """package merkle

// planted by the gate self-test: arithmetic and comparison operators the mutation tool can change.
func Planted(a, b int) int {
	if a > b {
		return a - b
	}
	if a < b {
		return b - a
	}
	return a + b
}
"""
GO_PLANTED_TEST_BLIND = """package merkle

import "testing"

func TestPlantedRuns(t *testing.T) {
	_ = Planted(3, 1)
	_ = Planted(1, 3)
	_ = Planted(3, 3)
}
"""
GO_PLANTED_TEST_ASSERTING = """package merkle

import "testing"

func TestPlantedRuns(t *testing.T) {
	if got := Planted(3, 1); got != 2 {
		t.Fatalf("Planted(3,1)=%d", got)
	}
	if got := Planted(1, 3); got != 2 {
		t.Fatalf("Planted(1,3)=%d", got)
	}
	if got := Planted(3, 3); got != 6 {
		t.Fatalf("Planted(3,3)=%d", got)
	}
}
"""
GO_EMPTY_TEST = """package merkle

import "testing"

func TestNothing(t *testing.T) {}
"""
RUST_PLANTED = """/// planted by the gate self-test: arithmetic and comparison operators the mutation tool can change.
pub fn planted(a: i64, b: i64) -> i64 {
    if a > b {
        a - b
    } else if a < b {
        b - a
    } else {
        a + b
    }
}
"""
RUST_TEST_BLIND = """
#[cfg(test)]
mod planted_tests {
    use super::planted;

    #[test]
    fn runs() {
        let _ = planted(3, 1);
        let _ = planted(1, 3);
        let _ = planted(3, 3);
    }
}
"""
RUST_TEST_ASSERTING = """
#[cfg(test)]
mod planted_tests {
    use super::planted;

    #[test]
    fn computes() {
        assert_eq!(planted(3, 1), 2);
        assert_eq!(planted(1, 3), 2);
        assert_eq!(planted(3, 3), 6);
    }
}
"""


# ---------------------------------------------------------------------------------------------- the cases


def case_go_cov(src: Path, work: Path) -> Result:
    """P1, Go: the per-package coverage threshold step of verify.yml, run unchanged."""
    script = extract_step(
        src / ".github/workflows/verify.yml",
        "coverage",
        "Per-package coverage thresholds",
        {"steps.scope.outputs.pkgs": "./pkg/merkle"},
    )
    good, weak = work / "go-cov-control", work / "go-cov-weak"
    copy_tree(src, good)
    copy_tree(src, weak)
    for t in (weak / "pkg/merkle").glob("*_test.go"):
        t.unlink()
    (weak / "pkg/merkle/zz_test.go").write_text(GO_EMPTY_TEST)
    c_rc, c_out = run(["bash", "-c", script], good)
    d_rc, d_out = run(["bash", "-c", script], weak)
    pc = re.search(r"pkg/merkle: ([\d.]+)% \(min (\d+)%\)", c_out)
    wc = re.search(r"pkg/merkle: ([\d.]+)% \(min (\d+)%\)", d_out)
    return Result(
        "P1-go",
        "the test suite of pkg/merkle is weakened",
        c_rc == 0 and pc is not None,
        f"exit {c_rc}, {pc.group(0) if pc else 'no coverage line'}",
        d_rc != 0 and "FAIL: coverage below threshold" in d_out and wc is not None,
        f"exit {d_rc}, {wc.group(0) if wc else 'no coverage line'}"
        + (
            ", 'FAIL: coverage below threshold'"
            if "FAIL: coverage below threshold" in d_out
            else ", marker missing"
        ),
    )


def case_rust_cov(src: Path, work: Path) -> Result:
    """P1, Rust: `cargo llvm-cov --fail-under-lines 90` as verify.yml runs it."""
    script = extract_step(
        src / ".github/workflows/verify.yml",
        "rust",
        "Coverage (fail under 90% lines)",
        {},
    )
    good, weak = work / "rust-cov-control", work / "rust-cov-weak"
    copy_tree(src, good)
    copy_tree(src, weak)
    for f in weak.rglob("*.rs"):
        if "target" in f.parts:
            continue
        text = f.read_text(encoding="utf-8")
        if "#[test]" in text:
            f.write_text(text.replace("#[test]", "#[allow(dead_code)]"))
    for d in weak.glob("*/tests"):
        shutil.rmtree(d)
    c_rc, c_out = run(["bash", "-c", script], good)
    d_rc, d_out = run(["bash", "-c", script], weak)

    def lines_pct(out: str) -> float | None:
        # the TOTAL row of the llvm-cov table: regions, functions, lines (the third percentage), branches
        m = re.search(r"^TOTAL\s.*$", out, re.M)
        pcts = re.findall(r"([\d.]+)%", m.group(0)) if m else []
        return float(pcts[2]) if len(pcts) >= 3 else None

    cp, dp = lines_pct(c_out), lines_pct(d_out)
    return Result(
        "P1-rust",
        "every test of the Rust workspace is disabled",
        c_rc == 0 and cp is not None and cp >= 90,
        f"exit {c_rc}, line coverage {cp}%",
        d_rc != 0 and dp is not None and dp < 90,
        f"exit {d_rc}, line coverage {dp}% (the gate's own floor is 90)",
    )


def case_go_mut(src: Path, work: Path) -> Result:
    """P2, Go: scripts/gremlins_gate.py (the per-package ratchet), planted code without / with an asserting test."""
    cmd = [
        sys.executable,
        "scripts/gremlins_gate.py",
        "--report",
        "gremlins-report.json",
        "--chunk",
        "0",
        "--plan-digest",
        "selftest",
        "./pkg/merkle",
    ]
    env = {"GOFLAGS": "-mod=readonly -trimpath"}
    good, bad = work / "go-mut-control", work / "go-mut-survivor"
    for d, test in ((good, GO_PLANTED_TEST_ASSERTING), (bad, GO_PLANTED_TEST_BLIND)):
        copy_tree(src, d)
        (d / "pkg/merkle/planted.go").write_text(GO_PLANTED)
        (d / "pkg/merkle/planted_test.go").write_text(test)
    c_rc, c_out = run(cmd, good, env)
    d_rc, d_out = run(cmd, bad, env)
    summary = r"killed (\d+), timed out (\d+), lived (\d+), not covered (\d+); efficacy ([\d.]+)"
    lived = re.search(summary, d_out)
    killed = re.search(summary, c_out)
    return Result(
        "P2-go",
        "a planted function whose tests assert nothing (surviving mutants)",
        c_rc == 0 and killed is not None and int(killed.group(3)) == 0,
        f"exit {c_rc}, {killed.group(0) if killed else 'no summary'}",
        d_rc != 0
        and lived is not None
        and int(lived.group(3)) > 0
        and "< 90.0" in d_out,
        f"exit {d_rc}, {lived.group(0) if lived else 'no summary'}",
    )


def case_rust_mut(src: Path, work: Path) -> Result:
    """P2, Rust: `cargo mutants --workspace --in-diff` as the PR gate runs it, planted code without / with assertions."""
    good, bad = work / "rust-mut-control", work / "rust-mut-survivor"
    out: dict[str, tuple[int, str]] = {}
    for name, d, test in (
        ("c", good, RUST_TEST_ASSERTING),
        ("d", bad, RUST_TEST_BLIND),
    ):
        copy_tree(src, d)
        env = {
            "GIT_AUTHOR_NAME": "t",
            "GIT_AUTHOR_EMAIL": "t@example.invalid",
            "GIT_COMMITTER_NAME": "t",
            "GIT_COMMITTER_EMAIL": "t@example.invalid",
        }
        for c in (
            ["git", "init", "-q"],
            ["git", "add", "-A"],
            ["git", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base"],
        ):
            rc, o = run(c, d, env)
            if rc != 0:
                raise SelfTestError(f"{' '.join(c)} failed in the copy: {o[-300:]}")
        lib = d / "cic-canonical/src/lib.rs"
        planted = d / "cic-canonical/src/planted.rs"
        planted.write_text(RUST_PLANTED + test)
        lib.write_text(lib.read_text(encoding="utf-8") + "\npub mod planted;\n")
        run(["git", "add", "-A"], d, env)
        rc, diff = run(["git", "diff", "--cached", "--", "*.rs"], d, env)
        (d / "pr.diff").write_text(diff)
        out[name] = run(
            ["cargo", "mutants", "--workspace", "--in-diff", "pr.diff", "--no-shuffle"],
            d,
        )
    (c_rc, c_out), (d_rc, d_out) = out["c"], out["d"]
    missed = re.search(r"(\d+) missed", d_out)
    caught = re.search(r"(\d+) caught", c_out)
    return Result(
        "P2-rust",
        "a planted function whose tests assert nothing (missed mutants)",
        c_rc == 0 and caught is not None,
        f"exit {c_rc}, {caught.group(0) if caught else 'no summary'}",
        d_rc != 0 and missed is not None and int(missed.group(1)) > 0,
        f"exit {d_rc}, {missed.group(0) if missed else 'no summary'}",
    )


CASES = {
    "go-cov": case_go_cov,
    "rust-cov": case_rust_cov,
    "go-mut": case_go_mut,
    "rust-mut": case_rust_mut,
}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--src", required=True, help="the checkout (never written)")
    ap.add_argument(
        "--work", help="directory for the isolated copies (default: a temp dir)"
    )
    ap.add_argument("--only", help=f"comma separated subset of {','.join(CASES)}")
    a = ap.parse_args(argv)
    src = Path(a.src).resolve()
    names = a.only.split(",") if a.only else list(CASES)
    unknown = [n for n in names if n not in CASES]
    if unknown:
        ap.error(f"unknown case(s) {unknown}")
    work = Path(a.work or tempfile.mkdtemp(prefix="gate-selftest-"))
    work.mkdir(parents=True, exist_ok=True)
    before = status(src)
    ok = True
    for n in names:
        r = CASES[n](src, work)
        print("\n".join(r.lines()))
        ok = ok and r.ok
    untouched = status(src) == before
    print(
        f"{'OK  ' if untouched else 'FAIL'} the checkout was not modified (git status before == after)"
    )
    ok = ok and untouched
    print(
        "GATE SELF-TEST: every control green and every defect rejected by the real gate"
        if ok
        else "GATE SELF-TEST: A CONTROL OR A DEFECT DID NOT BEHAVE AS EXPECTED"
    )
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
