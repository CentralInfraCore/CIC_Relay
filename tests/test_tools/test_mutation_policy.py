"""mutation-policy/2 (owner decisions of 2026-11-14): the primary score and the unviable metric.

R1 is a regression test of a CHANGED behaviour: it runs `quality_gate.py verify` on real
cargo-mutants outcomes and reads the printed primary score. On the previous tree (policy/1,
primary = caught/planned) the output has no policy/2 primary score at all and R1 fails there.
"""

import copy
import json
import os
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import quality_gate as qg  # noqa: E402

FIX = os.path.join(os.path.dirname(__file__), "fixtures/quality_gate")
GATE = os.path.join(SCRIPTS, "quality_gate.py")


def real(name):
    with open(os.path.join(FIX, name)) as fh:
        return json.load(fh)


def with_summaries(summaries):
    """Real outcomes where mutant #i has summaries[i]; the tool's own counters kept consistent."""
    o = copy.deepcopy(real("cargo-mutants-27.1.0-outcomes.json"))
    mutants = [r for r in o["outcomes"] if r["scenario"] != "Baseline"]
    for field in ("caught", "missed", "timeout", "unviable"):
        o[field] = 0
    for row, summary in zip(mutants, summaries):
        row["summary"] = summary
        o[qg.SUMMARY_TO_VERDICT[summary]] += 1
    return o


def run_verify(tmp_path, summaries):
    """`quality_gate.py verify` on a plan of the 5 real mutants and these verdicts; (rc, stdout)."""
    entries = real("cargo-mutants-27.1.0-list.json")
    plan = qg.build_rust_plan(
        json.dumps(entries).encode(), 0, ["cargo"], "27.1.0", "full"
    )
    out = with_summaries(summaries)
    rep = {
        "schema": qg.SHARD_SCHEMA,
        "plan_digest": plan["digest"],
        "shard": 0,
        "shards": 1,
        "tool_version": out["cargo_mutants_version"],
        "outcomes": out,
    }
    (tmp_path / "plan.json").write_text(json.dumps(plan))
    rdir = tmp_path / "reports"
    rdir.mkdir(exist_ok=True)
    (rdir / "shard-0.json").write_text(json.dumps(rep))
    r = subprocess.run(
        [
            sys.executable,
            GATE,
            "verify",
            "--rust-plan",
            str(tmp_path / "plan.json"),
            "--rust-reports",
            str(rdir),
            "--rust-shards",
            "1",
        ],
        capture_output=True,
        text=True,
    )
    return r.returncode, r.stdout + r.stderr


C, M, T, U = "CaughtMutant", "MissedMutant", "Timeout", "Unviable"


def primary_line(text):
    lines = [ln for ln in text.splitlines() if "(primary" in ln]
    assert lines, f"no primary score line in the output:\n{text}"
    return lines[0]


def test_r1_an_unviable_mutant_does_not_change_the_primary_score(tmp_path):
    (tmp_path / "a").mkdir()
    (tmp_path / "b").mkdir()
    rc_a, a = run_verify(tmp_path / "a", [C] * 5)
    rc_b, b = run_verify(tmp_path / "b", [C, C, C, C, U])
    assert rc_a == 0 and rc_b == 0
    assert "mutation-policy/2" in primary_line(a)
    assert "caught/(caught+missed) = 5/5 = 100.00%" in primary_line(a)
    # one caught mutant became unviable: the numerator and the denominator both lose it
    assert "caught/(caught+missed) = 4/4 = 100.00%" in primary_line(b)
    assert "unviable (mandatory metric, report-only): 1/5 = 20.00%" in b


def test_the_unviable_ratio_is_always_printed_even_when_zero(tmp_path):
    (tmp_path / "z").mkdir()
    _, text = run_verify(tmp_path / "z", [C] * 5)
    assert "unviable (mandatory metric, report-only): 0/5 = 0.00%" in text


def test_the_legacy_primary_of_policy_1_stays_visible(tmp_path):
    (tmp_path / "l").mkdir()
    _, text = run_verify(tmp_path / "l", [C, C, C, C, U])
    assert "legacy (policy/1 primary): caught/planned = 4/5" in text


@pytest.mark.parametrize(
    "summaries, rc, shown",
    [
        ([C] * 5, 0, "5/5 = 100.00%"),
        (
            [C, C, C, C, M],
            1,
            "4/5 = 80.00%",
        ),  # a survivor: red, and the score is a number below 100
        (
            [U] * 5,
            0,
            "NOT_APPLICABLE (ALL_UNVIABLE)",
        ),  # nothing evaluable: not 100%; unviable does not fail
        (
            [U, U, U, U, T],
            1,
            "NOT_APPLICABLE (NO_EVALUABLE_MUTANT)",
        ),  # a timeout is red; the score does not apply
    ],
)
def test_primary_score_states_and_gate_result(tmp_path, summaries, rc, shown):
    (tmp_path / "s").mkdir()
    got_rc, text = run_verify(tmp_path / "s", summaries)
    assert got_rc == rc, text
    assert shown in primary_line(text)


def test_a_survivor_is_still_red_whatever_the_score_is(tmp_path):
    # the 90% target is a reported value: it never replaces `missed > 0`
    (tmp_path / "m").mkdir()
    rc, text = run_verify(tmp_path / "m", [C, C, C, C, M])
    assert rc == 1 and "missed=1" in text


# --- the zero denominator (N12) --------------------------------------------------------------


def test_zero_denominator_is_neither_100_nor_0():
    cases = {
        "NO_MUTANTS_PLANNED": {
            "planned": 0,
            "caught": 0,
            "missed": 0,
            "timeout": 0,
            "unviable": 0,
        },
        "ALL_UNVIABLE": {
            "planned": 4,
            "caught": 0,
            "missed": 0,
            "timeout": 0,
            "unviable": 4,
        },
        "NO_EVALUABLE_MUTANT": {
            "planned": 4,
            "caught": 0,
            "missed": 0,
            "timeout": 1,
            "unviable": 3,
        },
    }
    for reason, acct in cases.items():
        s = qg.primary_score(acct)
        assert s == {"state": "NOT_APPLICABLE", "reason": reason}, (reason, s)
        assert "value" not in s


@pytest.mark.parametrize("acct", [None, {}, {"caught": 3}])
def test_a_missing_accounting_is_unverifiable_not_a_score(acct):
    assert qg.primary_score(acct) == {
        "state": "UNVERIFIABLE",
        "reason": "MEASUREMENT_MISSING",
    }
    assert qg.unviable_ratio(acct) == {
        "state": "UNVERIFIABLE",
        "reason": "MEASUREMENT_MISSING",
    }


def test_primary_score_measures_only_evaluable_mutants():
    s = qg.primary_score(
        {"planned": 10, "caught": 6, "missed": 2, "timeout": 1, "unviable": 1}
    )
    assert (
        s["state"] == "MEASURED"
        and s["value"] == 75.0
        and (s["caught"], s["evaluable"]) == (6, 8)
    )


def test_unviable_ratio():
    assert qg.unviable_ratio({"planned": 300, "unviable": 83})[
        "value"
    ] == pytest.approx(27.6667, abs=1e-3)
    assert qg.unviable_ratio({"planned": 0, "unviable": 0}) == {
        "state": "NOT_APPLICABLE",
        "reason": "NO_MUTANTS_PLANNED",
    }
