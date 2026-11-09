"""What the mutation gates mutate is discovered from the diff (scripts/mutation_plan.py)."""

import os
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

from mutation_plan import (  # noqa: E402
    chunk,
    go_packages,
    load_costs,
    rust_changed,
)

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


HINTS = ({"./heavy1": 125.0, "./heavy2": 60.0, "./mid": 26.0}, 10.0)


def test_with_cost_hints_the_heavy_packages_do_not_share_a_job():
    # round-robin over two jobs would put all three heavy ones into the first job
    pkgs = ["./heavy1", "./a", "./heavy2", "./b", "./mid", "./c"]
    chunks = chunk(pkgs, 2, HINTS)
    of = {p: c["id"] for c in chunks for p in c["pkgs"].split()}
    assert of["./heavy1"] != of["./heavy2"]
    assert of["./heavy1"] != of["./mid"] or of["./heavy2"] != of["./mid"]
    assert [c["pkgs"] for c in chunk(pkgs, 2)][0] == "./heavy1 ./heavy2 ./mid"


def test_with_cost_hints_every_package_is_in_exactly_one_job():
    pkgs = [f"./p{i}" for i in range(31)] + list(HINTS[0])
    chunks = chunk(pkgs, 6, HINTS)
    seen = [p for c in chunks for p in c["pkgs"].split()]
    assert sorted(seen) == sorted(pkgs) and len(chunks) == 6


def test_cost_hints_make_the_split_deterministic():
    pkgs = ["./b", "./a", "./heavy1", "./c"]
    assert chunk(pkgs, 2, HINTS) == chunk(list(reversed(pkgs)), 2, HINTS)


def test_an_unlisted_package_costs_the_default():
    # the unknown package (10) goes to the job with the least load, not with the heaviest
    chunks = chunk(["./heavy1", "./new"], 2, HINTS)
    assert [c["pkgs"] for c in chunks] == ["./heavy1", "./new"]


def test_without_cost_hints_it_stays_round_robin():
    chunks = chunk(["./a", "./b", "./c", "./d"], 2)
    assert [c["pkgs"] for c in chunks] == ["./a ./c", "./b ./d"]


def test_load_costs_reads_the_file_and_tolerates_a_bad_one(tmp_path):
    good = tmp_path / "c.json"
    good.write_text('{"default_seconds": 7, "packages": {"./a": 3}}')
    assert load_costs(str(good)) == ({"./a": 3.0}, 7.0)
    assert load_costs(str(tmp_path / "missing.json")) is None
    bad = tmp_path / "bad.json"
    bad.write_text("not json")
    assert load_costs(str(bad)) is None


def test_the_committed_cost_hints_are_loadable():
    root = os.path.join(SCRIPTS, "..", ".mutation-cost.json")
    loaded = load_costs(root)
    assert (
        loaded is not None
        and loaded[0]["./core/cabinet"] > loaded[0]["./core/nexus/ois"]
    )


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
    # GitHub Actions sets GITHUB_OUTPUT for every step; with it the planner writes the
    # file instead of stdout. The test reads stdout, so it must not depend on that.
    env = {k: v for k, v in os.environ.items() if k != "GITHUB_OUTPUT"}
    out = subprocess.run(
        [sys.executable, PLAN, *args],
        cwd=repo,
        capture_output=True,
        text=True,
        env=env,
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
