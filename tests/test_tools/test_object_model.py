"""The object-level measurement model (scripts/object_model.py): the new contracts' tests (N-group).

The inputs are real tool output (tests/test_tools/fixtures/object_model); derived cases are marked.
For an N test the previous state has nothing to run, so the evidence is: the good real input is
accepted, the bad input is rejected with the specific message, and the check is shown to have teeth
(see the report: the check removed on a scratch copy makes its test fail).
"""

import copy
import json
import os
import shutil
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import object_model as om  # noqa: E402
import quality_gate as qg  # noqa: E402

FIX = os.path.join(os.path.dirname(__file__), "fixtures/object_model")
SCOPE_FILE = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "../../.measurement-scope.json")
)


def real(name):
    with open(os.path.join(FIX, name)) as fh:
        return json.load(fh)


def text(name):
    with open(os.path.join(FIX, name)) as fh:
        return fh.read()


def goastdump_objects(**kw):
    blocks = om.parse_coverprofile(text("goastdump-cover.out"))
    return om.go_objects(
        "goastdump",
        "goastdump",
        real("goastdump-inventory.json"),
        blocks,
        detail=True,
        **kw,
    )


@pytest.fixture()
def rust_root(tmp_path):
    p = tmp_path / "cic-canonical/src"
    p.mkdir(parents=True)
    shutil.copy(os.path.join(FIX, "cic-canonical-lib.rs.txt"), p / "lib.rs")
    return str(tmp_path)


def rust_objects(root, summaries=None, **kw):
    outcomes = real("outcomes-cic-canonical.json")
    verdicts = qg.rust_verdicts(outcomes, "fixture")
    if summaries:  # DERIVED: another verdict for some mutants
        verdicts.update(summaries)
    return om.rust_objects(
        real("llvm-cov-cic-canonical.json"),
        real("demangled-cic-canonical.json"),
        root,
        real("mutants-cic-canonical.json"),
        verdicts,
        **kw,
    )


# --------------------------------------------------------------------------- scope (N8)


def test_the_repo_scope_is_valid_and_versioned():
    scope = json.load(open(SCOPE_FILE))
    om.check_scope(scope)
    assert scope["schema"] == "measurement-scope/1" and isinstance(
        scope["version"], int
    )


def test_every_tracked_go_and_rust_file_of_the_repo_is_classified():
    scope = json.load(open(SCOPE_FILE))
    root = os.path.abspath(os.path.join(SCRIPTS, ".."))
    tracked = subprocess.run(
        ["git", "-C", root, "ls-files"], capture_output=True, text=True, check=True
    ).stdout.splitlines()
    go, rs = om.scope_files(scope, "go", tracked), om.scope_files(
        scope, "rust", tracked
    )
    assert go["production"] and rs["production"] and "separate:goastdump" in go


def test_n8_a_file_the_scope_does_not_classify_is_an_error():
    scope = {
        "schema": om.SCOPE_SCHEMA,
        "version": 1,
        "rules": [{"lang": "go", "match": "core/*", "class": "production"}],
    }
    with pytest.raises(om.ModelError, match="not classified by the measurement scope"):
        om.scope_files(scope, "go", ["core/a.go", "tools/new/x.go"])


def test_n8_an_exclusion_without_a_reason_is_rejected():
    scope = {
        "schema": om.SCOPE_SCHEMA,
        "version": 1,
        "rules": [{"lang": "go", "match": "x/*", "class": "experimental"}],
    }
    with pytest.raises(om.ModelError, match="without a reason"):
        om.check_scope(scope)
    scope["rules"][0]["reason"] = "   "
    with pytest.raises(om.ModelError, match="without a reason"):
        om.check_scope(scope)


def test_the_scope_needs_a_schema_and_an_integer_version():
    with pytest.raises(om.ModelError):
        om.check_scope({"schema": "nope", "version": 1, "rules": []})
    with pytest.raises(om.ModelError):
        om.check_scope({"schema": om.SCOPE_SCHEMA, "version": "1", "rules": []})


# --------------------------------------------------------------------------- Go ids and the real goastdump unit


def test_go_object_ids():
    f = lambda **k: {"name": "N", "kind": "func", **k}  # noqa: E731
    assert (
        om.go_object_id("m", "a/b.go", f()) == "go:m/a.N"[: len("go:m/a")] + "/b.N"
        or True
    )
    assert om.go_object_id("m", "a/b.go", f()) == "go:m/a.N"
    assert om.go_object_id("m", "b.go", f()) == "go:m.N"
    assert om.go_object_id("m", "a/b.go", f(kind="method", recv="T")) == "go:m/a.T.N"
    assert (
        om.go_object_id("m", "a/b.go", f(kind="init", ord=2, name="init"))
        == "go:m/a.init#b.go#2"
    )
    assert (
        om.go_object_id("m", "a/b.go", f(), build="ffi_cgo")
        == "go:m/a.N@build(ffi_cgo)"
    )
    # the position is not part of the key
    assert om.go_object_id(
        "m", "a/b.go", f(start={"line": 1, "col": 1})
    ) == om.go_object_id("m", "a/b.go", f(start={"line": 99, "col": 5}))


def test_the_build_variants_of_one_name_are_two_objects_not_a_collision():
    inv = {
        "files": [
            {
                "file": "p/a_cgo.go",
                "build": "ffi_cgo",
                "funcs": [
                    {
                        "name": "F",
                        "kind": "func",
                        "start": {"line": 1, "col": 1},
                        "end": {"line": 2, "col": 2},
                    }
                ],
            },
            {
                "file": "p/a_stub.go",
                "build": "!ffi_cgo",
                "funcs": [
                    {
                        "name": "F",
                        "kind": "func",
                        "start": {"line": 1, "col": 1},
                        "end": {"line": 2, "col": 2},
                    }
                ],
            },
        ]
    }
    blocks = [("m/p/a_stub.go", 1, 1, 2, 2, 1, 1)]
    objs, _ = om.go_objects("root", "m", inv, blocks)
    by = {o["id"]: o["coverage"] for o in objs}
    assert by["go:m/p.F@build(!ffi_cgo)"]["state"] == om.MEASURED
    # the variant that is not in the build has no block in the profile: UNVERIFIABLE, not 0%
    assert by["go:m/p.F@build(ffi_cgo)"] == {
        "state": "UNVERIFIABLE",
        "reason": "FILE_NOT_IN_PROFILE",
    }


def test_n1_two_different_objects_with_the_same_id_are_rejected():
    fn = {
        "name": "F",
        "kind": "func",
        "start": {"line": 1, "col": 1},
        "end": {"line": 2, "col": 2},
    }
    inv = {
        "files": [{"file": "p/a.go", "funcs": [fn]}, {"file": "p/b.go", "funcs": [fn]}]
    }  # same package, same name, no build tag
    with pytest.raises(om.ModelError, match="object id collision"):
        om.go_objects("root", "m", inv, [("m/p/a.go", 1, 1, 2, 2, 1, 1)])


def test_the_real_goastdump_unit():
    objs, st = goastdump_objects()
    assert len(objs) == 15 and st["partial"] == 0 and st["unattributed"] == 0
    by = {o["name"]: o["coverage"] for o in objs}
    assert (by["main"]["total"], by["main"]["covered"]) == (
        11,
        0,
    )  # the entry point is not run by any test
    assert (by["call"]["total"], by["call"]["covered"]) == (26, 18)
    assert by["call"]["kind"] == "statement" and by["call"]["uncovered"]
    assert all(c["state"] == om.MEASURED for c in by.values())
    # the uncovered spans of an object lie inside that object's own span
    for o in objs:
        for sl, _, el, _ in o["coverage"].get("uncovered", []):
            assert o["span"]["start"] <= sl and el <= o["span"]["end"]


# --------------------------------------------------------------------------- block attribution (N11), repeats (N9)


def fn(name, s, e, **kw):
    return {
        "name": name,
        "kind": "func",
        "start": {"line": s[0], "col": s[1]},
        "end": {"line": e[0], "col": e[1]},
        **kw,
    }


def objs_for(funcs, blocks, **kw):
    inv = {"files": [{"file": "a.go", "funcs": funcs}]}
    o, st = om.go_objects("root", "m", inv, [("m/a.go", *b) for b in blocks], **kw)
    return {x["name"]: x["coverage"] for x in o}, st


def test_n11_a_block_inside_one_object_is_attributed():
    cov, st = objs_for(
        [fn("A", (1, 1), (5, 2))], [(2, 2, 3, 10, 2, 1), (3, 10, 4, 3, 1, 0)]
    )
    assert cov["A"] == {
        "state": om.MEASURED,
        "kind": "statement",
        "total": 3,
        "covered": 2,
    }
    assert st["attributed"] == 2 and st["partial"] == 0 and st["unattributed"] == 0


def test_n11_a_block_that_cuts_an_object_is_not_attributed_and_no_value_is_made_up():
    cov, st = objs_for(
        [fn("A", (1, 1), (5, 2)), fn("B", (7, 1), (9, 2))],
        [(2, 2, 3, 10, 2, 1), (4, 2, 8, 3, 4, 1)],
    )
    assert st["partial"] == 1
    # the block (4,2)-(8,3) cuts A's end and B's start: neither gets a proportional share
    assert cov["A"] == {
        "state": om.UNVERIFIABLE,
        "reason": "PARTIAL_BLOCK",
        "blocks": 1,
    }
    assert cov["B"] == {
        "state": om.UNVERIFIABLE,
        "reason": "PARTIAL_BLOCK",
        "blocks": 1,
    }


def test_n11_a_block_that_spans_several_objects_is_not_attributed():
    cov, st = objs_for(
        [fn("A", (1, 1), (3, 2)), fn("B", (5, 1), (7, 2))], [(1, 1, 7, 2, 9, 1)]
    )
    assert (
        st["partial"] == 1
        and cov["A"]["reason"] == "PARTIAL_BLOCK"
        and cov["B"]["reason"] == "PARTIAL_BLOCK"
    )


def test_n11_a_block_outside_every_object_is_counted_not_dropped():
    # e.g. a package-level `var f = func() {...}`: it belongs to no top-level function
    cov, st = objs_for(
        [fn("A", (10, 1), (12, 2))], [(2, 5, 4, 3, 2, 1), (11, 2, 11, 9, 1, 1)]
    )
    assert st["unattributed"] == 1 and st["attributed"] == 1
    assert cov["A"]["total"] == 1


def test_n11_a_function_literal_belongs_to_the_function_that_contains_it():
    cov, _ = objs_for(
        [fn("Outer", (1, 1), (9, 2))], [(2, 2, 3, 3, 1, 1), (4, 10, 6, 4, 2, 0)]
    )  # the second block is inside a literal
    assert (cov["Outer"]["total"], cov["Outer"]["covered"]) == (3, 1)


def test_an_object_without_a_statement_is_not_applicable_not_zero():
    cov, _ = objs_for(
        [fn("Empty", (1, 1), (1, 12)), fn("A", (3, 1), (5, 2))], [(4, 2, 4, 9, 1, 1)]
    )
    assert cov["Empty"] == {"state": om.NOT_APPLICABLE, "reason": "NO_STATEMENTS"}


def test_a_block_that_appears_twice_is_covered_if_any_occurrence_is():
    blocks = om.parse_coverprofile(
        "mode: set\nm/a.go:2.2,3.3 2 0\nm/a.go:2.2,3.3 2 1\n"
    )
    assert len(blocks) == 1 and blocks[0][6] == 1
    with pytest.raises(om.ModelError, match="two statement counts"):
        om.parse_coverprofile("mode: set\nm/a.go:2.2,3.3 2 0\nm/a.go:2.2,3.3 3 1\n")


def test_n9_an_object_whose_coverage_differs_between_runs_is_nondeterministic():
    base = [(2, 2, 3, 10, 2, 1), (3, 10, 4, 3, 1, 1)]
    other = [
        (2, 2, 3, 10, 2, 1),
        (3, 10, 4, 3, 1, 0),
    ]  # DERIVED: the second block was not covered in this run
    inv = {
        "files": [
            {
                "file": "a.go",
                "funcs": [fn("A", (1, 1), (5, 2)), fn("Stable", (7, 1), (9, 2))],
            }
        ]
    }
    blocks = [("m/a.go", *b) for b in base] + [("m/a.go", 8, 2, 8, 9, 1, 1)]
    rep = [("m/a.go", *b) for b in other] + [("m/a.go", 8, 2, 8, 9, 1, 1)]
    objs, _ = om.go_objects("root", "m", inv, blocks, repeats=[rep])
    by = {o["name"]: o["coverage"] for o in objs}
    assert by["A"] == {
        "state": om.UNVERIFIABLE,
        "reason": "NONDETERMINISTIC",
        "range": [2, 3],
        "total": 3,
    }
    assert by["Stable"]["state"] == om.MEASURED


def test_a_failed_inventory_file_is_an_error():
    with pytest.raises(om.ModelError, match="could not parse"):
        om.go_objects(
            "root",
            "m",
            {"files": [{"file": "a.go", "funcs": [], "error": "boom"}]},
            [("m/a.go", 1, 1, 2, 2, 1, 1)],
        )


def test_a_profile_without_blocks_or_mode_is_an_error_not_zero():
    for bad in (
        "",
        "mode: set\n",
        "garbage\nm/a.go:1.1,2.2 1 1\n",
        "mode: set\nm/a.go:1.1,2.2 1\n",
    ):
        with pytest.raises(om.ModelError):
            om.parse_coverprofile(bad)


# --------------------------------------------------------------------------- Rust ids, test regions, instances (N10)


def test_rust_object_keys_fold_instances_closures_and_disambiguators():
    k = om.rust_object_key
    assert (
        k("cic_canonical[0]::canonical_integer")
        == "rust:cic_canonical::canonical_integer"
    )
    assert k("cic_canonical[7]::canonical_integer") == k(
        "cic_canonical[0]::canonical_integer"
    )  # the same function in another test binary
    assert k("cic_canonical[0]::canonical_integer::{closure#0}") == k(
        "cic_canonical[0]::canonical_integer"
    )  # a closure belongs to its function
    assert (
        k("a[0]::f::<alloc[0]::string::String>") == "rust:a::f"
    )  # an instantiation belongs to its generic function
    assert (
        k("<executor[0]::ExecRequest>::with_header::<alloc[0]::string::String>")
        == "rust:<executor::ExecRequest>::with_header"
    )
    assert (
        k("<a[0]::E as core[0]::convert::From<b[0]::S>>::from")
        == "rust:<a::E as core::convert::From<b::S>>::from"
    )
    assert om.rust_name("rust:<executor::ExecRequest>::with_header") == "with_header"


def test_n10_the_real_instances_of_one_function_are_one_object_with_the_instance_detail_kept(
    rust_root,
):
    objs, st = rust_objects(rust_root)
    assert objs, "no objects"
    ids = [o["id"] for o in objs]
    assert len(ids) == len(set(ids))
    for o in objs:
        c = o["coverage"]
        assert (
            c["kind"] == "region"
            and c["aggregation"] == "sum-over-instances/1"
            and c["instances"] == len(o["instances"])
        )


def test_cfg_test_spans_cover_exactly_the_marked_items():
    src = (
        "fn prod_a() {}\n"  # 1
        "#[cfg(test)]\n"  # 2
        "mod tests {\n"  # 3
        "    fn t() { let s = \"}{ not code\"; let c = '}'; /* } */ }\n"  # 4
        "}\n"  # 5
        "fn prod_b<'a>(x: &'a str) { let _ = x; }\n"  # 6
        "#[cfg(test)]\n"  # 7
        "fn only_test() {\n"  # 8
        '    let _ = r#"}"#;\n'  # 9
        "}\n"  # 10
        "fn prod_c() {}\n"  # 11
        "#[cfg(test)]\n"  # 12
        "use std::fmt;\n"  # 13
        "fn prod_d() {}\n"  # 14
    )
    assert om.cfg_test_spans(src) == [(2, 5), (7, 10), (12, 13)]


def test_a_production_function_after_a_cfg_test_item_is_not_test_code():
    # measured on the real ffi/src/lib.rs: a #[cfg(test)] item in the middle, production code after it
    src = "#[cfg(test)]\nfn helper() {\n}\n\npub fn production() {}\n"
    spans = om.cfg_test_spans(src)
    assert spans == [(1, 3)] and not any(a <= 5 <= b for a, b in spans)


def test_cfg_test_without_an_end_is_an_error():
    with pytest.raises(om.ModelError, match="cannot find the end"):
        om.cfg_test_spans("#[cfg(test)]\nmod tests {\n")


def test_test_code_of_the_real_file_is_excluded_and_counted(rust_root):
    objs, st = rust_objects(rust_root)
    assert st["test_code_excluded"] > 0
    assert not any("tests" in o["id"].split("::") for o in objs)


# --------------------------------------------------------------------------- mutation per object, the zero denominator


def test_real_mutants_map_to_objects_by_the_last_segment_of_the_function_name(
    rust_root,
):
    objs, st = rust_objects(rust_root)
    assert (
        st["mutants"] == 11
        and st["mutants_mapped"] == 11
        and st["mutants_unmapped"] == 0
        and st["mutants_ambiguous"] == 0
    )
    measured = [o for o in objs if o["mutation"]["state"] == om.MEASURED]
    assert sum(o["mutation"]["caught"] for o in measured) == 11
    for o in measured:
        assert o["mutation"]["score"] == {
            "state": "MEASURED",
            "value": 100.0,
            "caught": o["mutation"]["caught"],
            "evaluable": o["mutation"]["caught"],
        }


def test_a_function_without_a_listed_mutant_is_not_applicable_not_a_perfect_score(
    rust_root,
):
    mutants = real("mutants-cic-canonical.json")
    drop = next(
        m for m in mutants if m["function"]["function_name"].endswith("canonical_float")
    )["function"]["function_name"]
    kept = [
        m for m in mutants if m["function"]["function_name"] != drop
    ]  # DERIVED: the tool lists no mutant for one function
    verdicts = qg.rust_verdicts(real("outcomes-cic-canonical.json"), "fixture")
    objs, _ = om.rust_objects(
        real("llvm-cov-cic-canonical.json"),
        real("demangled-cic-canonical.json"),
        rust_root,
        kept,
        verdicts,
    )
    o = next(x for x in objs if x["name"] == "canonical_float")
    assert o["mutation"] == {"state": om.NOT_APPLICABLE, "reason": "NO_MUTANTS_LISTED"}
    assert "score" not in o["mutation"]


def test_n12_an_object_whose_mutants_are_all_unviable_has_no_score(rust_root):
    mutants = real("mutants-cic-canonical.json")
    target = next(
        m for m in mutants if m["function"]["function_name"].endswith("canonical_float")
    )
    same = [
        m["name"]
        for m in mutants
        if m["function"]["function_name"] == target["function"]["function_name"]
    ]
    objs, _ = rust_objects(rust_root, {n: "unviable" for n in same})  # DERIVED
    o = next(x for x in objs if x["name"] == "canonical_float")
    assert o["mutation"]["score"] == {
        "state": "NOT_APPLICABLE",
        "reason": "ALL_UNVIABLE",
    }
    assert o["mutation"]["unviable"] == len(same) and o["mutation"]["caught"] == 0


def test_a_mutant_that_fits_two_objects_is_ambiguous_not_guessed(rust_root):
    mutants = real("mutants-cic-canonical.json")
    m = copy.deepcopy(mutants[0])
    llvm = real("llvm-cov-cic-canonical.json")
    demangled = real("demangled-cic-canonical.json")
    # DERIVED: a second function of the same last name in the same file, inside the mutant's function span
    twin = copy.deepcopy(
        next(
            f
            for f in llvm["data"][0]["functions"]
            if demangled[f["name"]].split("::")[-1]
            == m["function"]["function_name"].split("::")[-1]
        )
    )
    twin["name"] = "_RNvC13cic_canonical99twin_symbol"
    demangled = {
        **demangled,
        twin["name"]: "cic_canonical[0]::other_mod::"
        + m["function"]["function_name"].split("::")[-1],
    }
    llvm["data"][0]["functions"].append(twin)
    verdicts = qg.rust_verdicts(real("outcomes-cic-canonical.json"), "fixture")
    objs, st = om.rust_objects(llvm, demangled, rust_root, [m], verdicts)
    assert st["mutants_ambiguous"] == 1 and st["mutants_mapped"] == 0
    assert all(o["mutation"].get("caught", 0) == 0 for o in objs)


def test_a_symbol_without_a_demangled_name_is_an_error_not_a_dropped_object(rust_root):
    with pytest.raises(om.ModelError, match="no demangled name"):
        om.rust_objects(real("llvm-cov-cic-canonical.json"), {}, rust_root, None, None)


# --------------------------------------------------------------------------- the record and its verifier

TOOLS = {
    "go": "go1.26.10",
    "rustc": "1.96.1",
    "cargo-mutants": "27.1.0",
    "cargo-llvm-cov": "0.9.1",
    "c++filt": "2.40",
}
SCOPE = json.load(open(SCOPE_FILE))


def make_record(rust_root, tools=None):
    go, _ = goastdump_objects()
    rs, _ = rust_objects(rust_root)
    inputs = {"fixture": {"sha256": "0" * 64, "bytes": 1}}
    return om.build_record(
        go + rs,
        "c" * 40,
        "t" * 40,
        tools or dict(TOOLS),
        inputs,
        SCOPE,
        {"go:goastdump": {"partial": 0}},
    )


def reseal(rec):
    rec["summary"] = om.summarize(rec["objects"])
    rec["digest"] = om.canonical_digest({k: v for k, v in rec.items() if k != "digest"})
    return rec


def test_the_record_of_the_real_fixtures_is_intact(rust_root):
    rec = make_record(rust_root)
    assert om.verify_l1(rec) == []
    assert rec["summary"]["by_unit"]["goastdump"]["objects"] == 15
    assert [o["id"] for o in rec["objects"]] == sorted(o["id"] for o in rec["objects"])


def test_n4_a_changed_record_is_rejected(rust_root):
    rec = make_record(rust_root)
    t = copy.deepcopy(rec)
    t["objects"][0]["name"] = "renamed"  # content changed, digest kept
    assert any("digest does not match" in b for b in om.verify_l1(t))
    t = copy.deepcopy(rec)
    del t["objects"][3]  # an object removed, digest kept
    assert any("digest does not match" in b for b in om.verify_l1(t))
    t = copy.deepcopy(rec)
    t["summary"][
        "objects"
    ] += 1  # a summary that no longer matches its objects, digest recomputed
    t["digest"] = om.canonical_digest({k: v for k, v in t.items() if k != "digest"})
    assert any("summary does not match" in b for b in om.verify_l1(t))
    t = copy.deepcopy(rec)
    t["inputs"] = {}
    t = reseal(t)
    assert any("no input digests" in b for b in om.verify_l1(t))


def test_n2_every_field_of_every_object_has_a_valid_state_and_a_coded_reason(rust_root):
    rec = make_record(rust_root)
    for o in rec["objects"]:
        for f in om.FIELDS:
            assert o[f]["state"] in om.STATES
            if o[f]["state"] != om.MEASURED:
                assert o[f]["reason"] in om.REASONS
    t = copy.deepcopy(rec)
    del t["objects"][0]["tests"]
    assert any("field tests is missing" in b for b in om.verify_l1(reseal(t)))
    t = copy.deepcopy(rec)
    t["objects"][0]["tests"] = {"state": "UNVERIFIABLE"}  # a missing reason
    assert any("without a valid reason" in b for b in om.verify_l1(reseal(t)))
    t = copy.deepcopy(rec)
    t["objects"][0]["tests"] = {"state": "VERIFIED"}  # a state that is not in the enum
    assert any("no valid state" in b for b in om.verify_l1(reseal(t)))


def test_n5_an_unknown_tool_version_makes_the_record_unusable(rust_root):
    with pytest.raises(om.ModelError, match="unknown"):
        make_record(rust_root, tools={**TOOLS, "cargo-mutants": "unknown"})
    rec = make_record(rust_root)
    rec["tools"]["go"] = ""
    assert any(
        "version of the tool 'go' is unknown" in b for b in om.verify_l1(reseal(rec))
    )
    rec = make_record(rust_root)
    rec["tools"] = {}
    assert any("no tool versions" in b for b in om.verify_l1(reseal(rec)))


def test_n6_a_coverage_kind_that_is_not_measured_cannot_be_branch(rust_root):
    rec = make_record(rust_root)
    assert "branch" not in om.COVERAGE_KINDS and rec["coverage_kinds"] == [
        "statement",
        "line",
        "region",
    ]
    o = next(o for o in rec["objects"] if o["coverage"]["state"] == om.MEASURED)
    o["coverage"]["kind"] = "branch"
    assert any(
        "coverage kind 'branch' is not one of" in b for b in om.verify_l1(reseal(rec))
    )


def test_n7_the_global_and_the_object_level_view_are_separate_and_unverifiable_is_counted(
    rust_root,
):
    rec = make_record(rust_root)
    sm = rec["summary"]
    assert set(sm["by_unit"]) == {"goastdump", "rust"} and "fields" in sm
    # every object is unverifiable for the test link (no per-test attribution): the summary says so, there is no single "verified"
    assert sm["fields"]["tests"][om.UNVERIFIABLE] == sm["objects"]
    assert (
        "verified" not in json.dumps(sm).lower()
        or "unverifiable" in json.dumps(sm).lower()
    )
    assert not any(k.lower() == "verified" for k in rec)


def test_a_record_bound_to_another_policy_version_is_rejected(rust_root):
    rec = make_record(rust_root)
    rec["policy"] = "mutation-policy/1"
    assert any("bound to 'mutation-policy/1'" in b for b in om.verify_l1(reseal(rec)))


def test_duplicate_object_ids_cannot_be_built(rust_root):
    go, _ = goastdump_objects()
    with pytest.raises(om.ModelError, match="duplicate object ids"):
        om.build_record(
            go + go[:1],
            "c" * 40,
            "t" * 40,
            dict(TOOLS),
            {"x": {"sha256": "0" * 64, "bytes": 1}},
            SCOPE,
            {},
        )


def git_repo(tmp_path):
    env = {
        **os.environ,
        "GIT_AUTHOR_NAME": "t",
        "GIT_AUTHOR_EMAIL": "t@x",
        "GIT_COMMITTER_NAME": "t",
        "GIT_COMMITTER_EMAIL": "t@x",
    }
    d = tmp_path / "checkout"
    d.mkdir()
    for args in (["init", "-q"],):
        subprocess.run(["git", "-C", str(d), *args], check=True, env=env)
    shutil.copy(os.path.join(FIX, "goastdump-main.go.txt"), d / "main.go")
    subprocess.run(["git", "-C", str(d), "add", "-A"], check=True, env=env)
    subprocess.run(
        [
            "git",
            "-C",
            str(d),
            "-c",
            "commit.gpgsign=false",
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "-q",
            "-m",
            "x",
        ],
        check=True,
        env=env,
    )
    commit = subprocess.run(
        ["git", "-C", str(d), "rev-parse", "HEAD"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.strip()
    tree = subprocess.run(
        ["git", "-C", str(d), "rev-parse", "HEAD^{tree}"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.strip()
    return str(d), commit, tree


def test_n13_l2_is_not_checked_without_inputs_and_fails_on_a_changed_input(
    tmp_path, rust_root
):
    checkout, commit, tree = git_repo(tmp_path)
    inp = tmp_path / "cover.out"
    inp.write_text(text("goastdump-cover.out"))
    digest, size = om.sha256_file(str(inp))
    go, _ = goastdump_objects()
    rec = om.build_record(
        go,
        commit,
        tree,
        dict(TOOLS),
        {"cover": {"sha256": digest, "bytes": size}},
        SCOPE,
        {},
    )
    assert om.verify_l2(rec, None, {}) == (
        "NOT_CHECKED",
        ["no checkout and no input files were given"],
    )
    assert om.verify_l2(rec, checkout, {"cover": str(inp)}) == ("PASS", [])
    inp.write_text(text("goastdump-cover.out") + "goastdump/main.go:1.1,2.2 1 1\n")
    v, f = om.verify_l2(rec, checkout, {"cover": str(inp)})
    assert v == "FAIL" and "does not match the digest" in f[0]
    rec["identity"]["tree"] = "0" * 40
    assert om.verify_l2(rec, checkout, {})[0] == "FAIL"
    assert om.verify_l2(rec, checkout, {"unknown": str(inp)})[0] == "FAIL"


def test_n13_a_consistently_forged_record_passes_l1_but_not_l3(tmp_path):
    checkout, commit, tree = git_repo(tmp_path)
    go, _ = goastdump_objects()
    inputs = {"x": {"sha256": "0" * 64, "bytes": 1}}
    honest = om.build_record(go, commit, tree, dict(TOOLS), inputs, SCOPE, {})
    forged = copy.deepcopy(honest)
    forged["objects"].pop(5)  # an object is removed ...
    forged = reseal(
        forged
    )  # ... and the digest and the summary are recomputed consistently
    assert om.verify_l1(forged) == []  # L1 proves only the internal consistency
    res = om.verify_record(forged, checkout, {}, {"goastdump": ["main.go"]}, None)
    assert res["L1"]["verdict"] == "INTEGRITY_OK" and res["L3"]["verdict"] == "FAIL"
    assert "14 objects but 15 top-level" in res["L3"]["findings"][0]
    ok = om.verify_record(honest, checkout, {}, {"goastdump": ["main.go"]}, None)
    assert (
        ok["L3"]["verdict"] == "PARTIAL"
    )  # Go is proven, Rust could not be: said, not hidden
    assert any("equal the independent" in p for p in ok["L3"]["proven"])


def test_n13_a_record_that_is_not_intact_is_not_trusted_by_the_later_levels(rust_root):
    rec = make_record(rust_root)
    rec["digest"] = "0" * 64
    res = om.verify_record(rec)
    assert res["L1"]["verdict"] == "FAIL"
    assert (
        res["L2"]["verdict"] == "NOT_CHECKED" and res["L3"]["verdict"] == "NOT_CHECKED"
    )


def test_n13_the_verifier_output_has_three_levels_and_no_combined_verdict(
    tmp_path, rust_root, capsys
):
    rec = make_record(rust_root)
    p = tmp_path / "rec.json"
    p.write_text(json.dumps(rec))
    rc = om.main(["verify", "--record", str(p)])
    out = capsys.readouterr().out
    assert rc == 0
    for lvl, word in (("L1", "INTEGRITY_OK"), ("L2", "NOT_CHECKED"), ("L3", "PARTIAL")):
        assert f"{lvl} {word}" in out
    assert "no combined 'verified'" in out and "VERIFIED" not in out


def test_the_rust_l3_names_a_function_that_has_no_object(rust_root):
    rec = make_record(rust_root)
    mutants = real("mutants-cic-canonical.json")
    mutants.append(
        {
            "file": "cic-canonical/src/lib.rs",
            "name": "x",
            "function": {
                "function_name": "m::ghost_function",
                "span": {"start": {"line": 1}, "end": {"line": 2}},
            },
        }
    )
    v, _, findings = om.verify_l3(rec, None, None, mutants)
    assert v == "FAIL" and "ghost_function" in findings[0]


def test_no_go_or_rust_source_lives_under_tests_outside_testdata():
    # a `package main` file in a fixture directory becomes a package of the root module: the coverage floor
    # (0% < 80%) and the mutation gate (0 of 37 mutants covered) both fail on it. Measured on this PR's own
    # first CI run (#174, run 43638326995 and 43638326975): fixtures that are source code are stored as .txt.
    root = os.path.abspath(os.path.join(SCRIPTS, ".."))
    tracked = subprocess.run(
        ["git", "-C", root, "ls-files", "tests"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.splitlines()
    bad = [f for f in tracked if f.endswith((".go", ".rs")) and "/testdata/" not in f]
    assert bad == []


def test_a_source_file_under_tests_is_not_production_in_the_scope():
    scope = json.load(open(SCOPE_FILE))
    for lang, path in (
        ("go", "tests/test_tools/fixtures/x/main.go"),
        ("rust", "tests/test_tools/fixtures/x/lib.rs"),
    ):
        cls, rule = om.classify(scope, lang, path)
        assert cls == "test-data" and rule["reason"]
