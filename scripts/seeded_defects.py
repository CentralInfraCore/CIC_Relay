#!/usr/bin/env python3
"""Seeded defects: does the evidence verifier really turn red when it must? (CI self-test, PR-05)

Every case DERIVES a defective bundle from an honest one, runs the REAL verifier (scripts/object_model.py, not a
copy) and states which level must fail with which diagnostic code. Every negative case has a POSITIVE CONTROL: the
honest bundle must reach the expected honest verdict, otherwise the case proves nothing (a verifier that fails
everything would also "catch" every defect).

A bundle is the honest record plus everything the verifier needs: the checkout, the input files, the re-derived
values, the independent Go/Rust object sets, the required units, the cargo-mutants listing. The cases work on a
deep copy; nothing outside the temporary directory is written and the product tree is not touched.

Run:  seeded_defects.py --record R --root CHECKOUT --scope S --input NAME=PATH ... --rust-list L --expect-l3 PARTIAL|COMPLETE_FOR_SCOPE
      (the record and the real input files of a real measurement; the tests build the bundle from the PR-04 fixtures)
Exit: 0 only if every control is honest AND every defect is caught at the expected level with the expected code.
"""

import argparse
import copy
import json
import sys
from dataclasses import dataclass, field
from typing import Callable

import object_model as om


@dataclass
class Bundle:
    """The honest record and the context a full verification needs."""

    record: dict
    root: str | None = None
    inputs: dict = field(default_factory=dict)
    derived: dict | None = None
    go_independent: dict | None = None
    rust_independent: set | None = None
    required_units: set | None = None
    mutants: list | None = None
    go_files: dict | None = None

    def verify(self, record: dict | None = None, **over) -> dict:
        kw = dict(
            root=self.root,
            inputs=self.inputs,
            go_files=self.go_files,
            mutants=self.mutants,
            derived=self.derived,
            go_independent=self.go_independent,
            rust_independent=self.rust_independent,
            required_units=self.required_units,
        )
        kw.update(over)
        return om.verify_record(record if record is not None else self.record, **kw)


def reseal(rec: dict) -> dict:
    """Recompute the summary and the digest: what an attacker who fixes the record does."""
    rec = copy.deepcopy(rec)
    rec["summary"] = om.summarize(rec["objects"])
    rec["digest"] = om.canonical_digest({k: v for k, v in rec.items() if k != "digest"})
    return rec


def _first(rec: dict, pred: Callable[[dict], bool]) -> int:
    return next(i for i, o in enumerate(rec["objects"]) if pred(o))


def _go(o: dict) -> bool:
    return o["lang"] == "go"


def _measured_go(o: dict) -> bool:
    return (
        _go(o)
        and o["coverage"]["state"] == om.MEASURED
        and o["coverage"]["covered"] < o["coverage"]["total"]
    )


# --- the defects: each returns (record, bundle overrides) -----------------------------------------------------------


def d_delete(b: Bundle):
    r = copy.deepcopy(b.record)
    del r["objects"][_first(r, _go)]
    return reseal(r), {}


def d_insert(b: Bundle):
    r = copy.deepcopy(b.record)
    o = copy.deepcopy(r["objects"][_first(r, _go)])
    o["id"], o["name"] = "go:invented.DoesNotExist", "DoesNotExist"
    r["objects"].append(o)
    r["objects"].sort(key=lambda x: x["id"])
    return reseal(r), {}


def d_swap(b: Bundle):
    """Replace one object by an invented one: the count of the objects does not change."""
    r = copy.deepcopy(b.record)
    i = _first(r, _go)
    r["objects"][i] = dict(r["objects"][i], id="go:invented.Swapped", name="Swapped")
    r["objects"].sort(key=lambda x: x["id"])
    return reseal(r), {}


def d_dup_same_id(b: Bundle):
    r = copy.deepcopy(b.record)
    r["objects"].append(copy.deepcopy(r["objects"][_first(r, _go)]))
    r["objects"].sort(key=lambda x: x["id"])
    return reseal(r), {}


def d_dup_other_id(b: Bundle):
    """The same source object under a second id, the count kept equal by dropping another object."""
    r = copy.deepcopy(b.record)
    goes = [i for i, o in enumerate(r["objects"]) if _go(o)]
    dup = copy.deepcopy(r["objects"][goes[0]])
    dup["id"] += "#dup"
    r["objects"].append(dup)
    del r["objects"][goes[1]]
    r["objects"].sort(key=lambda x: x["id"])
    return reseal(r), {}


def d_tamper_naive(b: Bundle):
    r = copy.deepcopy(b.record)
    o = r["objects"][_first(r, _measured_go)]
    o["coverage"]["covered"] = o["coverage"]["total"]
    return r, {}  # digest and summary NOT recomputed


def d_tamper_resealed(b: Bundle):
    r = copy.deepcopy(b.record)
    o = r["objects"][_first(r, _measured_go)]
    o["coverage"]["covered"] = o["coverage"]["total"]
    o["coverage"].pop("uncovered", None)
    return reseal(r), {}


def d_other_commit(b: Bundle):
    r = copy.deepcopy(b.record)
    r["identity"] = {"commit": "0" * 40, "tree": "1" * 40}
    return reseal(r), {}


def d_forged_input_digest(b: Bundle):
    r = copy.deepcopy(b.record)
    name = sorted(r["inputs"])[0]
    r["inputs"][name] = {"sha256": "f" * 64, "bytes": r["inputs"][name]["bytes"]}
    return reseal(r), {}


def d_missing_field(b: Bundle):
    r = copy.deepcopy(b.record)
    del r["objects"][_first(r, lambda o: True)]["coverage"]
    return reseal(r), {}


def d_missing_unit(b: Bundle):
    """Every object of a unit the scope requires is gone (e.g. no Rust object at all)."""
    r = copy.deepcopy(b.record)
    unit = sorted(b.required_units or ())[-1]
    r["objects"] = [o for o in r["objects"] if o["unit"] != unit]
    return reseal(r), {}


def d_single_run(b: Bundle):
    """A coverage measured once, claimed stable."""
    r = copy.deepcopy(b.record)
    o = r["objects"][
        _first(r, lambda o: _go(o) and o["coverage"]["state"] == om.MEASURED)
    ]
    o["coverage"]["determinism"] = "OBSERVED_STABLE"
    o["coverage"]["runs"] = 1
    return reseal(r), {}


def d_forged_stability(b: Bundle):
    """A single-run record claims 20 runs and OBSERVED_STABLE, re-sealed: internally consistent (L1), the inputs are
    untouched, only the re-derivation from the bound profiles (runs counted from them) can see it.
    """
    r = copy.deepcopy(b.record)
    o = r["objects"][
        _first(r, lambda o: _go(o) and o["coverage"]["state"] == om.MEASURED)
    ]
    o["coverage"]["determinism"] = "OBSERVED_STABLE"
    o["coverage"]["runs"] = om.STABLE_MIN_RUNS
    return reseal(r), {}


def d_nondeterministic(b: Bundle):
    """An object whose coverage differs between runs (the real kind of case: see the PR-05 report)."""
    r = copy.deepcopy(b.record)
    o = r["objects"][
        _first(r, lambda o: _go(o) and o["coverage"]["state"] == om.MEASURED)
    ]
    tot = o["coverage"]["total"]
    o["coverage"] = om.unverifiable(
        "NONDETERMINISTIC", range=[max(tot - 1, 0), tot], total=tot, runs=6
    )
    return reseal(r), {}


@dataclass
class Case:
    id: str
    title: str
    derive: Callable[[Bundle], tuple]
    level: str  # L1 | L2 | L3 | STABILITY
    code: str  # the diagnostic code that must appear
    why: str = ""


CASES = [
    Case(
        "A.1", "an object is deleted (record re-sealed)", d_delete, "L3", "SET_MISMATCH"
    ),
    Case(
        "A.2",
        "a non-existent object is inserted (re-sealed)",
        d_insert,
        "L3",
        "SET_MISMATCH",
    ),
    Case(
        "A.3",
        "an object is swapped, the count is unchanged (re-sealed)",
        d_swap,
        "L3",
        "SET_MISMATCH",
        "a count cannot see a swap: this is the set-identity proof",
    ),
    Case(
        "A.4a",
        "an object is duplicated under the SAME id",
        d_dup_same_id,
        "L1",
        "DUPLICATE_ID",
        "caught by the integrity check before the completeness check can run",
    ),
    Case(
        "A.4b",
        "the same source object under ANOTHER id, count kept equal (re-sealed)",
        d_dup_other_id,
        "L3",
        "SET_MISMATCH",
    ),
    Case(
        "A.5a",
        "a measured value is changed, the record is NOT re-sealed",
        d_tamper_naive,
        "L1",
        "DIGEST_MISMATCH",
    ),
    Case(
        "A.5b",
        "a measured value is changed AND the record is re-sealed",
        d_tamper_resealed,
        "L2",
        "REDERIVATION_MISMATCH",
        "internally consistent, inputs untouched: only a re-derivation sees it",
    ),
    Case(
        "A.6",
        "evidence from another commit (re-sealed)",
        d_other_commit,
        "L2",
        "COMMIT_MISMATCH",
    ),
    Case(
        "A.7",
        "a forged input digest in the record (re-sealed)",
        d_forged_input_digest,
        "L2",
        "INPUT_DIGEST_MISMATCH",
    ),
    Case(
        "A.8a",
        "a mandatory field is missing (re-sealed)",
        d_missing_field,
        "L1",
        "MISSING_FIELD",
    ),
    Case(
        "A.8b",
        "every object of a unit the scope requires is missing (re-sealed)",
        d_missing_unit,
        "L3",
        "MISSING_UNIT",
    ),
    Case(
        "A.9a",
        "a coverage seen in ONE run is claimed stable (re-sealed)",
        d_single_run,
        "L1",
        "BAD_DETERMINISM",
    ),
    Case(
        "A.9c",
        "a stability claim (runs, OBSERVED_STABLE) is forged and the record is re-sealed",
        d_forged_stability,
        "L2",
        "REDERIVATION_MISMATCH",
    ),
    Case(
        "A.9b",
        "an object whose coverage differs between runs",
        d_nondeterministic,
        "STABILITY",
        "NOT_STABLE",
        "must not count as stable evidence",
    ),
]


def _codes(findings: list[str]) -> set[str]:
    return {f[1 : f.index("]")] for f in findings if f.startswith("[") and "]" in f}


def run_case(b: Bundle, c: Case) -> dict:
    rec, over = c.derive(b)
    res = b.verify(rec, **over)
    if c.level == "STABILITY":
        got = res["stability"]["verdict"]
        return {
            "id": c.id,
            "caught": got == c.code
            and res["stability"]["nondeterministic_objects"] != [],
            "got": got,
            "detail": res["stability"],
        }
    lvl = res[c.level]
    findings = lvl.get("findings", [])
    ok = lvl["verdict"] == "FAIL" and c.code in _codes(findings)
    return {
        "id": c.id,
        "caught": ok,
        "got": f"{c.level}={lvl['verdict']} codes={sorted(_codes(findings))}",
        "detail": findings[:2],
    }


def control(b: Bundle, honest: dict) -> dict:
    """The positive control: the honest bundle reaches the honest verdict."""
    res = b.verify()
    got = {k: res[k]["verdict"] for k in ("L1", "L2", "L3")}
    return {
        "ok": all(got[k] == v for k, v in honest.items()),
        "got": got,
        "expected": honest,
    }


def run_all(
    b: Bundle, honest: dict, cases: list[Case] | None = None
) -> tuple[bool, list[str]]:
    """Run the control and every case; (everything as expected, the report lines)."""
    lines: list[str] = []
    ctl = control(b, honest)
    lines.append(
        f"{'OK  ' if ctl['ok'] else 'FAIL'} control (honest bundle)  expected {ctl['expected']}  got {ctl['got']}"
    )
    ok = ctl["ok"]
    for c in cases or CASES:
        r = run_case(b, c)
        ok = ok and r["caught"]
        lines.append(
            f"{'OK  ' if r['caught'] else 'FAIL'} {c.id:<5} {c.title}  -> {r['got']}"
        )
    return ok, lines


def bundle_from_files(
    record_path: str,
    root: str,
    scope_path: str,
    input_args: list[str],
    rust_list: str | None,
) -> Bundle:
    """The bundle of a REAL record: the record, the checkout and the real input files; everything else is derived from them."""
    rec = json.loads(open(record_path, encoding="utf-8").read())
    scope = json.loads(open(scope_path, encoding="utf-8").read())
    om.check_scope(scope)
    inputs = dict(kv.split("=", 1) for kv in input_args)
    ctx = om.build_context(rec, root, scope, inputs)
    return Bundle(
        record=rec,
        root=root,
        inputs=inputs,
        derived=ctx["derived"],
        go_independent=ctx["go_independent"],
        rust_independent=ctx["rust_independent"],
        required_units=ctx["required_units"],
        mutants=(
            json.loads(open(rust_list, encoding="utf-8").read()) if rust_list else None
        ),
    )


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--list", action="store_true", help="list the cases and stop")
    ap.add_argument("--record", help="the real evidence record")
    ap.add_argument("--root", help="the checkout the record was made of")
    ap.add_argument("--scope", help="the measurement scope")
    ap.add_argument(
        "--input",
        action="append",
        default=[],
        help="NAME=PATH of an input file (as for object_model verify)",
    )
    ap.add_argument("--rust-list", help="cargo mutants --list --json")
    ap.add_argument(
        "--expect-l3",
        choices=["COMPLETE_FOR_SCOPE", "PARTIAL"],
        help="the L3 verdict the HONEST record must get. It is STATED here, not read from the result: a harness that "
        "adopts whatever the verifier says proves nothing.",
    )
    a = ap.parse_args(argv)
    if a.list:
        for c in CASES:
            print(f"{c.id:<5} expect {c.level} [{c.code}]  {c.title}")
        return 0
    if not (a.record and a.root and a.scope and a.expect_l3):
        ap.error("--record, --root, --scope and --expect-l3 are required (or --list)")
    b = bundle_from_files(a.record, a.root, a.scope, a.input, a.rust_list)
    ok, lines = run_all(
        b, {"L1": "INTEGRITY_OK", "L2": "BOUND_TO_INPUTS", "L3": a.expect_l3}
    )
    print("\n".join(lines))
    print(
        "SEEDED DEFECTS: every control honest and every defect caught"
        if ok
        else "SEEDED DEFECTS: A CONTROL OR A DEFECT DID NOT BEHAVE AS EXPECTED"
    )
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
