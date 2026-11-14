"""Completeness validation of the mutation gates (scripts/quality_gate.py, PR-03).

The Rust fixtures are real cargo-mutants 27.1.0 output (tests/test_tools/fixtures/quality_gate);
the cases with another verdict (missed, timeout, unviable) are derived from it by changing the
summary and the counters of the real file.
"""

import copy
import hashlib
import json
import os
import random
import stat
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import quality_gate as qg  # noqa: E402

FIX = os.path.join(os.path.dirname(__file__), "fixtures/quality_gate")
GATE = os.path.join(SCRIPTS, "quality_gate.py")
GREMLINS_GATE = os.path.join(SCRIPTS, "gremlins_gate.py")


def real(name):
    with open(os.path.join(FIX, name)) as fh:
        return json.load(fh)


def listing_bytes(entries=None):
    return json.dumps(
        entries if entries is not None else real("cargo-mutants-27.1.0-list.json")
    ).encode()


def rust_plan(entries=None):
    return qg.build_rust_plan(
        listing_bytes(entries),
        0,
        ["cargo", "mutants", "--list", "--json"],
        "27.1.0",
        "full",
    )


def shard_report(plan, shard=0, shards=1, outcomes=None):
    out = (
        outcomes if outcomes is not None else real("cargo-mutants-27.1.0-outcomes.json")
    )
    return {
        "schema": qg.SHARD_SCHEMA,
        "plan_digest": plan["digest"],
        "shard": shard,
        "shards": shards,
        "tool_version": out.get("cargo_mutants_version"),
        "outcomes": out,
    }


def with_verdict(outcomes, index, summary):
    """A copy of the real outcomes where mutant #index has another verdict (counters kept consistent)."""
    o = copy.deepcopy(outcomes)
    mutants = [r for r in o["outcomes"] if r["scenario"] != "Baseline"]
    old = qg.SUMMARY_TO_VERDICT[mutants[index]["summary"]]
    mutants[index]["summary"] = summary
    new = qg.SUMMARY_TO_VERDICT[summary]
    o[old] -= 1
    o[new] += 1
    return o


def write(tmp_path, name, doc):
    p = tmp_path / name
    p.write_text(json.dumps(doc))
    return str(p)


# ----------------------------------------------------------------------- F1: mutant ids


def test_the_real_listing_has_unique_ids_and_a_stable_digest():
    entries = real("cargo-mutants-27.1.0-list.json")
    plan = rust_plan(entries)
    assert plan["count"] == 5 and plan["state"] == qg.NONEMPTY
    assert plan["ids"] == sorted(e["name"] for e in entries)
    assert plan["id_contract"] == "mutant-id/1"


def test_f1_the_order_of_the_listing_does_not_change_the_plan():
    entries = real("cargo-mutants-27.1.0-list.json")
    shuffled = entries[:]
    random.Random(7).shuffle(shuffled)
    assert shuffled != entries
    a, b = rust_plan(entries), rust_plan(shuffled)
    assert a["ids"] == b["ids"]
    # the digest covers the ids, not the raw listing: only the listing digest differs
    assert {k: v for k, v in a.items() if k not in ("digest", "listing")} == {
        k: v for k, v in b.items() if k not in ("digest", "listing")
    }


def test_f1_two_different_mutants_never_share_an_id():
    entries = real("cargo-mutants-27.1.0-list.json")
    dup = entries + [
        dict(entries[0], replacement="something else")
    ]  # same name, another mutant
    with pytest.raises(qg.GateError, match="not unique"):
        qg.build_rust_plan(listing_bytes(dup), 0, ["cargo"], "27.1.0", "full")


def test_f1_an_id_that_differs_by_one_character_is_a_missing_and_an_unknown_verdict():
    plan = rust_plan()
    out = real("cargo-mutants-27.1.0-outcomes.json")
    mutant = next(r for r in out["outcomes"] if r["scenario"] != "Baseline")
    mutant["scenario"]["Mutant"]["name"] += "x"
    with pytest.raises(qg.GateError, match="not in the plan"):
        qg.verify_rust(plan, [("r", shard_report(plan, outcomes=out))], 1)


def test_f1_a_report_of_another_plan_is_rejected():
    plan = rust_plan()
    rep = shard_report(plan)
    rep["plan_digest"] = "0" * 64
    with pytest.raises(qg.GateError, match="different plan"):
        qg.verify_rust(plan, [("r", rep)], 1)


def test_f1_another_tool_version_in_the_report_is_rejected():
    plan = rust_plan()
    rep = shard_report(plan)
    rep["tool_version"] = "99.0.0"
    with pytest.raises(qg.GateError, match="measured with cargo-mutants"):
        qg.verify_rust(plan, [("r", rep)], 1)


def test_f1_another_contract_version_of_the_plan_is_rejected(tmp_path):
    plan = rust_plan()
    plan["id_contract"] = "mutant-id/2"
    plan["digest"] = qg.canonical_digest(
        {k: v for k, v in plan.items() if k != "digest"}
    )
    with pytest.raises(qg.GateError, match="schema/contract"):
        qg.load_plan(write(tmp_path, "p.json", plan), "rust-mutants")


# ----------------------------------------------------------------------- F2: the verdicts


def verify(plan, outcomes, shards=1):
    return qg.verify_rust(plan, [("r", shard_report(plan, outcomes=outcomes))], shards)


def test_f2_the_real_run_is_all_caught():
    acct = verify(rust_plan(), real("cargo-mutants-27.1.0-outcomes.json"))
    assert acct == {"planned": 5, "caught": 5, "missed": 0, "timeout": 0, "unviable": 0}


def test_f2_a_timeout_is_counted_on_its_own_and_is_not_a_catch():
    out = with_verdict(real("cargo-mutants-27.1.0-outcomes.json"), 0, "Timeout")
    acct = verify(rust_plan(), out)
    assert (acct["caught"], acct["timeout"], acct["planned"]) == (4, 1, 5)


def test_f2_an_unviable_mutant_stays_in_the_denominator():
    out = with_verdict(real("cargo-mutants-27.1.0-outcomes.json"), 0, "Unviable")
    acct = verify(rust_plan(), out)
    assert (acct["caught"], acct["unviable"], acct["planned"]) == (4, 1, 5)
    assert sum(acct[v] for v in qg.VERDICTS) == acct["planned"]


def test_f2_the_counters_of_the_tool_must_agree_with_its_rows():
    out = real("cargo-mutants-27.1.0-outcomes.json")
    out["caught"] = 4  # a tampered counter: one catch "disappears" from the numerator
    with pytest.raises(qg.GateError, match="counter caught"):
        verify(rust_plan(), out)


def test_f2_a_dropped_outcome_cannot_shrink_the_denominator():
    out = real("cargo-mutants-27.1.0-outcomes.json")
    out["outcomes"] = [
        r
        for r in out["outcomes"]
        if r["scenario"] == "Baseline" or r is not out["outcomes"][1]
    ]
    out["total_mutants"] -= 1
    out["caught"] -= 1
    with pytest.raises(qg.GateError, match="no verdict"):
        verify(rust_plan(), out)


def test_f2_an_unknown_verdict_is_rejected():
    out = real("cargo-mutants-27.1.0-outcomes.json")
    next(r for r in out["outcomes"] if r["scenario"] != "Baseline")[
        "summary"
    ] = "SomethingNew"
    with pytest.raises(qg.GateError, match="unknown verdict"):
        verify(rust_plan(), out)


def test_f2_a_duplicated_verdict_is_rejected():
    out = real("cargo-mutants-27.1.0-outcomes.json")
    out["outcomes"].append(copy.deepcopy(out["outcomes"][1]))
    out["total_mutants"] += 1
    out["caught"] += 1
    with pytest.raises(qg.GateError, match="more than one verdict"):
        verify(rust_plan(), out)


def test_f2_a_failed_baseline_is_rejected():
    out = real("cargo-mutants-27.1.0-outcomes.json")
    out["outcomes"][0]["summary"] = "Failure"
    with pytest.raises(qg.GateError, match="baseline"):
        verify(rust_plan(), out)


@pytest.mark.parametrize(
    "summary, fails",
    [
        ("CaughtMutant", False),
        ("Unviable", False),
        ("MissedMutant", True),
        ("Timeout", True),
    ],
)
def test_f2_what_fails_the_gate(tmp_path, summary, fails, capsys):
    plan = rust_plan()
    out = with_verdict(real("cargo-mutants-27.1.0-outcomes.json"), 0, summary)
    pdir = tmp_path / "plan"
    rdir = tmp_path / "reports"
    pdir.mkdir()
    rdir.mkdir()
    (pdir / "rust-plan.json").write_text(json.dumps(plan))
    (rdir / "shard-0.json").write_text(json.dumps(shard_report(plan, outcomes=out)))
    rc = qg.main(
        [
            "verify",
            "--rust-plan",
            str(pdir / "rust-plan.json"),
            "--rust-reports",
            str(rdir),
            "--rust-shards",
            "1",
        ]
    )
    text = capsys.readouterr().out
    assert (rc != 0) == fails, text
    assert (
        "mutation-policy/1" in text and "planned=5" in text
    )  # the accounting is always shown


# ----------------------------------------------------------------------- F3: plan states


def test_f3_an_empty_listing_that_succeeded_is_valid_and_empty(tmp_path):
    # measured: cargo-mutants 27.1.0 prints 0 bytes (not "[]") for a diff with no mutable code
    plan = qg.build_rust_plan(
        b"", 0, ["cargo", "mutants", "--list", "--json"], "27.1.0", "diff"
    )
    assert plan["state"] == qg.EMPTY and plan["count"] == 0
    assert (
        plan["listing"]["exit_code"] == 0
        and plan["listing"]["raw_sha256"] == hashlib.sha256(b"").hexdigest()
    )
    loaded = qg.load_plan(write(tmp_path, "p.json", plan), "rust-mutants")
    assert loaded["state"] == qg.EMPTY


def test_f3_a_failed_listing_is_never_an_empty_plan():
    for code in (1, 6):
        with pytest.raises(qg.GateError, match=f"status {code}"):
            qg.build_rust_plan(b"", code, ["cargo"], "27.1.0", "diff")


def test_f3_a_forged_empty_plan_without_a_successful_listing_is_invalid(tmp_path):
    plan = qg.build_rust_plan(b"", 0, ["cargo"], "27.1.0", "diff")
    plan["listing"]["exit_code"] = 6  # the listing failed, the record claims "empty"
    plan["digest"] = qg.canonical_digest(
        {k: v for k, v in plan.items() if k != "digest"}
    )
    with pytest.raises(qg.GateError, match="not backed by a successful listing"):
        qg.load_plan(write(tmp_path, "p.json", plan), "rust-mutants")


def test_f3_a_missing_plan_is_invalid(tmp_path):
    with pytest.raises(qg.GateError, match="PLAN_INVALID.*missing or unreadable"):
        qg.load_plan(str(tmp_path / "nope.json"), "rust-mutants")


def test_f3_a_zero_byte_plan_file_is_invalid_not_empty(tmp_path):
    p = tmp_path / "p.json"
    p.write_bytes(b"")
    with pytest.raises(qg.GateError, match="PLAN_INVALID"):
        qg.load_plan(str(p), "rust-mutants")


def test_f3_a_tampered_plan_is_invalid(tmp_path):
    plan = rust_plan()
    plan["ids"] = plan["ids"][:-1]
    plan[
        "count"
    ] -= 1  # the shrunken plan is consistent in itself, but not with its digest
    with pytest.raises(qg.GateError, match="digest"):
        qg.load_plan(write(tmp_path, "p.json", plan), "rust-mutants")


def test_f3_a_plan_of_the_wrong_kind_is_invalid(tmp_path):
    with pytest.raises(qg.GateError, match="go-packages"):
        qg.load_plan(
            write(tmp_path, "p.json", qg.build_go_plan(["./a"], {})), "rust-mutants"
        )


def test_f3_an_empty_plan_with_a_planner_reason_is_valid(tmp_path):
    p = tmp_path / "p.json"
    assert (
        qg.main(
            ["plan-rust", "--out", str(p), "--skipped", "planner: no Rust file changed"]
        )
        == 0
    )
    assert qg.load_plan(str(p), "rust-mutants")["state"] == qg.EMPTY


def test_f3_verdicts_for_an_empty_plan_are_rejected():
    plan = qg.build_rust_plan(b"", 0, ["cargo"], "27.1.0", "diff")
    other = rust_plan()
    with pytest.raises(qg.GateError, match="never planned"):
        qg.verify_rust(plan, [("r", shard_report(other))], 0)
    with pytest.raises(qg.GateError, match="never planned"):
        qg.verify_rust(plan, [], 3)


def test_f3_the_empty_plan_passes_with_nothing_run():
    plan = qg.build_rust_plan(b"", 0, ["cargo"], "27.1.0", "diff")
    assert qg.verify_rust(plan, [], 0)["planned"] == 0


def test_f3_a_nonempty_plan_with_no_shards_is_rejected():
    with pytest.raises(qg.GateError, match="0 shards"):
        qg.verify_rust(rust_plan(), [], 0)


def test_f3_a_skipped_job_is_not_evidence(tmp_path):
    # the Rust job was skipped (no shard reports) although the plan has mutants: red, whatever the
    # job result said
    with pytest.raises(qg.GateError, match="missing shard"):
        qg.verify_rust(rust_plan(), [], 2)


# ----------------------------------------------------------------------- shards of Rust


def test_a_missing_shard_makes_the_run_incomplete():
    plan = rust_plan()
    with pytest.raises(qg.GateError, match=r"missing shard report\(s\): \[1\]"):
        qg.verify_rust(plan, [("r0", shard_report(plan, 0, 2))], 2)


def test_the_same_shard_reported_twice_is_rejected():
    plan = rust_plan()
    reps = [("a", shard_report(plan, 0, 1)), ("b", shard_report(plan, 0, 1))]
    with pytest.raises(qg.GateError, match="reported twice"):
        qg.verify_rust(plan, reps, 1)


def test_a_mutant_verdict_in_two_shards_is_rejected():
    plan = rust_plan()
    reps = [("a", shard_report(plan, 0, 2)), ("b", shard_report(plan, 1, 2))]
    with pytest.raises(qg.GateError, match="already has a verdict from another shard"):
        qg.verify_rust(plan, reps, 2)


def test_a_planned_mutant_without_a_verdict_is_rejected():
    plan = rust_plan()
    out = real("cargo-mutants-27.1.0-outcomes.json")
    keep = [r for r in out["outcomes"] if r["scenario"] == "Baseline"] + [
        r for r in out["outcomes"] if r["scenario"] != "Baseline"
    ][:3]
    out["outcomes"] = keep
    out["total_mutants"] = out["caught"] = 3
    with pytest.raises(qg.GateError, match="have no verdict"):
        verify(plan, out)


def test_a_shard_that_does_not_fit_the_planned_count_is_rejected():
    plan = rust_plan()
    with pytest.raises(qg.GateError, match="does not fit"):
        qg.verify_rust(plan, [("r", shard_report(plan, 0, 3))], 2)


def test_the_shards_cover_the_whole_set_for_any_split():
    # the mutants of the plan split over more shards than there are mutants stay complete
    plan = rust_plan()
    out = real("cargo-mutants-27.1.0-outcomes.json")
    base = [r for r in out["outcomes"] if r["scenario"] == "Baseline"]
    muts = [r for r in out["outcomes"] if r["scenario"] != "Baseline"]
    reps = []
    for k in range(8):  # more shards than mutants: some shards hold nothing
        mine = muts[k::8]
        o = dict(out, outcomes=base + mine, total_mutants=len(mine), caught=len(mine))
        reps.append((f"s{k}", shard_report(plan, k, 8, o)))
    assert qg.verify_rust(plan, reps, 8)["caught"] == 5


def test_an_unreadable_report_is_an_error_not_a_skip(tmp_path):
    (tmp_path / "shard-0.json").write_text("{not json")
    with pytest.raises(qg.GateError, match="unreadable or corrupt"):
        qg.read_reports(str(tmp_path), "shard-*.json")


def test_wrap_shard_refuses_an_inconsistent_outcomes_file(tmp_path):
    out = real("cargo-mutants-27.1.0-outcomes.json")
    out["caught"] = 0
    with pytest.raises(qg.GateError, match="counter caught"):
        qg.wrap_shard("d" * 64, 0, 1, write(tmp_path, "o.json", out))


def test_wrap_shard_refuses_a_missing_outcomes_file(tmp_path):
    with pytest.raises(qg.GateError, match="missing or unreadable"):
        qg.wrap_shard("d" * 64, 0, 1, str(tmp_path / "none.json"))


# ----------------------------------------------------------------------- Go


def go_row(pkg, status="ok", killed=3, lived=0, nc=0, to=0):
    return {
        "pkg": pkg,
        "status": status,
        "killed": killed,
        "lived": lived,
        "not_covered": nc,
        "timed_out": to,
    }


def go_report(plan, chunk, rows):
    return {
        "schema": qg.GO_SCHEMA,
        "plan_digest": plan["digest"],
        "chunk": chunk,
        "packages": rows,
    }


@pytest.fixture()
def goplan():
    return qg.build_go_plan(["./a", "./b", "./c"], {})


def test_go_complete_run_passes(goplan):
    reps = [
        ("c0", go_report(goplan, 0, [go_row("./a"), go_row("./c", "no-mutants", 0)])),
        ("c1", go_report(goplan, 1, [go_row("./b", to=2)])),
    ]
    acct = qg.verify_go(goplan, reps, 2)
    assert acct == {"planned": 3, "ok": 2, "no-mutants": 1, "timed_out": 2}


def test_go_missing_chunk(goplan):
    with pytest.raises(qg.GateError, match=r"missing chunk report\(s\): \[1\]"):
        qg.verify_go(
            goplan,
            [
                (
                    "c0",
                    go_report(goplan, 0, [go_row("./a"), go_row("./b"), go_row("./c")]),
                )
            ],
            2,
        )


def test_go_package_without_a_verdict(goplan):
    reps = [("c0", go_report(goplan, 0, [go_row("./a"), go_row("./b")]))]
    with pytest.raises(qg.GateError, match="have no verdict"):
        qg.verify_go(goplan, reps, 1)


def test_go_package_not_in_the_plan(goplan):
    reps = [
        (
            "c0",
            go_report(goplan, 0, [go_row(p) for p in ("./a", "./b", "./c", "./zzz")]),
        )
    ]
    with pytest.raises(qg.GateError, match="not in the plan"):
        qg.verify_go(goplan, reps, 1)


def test_go_duplicated_package_verdict(goplan):
    reps = [
        (
            "c0",
            go_report(
                goplan, 0, [go_row("./a"), go_row("./a"), go_row("./b"), go_row("./c")]
            ),
        )
    ]
    with pytest.raises(qg.GateError, match="already has a verdict"):
        qg.verify_go(goplan, reps, 1)


def test_go_same_package_in_two_chunks(goplan):
    reps = [
        ("c0", go_report(goplan, 0, [go_row("./a"), go_row("./b")])),
        ("c1", go_report(goplan, 1, [go_row("./b"), go_row("./c")])),
    ]
    with pytest.raises(qg.GateError, match="already has a verdict"):
        qg.verify_go(goplan, reps, 2)


@pytest.mark.parametrize("status", ["fail", "unparseable"])
def test_go_a_failed_or_unparseable_package_is_red(goplan, status):
    reps = [
        (
            "c0",
            go_report(goplan, 0, [go_row("./a", status), go_row("./b"), go_row("./c")]),
        )
    ]
    with pytest.raises(qg.GateError, match="did not pass"):
        qg.verify_go(goplan, reps, 1)


def test_go_unknown_status_empty_chunk_and_foreign_plan(goplan):
    with pytest.raises(qg.GateError, match="unknown status"):
        qg.verify_go(goplan, [("c", go_report(goplan, 0, [go_row("./a", "great")]))], 1)
    with pytest.raises(qg.GateError, match="no package verdicts"):
        qg.verify_go(goplan, [("c", go_report(goplan, 0, []))], 1)
    rep = go_report(goplan, 0, [go_row("./a")])
    rep["plan_digest"] = "1" * 64
    with pytest.raises(qg.GateError, match="different plan"):
        qg.verify_go(goplan, [("c", rep)], 1)


def test_go_empty_plan_and_planned_but_no_chunks():
    empty = qg.build_go_plan([], {})
    assert empty["state"] == qg.EMPTY and qg.verify_go(empty, [], 0)["planned"] == 0
    with pytest.raises(qg.GateError, match="never|exist"):
        qg.verify_go(empty, [], 2)
    with pytest.raises(qg.GateError, match="0 chunks"):
        qg.verify_go(qg.build_go_plan(["./a"], {}), [], 0)


def test_go_plan_dedups_and_sorts():
    assert qg.build_go_plan(["./b", "./a", "./b"], {})["ids"] == ["./a", "./b"]


# ----------------------------------------------------------------------- coverage report


def test_coverprofile_valid(tmp_path):
    p = tmp_path / "c.out"
    p.write_text(
        "mode: set\nexample.com/m/a.go:3.10,5.2 1 1\nexample.com/m/a.go:7.1,8.2 2 0\n"
    )
    assert qg.check_coverprofile(str(p)) == 2


@pytest.mark.parametrize(
    "content, why",
    [
        (None, "missing or unreadable"),
        ("", "no valid 'mode:'"),
        ("mode: set\n", "no blocks"),
        ("garbage\nexample.com/m/a.go:3.10,5.2 1 1\n", "no valid 'mode:'"),
        ("mode: set\nexample.com/m/a.go:3.10,5.2 1\n", "malformed block"),
    ],
)
def test_a_broken_coverage_profile_is_neither_zero_nor_success(tmp_path, content, why):
    p = tmp_path / "c.out"
    if content is not None:
        p.write_text(content)
    with pytest.raises(qg.GateError, match=why):
        qg.check_coverprofile(str(p))


def test_coverprofile_cli_exit_codes(tmp_path):
    good = tmp_path / "g.out"
    good.write_text("mode: atomic\na/b.go:1.1,2.2 1 1\n")
    ok = subprocess.run(
        [sys.executable, GATE, "check-coverprofile", str(good)],
        capture_output=True,
        text=True,
    )
    bad = subprocess.run(
        [sys.executable, GATE, "check-coverprofile", str(tmp_path / "x")],
        capture_output=True,
        text=True,
    )
    assert ok.returncode == 0 and bad.returncode == 1 and bad.stdout == ""


# ----------------------------------------------------------------------- CLI of the plan


def fake_cargo(tmp_path, body):
    f = tmp_path / "cargo"
    f.write_text("#!/usr/bin/env bash\n" + body + "\n")
    f.chmod(f.stat().st_mode | stat.S_IEXEC)
    return str(f)


def test_plan_rust_cli_failed_listing_writes_no_plan_and_removes_a_stale_one(tmp_path):
    out = tmp_path / "plan.json"
    out.write_text("a stale plan from an earlier run")
    cargo = fake_cargo(
        tmp_path,
        'if [ "$2" = "--version" ]; then echo "cargo-mutants 27.1.0"; exit 0; fi; echo boom >&2; exit 6',
    )
    r = subprocess.run(
        [
            sys.executable,
            GATE,
            "plan-rust",
            "--out",
            str(out),
            "--",
            cargo,
            "mutants",
            "--list",
            "--json",
        ],
        capture_output=True,
        text=True,
    )
    assert r.returncode == 1 and not out.exists()
    assert "status 6" in r.stderr


def test_plan_rust_cli_valid_empty_and_nonempty(tmp_path):
    empty = fake_cargo(
        tmp_path,
        'if [ "$2" = "--version" ]; then echo "cargo-mutants 27.1.0"; exit 0; fi; exit 0',
    )
    out = tmp_path / "e.json"
    r = subprocess.run(
        [
            sys.executable,
            GATE,
            "plan-rust",
            "--out",
            str(out),
            "--",
            empty,
            "mutants",
            "--list",
            "--json",
        ],
        capture_output=True,
        text=True,
    )
    assert (
        r.returncode == 0
        and qg.load_plan(str(out), "rust-mutants")["state"] == qg.EMPTY
    ), r.stderr
    body = json.dumps(real("cargo-mutants-27.1.0-list.json")).replace("'", "'\\''")
    full = fake_cargo(
        tmp_path,
        f'if [ "$2" = "--version" ]; then echo "cargo-mutants 27.1.0"; exit 0; fi; echo \'{body}\'',
    )
    out2 = tmp_path / "n.json"
    r = subprocess.run(
        [
            sys.executable,
            GATE,
            "plan-rust",
            "--out",
            str(out2),
            "--",
            full,
            "mutants",
            "--list",
            "--json",
        ],
        capture_output=True,
        text=True,
    )
    plan = qg.load_plan(str(out2), "rust-mutants")
    assert (
        r.returncode == 0
        and plan["state"] == qg.NONEMPTY
        and plan["count"] == 5
        and plan["tool_version"] == "27.1.0"
    )


# ----------------------------------------------------------------------- gremlins_gate report


def test_gremlins_gate_writes_a_machine_readable_report(tmp_path):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    g = bindir / "gremlins"
    g.write_text(
        "#!/usr/bin/env bash\necho 'Killed: 9, Lived: 0, Not covered: 0'\necho 'Timed out: 1'\nexit 0\n"
    )
    g.chmod(g.stat().st_mode | stat.S_IEXEC)
    baseline = tmp_path / "b.json"
    baseline.write_text('{"packages": {}}')
    report = tmp_path / "chunk-3.json"
    r = subprocess.run(
        [
            sys.executable,
            GREMLINS_GATE,
            "--baseline",
            str(baseline),
            "--workers",
            "1",
            "--report",
            str(report),
            "--chunk",
            "3",
            "--plan-digest",
            "d" * 64,
            "./a",
        ],
        env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}"},
        capture_output=True,
        text=True,
    )
    assert r.returncode == 0, r.stdout
    doc = json.loads(report.read_text())
    assert (
        doc["schema"] == "gremlins-report/1"
        and doc["chunk"] == 3
        and doc["plan_digest"] == "d" * 64
    )
    assert doc["packages"] == [go_row("./a", "ok", 9, 0, 0, 1)]


def test_gremlins_gate_reports_a_failing_and_a_garbled_package(tmp_path):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    g = bindir / "gremlins"
    g.write_text(
        "#!/usr/bin/env bash\ncase \"$*\" in *bad*) echo 'what?'; exit 0;; esac\necho 'Killed: 1, Lived: 9, Not covered: 0'\nexit 0\n"
    )
    g.chmod(g.stat().st_mode | stat.S_IEXEC)
    baseline = tmp_path / "b.json"
    baseline.write_text('{"packages": {}}')
    report = tmp_path / "chunk-0.json"
    r = subprocess.run(
        [
            sys.executable,
            GREMLINS_GATE,
            "--baseline",
            str(baseline),
            "--workers",
            "1",
            "--report",
            str(report),
            "./weak",
            "./bad",
        ],
        env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}"},
        capture_output=True,
        text=True,
    )
    assert r.returncode == 1
    rows = {x["pkg"]: x["status"] for x in json.loads(report.read_text())["packages"]}
    assert rows == {"./weak": "fail", "./bad": "unparseable"}
