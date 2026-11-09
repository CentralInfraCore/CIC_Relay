"""How a gremlins result is judged (scripts/gremlins_gate.py)."""

import os
import sys

sys.path.insert(
    0, os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
)

from gremlins_gate import (  # noqa: E402
    evaluate,
    measured,
    thresholds,
    timeouts_dominate,
)


def test_timed_out_mutants_count_as_detected():
    # pkg/merkle: 14 killed + 3 that make a test hang, nothing lived
    eff, mc, problems = evaluate((14, 0, 0, 3), 90.0, 90.0)
    assert eff == 100.0 and mc == 100.0 and problems == []


def test_the_split_between_killed_and_timed_out_does_not_matter():
    # core/nexus/iac measured as 117+1 and as 110+8: the same package, the same sum
    a = evaluate((117, 14, 6, 1), 85.0, 90.0)
    b = evaluate((110, 14, 6, 8), 85.0, 90.0)
    assert a[0] == b[0] and a[1] == b[1] and a[2] == b[2] == []


def test_lived_mutants_still_fail():
    _, _, problems = evaluate((13, 7, 1, 0), 90.0, 90.0)
    assert any("efficacy" in p for p in problems)


def test_mostly_timeouts_is_rejected_even_if_it_reads_as_100_percent():
    # the Go test cache trap: 9 killed, 0 lived, 11 timed out of 20 mutants
    eff, _, problems = evaluate((9, 0, 1, 11), 60.0, 90.0)
    assert eff == 100.0
    assert any("timed out" in p for p in problems)


def test_a_few_timeouts_in_a_big_package_are_fine():
    assert evaluate((300, 5, 0, 8), 90.0, 90.0)[2] == []


def test_low_mutant_coverage_fails():
    _, _, problems = evaluate((7, 0, 9, 0), 90.0, 90.0)
    assert any("mutant coverage" in p for p in problems)


def test_measured_reads_the_summary_and_the_timeouts():
    text = "Killed: 14, Lived: 0, Not covered: 0\nTimed out: 3, Not viable: 0"
    assert measured(text) == (14, 0, 0, 3)
    assert measured("nothing here") is None


def test_thresholds_default_and_baseline():
    assert thresholds(None) == (90.0, 90.0)
    eff, mc = thresholds({"killed": 13, "lived": 7, "not_covered": 1})
    assert (eff, mc) == (60.0, 90.0)


def test_timeouts_dominate_only_when_they_are_many():
    assert timeouts_dominate((9, 0, 1, 11)) is True  # the cache trap
    assert timeouts_dominate((70, 5, 0, 70)) is True
    assert timeouts_dominate((14, 0, 0, 3)) is False  # pkg/merkle
    assert timeouts_dominate((117, 14, 6, 1)) is False
    assert timeouts_dominate((0, 0, 0, 0)) is False
