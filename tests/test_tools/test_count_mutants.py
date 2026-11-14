"""scripts/count_mutants.py: a failed mutant listing is never "zero mutants" (PR-02)."""

import os
import stat
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import count_mutants  # noqa: E402

COUNT = os.path.join(SCRIPTS, "count_mutants.py")


def fake(tmp_path, body):
    """An executable shell script standing in for `cargo mutants --list`."""
    f = tmp_path / "fake-list"
    f.write_text("#!/usr/bin/env bash\n" + body + "\n")
    f.chmod(f.stat().st_mode | stat.S_IEXEC)
    return str(f)


def run(cmd, *opts):
    return subprocess.run(
        [sys.executable, COUNT, *opts, "--", *cmd], capture_output=True, text=True
    )


def test_valid_empty_listing_is_zero(tmp_path):
    # case A of the PR-01 reproduction: the diff applies, no mutable code, exit 0, no output
    r = run([fake(tmp_path, "exit 0")])
    assert (r.returncode, r.stdout) == (0, "0\n")


def test_listing_counts_the_non_empty_lines(tmp_path):
    r = run([fake(tmp_path, "printf 'a\\n\\nb\\n  \\nc\\n'")])
    assert (r.returncode, r.stdout) == (0, "3\n")


@pytest.mark.parametrize("status", [1, 6, 255])
def test_failed_listing_is_not_zero_mutants(tmp_path, status):
    # cases B (exit 6, unreadable diff) and C (exit 1, broken config): empty stdout, non-zero
    r = run([fake(tmp_path, f"echo 'error: nope' >&2; exit {status}")])
    assert r.returncode == 1
    assert r.stdout == ""
    assert f"exit status {status}" in r.stderr


def test_partial_listing_then_failure_is_a_failure(tmp_path):
    # the listing dies half way: the lines already printed must not become a (smaller) count
    r = run([fake(tmp_path, "printf 'a\\nb\\n'; exit 2")])
    assert r.returncode == 1 and r.stdout == ""


def test_missing_command_is_a_failure(tmp_path):
    r = run([str(tmp_path / "does-not-exist")])
    assert r.returncode == 1 and r.stdout == ""
    assert "cannot run" in r.stderr


def test_no_command_is_a_usage_error():
    r = subprocess.run([sys.executable, COUNT, "--"], capture_output=True, text=True)
    assert r.returncode == 2 and r.stdout == ""


def test_listing_is_kept_as_evidence(tmp_path):
    out = tmp_path / "list.txt"
    r = run([fake(tmp_path, "printf 'x\\ny\\n'")], "--out", str(out))
    assert r.stdout == "2\n" and out.read_text() == "x\ny\n"


def test_count_listing_raises_on_failure(tmp_path):
    with pytest.raises(RuntimeError):
        count_mutants.count_listing([fake(tmp_path, "exit 6")], str(tmp_path / "o"))
