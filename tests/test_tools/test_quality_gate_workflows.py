"""The completeness check is wired into the mutation workflows and cannot be bypassed (PR-03).

The steps are run for real: the `run:` text is taken from the workflow YAML and executed under
`bash -e` (the default shell of an Actions step) with a fake `cargo` / a stub report script.

The counter-test of the old defect is `test_gate_is_red_when_the_completeness_check_is_not_`
`successful`: before this change the `gate` job looked at the result of each job only, so a run in
which every job was `success` was green even if the planned and the measured mutants differed.
On the previous workflow that test fails with `assert 0 != 0` (the gate step exits 0).
"""

import json
import os
import stat
import subprocess

import pytest
import yaml

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
WORKFLOWS = os.path.join(ROOT, ".github/workflows")
FIX = os.path.join(os.path.dirname(__file__), "fixtures/quality_gate")


def load(workflow):
    with open(os.path.join(WORKFLOWS, workflow)) as fh:
        return yaml.safe_load(fh)


def step(workflow, job, key, value="id"):
    for s in load(workflow)["jobs"][job]["steps"]:
        if (key == "id" and s.get("id") == value) or (
            key == "name" and s.get("name") == value
        ):
            return s
    raise AssertionError(f"{workflow}: no step {value!r} in job {job!r}")


def run_script(script, cwd, env, path_prefix=None):
    full = {**os.environ, **env}
    if path_prefix:
        full["PATH"] = f"{path_prefix}:{full['PATH']}"
    return subprocess.run(
        ["bash", "-e", "-c", script], cwd=cwd, env=full, capture_output=True, text=True
    )


# ----------------------------------------------------------------------- the gate job

GATE_OK = {
    "PLAN": "success",
    "GO": "success",
    "RUST": "success",
    "GUARD": "success",
    "COMPLETENESS": "success",
}


def gate_script():
    return step("mutation-pr.yml", "gate", "name", "Everything planned passed")["run"]


def gate(tmp_path, **over):
    return run_script(gate_script(), str(tmp_path), {**GATE_OK, **over})


def test_gate_is_green_when_everything_passed(tmp_path):
    assert gate(tmp_path).returncode == 0


def test_gate_is_red_when_the_completeness_check_is_not_successful(tmp_path):
    # every job is a success: only the comparison of planned and measured says "no"
    for result in ("failure", "cancelled", "skipped", ""):
        r = gate(tmp_path, COMPLETENESS=result)
        assert r.returncode != 0, f"completeness={result!r}: the gate accepted it"
        assert "do not match" in r.stdout + r.stderr


@pytest.mark.parametrize("who", ["PLAN", "GUARD", "GO", "RUST"])
def test_gate_still_fails_on_every_other_failure(tmp_path, who):
    assert gate(tmp_path, **{who: "failure"}).returncode != 0


@pytest.mark.parametrize("who", ["GO", "RUST"])
def test_a_skipped_mutation_job_is_accepted_only_through_the_completeness_check(
    tmp_path, who
):
    # skipped + the check green (it verified PLAN_VALID_EMPTY): fine; skipped + the check red: not
    assert gate(tmp_path, **{who: "skipped"}).returncode == 0
    assert gate(tmp_path, **{who: "skipped", "COMPLETENESS": "failure"}).returncode != 0


# ----------------------------------------------------------------------- the nightly report


def test_nightly_report_is_a_failure_when_the_completeness_check_is_not_successful(
    tmp_path,
):
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    (scripts / "ci_report.sh").write_text(
        '#!/usr/bin/env bash\necho "$3" > "$PWD/status"\n'
    )
    s = step(
        "nightly.yml",
        "report",
        "name",
        "Report (open or update the issue on failure, close it on success)",
    )
    base = {
        "SECURITY": "success",
        "PLAN": "success",
        "GO": "success",
        "RUST": "success",
        "TEST": "",
        "GH_TOKEN": "x",
    }
    for completeness, want in (
        ("success", "success"),
        ("failure", "failure"),
        ("skipped", "failure"),
    ):
        r = run_script(s["run"], str(tmp_path), {**base, "COMPLETENESS": completeness})
        assert r.returncode == 0, r.stderr
        assert (tmp_path / "status").read_text().strip() == want, completeness


# ----------------------------------------------------------------------- the plan record steps

FAKE_CARGO = """#!/usr/bin/env bash
case "$*" in
  *--version*) echo "cargo-mutants 27.1.0"; exit 0 ;;
esac
case "$FAKE_MODE" in
  EMPTY) exit 0 ;;
  FAIL) echo "ERROR cannot read the diff" >&2; exit 6 ;;
  LIST) cat "$FIXTURE"; exit 0 ;;
esac
"""


@pytest.fixture()
def workdir(tmp_path):
    """A scratch git repo whose `scripts/` is the repo's, with a fake cargo on the PATH."""
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    env = {
        "GIT_AUTHOR_NAME": "t",
        "GIT_AUTHOR_EMAIL": "t@x",
        "GIT_COMMITTER_NAME": "t",
        "GIT_COMMITTER_EMAIL": "t@x",
    }
    subprocess.run(
        [
            "git",
            "-C",
            str(tmp_path),
            "-c",
            "commit.gpgsign=false",
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "-q",
            "--allow-empty",
            "-m",
            "x",
        ],
        check=True,
        env={**os.environ, **env},
    )
    os.symlink(os.path.join(ROOT, "scripts"), tmp_path / "scripts")
    bindir = tmp_path / "bin"
    bindir.mkdir()
    cargo = bindir / "cargo"
    cargo.write_text(FAKE_CARGO)
    cargo.chmod(cargo.stat().st_mode | stat.S_IEXEC)
    return tmp_path, str(bindir)


PLAN_STEPS = [
    ("mutation-pr.yml", "plan", {"BASE_SHA": "HEAD", "HEAD_SHA": "HEAD"}),
    ("nightly.yml", "plan", {"BASE": "HEAD", "HEAD_SHA": "HEAD"}),
]


def rust_plan_step(workflow, job, tmp_path, bindir, mode, extra, **env):
    s = step(workflow, job, "id", "rustplan")
    out = tmp_path / "gh_out"
    out.write_text("")
    e = {
        "FAKE_MODE": mode,
        "FIXTURE": os.path.join(FIX, "cargo-mutants-27.1.0-list.json"),
        "GITHUB_OUTPUT": str(out),
        "RUST": "true",
        "RUST_FULL": "true",
        "COUNTED": "5",
        **extra,
        **env,
    }
    r = run_script(s["run"], str(tmp_path), e, bindir)
    outputs = dict(
        line.split("=", 1) for line in out.read_text().splitlines() if "=" in line
    )
    return r, outputs


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_the_rust_plan_record_of_a_real_listing(workdir, workflow, job, extra):
    tmp_path, bindir = workdir
    r, out = rust_plan_step(workflow, job, tmp_path, bindir, "LIST", extra)
    assert r.returncode == 0, r.stdout + r.stderr
    plan = json.loads((tmp_path / "plan/rust-plan.json").read_text())
    assert plan["state"] == "PLAN_VALID_NONEMPTY" and plan["count"] == 5
    assert out["digest"] == plan["digest"]


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_a_failed_listing_fails_the_plan_record_and_leaves_no_plan(
    workdir, workflow, job, extra
):
    tmp_path, bindir = workdir
    r, out = rust_plan_step(workflow, job, tmp_path, bindir, "FAIL", extra)
    assert r.returncode != 0
    assert not (tmp_path / "plan/rust-plan.json").exists()
    assert "digest" not in out


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_a_valid_empty_listing_is_a_valid_empty_plan(workdir, workflow, job, extra):
    tmp_path, bindir = workdir
    r, out = rust_plan_step(
        workflow, job, tmp_path, bindir, "EMPTY", extra, COUNTED="0"
    )
    assert r.returncode == 0, r.stdout + r.stderr
    assert (
        json.loads((tmp_path / "plan/rust-plan.json").read_text())["state"]
        == "PLAN_VALID_EMPTY"
    )


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_the_plan_record_must_agree_with_the_count_step(workdir, workflow, job, extra):
    tmp_path, bindir = workdir
    r, _ = rust_plan_step(workflow, job, tmp_path, bindir, "LIST", extra, COUNTED="4")
    assert r.returncode != 0
    assert "count step" in r.stdout + r.stderr


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_no_rust_change_is_an_empty_plan_with_a_reason(workdir, workflow, job, extra):
    tmp_path, bindir = workdir
    r, _ = rust_plan_step(
        workflow, job, tmp_path, bindir, "LIST", extra, RUST="false", COUNTED="0"
    )
    assert r.returncode == 0, r.stdout + r.stderr
    plan = json.loads((tmp_path / "plan/rust-plan.json").read_text())
    assert plan["state"] == "PLAN_VALID_EMPTY" and plan["skipped"]


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_the_diff_mode_lists_with_the_diff(workdir, workflow, job, extra):
    tmp_path, bindir = workdir
    r, _ = rust_plan_step(
        workflow, job, tmp_path, bindir, "LIST", extra, RUST_FULL="false"
    )
    assert r.returncode == 0, r.stdout + r.stderr
    plan = json.loads((tmp_path / "plan/rust-plan.json").read_text())
    assert plan["mode"] == "diff" and "--in-diff" in plan["listing"]["command"]


@pytest.mark.parametrize("workflow, job, extra", PLAN_STEPS)
def test_the_go_plan_record(workdir, workflow, job, extra):
    tmp_path, _ = workdir
    s = step(workflow, job, "id", "goplan")
    out = tmp_path / "gh_out"
    out.write_text("")
    env = {
        "GO": '[{"id": 0, "pkgs": "./b ./a"}, {"id": 1, "pkgs": "./c"}]',
        "GO_PKGS": "./b ./a ./c",
        "GO_FULL": "false",
        "GITHUB_OUTPUT": str(out),
    }
    r = run_script(s["run"], str(tmp_path), env)
    assert r.returncode == 0, r.stdout + r.stderr
    plan = json.loads((tmp_path / "plan/go-plan.json").read_text())
    assert plan["ids"] == ["./a", "./b", "./c"]
    outputs = dict(
        line.split("=", 1) for line in out.read_text().splitlines() if "=" in line
    )
    assert outputs["chunks"] == "2" and outputs["digest"] == plan["digest"]


# ----------------------------------------------------------------------- the wiring


@pytest.mark.parametrize("workflow", ["mutation-pr.yml", "nightly.yml"])
def test_the_reports_are_handed_in_and_collected_under_matching_names(workflow):
    doc = load(workflow)
    jobs = doc["jobs"]
    uploads = {
        job: [
            s["with"]["name"]
            for s in jobs[job]["steps"]
            if "upload-artifact" in str(s.get("uses"))
        ]
        for job in ("plan", "go", "rust")
    }
    assert uploads["plan"] == ["mutation-plan"]
    assert uploads["go"] == ["go-chunk-${{ matrix.chunk.id }}"]
    assert uploads["rust"] == ["rust-shard-${{ matrix.shard }}"]
    downloads = [
        s["with"]
        for s in jobs["completeness"]["steps"]
        if "download-artifact" in str(s.get("uses"))
    ]
    assert {d.get("name") or d.get("pattern") for d in downloads} == {
        "mutation-plan",
        "rust-shard-*",
        "go-chunk-*",
    }
    # the report steps run even when the mutation step failed, but never hide its failure
    for job in ("go", "rust"):
        for s in jobs[job]["steps"]:
            if "upload-artifact" in str(s.get("uses")) or "Bind the shard" in s.get(
                "name", ""
            ):
                assert s["if"] == "${{ always() }}"
            assert not s.get("continue-on-error")


@pytest.mark.parametrize("workflow", ["mutation-pr.yml", "nightly.yml"])
def test_the_completeness_job_waits_for_the_mutation_jobs_and_always_runs(workflow):
    c = load(workflow)["jobs"]["completeness"]
    assert set(c["needs"]) == {"plan", "go", "rust"}
    assert "always()" in c["if"]
    assert not c.get("continue-on-error")


def test_the_gate_depends_on_the_completeness_job():
    assert "completeness" in load("mutation-pr.yml")["jobs"]["gate"]["needs"]
    assert "completeness" in load("nightly.yml")["jobs"]["report"]["needs"]


def test_every_shard_and_chunk_is_bound_to_the_plan_digest():
    for workflow in ("mutation-pr.yml", "nightly.yml"):
        text = open(os.path.join(WORKFLOWS, workflow)).read()
        assert "needs.plan.outputs.rust_plan_digest" in text
        assert "needs.plan.outputs.go_plan_digest" in text
