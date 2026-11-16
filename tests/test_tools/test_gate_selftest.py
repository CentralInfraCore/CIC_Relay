"""PR-06 probes 1 and 2: the self-test of the real gates (scripts/gate_selftest.py)."""

import os
import shutil
import sys

import pytest
import yaml

HERE = os.path.dirname(__file__)
ROOT = os.path.abspath(os.path.join(HERE, "../.."))
sys.path.insert(0, os.path.join(ROOT, "scripts"))

import gate_selftest as gs  # noqa: E402

WF = os.path.join(ROOT, ".github/workflows")


def _wf(tmp_path, steps):
    p = tmp_path / "wf.yml"
    p.write_text(yaml.safe_dump({"jobs": {"j": {"steps": steps}}}))
    return p


def test_extract_step_returns_the_script_unchanged_except_the_named_expressions(
    tmp_path,
):
    script = (
        'echo "${{ steps.scope.outputs.pkgs }}"\nif [ "$X" = a ]; then echo hi; fi\n'
    )
    p = _wf(tmp_path, [{"name": "S", "run": script}])
    got = gs.extract_step(p, "j", "S", {"steps.scope.outputs.pkgs": "./pkg/merkle"})
    assert got == script.replace("${{ steps.scope.outputs.pkgs }}", "./pkg/merkle")


def test_extract_step_refuses_an_expression_it_cannot_fill_in(tmp_path):
    p = _wf(tmp_path, [{"name": "S", "run": "echo ${{ github.sha }}"}])
    with pytest.raises(gs.SelfTestError, match="github.sha"):
        gs.extract_step(p, "j", "S", {})


@pytest.mark.parametrize(
    "steps",
    [
        [],
        [{"name": "other", "run": "x"}],
        [{"name": "S", "run": "x"}, {"name": "S", "run": "y"}],
        [{"name": "S", "uses": "actions/checkout@v4"}],
    ],
)
def test_extract_step_refuses_a_missing_ambiguous_or_script_less_step(tmp_path, steps):
    with pytest.raises(gs.SelfTestError):
        gs.extract_step(_wf(tmp_path, steps), "j", "S", {})


def test_the_gates_the_cases_extract_exist_in_the_real_workflows():
    cov = gs.extract_step(
        __import__("pathlib").Path(WF, "verify.yml"),
        "coverage",
        "Per-package coverage thresholds",
        {"steps.scope.outputs.pkgs": "./pkg/merkle"},
    )
    assert "*/pkg/merkle)" in cov and "FAIL: coverage below threshold" in cov
    rust = gs.extract_step(
        __import__("pathlib").Path(WF, "verify.yml"),
        "rust",
        "Coverage (fail under 90% lines)",
        {},
    )
    assert "--fail-under-lines" in rust


def test_the_planted_code_comes_in_a_blind_and_an_asserting_variant():
    # the pair is what makes a case a control: the same code, with and without assertions
    assert (
        "assert_eq!" in gs.RUST_TEST_ASSERTING
        and "assert_eq!" not in gs.RUST_TEST_BLIND
    )
    assert (
        "Fatalf" in gs.GO_PLANTED_TEST_ASSERTING
        and "Fatalf" not in gs.GO_PLANTED_TEST_BLIND
    )


def test_the_workflow_is_report_only_and_runs_when_a_gate_it_exercises_changes():
    doc = yaml.safe_load(open(os.path.join(WF, "gate-selftest.yml")))
    on = doc.get("on", doc.get(True))
    assert set(on) == {"pull_request", "workflow_dispatch"}
    # read-only: the contents, and the approval comments of a changed pin file (measurement-reference, #195)
    assert doc["permissions"] == {"contents": "read", "pull-requests": "read"}
    for path in (
        ".github/workflows/verify.yml",
        "scripts/gate_selftest.py",
        "scripts/gremlins_gate.py",
        ".measurement-tools.json",
    ):
        assert path in on["pull_request"]["paths"]
    text = open(os.path.join(WF, "gate-selftest.yml")).read()
    assert "continue-on-error" not in text and "|| true" not in text


def test_the_gate_selftest_is_gate_logic_for_the_planner():
    import mutation_plan as mp

    assert "scripts/gate_selftest.py" in mp.GATE_LOGIC_PATHS


@pytest.mark.skipif(
    shutil.which("go") is None, reason="the Go toolchain is not installed"
)
def test_the_go_coverage_case_is_green_on_the_real_gate_and_goes_red_when_the_gate_is_weakened(
    tmp_path,
):
    src = tmp_path / "src"
    gs.copy_tree(__import__("pathlib").Path(ROOT), src)
    ok = gs.case_go_cov(src, tmp_path / "w1")
    assert ok.ok, ok.lines()
    # the tooth: lower the threshold of the package in the gate itself; the weakened suite is then no longer rejected
    wf = src / ".github/workflows/verify.yml"
    text = wf.read_text()
    assert text.count("*/pkg/merkle)                  min=97  ;;") == 1
    wf.write_text(
        text.replace(
            "*/pkg/merkle)                  min=97  ;;",
            "*/pkg/merkle)                  min=0   ;;",
        )
    )
    weak = gs.case_go_cov(src, tmp_path / "w2")
    assert weak.control_ok and not weak.defect_ok, weak.lines()
