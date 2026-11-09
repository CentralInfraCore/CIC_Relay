"""The mutation ratchet baseline may only get stricter (scripts/baseline_guard.py)."""

import json
import os
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

from baseline_guard import check, load  # noqa: E402

GUARD = os.path.join(SCRIPTS, "baseline_guard.py")

# 13 killed / 7 lived / 1 not covered -> efficacy 65.0 (need 60.0), coverage 95.2
GIT = {"killed": 13, "lived": 7, "not_covered": 1}


def doc(**pkgs):
    return {"packages": pkgs}


def write(tmp_path, name, content):
    p = tmp_path / name
    p.write_text(content if isinstance(content, str) else json.dumps(content))
    return str(p)


def run(base, head):
    return subprocess.run(
        [sys.executable, GUARD, "--base", base, "--head", head],
        capture_output=True,
        text=True,
        cwd=SCRIPTS,
    )


def test_unchanged_baseline_passes():
    assert check({"./a": GIT}, {"./a": GIT}) == []


def test_raising_an_entry_passes():
    better = {"killed": 19, "lived": 1, "not_covered": 1}
    assert check({"./a": GIT}, {"./a": better}) == []


def test_removing_an_entry_passes():
    assert check({"./a": GIT}, {}) == []


def test_lowering_efficacy_fails():
    worse = {"killed": 8, "lived": 12, "not_covered": 1}
    problems = check({"./a": GIT}, {"./a": worse})
    assert problems and "efficacy requirement lowered" in problems[0]


def test_lowering_mutant_coverage_fails():
    worse = {"killed": 13, "lived": 7, "not_covered": 40}
    problems = check({"./a": GIT}, {"./a": worse})
    assert any("mutant coverage requirement lowered" in p for p in problems)


def test_new_entry_for_a_new_package_fails():
    problems = check({"./a": GIT}, {"./a": GIT, "./new": GIT})
    assert len(problems) == 1 and "./new" in problems[0]


@pytest.mark.parametrize(
    "content",
    [
        "not json",
        {"packages": []},
        {"packages": {"./a": {"killed": -1, "lived": 0, "not_covered": 0}}},
        {"packages": {"./a": {"killed": 1, "lived": 0}}},
        {"packages": {"./a": {"killed": True, "lived": 0, "not_covered": 0}}},
    ],
)
def test_malformed_head_baseline_fails(tmp_path, content):
    base = write(tmp_path, "base.json", doc(**{"./a": GIT}))
    head = write(tmp_path, "head.json", content)
    result = run(base, head)
    assert result.returncode == 1 and "FAIL" in result.stdout


def test_missing_head_baseline_fails(tmp_path):
    base = write(tmp_path, "base.json", doc(**{"./a": GIT}))
    result = run(base, str(tmp_path / "gone.json"))
    assert result.returncode == 1 and "cannot be read" in result.stdout


def test_cli_exit_codes(tmp_path):
    base = write(tmp_path, "base.json", doc(**{"./a": GIT}))
    same = write(tmp_path, "same.json", doc(**{"./a": GIT}))
    low = write(
        tmp_path,
        "low.json",
        doc(**{"./a": {"killed": 8, "lived": 12, "not_covered": 1}}),
    )
    assert run(base, same).returncode == 0
    assert run(base, low).returncode == 1


def test_load_reads_the_real_baseline():
    root = os.path.join(SCRIPTS, "..", ".mutation-baseline.json")
    assert len(load(root)) > 0
