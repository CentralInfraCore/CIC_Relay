#!/usr/bin/env python3
"""Guard the mutation ratchet baseline: it may only get stricter.

The baseline (.mutation-baseline.json) lists the packages that were below 90 when it
was taken, with what they achieved. Without a guard, a pull request could lower an
entry, or add one for a weak new package, and the gate would pass. This compares the
baseline of the pull request with the one of its base:

* an entry may be removed (that package then has to reach 90: stricter);
* an entry may be raised;
* an entry must not be lowered, on efficacy or on mutant coverage;
* an entry must not be added for a package that has none on the base (a new package
  starts at 90, the baseline is not a way around that);
* the file must exist and be well formed.

The workflow runs this script, and scripts/gremlins_gate.py it imports, from the BASE
commit, so the pull request cannot weaken the guard it is judged by.

usage: baseline_guard.py --base BASE.json --head HEAD.json
"""

import argparse
import json
import sys
from pathlib import Path

from gremlins_gate import thresholds

EPS = 1e-9


def load(path: str) -> dict[str, dict]:
    doc = json.loads(Path(path).read_text())
    pkgs = doc["packages"]
    if not isinstance(pkgs, dict):
        raise ValueError("'packages' is not an object")
    for name, e in pkgs.items():
        for key in ("killed", "lived", "not_covered"):
            v = e.get(key)
            if not isinstance(v, int) or isinstance(v, bool) or v < 0:
                raise ValueError(f"{name}: '{key}' must be a non-negative integer")
    return pkgs


def check(base: dict[str, dict], head: dict[str, dict]) -> list[str]:
    problems = []
    for name, e in sorted(head.items()):
        if name not in base:
            problems.append(
                f"{name}: new baseline entry; a new package starts at 90, "
                "the baseline cannot lower that"
            )
            continue
        eff_h, mc_h = thresholds(e)
        eff_b, mc_b = thresholds(base[name])
        if eff_h + EPS < eff_b:
            problems.append(f"{name}: efficacy requirement lowered {eff_b} -> {eff_h}")
        if mc_h + EPS < mc_b:
            problems.append(
                f"{name}: mutant coverage requirement lowered {mc_b} -> {mc_h}"
            )
    return problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    ap.add_argument("--head", required=True)
    a = ap.parse_args()
    try:
        base = load(a.base)
    except (OSError, ValueError, KeyError) as exc:
        print(f"FAIL the base baseline cannot be read: {exc!r}")
        return 1
    try:
        head = load(a.head)
    except (OSError, ValueError, KeyError) as exc:
        print(f"FAIL the baseline of this change cannot be read: {exc!r}")
        return 1
    problems = check(base, head)
    removed = sorted(set(base) - set(head))
    for name in removed:
        print(f"note {name}: entry removed, the package now needs 90")
    if problems:
        for p in problems:
            print("FAIL " + p)
        return 1
    print(f"ok   baseline not weakened ({len(head)} entries, {len(removed)} removed)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
