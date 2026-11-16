"""The object-evidence workflow is report-only and cannot gate or skip anything (PR-04)."""

import os
import subprocess
import sys

import pytest
import yaml

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
WF = os.path.join(ROOT, ".github/workflows/object-evidence.yml")


def load(name="object-evidence.yml"):
    with open(os.path.join(ROOT, ".github/workflows", name)) as fh:
        return yaml.safe_load(fh)


def test_the_workflow_runs_on_pull_requests_and_by_hand_only():
    on = load().get("on") or load().get(True)
    assert set(on) == {"pull_request", "workflow_dispatch"}


def test_it_is_read_only_and_never_hides_a_failure():
    doc = load()
    assert doc["permissions"] == {"contents": "read", "pull-requests": "read"}
    for job in doc["jobs"].values():
        assert not job.get("continue-on-error")
        for st in job["steps"]:
            assert not st.get("continue-on-error")
    assert "REPORT-ONLY" in open(WF).read()


def test_nothing_waits_for_it_and_the_mutation_gate_is_unchanged():
    for name in os.listdir(os.path.join(ROOT, ".github/workflows")):
        if name == "object-evidence.yml":
            continue
        for job in load(name)["jobs"].values():
            needs = job.get("needs", [])
            needs = [needs] if isinstance(needs, str) else needs
            assert "object-evidence" not in needs
    assert set(load("mutation-pr.yml")["jobs"]["gate"]["needs"]) == {
        "plan",
        "go",
        "rust",
        "completeness",
        "baseline-guard",
    }


def test_the_record_is_verified_at_its_three_levels_against_the_checkout_and_the_inputs():
    steps = {s["name"]: s for s in load()["jobs"]["object-evidence"]["steps"]}
    verify = next(v for k, v in steps.items() if k.startswith("Verify the record"))[
        "run"
    ]
    for needle in (
        "object_model.py verify",
        "--root .",
        "--scope .measurement-scope.json",
        "--rust-list",
        "--input",
    ):
        assert needle in verify
    build = next(
        v for k, v in steps.items() if k.startswith("Build the evidence record")
    )["run"]
    assert "--go-unit" in build and "goastdump" in build and "--llvm-cov" in build
    # the verdicts of the mutation run are not produced here: the record says UNVERIFIABLE for them
    assert "--rust-outcomes" not in build


def test_the_measuring_tools_are_the_real_ones_and_their_versions_are_recorded():
    run = next(
        s
        for s in load()["jobs"]["object-evidence"]["steps"]
        if s["name"].startswith("Measure")
    )["run"]
    for needle in (
        "cargo llvm-cov",
        "cargo mutants",
        "go test",
        "-covermode=set",
        "objinventory",
        "go version",
        "rustc --version",
    ):
        assert needle in run


def test_scope_list_prints_the_files_of_a_class():
    out = subprocess.run(
        [
            sys.executable,
            os.path.join(ROOT, "scripts/object_model.py"),
            "scope-list",
            "--root",
            ROOT,
            "--scope",
            os.path.join(ROOT, ".measurement-scope.json"),
            "--lang",
            "go",
            "--class",
            "separate:goastdump",
        ],
        capture_output=True,
        text=True,
    )
    assert out.returncode == 0 and out.stdout.split() == ["tools/goastdump/main.go"]


@pytest.mark.parametrize(
    "path",
    [
        "scripts/object_model.py",
        "scripts/seeded_defects.py",
        ".measurement-scope.json",
        ".measurement-tools.json",
    ],
)
def test_a_change_of_the_object_model_or_the_scope_is_gate_logic(path):
    sys.path.insert(0, os.path.join(ROOT, "scripts"))
    import mutation_plan

    assert path in mutation_plan.GATE_LOGIC_PATHS


def test_the_seeded_defect_harness_runs_on_the_real_record_with_a_stated_expectation():
    steps = {s["name"]: s for s in load()["jobs"]["object-evidence"]["steps"]}
    run = next(v for k, v in steps.items() if k.startswith("Seeded defects"))["run"]
    # the honest L3 is STATED (not adopted from the result), the harness gets the real inputs
    for needle in (
        "seeded_defects.py",
        "--expect-l3 PARTIAL",
        "--record",
        "--root .",
        "--rust-list",
        "--input",
        "go-coverprofile:root",
    ):
        assert needle in run
    assert (
        "COMPLETE_FOR_SCOPE" not in run
    )  # the real workspace is not proven complete on the Rust side
