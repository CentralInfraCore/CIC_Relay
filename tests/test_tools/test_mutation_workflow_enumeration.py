"""The Rust mutant enumeration of the mutation workflows fails closed (PR-02).

The workflow step that counts the mutants and picks the shard count is run for real: the
`run:` text is taken from the workflow file and executed under `bash -e` (the default shell
of a GitHub Actions step: no pipefail) with a fake `cargo` that behaves like the three
cases measured in PR-01:

  A  valid, empty listing          exit 0, no output   -> step succeeds, shards=0
  B  unreadable --in-diff file     exit 6, no output   -> step MUST fail
  C  broken .cargo/mutants.toml    exit 1, no output   -> step MUST fail

Counter-test: the old step text (`| grep -c . || true`) is run through the same harness and
shown to return success with shards=0 for B and C, i.e. the harness does see the defect.
"""

import os
import re
import stat
import subprocess

import pytest
import yaml

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
WORKFLOWS = os.path.join(ROOT, ".github/workflows")

# (workflow, job) of the two steps that enumerate the Rust mutants
STEPS = [("mutation-pr.yml", "plan"), ("nightly.yml", "plan")]

OLD_STEP = """
shards=0
n=$(cargo mutants --workspace --list --in-diff /tmp/x.diff | grep -c . || true)
echo "rust mutants: $n"
if [ "$n" -gt 0 ]; then
  shards=$(( (n + 39) / 40 ))
fi
echo "shards=$shards" >> "$GITHUB_OUTPUT"
"""

FAKE_CARGO = """#!/usr/bin/env bash
case "$FAKE_MODE" in
  A) exit 0 ;;
  B) echo "ERROR cannot read the diff" >&2; exit 6 ;;
  C) echo "ERROR TOML parse error: duplicate key" >&2; exit 1 ;;
  N) printf 'm1\\nm2\\nm3\\n'; exit 0 ;;
esac
"""


def step_run(workflow, job, step_id):
    with open(os.path.join(WORKFLOWS, workflow)) as fh:
        doc = yaml.safe_load(fh)
    for step in doc["jobs"][job]["steps"]:
        if step.get("id") == step_id:
            return step["run"]
    raise AssertionError(f"{workflow}: no step '{step_id}' in job '{job}'")


def run_step(script, tmp_path, mode, **env):
    """Run a step body under `bash -e` in the repo root with a fake cargo; (exit, outputs)."""
    # an Actions expression (`${{ ... }}`) is not valid bash: expand the ones an older step
    # text used inline, so that a run can only fail for a reason that is about the logic
    script = script.replace("${{ steps.plan.outputs.rust }}", env.get("RUST", "true"))
    assert "${{" not in script, "an Actions expression the harness does not expand"
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    cargo = bindir / "cargo"
    cargo.write_text(FAKE_CARGO)
    cargo.chmod(cargo.stat().st_mode | stat.S_IEXEC)
    out = tmp_path / "github_output"
    out.write_text("")
    full_env = {
        **os.environ,
        "PATH": f"{bindir}:{os.environ['PATH']}",
        "FAKE_MODE": mode,
        "GITHUB_OUTPUT": str(out),
        "BASE_SHA": "HEAD",
        "HEAD_SHA": "HEAD",
        "HEAD": "HEAD",
        **env,
    }
    r = subprocess.run(
        ["bash", "-e", "-c", script],
        cwd=ROOT,
        env=full_env,
        capture_output=True,
        text=True,
    )
    outputs = dict(
        line.split("=", 1) for line in out.read_text().splitlines() if "=" in line
    )
    return r, outputs


@pytest.mark.parametrize("mode", ["B", "C"])
def test_counter_test_the_old_step_masks_a_failed_listing(tmp_path, mode):
    # the defect of PR-01 (D): success and zero shards although the listing failed
    r, outputs = run_step(OLD_STEP, tmp_path, mode)
    assert r.returncode == 0 and outputs["shards"] == "0"


def test_counter_test_the_old_step_is_right_only_for_the_valid_empty_case(tmp_path):
    r, outputs = run_step(OLD_STEP, tmp_path, "A")
    assert r.returncode == 0 and outputs["shards"] == "0"


@pytest.mark.parametrize("workflow, job", STEPS)
def test_valid_empty_listing_succeeds_with_zero_shards(tmp_path, workflow, job):
    script = step_run(workflow, job, "count")
    r, outputs = run_step(
        script, tmp_path, "A", BASE="HEAD", RUST="true", RUST_FULL="false"
    )
    assert r.returncode == 0, r.stderr
    assert outputs["shards"] == "0"


@pytest.mark.parametrize("workflow, job", STEPS)
@pytest.mark.parametrize("mode", ["B", "C"])
@pytest.mark.parametrize("rust_full", ["false", "true"])
def test_failed_listing_fails_the_step(tmp_path, workflow, job, mode, rust_full):
    script = step_run(workflow, job, "count")
    r, outputs = run_step(
        script, tmp_path, mode, BASE="HEAD", RUST="true", RUST_FULL=rust_full
    )
    assert r.returncode != 0, "a failed listing must not pass the planning step"
    # ... and it must fail BECAUSE the listing failed, not for an unrelated shell error
    assert "the Rust mutant listing failed" in r.stdout + r.stderr, (r.stdout, r.stderr)
    assert (
        "shards" not in outputs
    ), "no shard decision may be written after a failed listing"


@pytest.mark.parametrize("workflow, job", STEPS)
def test_a_real_listing_chooses_the_shards(tmp_path, workflow, job):
    script = step_run(workflow, job, "count")
    r, outputs = run_step(
        script, tmp_path, "N", BASE="HEAD", RUST="true", RUST_FULL="false"
    )
    assert r.returncode == 0, r.stderr
    assert outputs["shards"] == "1"


@pytest.mark.parametrize("workflow", ["mutation-pr.yml", "nightly.yml", "mutation.yml"])
def test_no_listing_is_piped_or_masked(workflow):
    text = open(os.path.join(WORKFLOWS, workflow)).read()
    assert not re.search(
        r"cargo mutants[^\n]*--list[^\n]*\|", text
    ), "a listing is piped"
    assert not re.search(
        r"cargo mutants[^\n]*--list[^\n]*\|\|", text
    ), "a listing is masked"
    assert "grep -c" not in text


@pytest.mark.parametrize("workflow", ["mutation-pr.yml", "nightly.yml"])
def test_every_listing_goes_through_a_checked_wrapper(workflow):
    # a listing is run by count_mutants.py (the count) or by quality_gate.py plan-rust (the plan
    # record); both check the exit status of the listing before they use its output
    text = open(os.path.join(WORKFLOWS, workflow)).read()
    listings = re.findall(r"cargo mutants[^\n]*--list", text)
    assert listings, "no listing found: the test has lost its subject"
    wrapped = re.findall(
        r"(?:count_mutants\.py|quality_gate\.py plan-rust)[^\n]*\\?\n?[^\n]*cargo mutants[^\n]*--list",
        text,
    )
    assert len(wrapped) == len(listings), "a listing runs outside the checked wrappers"


def test_the_shards_follow_the_planned_mode():
    # the listing and the shard run must agree: a FULL plan must not run narrowed shards
    for workflow in ("mutation-pr.yml", "nightly.yml"):
        text = open(os.path.join(WORKFLOWS, workflow)).read()
        assert "rust_full" in text
        assert re.search(
            r"RUST_FULL: \$\{\{ needs\.plan\.outputs\.rust_full \}\}", text
        )
