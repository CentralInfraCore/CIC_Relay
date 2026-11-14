"""The planner probes P1-P9 of the CI inventory (PR-01), as regression tests (PR-02).

Each test runs scripts/mutation_plan.py on a scratch git repository and states the plan the
CI must get. P1-P6 and P8 reproduce the blind spots measured on bc2c4e0 (see
jobs/relay-ci-parity-01-inventory/output/inventory-and-gap-analysis.md, section 4.2): on the
old planner they fail. P7 and P9 were working checks and must stay so.
"""

import json
import os
import subprocess
import sys

import pytest

# MUTATION_PLAN_SCRIPT points the probes at another planner (the counter-test: the old one).
PLAN = os.environ.get("MUTATION_PLAN_SCRIPT") or os.path.abspath(
    os.path.join(os.path.dirname(__file__), "../../scripts/mutation_plan.py")
)
GIT_ENV = {
    **os.environ,
    "GIT_AUTHOR_NAME": "t",
    "GIT_AUTHOR_EMAIL": "t@example.invalid",
    "GIT_COMMITTER_NAME": "t",
    "GIT_COMMITTER_EMAIL": "t@example.invalid",
}

BASE_FILES = {
    "go.mod": "module example.com/m\n\ngo 1.22\n",
    "go.sum": "",
    ".gremlins.yaml": "unleash: {}\n",
    "core/cabinet/cabinet.go": "package cabinet\n",
    "core/cabinet/cabinet_test.go": "package cabinet\n",
    "core/cabinet/testdata/golden.json": "{}\n",
    "core/other/other.go": "package other\n",
    "core/onlytest/x_test.go": "package onlytest\n",
    "tools/goastdump/go.mod": "module example.com/goastdump\n\ngo 1.22\n",
    "tools/goastdump/main.go": "package main\n",
    "scripts/gremlins_gate.py": "# gate\n",
    "Cargo.toml": "[workspace]\n",
    "crate/src/lib.rs": "pub fn f() {}\n",
    "crate/tests/it.rs": "#[test] fn t() {}\n",
    "README.md": "x\n",
}


def git(repo, *args):
    return subprocess.run(
        ["git", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", *args],
        cwd=repo,
        env=GIT_ENV,
        check=True,
        capture_output=True,
        text=True,
    ).stdout


@pytest.fixture()
def repo(tmp_path):
    git(tmp_path, "init", "-q", "-b", "main")
    for path, text in BASE_FILES.items():
        f = tmp_path / path
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_text(text)
    git(tmp_path, "add", "-A")
    git(tmp_path, "commit", "-q", "-m", "base")
    git(tmp_path, "tag", "base")
    return tmp_path


def change(repo, edits=None, deletes=()):
    for path, text in (edits or {}).items():
        f = repo / path
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_text(text)
    for path in deletes:
        git(repo, "rm", "-q", "-r", path)
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "change")


def plan(repo, base="base"):
    """Run the planner; return its key=value output as a dict."""
    r = subprocess.run(
        [
            sys.executable,
            PLAN,
            "--base",
            base,
            "--head",
            "HEAD",
            "--costs",
            "/nonexistent",
        ],
        cwd=repo,
        env={k: v for k, v in GIT_ENV.items() if k != "GITHUB_OUTPUT"},
        capture_output=True,
        text=True,
    )
    assert r.returncode == 0, r.stderr
    return dict(line.split("=", 1) for line in r.stdout.splitlines() if "=" in line)


def pkgs(out):
    return out["go_pkgs"].split() if out["go_pkgs"] else []


def test_p1_test_only_change_plans_the_package(repo):
    change(repo, {"core/cabinet/cabinet_test.go": "package cabinet // weaker\n"})
    out = plan(repo)
    assert pkgs(out) == ["./core/cabinet"]
    assert out["go_full"] == "false"


def test_p2_deleted_source_file_is_not_silent(repo):
    change(repo, {"core/cabinet/extra.go": "package cabinet\n"})
    git(repo, "tag", "-f", "base")
    change(repo, deletes=["core/cabinet/extra.go"])
    assert pkgs(plan(repo)) == ["./core/cabinet"]


def test_p2_deleted_test_file_is_not_silent(repo):
    change(repo, deletes=["core/cabinet/cabinet_test.go"])
    assert pkgs(plan(repo)) == ["./core/cabinet"]


@pytest.mark.parametrize("path", ["go.mod", "go.sum", ".gremlins.yaml"])
def test_p3_dependency_or_mutation_config_escalates_go_to_full(repo, path):
    change(repo, {path: BASE_FILES[path] + "# changed\n"})
    out = plan(repo)
    assert out["go_full"] == "true"
    assert pkgs(out) == ["./core/cabinet", "./core/other"]
    assert path in out["full_reasons"]


def test_p4_gate_logic_escalates_everything(repo):
    change(repo, {"scripts/gremlins_gate.py": "# weaker gate\n"})
    out = plan(repo)
    assert out["go_full"] == "true" and out["rust_full"] == "true"
    assert out["rust"] == "true"
    assert pkgs(out) == ["./core/cabinet", "./core/other"]


def test_p5_cargo_manifest_escalates_rust_to_every_mutant(repo):
    change(repo, {"Cargo.toml": "[workspace]\n# changed\n"})
    out = plan(repo)
    assert out["rust"] == "true" and out["rust_full"] == "true"
    assert out["go_full"] == "false"


def test_rust_test_file_escalates_rust(repo):
    # cargo-mutants never mutates tests, so --in-diff would plan 0 mutants for this change
    change(repo, {"crate/tests/it.rs": "#[test] fn t() { assert!(true); }\n"})
    out = plan(repo)
    assert out["rust"] == "true" and out["rust_full"] == "true"


def test_rust_source_change_stays_narrow(repo):
    change(repo, {"crate/src/lib.rs": "pub fn f() { let _ = 1; }\n"})
    out = plan(repo)
    assert out["rust"] == "true" and out["rust_full"] == "false"


def test_p6_testdata_change_plans_the_owning_package(repo):
    change(repo, {"core/cabinet/testdata/golden.json": '{"changed": true}\n'})
    assert pkgs(plan(repo)) == ["./core/cabinet"]


def test_p7_new_package_is_planned(repo):
    change(repo, {"pkg/newpkg/new.go": "package newpkg\n"})
    out = plan(repo)
    assert pkgs(out) == ["./pkg/newpkg"]
    assert json.loads(out["go"]) == [{"id": 0, "pkgs": "./pkg/newpkg"}]


def test_p8_nested_module_is_still_ignored(repo):
    change(repo, {"tools/goastdump/main.go": "package main // changed\n"})
    assert pkgs(plan(repo)) == []


def test_package_with_only_tests_is_not_planned(repo):
    change(repo, {"core/onlytest/x_test.go": "package onlytest // changed\n"})
    assert pkgs(plan(repo)) == []


def test_unrelated_change_plans_nothing(repo):
    change(repo, {"README.md": "changed\n"})
    out = plan(repo)
    assert pkgs(out) == [] and out["rust"] == "false" and out["full_reasons"] == ""


def test_p9_unknown_base_is_fail_closed(repo):
    r = subprocess.run(
        [sys.executable, PLAN, "--base", "no-such-ref", "--head", "HEAD"],
        cwd=repo,
        capture_output=True,
        text=True,
    )
    assert r.returncode != 0
