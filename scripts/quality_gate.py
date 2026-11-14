#!/usr/bin/env python3
"""Completeness validation of the mutation gates (fail-closed).

What a green `cargo mutants --shard` or `gremlins` job proves is that the tool exited 0 on
the part it was given. It does not prove that every PLANNED mutant (or package) got exactly
one interpretable verdict. This script closes that gap: the plan is recorded as a verifiable
record, every shard/chunk hands in a report bound to that plan, and `verify` accepts the
result only if the planned set and the measured set are the same.

Commands
  plan-rust   list the Rust mutants (`cargo mutants --list --json ...`) and write the plan
              record; the listing exit status is part of the record
  plan-go     write the Go plan record from the planner's package list
  wrap-shard  bind one `cargo mutants` outcomes.json to the plan (a shard report)
  verify      check plan + shard reports + chunk reports against each other
  check-coverprofile  a Go coverage profile must exist, parse and hold at least one block

Contracts (versioned; a change is a change of the gate logic and is planned as such by
scripts/mutation_plan.py):
  mutation-plan/1      the plan record (this file, build_rust_plan / build_go_plan)
  mutant-id/1          the id of a Rust mutant is the `name` cargo-mutants prints for it
                       ("file:line:col: replace f -> T with X"). The plan holds the ids sorted
                       and duplicate-free, so the order of the listing and the shard layout
                       do not matter; two different mutants never share an id (a listing
                       with a duplicate id is an invalid plan, not a merged one).
  mutation-shard-report/1, gremlins-report/1   the reports of a shard / a chunk
  mutation-policy/1    how the verdicts are accounted (see POLICY)

Plan states (named in the output):
  PLAN_VALID_NONEMPTY  a valid plan with at least one mutant/package
  PLAN_VALID_EMPTY     PROVABLY valid and empty: the listing exited 0 and printed nothing
                       (measured: cargo-mutants 27.1.0 prints 0 bytes, not "[]", for a diff with
                       no mutable code), or the planner reported nothing to do. The exit status
                       and the digest of the output are in the same record.
  PLAN_INVALID         anything else: missing, unreadable, not matching its digest, wrong
                       schema or contract, a failed listing. Always a failure.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

PLAN_SCHEMA = "mutation-plan/1"
ID_CONTRACT = "mutant-id/1"
SHARD_SCHEMA = "mutation-shard-report/1"
GO_SCHEMA = "gremlins-report/1"

NONEMPTY = "PLAN_VALID_NONEMPTY"
EMPTY = "PLAN_VALID_EMPTY"
INVALID = "PLAN_INVALID"

# mutation-policy/1: the verdicts of a Rust mutant, each counted on its own. The plan size is
# the denominator: nothing leaves it (a timed-out or unviable mutant stays a planned mutant).
#   caught    detected by the tests                      -> ok
#   missed    survived                                   -> FAIL
#   timeout   no verdict in time                         -> FAIL (it is not a detection here)
#   unviable  the mutant does not compile                -> reported, does not fail
#             (the behaviour the gate had; the owner decides whether it becomes a failure,
#              and that decision is a change of this policy version, not an exception)
POLICY = "mutation-policy/1"
SUMMARY_TO_VERDICT = {
    "CaughtMutant": "caught",
    "MissedMutant": "missed",
    "Timeout": "timeout",
    "Unviable": "unviable",
}
VERDICTS = ("caught", "missed", "timeout", "unviable")
FAILING = ("missed", "timeout")


class GateError(Exception):
    """The input cannot be trusted: the gate fails."""


def sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def canonical_digest(obj: object) -> str:
    """sha256 of the canonical JSON (sorted keys, no whitespace) of obj."""
    text = json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return sha256_hex(text.encode("utf-8"))


# --------------------------------------------------------------------------- plan records


def plan_body(kind: str, ids: list[str], extra: dict) -> dict:
    return {
        "schema": PLAN_SCHEMA,
        "id_contract": ID_CONTRACT,
        "kind": kind,
        "ids": ids,
        "count": len(ids),
        **extra,
    }


def seal(body: dict) -> dict:
    """Add the state and the digest (over everything but the digest itself)."""
    body = dict(body)
    body["state"] = NONEMPTY if body["count"] else EMPTY
    body["digest"] = canonical_digest({k: v for k, v in body.items() if k != "digest"})
    return body


def rust_ids(listing_json: str) -> list[str]:
    """The sorted, duplicate-free mutant ids of a `cargo mutants --list --json` output."""
    try:
        doc = json.loads(listing_json)
    except ValueError as e:
        raise GateError(f"the mutant listing is not JSON: {e}") from e
    if not isinstance(doc, list):
        raise GateError("the mutant listing is not a JSON list")
    ids = []
    for entry in doc:
        name = entry.get("name") if isinstance(entry, dict) else None
        if not isinstance(name, str) or not name:
            raise GateError("a mutant of the listing has no name")
        ids.append(name)
    dup = sorted({i for i in ids if ids.count(i) > 1})
    if dup:
        raise GateError(f"{len(dup)} mutant id(s) are not unique, e.g. {dup[0]!r}")
    return sorted(ids)


def build_rust_plan(
    raw: bytes, exit_code: int, command: list[str], tool_version: str, mode: str
) -> dict:
    """The plan record of a finished listing. Raises GateError for anything but a success."""
    if exit_code != 0:
        raise GateError(
            f"the listing exited with status {exit_code}: {' '.join(command)}"
        )
    text = raw.decode("utf-8", errors="strict")
    # measured: a valid listing without any mutant prints nothing at all (exit 0)
    ids = rust_ids(text) if text.strip() else []
    listing = {
        "command": command,
        "exit_code": exit_code,
        "raw_bytes": len(raw),
        "raw_sha256": sha256_hex(raw),
    }
    return seal(
        plan_body(
            "rust-mutants",
            ids,
            {
                "tool": "cargo-mutants",
                "tool_version": tool_version,
                "mode": mode,
                "listing": listing,
            },
        )
    )


def build_go_plan(pkgs: list[str], source: dict) -> dict:
    uniq = sorted(set(pkgs))
    return seal(plan_body("go-packages", uniq, {"tool": "gremlins", "source": source}))


def load_plan(path: str, kind: str) -> dict:
    """Read and verify a plan record: PLAN_INVALID (GateError) unless it is intact."""
    try:
        doc = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        raise GateError(
            f"{INVALID}: the plan {path} is missing or unreadable: {e}"
        ) from e
    if not isinstance(doc, dict):
        raise GateError(f"{INVALID}: the plan {path} is not an object")
    if doc.get("schema") != PLAN_SCHEMA or doc.get("id_contract") != ID_CONTRACT:
        raise GateError(
            f"{INVALID}: the plan {path} has schema/contract "
            f"{doc.get('schema')!r}/{doc.get('id_contract')!r}, expected {PLAN_SCHEMA}/{ID_CONTRACT}"
        )
    if doc.get("kind") != kind:
        raise GateError(
            f"{INVALID}: the plan {path} is a {doc.get('kind')!r} plan, expected {kind!r}"
        )
    ids = doc.get("ids")
    if not isinstance(ids, list) or not all(isinstance(i, str) for i in ids):
        raise GateError(f"{INVALID}: the plan {path} has no id list")
    if ids != sorted(ids) or len(set(ids)) != len(ids):
        raise GateError(
            f"{INVALID}: the ids of the plan {path} are not sorted and unique"
        )
    if doc.get("count") != len(ids):
        raise GateError(
            f"{INVALID}: the plan {path} says {doc.get('count')} ids but holds {len(ids)}"
        )
    want = canonical_digest({k: v for k, v in doc.items() if k != "digest"})
    if doc.get("digest") != want:
        raise GateError(
            f"{INVALID}: the plan {path} does not match its digest (tampered or corrupt)"
        )
    state = NONEMPTY if ids else EMPTY
    if doc.get("state") != state:
        raise GateError(
            f"{INVALID}: the plan {path} states {doc.get('state')!r}, its content is {state}"
        )
    if kind == "rust-mutants" and not ids:
        lst = doc.get("listing")
        if not (
            isinstance(lst, dict)
            and lst.get("exit_code") == 0
            and lst.get("raw_bytes") == 0
            and lst.get("raw_sha256") == sha256_hex(b"")
        ) and not doc.get("skipped"):
            raise GateError(
                f"{INVALID}: the empty plan {path} is not backed by a successful listing "
                "(exit 0, no output) or by a planner decision"
            )
    return doc


# --------------------------------------------------------------------------- Rust reports


def rust_verdicts(outcomes: dict, where: str) -> dict[str, str]:
    """{mutant id: verdict} of one outcomes.json; rejects an inconsistent or unknown report."""
    rows = outcomes.get("outcomes")
    if not isinstance(rows, list):
        raise GateError(f"{where}: outcomes.json has no outcomes list")
    verdicts: dict[str, str] = {}
    for row in rows:
        scenario = row.get("scenario") if isinstance(row, dict) else None
        if scenario == "Baseline":
            if row.get("summary") != "Success":
                raise GateError(
                    f"{where}: the unmutated baseline did not succeed ({row.get('summary')!r})"
                )
            continue
        mutant = scenario.get("Mutant") if isinstance(scenario, dict) else None
        name = mutant.get("name") if isinstance(mutant, dict) else None
        if not isinstance(name, str):
            raise GateError(f"{where}: an outcome has no mutant name")
        verdict = SUMMARY_TO_VERDICT.get(row.get("summary"))
        if verdict is None:
            raise GateError(
                f"{where}: unknown verdict {row.get('summary')!r} for {name!r}"
            )
        if name in verdicts:
            raise GateError(f"{where}: more than one verdict for {name!r}")
        verdicts[name] = verdict
    # the tool's own counters must agree with its rows (a tampered or truncated file does not)
    for field, verdict in (
        ("caught", "caught"),
        ("missed", "missed"),
        ("timeout", "timeout"),
        ("unviable", "unviable"),
    ):
        have = sum(1 for v in verdicts.values() if v == verdict)
        if outcomes.get(field) != have:
            raise GateError(
                f"{where}: counter {field}={outcomes.get(field)!r} but {have} such outcomes"
            )
    if outcomes.get("total_mutants") != len(verdicts):
        raise GateError(
            f"{where}: total_mutants={outcomes.get('total_mutants')!r} but {len(verdicts)} outcomes"
        )
    return verdicts


def wrap_shard(plan_digest: str, shard: int, shards: int, outcomes_path: str) -> dict:
    try:
        outcomes = json.loads(Path(outcomes_path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        raise GateError(
            f"the outcomes {outcomes_path} are missing or unreadable: {e}"
        ) from e
    rust_verdicts(outcomes, outcomes_path)  # refuse to wrap an inconsistent file
    return {
        "schema": SHARD_SCHEMA,
        "plan_digest": plan_digest,
        "shard": shard,
        "shards": shards,
        "tool_version": outcomes.get("cargo_mutants_version"),
        "outcomes": outcomes,
    }


def read_reports(directory: str, pattern: str) -> list[tuple[str, dict]]:
    out = []
    for p in sorted(Path(directory).rglob(pattern)) if Path(directory).is_dir() else []:
        try:
            out.append((str(p), json.loads(p.read_text(encoding="utf-8"))))
        except (OSError, ValueError) as e:
            raise GateError(f"the report {p} is unreadable or corrupt: {e}") from e
    return out


def verify_rust(plan: dict, reports: list[tuple[str, dict]], shards: int) -> dict:
    """Planned mutants == measured mutants, one verdict each. Returns the accounting."""
    if plan["state"] == EMPTY:
        if shards != 0 or reports:
            raise GateError(
                f"the Rust plan is {EMPTY} but {shards} shard(s) / {len(reports)} report(s) exist: "
                "verdicts for a mutant that was never planned"
            )
        return {"planned": 0, **{v: 0 for v in VERDICTS}}
    if shards < 1:
        raise GateError(
            f"the Rust plan has {plan['count']} mutants but 0 shards were planned"
        )

    seen_shards: dict[int, str] = {}
    verdicts: dict[str, str] = {}
    for where, rep in reports:
        if not isinstance(rep, dict) or rep.get("schema") != SHARD_SCHEMA:
            raise GateError(f"{where}: not a {SHARD_SCHEMA} report")
        if rep.get("plan_digest") != plan["digest"]:
            raise GateError(f"{where}: the report belongs to a different plan")
        if (
            rep.get("shards") != shards
            or not isinstance(rep.get("shard"), int)
            or not 0 <= rep["shard"] < shards
        ):
            raise GateError(
                f"{where}: shard {rep.get('shard')!r}/{rep.get('shards')!r} does not fit {shards} planned shards"
            )
        if rep["shard"] in seen_shards:
            raise GateError(
                f"{where}: shard {rep['shard']} was reported twice (also {seen_shards[rep['shard']]})"
            )
        seen_shards[rep["shard"]] = where
        if rep.get("tool_version") != plan.get("tool_version"):
            raise GateError(
                f"{where}: measured with cargo-mutants {rep.get('tool_version')!r}, "
                f"planned with {plan.get('tool_version')!r}"
            )
        for name, verdict in rust_verdicts(rep.get("outcomes") or {}, where).items():
            if name in verdicts:
                raise GateError(
                    f"{where}: {name!r} already has a verdict from another shard"
                )
            verdicts[name] = verdict

    missing_shards = sorted(set(range(shards)) - set(seen_shards))
    if missing_shards:
        raise GateError(f"missing shard report(s): {missing_shards} of {shards}")
    planned = set(plan["ids"])
    unknown = sorted(set(verdicts) - planned)
    if unknown:
        raise GateError(
            f"{len(unknown)} verdict(s) for mutants that are not in the plan, e.g. {unknown[0]!r}"
        )
    unmeasured = sorted(planned - set(verdicts))
    if unmeasured:
        raise GateError(
            f"{len(unmeasured)} planned mutant(s) have no verdict, e.g. {unmeasured[0]!r}"
        )
    acct = {
        "planned": len(planned),
        **{v: sum(1 for x in verdicts.values() if x == v) for v in VERDICTS},
    }
    if sum(acct[v] for v in VERDICTS) != acct["planned"]:
        raise GateError(
            f"the verdicts add up to {sum(acct[v] for v in VERDICTS)}, the plan has {acct['planned']}"
        )
    return acct


# --------------------------------------------------------------------------- Go reports

GO_STATUS = ("ok", "no-mutants", "fail", "unparseable")


def verify_go(plan: dict, reports: list[tuple[str, dict]], chunks: int) -> dict:
    """Planned packages == measured packages, one verdict each."""
    if plan["state"] == EMPTY:
        if chunks != 0 or reports:
            raise GateError(
                f"the Go plan is {EMPTY} but {chunks} chunk(s) / {len(reports)} report(s) exist"
            )
        return {"planned": 0, "ok": 0, "no-mutants": 0, "timed_out": 0}
    if chunks < 1:
        raise GateError(
            f"the Go plan has {plan['count']} package(s) but 0 chunks were planned"
        )

    seen: dict[int, str] = {}
    pkgs: dict[str, dict] = {}
    for where, rep in reports:
        if not isinstance(rep, dict) or rep.get("schema") != GO_SCHEMA:
            raise GateError(f"{where}: not a {GO_SCHEMA} report")
        if rep.get("plan_digest") != plan["digest"]:
            raise GateError(f"{where}: the report belongs to a different plan")
        if not isinstance(rep.get("chunk"), int) or not 0 <= rep["chunk"] < chunks:
            raise GateError(
                f"{where}: chunk {rep.get('chunk')!r} does not fit {chunks} planned chunks"
            )
        if rep["chunk"] in seen:
            raise GateError(
                f"{where}: chunk {rep['chunk']} was reported twice (also {seen[rep['chunk']]})"
            )
        seen[rep["chunk"]] = where
        rows = rep.get("packages")
        if not isinstance(rows, list) or not rows:
            raise GateError(f"{where}: the chunk report has no package verdicts")
        for row in rows:
            pkg = row.get("pkg") if isinstance(row, dict) else None
            if not isinstance(pkg, str):
                raise GateError(f"{where}: a package verdict has no package")
            if pkg in pkgs:
                raise GateError(
                    f"{where}: {pkg!r} already has a verdict from another chunk or row"
                )
            if row.get("status") not in GO_STATUS:
                raise GateError(
                    f"{where}: unknown status {row.get('status')!r} for {pkg!r}"
                )
            if row["status"] in ("fail", "unparseable"):
                raise GateError(f"{where}: {pkg} did not pass ({row['status']})")
            for f in ("killed", "lived", "not_covered", "timed_out"):
                if not isinstance(row.get(f), int) or row[f] < 0:
                    raise GateError(f"{where}: {pkg}: {f} is not a count")
            pkgs[pkg] = row
    missing = sorted(set(range(chunks)) - set(seen))
    if missing:
        raise GateError(f"missing chunk report(s): {missing} of {chunks}")
    planned = set(plan["ids"])
    unknown = sorted(set(pkgs) - planned)
    if unknown:
        raise GateError(
            f"{len(unknown)} verdict(s) for packages that are not in the plan, e.g. {unknown[0]!r}"
        )
    unmeasured = sorted(planned - set(pkgs))
    if unmeasured:
        raise GateError(
            f"{len(unmeasured)} planned package(s) have no verdict, e.g. {unmeasured[0]!r}"
        )
    return {
        "planned": len(planned),
        "ok": sum(1 for r in pkgs.values() if r["status"] == "ok"),
        "no-mutants": sum(1 for r in pkgs.values() if r["status"] == "no-mutants"),
        "timed_out": sum(r["timed_out"] for r in pkgs.values()),
    }


# --------------------------------------------------------------------------- coverage

_BLOCK = re.compile(r"^[^:\s]+:\d+\.\d+,\d+\.\d+ \d+ \d+$")


def check_coverprofile(path: str) -> int:
    """The number of blocks of a Go coverage profile; a missing/empty/garbled one is an error.

    A profile that cannot be read is NOT a measurement of zero and not a success.
    """
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError as e:
        raise GateError(
            f"the coverage profile {path} is missing or unreadable: {e}"
        ) from e
    if not lines or not re.match(r"^mode: (set|count|atomic)$", lines[0]):
        raise GateError(
            f"the coverage profile {path} has no valid 'mode:' line (empty or not a profile)"
        )
    blocks = [ln for ln in lines[1:] if ln.strip()]
    if not blocks:
        raise GateError(
            f"the coverage profile {path} holds no blocks: nothing was measured"
        )
    bad = [ln for ln in blocks if not _BLOCK.match(ln)]
    if bad:
        raise GateError(
            f"the coverage profile {path} has a malformed block line: {bad[0]!r}"
        )
    return len(blocks)


# --------------------------------------------------------------------------- CLI


def _run_listing(cmd: list[str]) -> tuple[bytes, int]:
    try:
        proc = subprocess.run(cmd, stdout=subprocess.PIPE, check=False)
    except OSError as e:
        raise GateError(f"cannot run the listing command {cmd!r}: {e}") from e
    return proc.stdout, proc.returncode


def _tool_version(binary: list[str]) -> str:
    try:
        out = subprocess.run(
            binary + ["--version"], capture_output=True, text=True, check=True
        ).stdout
    except (OSError, subprocess.CalledProcessError) as e:
        raise GateError(f"cannot read the tool version: {e}") from e
    m = re.search(r"(\d+\.\d+\.\d+)", out)
    if not m:
        raise GateError(f"cannot read a version from {out!r}")
    return m.group(1)


def _write(path: str, doc: dict) -> None:
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(
        json.dumps(doc, indent=1, sort_keys=True) + "\n", encoding="utf-8"
    )


def cmd_plan_rust(a: argparse.Namespace) -> int:
    # a stale record must never survive a failed run
    if os.path.exists(a.out):
        os.unlink(a.out)
    if a.skipped:
        plan = seal(
            plan_body(
                "rust-mutants",
                [],
                {
                    "tool": "cargo-mutants",
                    "tool_version": a.tool_version or "n/a",
                    "mode": "none",
                    "skipped": a.skipped,
                },
            )
        )
    else:
        cmd = a.cmd[1:] if a.cmd[:1] == ["--"] else a.cmd
        if not cmd:
            raise GateError("a listing command is required after --")
        version = a.tool_version or _tool_version(cmd[:2])
        raw, code = _run_listing(cmd)
        plan = build_rust_plan(raw, code, cmd, version, a.mode)
    _write(a.out, plan)
    print(f"{plan['state']}: {plan['count']} mutants, digest {plan['digest'][:16]}")
    return 0


def cmd_plan_go(a: argparse.Namespace) -> int:
    if os.path.exists(a.out):
        os.unlink(a.out)
    plan = build_go_plan(
        a.pkgs.split(), {"planner": "scripts/mutation_plan.py", "mode": a.mode}
    )
    _write(a.out, plan)
    print(f"{plan['state']}: {plan['count']} package(s), digest {plan['digest'][:16]}")
    return 0


def cmd_wrap_shard(a: argparse.Namespace) -> int:
    rep = wrap_shard(a.plan_digest, a.shard, a.shards, a.outcomes)
    _write(a.out, rep)
    print(
        f"shard {a.shard}/{a.shards}: {rep['outcomes']['total_mutants']} verdicts bound to plan {a.plan_digest[:16]}"
    )
    return 0


def cmd_verify(a: argparse.Namespace) -> int:
    ok = True
    if a.rust_plan:
        try:
            plan = load_plan(a.rust_plan, "rust-mutants")
            acct = verify_rust(
                plan, read_reports(a.rust_reports, "shard-*.json"), a.rust_shards
            )
            line = " ".join(f"{k}={acct[k]}" for k in ("planned", *VERDICTS))
            print(f"rust {plan['state']} [{POLICY}]: {line}")
            if acct["planned"]:
                viable = acct["planned"] - acct["unviable"]
                print(
                    f"rust score: caught/planned = {acct['caught']}/{acct['planned']}; "
                    f"caught/(planned-unviable) = {acct['caught']}/{viable}"
                )
            bad = [f"{v}={acct[v]}" for v in FAILING if acct[v]]
            if bad:
                raise GateError("Rust verdicts that fail the gate: " + ", ".join(bad))
        except GateError as e:
            print(f"::error::rust: {e}")
            ok = False
    if a.go_plan:
        try:
            plan = load_plan(a.go_plan, "go-packages")
            acct = verify_go(
                plan, read_reports(a.go_reports, "chunk-*.json"), a.go_chunks
            )
            print(
                f"go {plan['state']}: " + " ".join(f"{k}={v}" for k, v in acct.items())
            )
        except GateError as e:
            print(f"::error::go: {e}")
            ok = False
    return 0 if ok else 1


def cmd_check_coverprofile(a: argparse.Namespace) -> int:
    n = check_coverprofile(a.profile)
    print(f"{a.profile}: {n} blocks")
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    sub = ap.add_subparsers(dest="command", required=True)

    p = sub.add_parser("plan-rust")
    p.add_argument("--out", required=True)
    p.add_argument("--mode", default="full", choices=["full", "diff"])
    p.add_argument("--tool-version")
    p.add_argument("--skipped", help="no listing was needed; the planner's reason")
    p.add_argument("cmd", nargs=argparse.REMAINDER)
    p.set_defaults(fn=cmd_plan_rust)

    p = sub.add_parser("plan-go")
    p.add_argument("--out", required=True)
    p.add_argument("--mode", default="diff", choices=["full", "diff"])
    p.add_argument(
        "--pkgs", default="", help="space-separated, as the planner prints them"
    )
    p.set_defaults(fn=cmd_plan_go)

    p = sub.add_parser("wrap-shard")
    p.add_argument("--plan-digest", required=True)
    p.add_argument("--shard", type=int, required=True)
    p.add_argument("--shards", type=int, required=True)
    p.add_argument("--outcomes", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_wrap_shard)

    p = sub.add_parser("verify")
    p.add_argument("--rust-plan")
    p.add_argument("--rust-reports", default="")
    p.add_argument("--rust-shards", type=int, default=0)
    p.add_argument("--go-plan")
    p.add_argument("--go-reports", default="")
    p.add_argument("--go-chunks", type=int, default=0)
    p.set_defaults(fn=cmd_verify)

    p = sub.add_parser("check-coverprofile")
    p.add_argument("profile")
    p.set_defaults(fn=cmd_check_coverprofile)

    a = ap.parse_args(argv)
    try:
        return a.fn(a)
    except GateError as e:
        print(f"::error::{e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
