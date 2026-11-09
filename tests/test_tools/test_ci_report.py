"""One issue per ongoing problem of a scheduled run (scripts/ci_report.sh)."""

import os
import stat
import subprocess

import pytest

SCRIPT = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "../../scripts/ci_report.sh")
)

# A stand-in for gh: records every call; `issue list` answers with $FAKE_OPEN.
FAKE_GH = """#!/usr/bin/env bash
echo "gh $*" >> "$FAKE_LOG"
if [ "$1" = "issue" ] && [ "$2" = "list" ]; then
  printf '%s' "${FAKE_OPEN:-}"
fi
exit 0
"""


def run(tmp_path, status, open_issue=""):
    gh = tmp_path / "gh"
    gh.write_text(FAKE_GH)
    gh.chmod(gh.stat().st_mode | stat.S_IEXEC)
    log = tmp_path / "calls.log"
    env = {
        **os.environ,
        "PATH": f"{tmp_path}:{os.environ['PATH']}",
        "FAKE_LOG": str(log),
        "FAKE_OPEN": open_issue,
        "GITHUB_REPOSITORY": "o/r",
        "GITHUB_SERVER_URL": "https://github.com",
        "GITHUB_RUN_ID": "42",
        "GITHUB_SHA": "abc",
        "GITHUB_EVENT_NAME": "schedule",
    }
    result = subprocess.run(
        ["bash", SCRIPT, "nightly-failure", "Nightly failed", status, "go=failure"],
        env=env,
        capture_output=True,
        text=True,
    )
    calls = log.read_text().splitlines() if log.exists() else []
    return result, calls


def verbs(calls):
    return [" ".join(c.split()[1:3]) for c in calls]


def test_failure_without_an_open_issue_creates_one(tmp_path):
    result, calls = run(tmp_path, "failure")
    assert result.returncode == 0
    assert "issue create" in verbs(calls)
    assert "issue comment" not in verbs(calls)
    create = next(c for c in calls if " issue create" in c)
    assert "--label nightly-failure" in create


def test_failure_with_an_open_issue_comments_instead_of_a_second_issue(tmp_path):
    result, calls = run(tmp_path, "failure", open_issue="7")
    assert result.returncode == 0
    assert "issue comment" in verbs(calls)
    assert "issue create" not in verbs(calls)
    assert any(" issue comment 7 " in c for c in calls)


def test_success_closes_the_open_issue(tmp_path):
    result, calls = run(tmp_path, "success", open_issue="7")
    assert result.returncode == 0
    assert any(" issue close 7 " in c for c in calls)


def test_success_without_an_open_issue_does_nothing(tmp_path):
    result, calls = run(tmp_path, "success")
    assert result.returncode == 0
    assert "issue close" not in verbs(calls) and "issue create" not in verbs(calls)


@pytest.mark.parametrize("status", ["", "failed", "ok"])
def test_unknown_status_is_an_error(tmp_path, status):
    result, calls = run(tmp_path, status)
    assert result.returncode == 2
    assert "issue create" not in verbs(calls)
