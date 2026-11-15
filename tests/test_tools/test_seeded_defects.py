"""Seeded defects against the evidence verifier (PR-05): every defect is caught, every negative has a positive control,
and the harness itself is shown to have teeth (the verifier's checks removed on a scratch copy turn it red).
"""

import copy
import json
import os
import shutil
import subprocess
import sys

import pytest

HERE = os.path.dirname(__file__)
ROOT = os.path.abspath(os.path.join(HERE, "../.."))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(ROOT, "scripts"))

import object_model as om  # noqa: E402
import seeded_bundle as sb  # type: ignore[import-not-found]  # noqa: E402
import seeded_defects as sd  # noqa: E402


@pytest.fixture(scope="module")
def bundle(tmp_path_factory):
    return sb.fixture_bundle(str(tmp_path_factory.mktemp("honest")))


CASE_IDS = [c.id for c in sd.CASES]


# --------------------------------------------------------------------------- the positive control and the cases


def test_the_positive_control_the_honest_bundle_reaches_the_honest_verdict(bundle):
    c = sd.control(bundle, sb.HONEST)
    assert c["ok"], c
    assert (
        bundle.verify()["stability"]["verdict"] == "NOT_STABLE"
    )  # honest, but single runs: nothing is claimed stable


@pytest.mark.parametrize("case", sd.CASES, ids=CASE_IDS)
def test_each_seeded_defect_is_caught_at_the_expected_level_with_its_own_code(
    bundle, case
):
    r = sd.run_case(bundle, case)
    assert r["caught"], r


@pytest.mark.parametrize(
    "case", [c for c in sd.CASES if c.level != "STABILITY"], ids=lambda c: c.id
)
def test_the_honest_bundle_has_none_of_the_findings_the_defect_causes(bundle, case):
    # the pair of every negative case: without the defect the same level has no finding with that code
    res = bundle.verify()
    findings = res["tools" if case.level == "TOOLS" else case.level].get("findings", [])
    assert case.code not in sd._codes(findings), findings


def test_all_cases_run_together_and_the_harness_reports_ok(bundle):
    ok, lines = sd.run_all(bundle, sb.HONEST)
    assert ok, "\n".join(lines)
    assert len(lines) == 1 + len(sd.CASES)


def test_the_harness_does_not_change_the_bundle_or_the_checkout(bundle):
    before = json.dumps(bundle.record, sort_keys=True)
    sd.run_all(bundle, sb.HONEST)
    assert json.dumps(bundle.record, sort_keys=True) == before
    status = subprocess.run(
        ["git", "-C", bundle.root, "status", "--porcelain"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    assert status == ""


def test_the_cases_have_unique_ids_and_name_a_code():
    assert len(CASE_IDS) == len(set(CASE_IDS))
    assert all(
        c.code and c.level in ("L1", "L2", "L3", "STABILITY", "TOOLS") for c in sd.CASES
    )


# --------------------------------------------------------------------------- R: the changed behaviour (count -> set)


def test_r_a_swapped_object_is_invisible_to_the_count_only_check_and_caught_by_the_set_check(
    bundle,
):
    rec, _ = sd.d_swap(bundle)
    # the OLD way: only the number of objects against the number of `func` lines
    old_v, _, old_findings = om.verify_l3(
        rec, bundle.root, {"goastdump": ["main.go"]}, None
    )
    assert (
        old_v != "FAIL"
    ), "the count-only check was expected to miss the swap (this documents the defect)"
    # the NEW way: the set of ids against an independent enumeration
    new_v, _, new_findings = om.verify_l3(
        rec, bundle.root, None, None, go_independent=bundle.go_independent
    )
    assert new_v == "FAIL" and any(
        "SET_MISMATCH" in f and "Swapped" in f for f in new_findings
    ), new_findings


def test_r_the_set_difference_names_the_missing_and_the_unexpected_ids(bundle):
    rec, _ = sd.d_swap(bundle)
    _, _, findings = om.verify_l3(
        rec, bundle.root, None, None, go_independent=bundle.go_independent
    )
    msg = next(f for f in findings if "SET_MISMATCH" in f)
    assert "1 missing" in msg and "1 unexpected" in msg and "go:invented.Swapped" in msg


# --------------------------------------------------------------------------- L3: PASS only for proven completeness


def test_l3_is_partial_not_pass_when_the_rust_side_has_no_independent_inventory(bundle):
    res = bundle.verify(rust_independent=None)
    assert res["L3"]["verdict"] == "PARTIAL"
    assert any("no independent object inventory" in u for u in res["L3"]["unproven"])


def test_l3_is_partial_when_go_was_only_counted(bundle):
    res = bundle.verify(go_independent=None, go_files={"goastdump": ["main.go"]})
    assert res["L3"]["verdict"] == "PARTIAL"
    assert any("only the COUNT was compared" in u for u in res["L3"]["unproven"])


def test_l3_is_never_pass_while_anything_is_unproven(bundle):
    for over in (
        {"rust_independent": None},
        {"go_independent": None, "go_files": None},
    ):
        assert bundle.verify(**over)["L3"]["verdict"] != "COMPLETE_FOR_SCOPE"


def test_the_independent_rust_enumeration_refuses_what_it_cannot_name():
    ids, unmapped = om.independent_rust_free_functions(
        "pub fn a() {}\nimpl T { fn m(&self) {} }\nmod inner { pub fn b() {} }\n", "c"
    )
    assert (
        ids == {"rust:c::a"} and unmapped == 2
    )  # an impl and an inline mod: not proven, so the caller keeps PARTIAL


def test_the_independent_rust_enumeration_skips_test_items_and_nested_fns():
    src = "pub fn a() {}\nfn b() { fn inner() {} }\n#[cfg(test)]\nmod tests {\n    fn t() {}\n}\n"
    ids, unmapped = om.independent_rust_free_functions(src, "c")
    # `mod tests` is a test item (skipped); `inner` is a nested fn that llvm-cov lists but this scan cannot name: it is unmapped
    assert ids == {"rust:c::a", "rust:c::b"} and unmapped == 1


# --------------------------------------------------------------------------- L2: values are re-derived from the inputs


def test_l2_is_only_partial_when_the_values_were_not_re_derived(bundle):
    res = bundle.verify(derived=None)
    assert (
        res["L2"]["verdict"] == "PARTIAL"
        and "not re-derived" in res["L2"]["unproven"][0]
    )


def test_the_rederivation_agrees_with_the_builder_on_every_real_object(bundle):
    for o in bundle.record["objects"]:
        want = bundle.derived[o["id"]]["coverage"]
        assert om._cmp_cov(want, o["coverage"]), (o["id"], want, o["coverage"])


def layouts():
    def fn(n, s, e):
        return {
            "name": n,
            "kind": "func",
            "start": {"line": s[0], "col": s[1]},
            "end": {"line": e[0], "col": e[1]},
        }

    two = [fn("A", (1, 1), (5, 2)), fn("B", (7, 1), (9, 2))]
    return {
        "inside": (two, [(2, 2, 3, 10, 2, 1), (3, 10, 4, 3, 1, 0)]),
        "cuts-an-object": (two, [(2, 2, 3, 10, 2, 1), (4, 2, 8, 3, 4, 1)]),
        "spans-several": (
            [fn("A", (1, 1), (3, 2)), fn("B", (5, 1), (7, 2))],
            [(1, 1, 7, 2, 9, 1)],
        ),
        "outside-every-object": (
            [fn("A", (10, 1), (12, 2))],
            [(2, 5, 4, 3, 2, 1), (11, 2, 11, 9, 1, 1)],
        ),
        "no-statement": (
            [fn("E", (1, 1), (1, 12)), fn("A", (3, 1), (5, 2))],
            [(4, 2, 4, 9, 1, 1)],
        ),
    }


@pytest.mark.parametrize("name", list(layouts()))
def test_the_two_independent_algorithms_agree_on_the_edge_layouts(name):
    funcs, blocks = layouts()[name]
    inv = {"files": [{"file": "a.go", "funcs": funcs}]}
    blks = [("m/a.go", *b) for b in blocks]
    built, _ = om.go_objects("root", "m", inv, blks)
    derived = om.derive_go_coverage("root", "m", inv, blks)
    assert {o["id"] for o in built} == set(derived)
    for o in built:
        assert om._cmp_cov(derived[o["id"]]["coverage"], o["coverage"]), (
            name,
            o["id"],
            derived[o["id"]],
            o["coverage"],
        )


def test_the_rederivation_sees_a_value_that_the_builder_and_a_forger_agree_on_wrongly(
    bundle,
):
    # the forger changes the VALUE and re-seals; the inputs (here the derived values) still say the truth
    rec, _ = sd.d_tamper_resealed(bundle)
    res = bundle.verify(rec)
    assert res["L1"]["verdict"] == "INTEGRITY_OK"  # internally consistent
    assert res["L2"]["verdict"] == "FAIL" and any(
        "REDERIVATION_MISMATCH" in f for f in res["L2"]["findings"]
    )


# --------------------------------------------------------------------------- the independent Go enumeration


def test_the_independent_go_enumeration_equals_the_inventory_on_the_real_source(bundle):
    inv_ids = {o["id"] for o in bundle.record["objects"] if o["unit"] == "goastdump"}
    assert bundle.go_independent["goastdump"] == inv_ids and len(inv_ids) == 15


def test_the_independent_go_enumeration_on_a_tricky_source(tmp_path):
    src = (
        "//go:build linux && !nocgo\n\npackage p\n\n"
        "func Top(a int) int { return a }\n"
        "func (t *T) Ptr() {}\nfunc (t T) Val() {}\nfunc (g *G[K]) Gen() {}\nfunc (*U) Anon() {}\n"
        "func Generic[T any](x T) T { return x }\n"
        "func init() {}\nfunc init() {}\n"
        "var hook = func() int { return 1 }\n"
        "// func Commented() {}\n"
    )
    (tmp_path / "p").mkdir()
    (tmp_path / "p/a.go").write_text(src)
    ids = om.independent_go_ids(str(tmp_path), "m", ".", ["p/a.go"])
    b = "@build(linux && !nocgo)"
    assert ids == {
        f"go:m/p.{n}{b}"
        for n in (
            "Top",
            "T.Ptr",
            "T.Val",
            "G.Gen",
            "U.Anon",
            "Generic",
            "init#a.go#1",
            "init#a.go#2",
        )
    }


# --------------------------------------------------------------------------- A.9: determinism


def test_a_single_run_is_never_stable_and_twenty_identical_runs_are(tmp_path):
    one = sb.fixture_bundle(str(tmp_path / "one"))
    assert (
        one.verify()["stability"]["verdict"] == "NOT_STABLE"
        and one.verify()["stability"]["not_shown"] > 0
    )
    many = sb.fixture_bundle(
        str(tmp_path / "many"), repeats=om.STABLE_MIN_RUNS - 1
    )  # the positive control: 20 identical runs
    go = copy.deepcopy(many.record)
    go["objects"] = [o for o in go["objects"] if o["lang"] == "go"]
    st = om.stability(sd.reseal(go))
    assert st["verdict"] == "STABLE" and st["observed_stable"] == st["measured"] > 0
    assert all(
        o["coverage"]["runs"] == om.STABLE_MIN_RUNS
        for o in go["objects"]
        if o["coverage"]["state"] == om.MEASURED
    )


def test_nineteen_identical_runs_are_not_enough(tmp_path):
    b = sb.fixture_bundle(str(tmp_path), repeats=om.STABLE_MIN_RUNS - 2)
    o = next(
        o
        for o in b.record["objects"]
        if o["lang"] == "go" and o["coverage"]["state"] == om.MEASURED
    )
    assert (
        o["coverage"]["runs"] == om.STABLE_MIN_RUNS - 1
        and o["coverage"]["determinism"] == "NOT_SHOWN"
    )


def test_a_stable_claim_from_too_few_runs_is_rejected(bundle):
    rec, _ = sd.d_single_run(bundle)
    assert any("BAD_DETERMINISM" in f and "at least" in f for f in om.verify_l1(rec))


def test_the_nondeterministic_object_is_not_measured_and_makes_the_evidence_not_stable(
    bundle,
):
    rec, _ = sd.d_nondeterministic(bundle)
    o = next(
        o for o in rec["objects"] if o["coverage"].get("reason") == "NONDETERMINISTIC"
    )
    assert o["coverage"]["state"] == om.UNVERIFIABLE and "covered" not in o["coverage"]
    st = om.stability(rec)
    assert st["verdict"] == "NOT_STABLE" and o["id"] in st["nondeterministic_objects"]


# --------------------------------------------------------------------------- the harness has teeth

SCRATCH_RUNNER = """
import json, sys, tempfile
sys.path.insert(0, 'tests/test_tools'); sys.path.insert(0, 'scripts')
import seeded_bundle as sb, seeded_defects as sd
b = sb.fixture_bundle(tempfile.mkdtemp())
ctl = sd.control(b, sb.HONEST)
print(json.dumps({'control': ctl['ok'], 'cases': {c.id: sd.run_case(b, c)['caught'] for c in sd.CASES}}))
"""

# the checks that live in quality_gate.py (shared with the CI jobs), the others are in object_model.py
QG_TEETH = {
    "the pinned-version comparison",
    "the unpinned-tool check",
    "the missing-tool check",
}

# (check removed on the scratch copy, the cases that must then be seen as NOT caught)
TEETH = [
    (
        "the go set comparison",
        "if have != want:",
        "if False:",
        {"A.1", "A.2", "A.3", "A.4b"},
    ),
    (
        "the re-derivation comparison",
        'elif not _cmp_cov(want["coverage"], got):',
        "elif False:",
        {"A.5b"},
    ),
    (
        "the runs/determinism part of the re-derivation comparison",
        '("kind", "total", "covered", "runs", "determinism")',
        '("kind", "total", "covered")',
        {"A.9c"},
    ),
    (
        "the unknown-tool-version finding",
        '"TOOL_UNKNOWN",',
        '"TOOL_UNKNOWN_X",',
        {"P8.a"},
    ),
    (
        "the pinned-version comparison",
        "elif recorded != want:",
        "elif False:",
        {"P8.b"},
    ),
    (
        "the unpinned-tool check",
        "if want is None:",
        "if False:",
        {"P8.d"},
    ),
    (
        "the missing-tool check",
        "if name not in declared:",
        "if False:",
        {"P8.c"},
    ),
    (
        "the commit/tree identity",
        'if tree != rec["identity"]["tree"] or commit != rec["identity"]["commit"]:',
        "if False:",
        {"A.6"},
    ),
    (
        "the input digest comparison",
        'if digest != want["sha256"] or size != want["bytes"]:',
        "if False:",
        {"A.7"},
    ),
    (
        "the record digest check",
        'if rec.get("digest") != canonical_digest(',
        'if False and rec.get("digest") != canonical_digest(',
        {"A.5a"},
    ),
    (
        "the unique-id check",
        "if ids != sorted(ids) or len(set(ids)) != len(ids):",
        "if False:",
        {"A.4a"},
    ),
    (
        "the missing-field finding",
        '"MISSING_FIELD",',
        '"MISSING_FIELD_X",',
        {"A.8a"},
    ),
    (
        "the required-unit check",
        'if not any(o["unit"] == unit for o in objs):',
        "if False:",
        {"A.8b"},
    ),
    (
        "the stable-from-too-few-runs check",
        'elif det == "OBSERVED_STABLE" and runs < STABLE_MIN_RUNS:',
        "elif False:",
        {"A.9a"},
    ),
    (
        "the nondeterministic-object detection",
        "nondet = sorted(",
        "nondet = [] and sorted(",
        {"A.9b"},
    ),
]


def scratch(tmp_path, name):
    d = tmp_path / name
    shutil.copytree(
        os.path.join(ROOT, "scripts"),
        d / "scripts",
        ignore=shutil.ignore_patterns("__pycache__"),
    )
    (d / "tests/test_tools").mkdir(parents=True)
    shutil.copy(
        os.path.join(HERE, "seeded_bundle.py"), d / "tests/test_tools/seeded_bundle.py"
    )
    shutil.copytree(os.path.join(HERE, "fixtures"), d / "tests/test_tools/fixtures")
    shutil.copy(
        os.path.join(ROOT, ".measurement-scope.json"), d / ".measurement-scope.json"
    )
    return d


def run_scratch(d):
    r = subprocess.run(
        [sys.executable, "-c", SCRATCH_RUNNER], cwd=d, capture_output=True, text=True
    )
    assert r.returncode == 0, r.stderr
    return json.loads(r.stdout.strip().splitlines()[-1])


def test_the_scratch_copy_is_green_before_any_check_is_removed(tmp_path):
    out = run_scratch(scratch(tmp_path, "clean"))
    assert out["control"] is True and all(out["cases"].values()), out


@pytest.mark.parametrize(
    "label, anchor, replacement, expected_missed", TEETH, ids=[t[0] for t in TEETH]
)
def test_the_harness_goes_red_when_a_check_of_the_verifier_is_removed(
    tmp_path, label, anchor, replacement, expected_missed
):
    d = scratch(tmp_path, "weakened")
    # the check lives in object_model.py, or in quality_gate.py for the shared tool-pin logic
    target = "quality_gate.py" if label in QG_TEETH else "object_model.py"
    p = d / "scripts" / target
    src = p.read_text()
    assert (
        src.count(anchor) == 1
    ), f"the anchor of {label!r} is not in {target} exactly once: the teeth test has lost its subject"
    p.write_text(src.replace(anchor, replacement))
    out = run_scratch(d)
    missed = {k for k, v in out["cases"].items() if not v}
    assert (
        expected_missed <= missed
    ), f"removing {label!r} must make {sorted(expected_missed)} escape, got {sorted(missed)}"
    assert (
        out["control"] is True or label == "the record digest check"
    ), "the positive control must stay honest: the harness caught the defect, not a broken control"


# --- F1 (PR-05 review): a stability claim must be provable from the bound input profiles, like a value ---


def _go_inv_and_blocks():
    inv = {
        "files": [
            {
                "file": "a.go",
                "funcs": [
                    {
                        "kind": "func",
                        "name": "F",
                        "recv": "",
                        "start": {"line": 1, "col": 1},
                        "end": {"line": 9, "col": 2},
                    }
                ],
            }
        ]
    }
    blocks = [("m/a.go", 2, 2, 3, 10, 2, 1), ("m/a.go", 4, 2, 5, 10, 2, 0)]
    return inv, blocks


def test_r_f1_runs_and_determinism_are_part_of_the_comparison():
    want = {
        "state": om.MEASURED,
        "kind": "statement",
        "total": 4,
        "covered": 2,
        "runs": 1,
        "determinism": "NOT_SHOWN",
    }
    assert om._cmp_cov(want, dict(want))
    assert not om._cmp_cov(want, {**want, "runs": 20})
    assert not om._cmp_cov(want, {**want, "determinism": "OBSERVED_STABLE"})


def test_r_f1_the_derived_runs_are_counted_from_the_bound_profiles():
    inv, blocks = _go_inv_and_blocks()
    one = next(iter(om.derive_go_coverage("u", "m", inv, blocks).values()))["coverage"]
    assert (one["runs"], one["determinism"]) == (1, "NOT_SHOWN")
    reps = [list(blocks) for _ in range(om.STABLE_MIN_RUNS - 1)]
    many = next(
        iter(om.derive_go_coverage("u", "m", inv, blocks, repeats=reps).values())
    )["coverage"]
    assert (many["runs"], many["determinism"]) == (
        om.STABLE_MIN_RUNS,
        "OBSERVED_STABLE",
    )
    short = next(
        iter(om.derive_go_coverage("u", "m", inv, blocks, repeats=reps[:-1]).values())
    )["coverage"]
    assert short["determinism"] == "NOT_SHOWN", "19 runs are not enough"


def test_f1_the_forged_stability_passes_l1_and_is_caught_only_by_l2(bundle):
    rec, over = sd.d_forged_stability(bundle)
    r = bundle.verify(rec, **over)
    assert (
        r["L1"]["verdict"] == "INTEGRITY_OK"
    ), "the forgery is internally consistent: only a re-derivation can see it"
    assert r["L2"]["verdict"] == "FAIL" and any(
        "REDERIVATION_MISMATCH" in f for f in r["L2"]["findings"]
    )


# --- PR-06 A.8 (probe 8): a modified, missing, unknown or unpinned measuring tool makes the result unusable ---


def _pins(bundle):
    return dict(bundle.record["tools"])


def test_n_a8_the_honest_record_with_the_right_pins_is_accepted(bundle):
    assert bundle.verify()["tools"] == {"verdict": "TOOLS_PINNED", "findings": []}


def test_n_a8_without_pins_the_tools_are_not_checked_and_that_is_not_a_pass(bundle):
    r = om.verify_tools(bundle.record, None)
    assert r["verdict"] == "NOT_CHECKED" and r["verdict"] != "TOOLS_PINNED"


def test_n_a8_unusable_and_unverifiable_name_the_tool(bundle):
    tool = sorted(bundle.record["tools"])[0]
    rec, _ = sd.d_tool_modified(bundle)
    r = bundle.verify(rec)["tools"]
    assert r["verdict"] == "UNUSABLE" and any(tool in f for f in r["findings"])
    rec, _ = sd.d_tool_missing(bundle)
    r = bundle.verify(rec)["tools"]
    assert r["verdict"] == "UNVERIFIABLE" and any(tool in f for f in r["findings"])


def test_n_a8_unusable_wins_over_unverifiable():
    pins = {"tools": {"a": "1", "b": "1"}, "record_tools": None}
    rec = {"tools": {"a": "2"}}  # a: wrong version, b: missing
    r = om.verify_tools(rec, pins)
    assert r["verdict"] == "UNUSABLE" and len(r["findings"]) == 2


@pytest.mark.parametrize(
    "content",
    [
        "{}",
        '{"schema": "measurement-tools/1", "tools": {}}',
        '{"schema": "measurement-tools/2", "tools": {"go": "1"}}',
        '{"schema": "measurement-tools/1", "tools": {"go": ""}}',
        "not json",
    ],
)
def test_n_a8_a_bad_pin_file_is_an_error_not_an_empty_pin_set(tmp_path, content):
    f = tmp_path / "pins.json"
    f.write_text(content)
    with pytest.raises(om.ModelError):
        om.load_tool_pins(str(f))


def test_n_a8_the_command_line_exits_non_zero_for_a_modified_tool(tmp_path, bundle):
    rec, _ = sd.d_tool_modified(bundle)
    (tmp_path / "rec.json").write_text(json.dumps(rec))
    (tmp_path / "pins.json").write_text(
        json.dumps({"schema": "measurement-tools/1", "tools": _pins(bundle)})
    )
    argv = [
        "verify",
        "--record",
        str(tmp_path / "rec.json"),
        "--tool-pins",
        str(tmp_path / "pins.json"),
    ]
    assert om.main(argv) == 1
    (tmp_path / "rec.json").write_text(json.dumps(bundle.record))
    assert om.main(argv) == 0


def test_r_a8_the_real_pin_file_matches_the_tools_the_workflow_records():
    pins = om.load_tool_pins(os.path.join(ROOT, ".measurement-tools.json"))["tools"]
    wf = open(os.path.join(ROOT, ".github/workflows/object-evidence.yml")).read()
    assert "--tool-pins .measurement-tools.json" in wf
    for tool in ("go", "rustc", "cargo-llvm-cov", "cargo-mutants", "objinventory"):
        assert tool in pins
