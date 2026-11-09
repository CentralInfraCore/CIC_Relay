"""What the mutation gates mutate is discovered from the diff (scripts/mutation_plan.py)."""

import os
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

from mutation_plan import chunk, go_packages, rust_changed  # noqa: E402

PLAN = os.path.join(SCRIPTS, "mutation_plan.py")
ROOT_ONLY = {""}
WITH_NESTED = {"", "tools/goastdump"}


def test_non_test_go_source_selects_its_package():
    assert go_packages(["core/cabinet/cabinet.go"], ROOT_ONLY) == ["./core/cabinet"]


def test_test_only_change_selects_nothing():
    assert go_packages(["core/cabinet/cabinet_test.go"], ROOT_ONLY) == []


@pytest.mark.parametrize(
    "path", ["core/x/testdata/a.go", "vendor/y/b.go", "docs/readme.md", "Cargo.toml"]
)
def test_testdata_vendor_and_non_go_are_ignored(path):
    assert go_packages([path], ROOT_ONLY) == []


def test_nested_module_is_not_part_of_the_root_module():
    files = ["tools/goastdump/main.go", "tools/other/x.go"]
    assert go_packages(files, WITH_NESTED) == ["./tools/other"]


def test_root_package_is_dot():
    assert go_packages(["main.go"], ROOT_ONLY) == ["."]


def test_packages_are_unique_and_sorted():
    files = ["b/x.go", "a/y.go", "b/z.go"]
    assert go_packages(files, ROOT_ONLY) == ["./a", "./b"]


@pytest.mark.parametrize(
    "files, expected",
    [
        (["cic-canonical/src/lib.rs"], True),
        (["Cargo.toml"], True),
        (["module/Cargo.lock"], True),
        (["rust-toolchain.toml"], True),
        (["README.md", "core/a.go"], False),
        ([], False),
    ],
)
def test_rust_changed(files, expected):
    assert rust_changed(files) is expected


def test_chunks_are_bounded_and_cover_every_package_once():
    pkgs = [f"./p{i}" for i in range(31)]
    chunks = chunk(pkgs, 6)
    assert len(chunks) == 6
    seen = [p for c in chunks for p in c["pkgs"].split()]
    assert sorted(seen) == sorted(pkgs)


def test_no_packages_no_chunks():
    assert chunk([], 6) == []


def git(cwd, *args):
    subprocess.run(["git", "-C", str(cwd), *args], check=True, capture_output=True)


@pytest.fixture
def repo(tmp_path):
    git(tmp_path, "init", "-q")
    git(tmp_path, "config", "user.email", "t@example.org")
    git(tmp_path, "config", "user.name", "t")
    git(tmp_path, "config", "commit.gpgsign", "false")
    for path, text in {
        "go.mod": "module m\n",
        "a/a.go": "package a\n",
        "a/a_test.go": "package a\n",
        "b/b.go": "package b\n",
        "tools/n/go.mod": "module n\n",
        "tools/n/n.go": "package n\n",
        "lib.rs": "fn f() {}\n",
    }.items():
        f = tmp_path / path
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_text(text)
    git(tmp_path, "add", "-A")
    git(tmp_path, "commit", "-q", "-m", "base")
    return tmp_path


def plan(repo, *args):
    out = subprocess.run(
        [sys.executable, PLAN, *args],
        cwd=repo,
        capture_output=True,
        text=True,
    )
    return out, dict(
        line.split("=", 1) for line in out.stdout.splitlines() if "=" in line
    )


def test_cli_diff_mode_selects_only_the_changed_package(repo):
    (repo / "a" / "a.go").write_text("package a\n// changed\n")
    git(repo, "commit", "-qam", "change")
    out, res = plan(repo, "--base", "HEAD~1", "--head", "HEAD")
    assert out.returncode == 0
    assert res["go_pkgs"] == "./a"
    assert res["rust"] == "false"


def test_cli_all_mode_plans_every_root_module_package(repo):
    out, res = plan(repo, "--all", "--head", "HEAD")
    assert out.returncode == 0
    assert res["go_pkgs"] == "./a ./b"  # the nested module is not included
    assert res["rust"] == "true"


def test_cli_needs_a_base_unless_all(repo):
    out, _ = plan(repo, "--head", "HEAD")
    assert out.returncode != 0 and "--base is required" in out.stderr
