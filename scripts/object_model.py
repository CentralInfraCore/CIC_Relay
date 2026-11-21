#!/usr/bin/env python3
"""Object-level measurement model (report-only): what check did each production object get?

Global percentages say how much of a package ran. They do not say, per function or method,
which coverage and which mutation verdicts it has, or that for some objects nothing can be
said. This module builds that model and a canonical, digested evidence record of it, and a
verifier that states what it proves and what it does not.

Contracts (versioned; a change is a change of the gate logic, planned as such by mutation_plan.py)
  object-id/1         the key of an object (see go_object_id / rust_object_key)
  object-evidence/1   the record (build_record) and its three verification levels (verify_record)
  measurement-scope/1 which files are production, test, generated, experimental, measured apart

Every evidence field of every object is exactly one of
  MEASURED         a measured value
  NOT_APPLICABLE   nothing to measure, for a stated reason
  UNVERIFIABLE     it cannot be established, with a coded reason: never a hidden success

The coverage KIND is part of the data and is one of statement | line | region. There is no
"branch": the stable toolchain gives none (measured: `cargo llvm-cov --branch` needs nightly), so
nothing here may be called branch coverage.

Attribution is never estimated. A Go cover block belongs to an object only if it lies completely
inside that object's span; a block that cuts an object, spans several, or lies in none is counted
and makes the affected fields UNVERIFIABLE. No value is ever split proportionally.

Verification levels (never merged into one "verified"):
  L1 INTEGRITY_OK       schema, digest, totals recount, enums: the record is internally consistent
  L2 BOUND_TO_INPUTS    the commit/tree and the input files are the ones the record names
                        (NOT_CHECKED without them)
  L3 COMPLETE_FOR_SCOPE the object list is complete against an independent enumeration
                        (PARTIAL where only a part can be shown)
A digest does not prove where a measurement came from: that the tools really ran on that tree
is not shown by any of the three levels.
"""

import argparse
import fnmatch
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

import quality_gate as qg

ID_CONTRACT = "object-id/1"
EVIDENCE_SCHEMA = "object-evidence/2"
SCOPE_SCHEMA = "measurement-scope/1"

MEASURED = "MEASURED"
NOT_APPLICABLE = "NOT_APPLICABLE"
UNVERIFIABLE = "UNVERIFIABLE"
STATES = (MEASURED, NOT_APPLICABLE, UNVERIFIABLE)
COVERAGE_KINDS = (
    "statement",
    "line",
    "region",
)  # no "branch": see the module docstring

REASONS = {
    "FILE_NOT_IN_PROFILE",  # the file has no block in the coverage profile (not built / not tested)
    "PARTIAL_BLOCK",  # a cover block cuts the object: not attributable, no value is made up
    "AMBIGUOUS_MAPPING",  # an instance or mutant fits several objects (or none): never a guess
    "NONDETERMINISTIC",  # repeated measurements disagree
    "TOOL_LIMIT",  # the available tool does not provide it
    "NO_MAPPING",  # no way to connect it to this object
    "NO_STATEMENTS",  # the object has no executable statement
    "NO_MUTANTS_LISTED",  # the mutation tool lists no mutant for this function
    "NO_MUTANTS_PLANNED",
    "ALL_UNVIABLE",
    "NO_EVALUABLE_MUTANT",
    "MEASUREMENT_MISSING",
}
FIELDS = ("coverage", "mutation", "tests")


class ModelError(Exception):
    """The input cannot be trusted: no record is built / the record is rejected."""


def field(state: str, **kw) -> dict:
    """A per-object field value - its state plus any detail."""
    return {"state": state, **kw}


def unverifiable(reason: str, **kw) -> dict:
    """A field in state UNVERIFIABLE, with the reason it could not be established."""
    return field(UNVERIFIABLE, reason=reason, **kw)


def not_applicable(reason: str, **kw) -> dict:
    """A field in state NOT_APPLICABLE, with the reason it does not apply to the object."""
    return field(NOT_APPLICABLE, reason=reason, **kw)


def sha256_file(path: str) -> tuple[str, int]:
    """The hex SHA-256 and the size in bytes of a file."""
    data = Path(path).read_bytes()
    return hashlib.sha256(data).hexdigest(), len(data)


# --------------------------------------------------------------------------- measurement scope


def classify(scope: dict, lang: str, path: str) -> tuple[str, dict]:
    """The first matching rule of the scope decides: (class, rule). No match is an error: a file the
    scope does not classify is neither measured nor silently left out."""
    for rule in scope["rules"]:
        if rule["lang"] == lang and fnmatch.fnmatch(path, rule["match"]):
            return rule["class"], rule
    raise ModelError(
        f"{path}: not classified by the measurement scope (neither included nor excluded with a reason)"
    )


def check_scope(scope: dict) -> None:
    """The scope file itself: schema, versioned, every exclusion has a reason."""
    if scope.get("schema") != SCOPE_SCHEMA or not isinstance(scope.get("version"), int):
        raise ModelError(f"the scope is not a {SCOPE_SCHEMA} with an integer version")
    classes = {
        "production",
        "test",
        "test-data",
        "generated",
        "experimental",
        "separate",
        "tool",
    }
    for i, rule in enumerate(scope.get("rules", [])):
        if (
            rule.get("class") not in classes
            or not rule.get("match")
            or rule.get("lang") not in ("go", "rust")
        ):
            raise ModelError(f"scope rule #{i} is malformed: {rule}")
        if rule["class"] != "production" and not (rule.get("reason") or "").strip():
            raise ModelError(
                f"scope rule #{i} ({rule['match']}) excludes without a reason"
            )


def scope_files(scope: dict, lang: str, tracked: list[str]) -> dict[str, list[str]]:
    """Group the tracked files of a language by scope class; an unclassified file is an error."""
    out: dict[str, list[str]] = {}
    for f in tracked:
        if not f.endswith(".go" if lang == "go" else ".rs"):
            continue
        cls, rule = classify(scope, lang, f)
        out.setdefault(
            cls if cls != "separate" else f"separate:{rule.get('unit', '?')}", []
        ).append(f)
    return out


# --------------------------------------------------------------------------- Go


def go_module_path(gomod_text: str) -> str:
    """The module path declared in a go.mod text; ModelError if it has no module line."""
    m = re.search(r"^module\s+(\S+)", gomod_text, re.M)
    if not m:
        raise ModelError("go.mod has no module line")
    return m.group(1)


_BLOCK = re.compile(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")


def parse_coverprofile(text: str) -> list[tuple]:
    """The blocks (file, sl, sc, el, ec, nstmt, count) of a Go cover profile, merged with `set`
    semantics (a block that appears more than once is covered if any occurrence is)."""
    lines = text.splitlines()
    if not lines or not re.match(r"^mode: (set|count|atomic)$", lines[0]):
        raise ModelError("the coverage profile has no valid mode line")
    merged: dict[tuple, list[int]] = {}
    for ln in lines[1:]:
        if not ln.strip():
            continue
        m = _BLOCK.match(ln)
        if not m:
            raise ModelError(f"malformed cover block: {ln!r}")
        f, sl, sc, el, ec, n, c = m.group(1), *map(int, m.groups()[1:])
        cur = merged.setdefault((f, sl, sc, el, ec), [n, 0])
        if cur[0] != n:
            raise ModelError(f"the same block has two statement counts: {ln!r}")
        cur[1] = max(cur[1], c)
    if not merged:
        raise ModelError("the coverage profile holds no blocks")
    return [(*k, v[0], v[1]) for k, v in sorted(merged.items())]


def go_object_id(module: str, file: str, fn: dict, build: str = "") -> str:
    """object-id/1 for Go: go:<import path>.<Name> | .<Recv>.<Name> | .init#<file>#<ord>, plus
    @build(<constraint>) when the file carries one. The position is NOT part of the key (an edit
    above the function does not rename it)."""
    d = file.rsplit("/", 1)[0] if "/" in file else ""
    pkg = module + ("/" + d if d else "")
    if fn["kind"] == "init":
        name = f"init#{file.rsplit('/', 1)[-1]}#{fn['ord']}"
    elif fn["kind"] == "method":
        name = f"{fn['recv']}.{fn['name']}"
    else:
        name = fn["name"]
    key = f"go:{pkg}.{name}"
    return key + (f"@build({' '.join(build.split())})" if build else "")


def _pos(line: int, col: int) -> tuple[int, int]:
    """A source position as a comparable (line, column) pair."""
    return (line, col)


def _attribute(blocks: list[tuple], funcs: list[tuple]) -> tuple[dict, dict]:
    """Attribute the blocks of ONE file to its functions.

    funcs: [(key, start(line,col), end(line,col))]. Returns ({key: [blocks]}, stats) where stats
    counts the blocks that could not be attributed: partial (cuts an object or spans several) and
    unattributed (inside no object). A block belongs to an object only if it lies completely inside.
    """
    got: dict[str, list[tuple]] = {k: [] for k, _, _ in funcs}
    partial_for: dict[str, int] = {k: 0 for k, _, _ in funcs}
    stats = {"attributed": 0, "partial": 0, "unattributed": 0}
    for b in blocks:
        bs, be = _pos(b[1], b[2]), _pos(b[3], b[4])
        inside = [k for k, fs, fe in funcs if fs <= bs and be <= fe]
        touching = [k for k, fs, fe in funcs if not (be <= fs or bs >= fe)]
        if len(inside) == 1 and touching == inside:
            got[inside[0]].append(b)
            stats["attributed"] += 1
        elif touching:  # cuts an object, or lies in several
            stats["partial"] += 1
            for k in touching:
                partial_for[k] += 1
        else:
            stats["unattributed"] += 1
    return {"blocks": got, "partial": partial_for}, stats


def go_objects(
    unit: str,
    module: str,
    inventory: dict,
    blocks: list[tuple],
    prefix: str = "",
    repeats: list[list[tuple]] | None = None,
    detail: bool = False,
) -> tuple[list[dict], dict]:
    """The Go objects of one measurement unit (a module) and the attribution statistics.

    prefix: the profile path of a file is module + "/" + relative path; for a nested module the
    inventory paths are relative to it. repeats: further profiles of the same tree; an object whose
    covered count differs between them is NONDETERMINISTIC. detail: keep the uncovered spans.
    """
    by_file: dict[str, list[tuple]] = {}
    for b in blocks:
        by_file.setdefault(b[0], []).append(b)
    objects: list[dict] = []
    stats = {
        "attributed": 0,
        "partial": 0,
        "unattributed": 0,
        "profile_files_not_in_inventory": 0,
    }
    inv_files = set()
    seen_ids: dict[str, str] = {}
    for fdump in inventory["files"]:
        if fdump.get("error"):
            raise ModelError(
                f"{fdump['file']}: the inventory could not parse it: {fdump['error']}"
            )
        rel = fdump["file"]
        inv_files.add(module + "/" + rel)
        funcs = []
        for fn in fdump["funcs"]:
            oid = go_object_id(module, rel, fn, fdump.get("build", ""))
            if oid in seen_ids:
                raise ModelError(
                    f"object id collision: {oid!r} ({seen_ids[oid]} and {rel})"
                )
            seen_ids[oid] = rel
            funcs.append(
                (
                    oid,
                    _pos(fn["start"]["line"], fn["start"]["col"]),
                    _pos(fn["end"]["line"], fn["end"]["col"]),
                    fn,
                )
            )
        file_blocks = by_file.get(module + "/" + rel, [])
        res, st = _attribute(file_blocks, [(k, s, e) for k, s, e, _ in funcs])
        for k in stats:
            if k in st:
                stats[k] += st[k]
        in_profile = bool(file_blocks)
        for oid, s, e, fn in funcs:
            mine = res["blocks"][oid]
            cov: dict
            if not in_profile:
                cov = unverifiable("FILE_NOT_IN_PROFILE")
            elif res["partial"][oid]:
                cov = unverifiable("PARTIAL_BLOCK", blocks=res["partial"][oid])
            elif not mine or sum(b[5] for b in mine) == 0:
                # no block, or only blocks without a statement (an empty function body): 0 of 0 is not a measurement
                cov = not_applicable("NO_STATEMENTS")
            else:
                total = sum(b[5] for b in mine)
                covered = sum(b[5] for b in mine if b[6] > 0)
                cov = field(MEASURED, kind="statement", total=total, covered=covered)
                n_runs = 1 + len(repeats or [])
                cov["runs"] = n_runs
                cov["determinism"] = (
                    "OBSERVED_STABLE" if n_runs >= STABLE_MIN_RUNS else "NOT_SHOWN"
                )
                if repeats:
                    seen = {covered}
                    for rep in repeats:
                        r_by = {b[:5]: b for b in rep}
                        seen.add(
                            sum(b[5] for b in mine if r_by.get(b[:5], (0,) * 7)[6] > 0)
                        )
                    if len(seen) > 1:
                        cov = unverifiable(
                            "NONDETERMINISTIC",
                            range=[min(seen), max(seen)],
                            total=total,
                            runs=n_runs,
                        )
                if detail and cov["state"] == MEASURED:
                    cov["uncovered"] = [
                        [b[1], b[2], b[3], b[4]] for b in mine if b[6] == 0
                    ]
            objects.append(
                {
                    "id": oid,
                    "lang": "go",
                    "unit": unit,
                    "kind": fn["kind"],
                    "name": fn["name"],
                    "file": rel,
                    "span": {"start": s[0], "end": e[0]},
                    "coverage": cov,
                    "mutation": unverifiable(
                        "TOOL_LIMIT",
                        note="gremlins prints per-mutant lines, they are not mapped",
                    ),
                    "tests": unverifiable("TOOL_LIMIT", note="no per-test attribution"),
                }
            )
    stats["profile_files_not_in_inventory"] = len(
        {f for f in by_file if f not in inv_files}
    )
    return objects, stats


# --------------------------------------------------------------------------- Rust

_CRATE_DISAMBIGUATOR = re.compile(r"\[[0-9a-f]+\]")
_CLOSURE_TAIL = re.compile(r"::\{(?:closure|async block|async fn body)#\d+\}$")


def _strip_generic_suffix(name: str) -> str:
    """`a::f::<T, U>` -> `a::f`: an instantiation belongs to its generic function."""
    while name.endswith(">"):
        depth = 0
        for i in range(len(name) - 1, -1, -1):
            c = name[i]
            depth += (c == ">") - (c == "<")
            if depth == 0:
                if name[max(0, i - 2) : i] == "::":
                    name = name[: i - 2]
                    break
                return name
        else:
            return name
    return name


def rust_object_key(demangled: str) -> str:
    """object-id/1 for Rust: rust:<demangled path>, with the per-binary crate disambiguators
    ([0], [1] ...) removed, a closure folded into the function that contains it, and a generic
    instantiation folded into its generic function. The file and the span are attributes, not the key.
    """
    n = _CRATE_DISAMBIGUATOR.sub("", demangled)
    for _ in range(8):
        before = n
        n = _CLOSURE_TAIL.sub("", n)
        n = _strip_generic_suffix(n)
        if n == before:
            break
    return "rust:" + n


def rust_name(key: str) -> str:
    """The last path segment of a key: the function name the mutation tool also reports."""
    body = key[len("rust:") :]
    body = re.sub(r"<[^<>]*>$", "", body)
    return re.split(r"::", body)[-1]


def _scan(text: str):
    """Yield (index, char, line) for the characters of a Rust source that are CODE: comments, string
    literals (incl. raw strings) and char literals are skipped, a lifetime is not a char literal.
    """
    i, n, line = 0, len(text), 1
    while i < n:
        c = text[i]
        two = text[i : i + 2]
        if two == "//":
            while i < n and text[i] != "\n":
                i += 1
            continue
        if two == "/*":
            depth, i = 1, i + 2
            while i < n and depth:
                if text[i : i + 2] == "/*":
                    depth, i = depth + 1, i + 2
                elif text[i : i + 2] == "*/":
                    depth, i = depth - 1, i + 2
                else:
                    line += text[i] == "\n"
                    i += 1
            continue
        m = re.match(r'b?r(#*)"', text[i:])
        if m:  # raw string: ends at "###
            end = text.find('"' + m.group(1), i + m.end())
            end = n if end < 0 else end + 1 + len(m.group(1))
            line += text.count("\n", i, end)
            i = end
            continue
        if c == '"':
            i += 1
            while i < n and text[i] != '"':
                if text[i] == "\\":
                    i += 1
                line += text[i] == "\n" if i < n else 0
                i += 1
            i += 1
            continue
        if c == "'":
            # a char literal ('x', '\\n', '\\u{..}') ends with a quote within a few chars; a lifetime does not
            m = re.match(
                r"'(?:\\(?:u\{[0-9a-fA-F]+\}|x[0-9a-fA-F]{2}|.)|[^'\\])'", text[i:]
            )
            if m:
                i += m.end()
                continue
        if c == "\n":
            line += 1
        yield i, c, line
        i += 1


def cfg_test_spans(text: str) -> list[tuple[int, int]]:
    """The (first line, last line) of every item that carries #[cfg(test)]: a `mod tests { .. }`, a
    single test-only function, or a `use`/`mod x;` ending at its `;`. Production code that merely FOLLOWS
    such an item is not test code (a first-#[cfg(test)]-to-the-end rule would drop it: measured on
    ffi/src/lib.rs, where a production function follows a #[cfg(test)] item in the middle of the file).
    """
    spans: list[tuple[int, int]] = []
    code = list(_scan(text))
    attr = re.compile(r"#\[cfg\(test\)\]")
    for m in attr.finditer(text):
        pos = m.start()
        start_line = text.count("\n", 0, pos) + 1
        depth, opened, end_line = 0, False, None
        for i, c, ln in code:
            if i < m.end():
                continue
            if c == "{":
                depth, opened = depth + 1, True
            elif c == "}" and opened:
                depth -= 1
                if depth == 0:
                    end_line = ln
                    break
            elif c == ";" and not opened:
                end_line = ln
                break
        if end_line is None:
            raise ModelError(
                f"cannot find the end of the #[cfg(test)] item at line {start_line}"
            )
        spans.append((start_line, end_line))
    return spans


def _regions(fn: dict) -> list[list[int]]:
    """The code regions of an llvm-cov function entry (kind 0)."""
    return [r for r in fn.get("regions", []) if len(r) >= 8 and r[7] == 0]


def rust_objects(
    llvm: dict,
    demangled: dict[str, str],
    root: str | None,
    mutants: list[dict] | None,
    verdicts: dict[str, str] | None,
    prefix: str = "/w/",
) -> tuple[list[dict], dict]:
    """The Rust objects from a real `cargo llvm-cov --json` export, the demangled names of its
    symbols, the cargo-mutants listing and the verdicts of the run.

    Test code is not a production object: a file under a tests/ or benches/ directory, or a function
    inside an item that carries #[cfg(test)] (see cfg_test_spans), is counted as excluded (never silently
    dropped).
    """
    data = llvm["data"][0]
    cfg_spans: dict[str, list[tuple[int, int]]] = {}
    groups: dict[str, list[dict]] = {}
    excluded = {"test_code": 0, "no_name": 0}
    for fn in data["functions"]:
        sym = fn["name"]
        if sym not in demangled:
            excluded["no_name"] += 1
            raise ModelError(
                f"no demangled name for symbol {sym!r}: the object cannot be named"
            )
        file = fn["filenames"][0]
        rel = file[len(prefix) :] if file.startswith(prefix) else file
        regs = _regions(fn)
        first_line = min((r[0] for r in regs), default=0)
        parts = rel.split("/")
        is_test = "tests" in parts[:-1] or "benches" in parts[:-1]
        if not is_test and root and rel.endswith(".rs"):
            if rel not in cfg_spans:
                p = Path(root) / rel
                cfg_spans[rel] = (
                    cfg_test_spans(p.read_text(encoding="utf-8")) if p.is_file() else []
                )
            is_test = any(a <= first_line <= b for a, b in cfg_spans[rel])
        if is_test:
            excluded["test_code"] += 1
            continue
        key = rust_object_key(demangled[sym])
        groups.setdefault(key, []).append(
            {
                "symbol": sym,
                "file": rel,
                "regions": regs,
                "count": fn.get("count", 0),
                "demangled": demangled[sym],
            }
        )

    objects: list[dict] = []
    for key in sorted(groups):
        inst = groups[key]
        files = {i["file"] for i in inst}
        total = sum(len(i["regions"]) for i in inst)
        covered = sum(1 for i in inst for r in i["regions"] if r[4] > 0)
        starts = [r[0] for i in inst for r in i["regions"]]
        ends = [r[2] for i in inst for r in i["regions"]]
        if len(files) != 1:
            cov = unverifiable("AMBIGUOUS_MAPPING", files=sorted(files))
        elif not total:
            cov = not_applicable("NO_STATEMENTS")
        else:
            cov = field(
                MEASURED,
                kind="region",
                total=total,
                covered=covered,
                instances=len(inst),
                aggregation="sum-over-instances/1",
                runs=1,
                determinism="NOT_SHOWN",
            )
        objects.append(
            {
                "id": key,
                "lang": "rust",
                "unit": "rust",
                "kind": "function",
                "name": rust_name(key),
                "file": sorted(files)[0],
                "span": {"start": min(starts, default=0), "end": max(ends, default=0)},
                "instances": [
                    {"symbol": i["symbol"], "count": i["count"]}
                    for i in sorted(inst, key=lambda x: x["symbol"])
                ],
                "coverage": cov,
                "mutation": unverifiable("MEASUREMENT_MISSING"),
                "tests": unverifiable("TOOL_LIMIT", note="no per-test attribution"),
            }
        )

    stats = {
        "test_code_excluded": excluded["test_code"],
        "mutants": 0,
        "mutants_mapped": 0,
        "mutants_ambiguous": 0,
        "mutants_unmapped": 0,
        "mutants_without_function": 0,
    }
    if mutants is not None and verdicts is not None:
        by_file: dict[str, list[dict]] = {}
        for o in objects:
            by_file.setdefault(o["file"], []).append(o)
        counts: dict[str, dict[str, int]] = {
            o["id"]: dict.fromkeys(qg.VERDICTS, 0) for o in objects
        }
        for m in mutants:
            stats["mutants"] += 1
            if not m.get(
                "function"
            ):  # real data: a mutant outside any function (e.g. a struct field)
                stats["mutants_without_function"] += 1
                continue
            fs, fe = (
                m["function"]["span"]["start"]["line"],
                m["function"]["span"]["end"]["line"],
            )
            # measured: cargo-mutants qualifies the name ("logic::echo", "Type::method"); the object
            # name is the last segment, so the last segment is what is compared
            fname = m["function"]["function_name"].split("::")[-1]
            cand = [
                o
                for o in by_file.get(m["file"], [])
                if o["name"] == fname and fs <= o["span"]["start"] <= fe
            ]
            if len(cand) > 1:
                stats["mutants_ambiguous"] += 1
                continue
            if not cand:
                stats["mutants_unmapped"] += 1
                continue
            verdict = verdicts.get(m["name"])
            if verdict is None:
                raise ModelError(
                    f"mutant {m['name']!r} is in the listing but has no verdict"
                )
            counts[cand[0]["id"]][verdict] += 1
            stats["mutants_mapped"] += 1
        for o in objects:
            c = counts[o["id"]]
            planned = sum(c.values())
            if not planned:
                o["mutation"] = not_applicable("NO_MUTANTS_LISTED")
            else:
                acct = {"planned": planned, **c}
                score = qg.primary_score(acct)
                o["mutation"] = field(MEASURED, **c, planned=planned, score=score)
    return objects, stats


# --------------------------------------------------------------------------- the record


def canonical_digest(obj: object) -> str:
    """The sha256 of the canonical JSON of obj - the same function as quality_gate's."""
    return qg.canonical_digest(obj)


def summarize(objects: list[dict]) -> dict:
    """Totals by state, per field and per unit. Tolerant of a broken object (a missing field is counted
    under the state `MISSING`, it does not crash): the verifier must report it, not die on it.
    """
    out: dict = {"objects": len(objects), "by_unit": {}, "fields": {}}
    for o in objects:
        u = out["by_unit"].setdefault(o.get("unit"), {"objects": 0, "fields": {}})
        u["objects"] += 1
        for f in FIELDS:
            state = (
                (o.get(f) or {}).get("state", "MISSING")
                if isinstance(o.get(f), dict)
                else "MISSING"
            )
            for tgt in (out["fields"], u["fields"]):
                cell = tgt.setdefault(f, {s: 0 for s in STATES})
                cell[state] = cell.get(state, 0) + 1
    reasons: dict[str, int] = {}
    for o in objects:
        for f in FIELDS:
            if (
                isinstance(o.get(f), dict)
                and o[f].get("state") != MEASURED
                and "reason" in o[f]
            ):
                key = f"{f}:{o[f]['state']}:{o[f]['reason']}"
                reasons[key] = reasons.get(key, 0) + 1
    out["reasons"] = dict(sorted(reasons.items()))
    return out


def build_record(
    objects: list[dict],
    commit: str,
    tree: str,
    tools: dict,
    inputs: dict,
    scope: dict,
    attribution: dict,
) -> dict:
    """Assemble and seal the evidence record of one measurement. Refuses (ModelError) an
    unknown tool version and duplicate object ids. The objects are sorted by id; the
    record binds the commit and tree, the scope digest, the tool versions, the input
    file digests and the attribution statistics, and carries its summary and its digest.
    """
    for name, ver in tools.items():
        if not ver or str(ver).lower() in ("unknown", "none", ""):
            raise ModelError(
                f"the version of the tool {name!r} is unknown: the record would be unusable"
            )
    ids = [o["id"] for o in objects]
    if len(set(ids)) != len(ids):
        raise ModelError("duplicate object ids in the record")
    ordered = sorted(objects, key=lambda o: o["id"])
    body: dict = {
        "schema": EVIDENCE_SCHEMA,
        "id_contract": ID_CONTRACT,
        "policy": qg.POLICY,
        "coverage_kinds": list(COVERAGE_KINDS),
        "identity": {"commit": commit, "tree": tree},
        "scope": {
            "schema": SCOPE_SCHEMA,
            "version": scope["version"],
            "digest": canonical_digest(scope),
        },
        "tools": dict(sorted(tools.items())),
        "inputs": dict(sorted(inputs.items())),
        "attribution": attribution,
        "objects": ordered,
    }
    body["summary"] = summarize(ordered)
    body["digest"] = canonical_digest({k: v for k, v in body.items() if k != "digest"})
    return body


# --------------------------------------------------------------------------- verification

# object-evidence/2 (schema decision, PR-05): `runs` and `determinism` are new, REQUIRED fields of a MEASURED
# coverage field. They are not additive-optional, because a record that does not say how many runs a value comes
# from cannot be told apart from one that is stable. /1 records were report-only artifacts nobody consumes, so
# /2 is a strict replacement: a /1 record is rejected as an unknown schema (SCHEMA), not silently upgraded.
STABLE_MIN_RUNS = 20  # the least number of identical independent runs that is called OBSERVED_STABLE (see the PR-05 report)
DETERMINISM = ("NOT_SHOWN", "OBSERVED_STABLE")


def code(c: str, msg: str) -> str:
    """A finding with its own diagnostic code, so that two different faults never share one message."""
    return f"[{c}] {msg}"


def verify_l1(rec: dict) -> list[str]:
    """Record integrity: every problem found (empty = INTEGRITY_OK)."""
    bad: list[str] = []
    if (
        not isinstance(rec, dict)
        or rec.get("schema") != EVIDENCE_SCHEMA
        or rec.get("id_contract") != ID_CONTRACT
    ):
        return [
            code("SCHEMA", f"schema/contract is not {EVIDENCE_SCHEMA}/{ID_CONTRACT}")
        ]
    if rec.get("digest") != canonical_digest(
        {k: v for k, v in rec.items() if k != "digest"}
    ):
        bad.append(code("DIGEST_MISMATCH", "the digest does not match the content"))
    if rec.get("policy") != qg.POLICY:
        bad.append(
            code(
                "POLICY_MISMATCH",
                f"the record is bound to {rec.get('policy')!r}, the gate to {qg.POLICY!r}",
            )
        )
    for name, ver in (rec.get("tools") or {}).items():
        if not ver or str(ver).lower() in ("unknown", "none"):
            bad.append(
                code("TOOL_UNKNOWN", f"the version of the tool {name!r} is unknown")
            )
    if not rec.get("tools"):
        bad.append(code("TOOL_MISSING", "no tool versions are recorded"))
    objs = rec.get("objects")
    if not isinstance(objs, list):
        return bad + [code("NO_OBJECTS", "no object list")]
    ids = [o.get("id") for o in objs]
    if ids != sorted(ids) or len(set(ids)) != len(ids):
        bad.append(code("DUPLICATE_ID", "the object ids are not sorted and unique"))
    for o in objs:
        for f in FIELDS:
            v = o.get(f)
            if not isinstance(v, dict) or v.get("state") not in STATES:
                bad.append(
                    code(
                        "MISSING_FIELD",
                        f"{o.get('id')}: field {f} is missing or has no valid state",
                    )
                )
                continue
            if v["state"] != MEASURED and v.get("reason") not in REASONS:
                bad.append(
                    code(
                        "BAD_REASON",
                        f"{o.get('id')}: field {f} is {v['state']} without a valid reason ({v.get('reason')!r})",
                    )
                )
            if f == "coverage" and v["state"] == MEASURED:
                if v.get("kind") not in COVERAGE_KINDS:
                    bad.append(
                        code(
                            "BAD_KIND",
                            f"{o.get('id')}: coverage kind {v.get('kind')!r} is not one of {COVERAGE_KINDS}",
                        )
                    )
                runs, det = v.get("runs"), v.get("determinism")
                if not isinstance(runs, int) or runs < 1 or det not in DETERMINISM:
                    bad.append(
                        code(
                            "BAD_DETERMINISM",
                            f"{o.get('id')}: a measured coverage needs runs >= 1 and determinism in {DETERMINISM}",
                        )
                    )
                elif det == "OBSERVED_STABLE" and runs < STABLE_MIN_RUNS:
                    bad.append(
                        code(
                            "BAD_DETERMINISM",
                            f"{o.get('id')}: OBSERVED_STABLE from {runs} run(s), at least {STABLE_MIN_RUNS} are needed",
                        )
                    )
            if (
                f == "coverage"
                and v.get("reason") == "NONDETERMINISTIC"
                and not (isinstance(v.get("runs"), int) and v["runs"] >= 2)
            ):
                bad.append(
                    code(
                        "BAD_DETERMINISM",
                        f"{o.get('id')}: NONDETERMINISTIC needs the number of runs (>= 2)",
                    )
                )
    if rec.get("summary") != summarize(objs):
        bad.append(code("SUMMARY_MISMATCH", "the summary does not match the objects"))
    if not rec.get("inputs"):
        bad.append(code("NO_INPUTS", "no input digests are recorded"))
    return bad


def load_tool_pins(path: str) -> dict:
    """The pinned versions of the measuring tools (`.measurement-tools.json`); see quality_gate.load_tool_pins."""
    try:
        return qg.load_tool_pins(path)
    except qg.GateError as e:
        raise ModelError(str(e)) from e


def verify_tools(rec: dict, pins: dict | None, quality: bool = False) -> dict:
    """Is every tool of the record the pinned one, and does the record declare every tool it must? Fail closed.

    UNUSABLE: the version is known but is not the pinned one. UNVERIFIABLE: the version cannot be established
    (missing, empty, `unknown`) or the tool is not pinned at all (an unknown tool decides nothing). Either verdict
    rejects the measurement. The versions are what the record DECLARES: that the declared tool really ran is
    provenance, not checked here. `pins` is the loaded `.measurement-tools.json`; the tools the record must declare
    are its `record_tools` (all pinned tools when absent)."""
    if pins is None:
        if quality:
            # a quality verdict without the pin configuration cannot say which tools measured: fail closed
            return {
                "verdict": "UNVERIFIABLE",
                "findings": [
                    code(
                        "TOOL_PINS_MISSING",
                        "no tool pins were given: the tools cannot be verified (quality mode)",
                    )
                ],
            }
        return {"verdict": "NOT_CHECKED", "findings": ["no tool pins were given"]}
    required = pins["surfaces"]["object-evidence"]
    unusable, unverifiable = qg.tool_pin_findings(
        pins["tools"], rec.get("tools") or {}, required
    )
    verdict = (
        "UNUSABLE" if unusable else "UNVERIFIABLE" if unverifiable else "TOOLS_PINNED"
    )
    return {"verdict": verdict, "findings": unusable + unverifiable}


def stability(rec: dict) -> dict:
    """Is the evidence STABLE? Only if every measured coverage was seen identical in >= STABLE_MIN_RUNS independent
    runs and no object is NONDETERMINISTIC. A single run is never stable: it shows nothing about determinism.
    """
    cov = [
        o["coverage"]
        for o in rec.get("objects", [])
        if isinstance(o.get("coverage"), dict)
    ]
    nondet = sorted(
        o["id"]
        for o in rec.get("objects", [])
        if (o.get("coverage") or {}).get("reason") == "NONDETERMINISTIC"
    )
    measured = [c for c in cov if c.get("state") == MEASURED]
    shown = sum(1 for c in measured if c.get("determinism") == "OBSERVED_STABLE")
    return {
        "verdict": (
            "STABLE"
            if measured and shown == len(measured) and not nondet
            else "NOT_STABLE"
        ),
        "measured": len(measured),
        "observed_stable": shown,
        "not_shown": len(measured) - shown,
        "nondeterministic_objects": nondet,
    }


def _cmp_cov(want: dict, got: dict) -> bool:
    """Do two coverage fields say the same? runs and determinism ARE compared (a stability claim must be provable from the
    bound input profiles, like a value); only the uncovered spans are not."""
    if want.get("state") != got.get("state") or want.get("reason") != got.get("reason"):
        return False
    return (
        all(
            want.get(k) == got.get(k)
            for k in ("kind", "total", "covered", "runs", "determinism")
        )
        if want.get("state") == MEASURED
        else True
    )


def verify_l2(
    rec: dict, root: str | None, inputs: dict[str, str], derived: dict | None = None
) -> tuple[str, list[str]]:
    """Binding to the inputs: PASS | FAIL | NOT_CHECKED, with the findings.

    The checkout identity and the input digests are checked as before. With `derived` ({object id: the coverage field
    re-derived from the INPUT FILES}) the measured values are checked too: a record whose values were forged and
    re-sealed (digest and summary recomputed) is internally consistent and its inputs are untouched, so only a
    re-derivation can see it (REDERIVATION_MISMATCH)."""
    if root is None and not inputs:
        return "NOT_CHECKED", ["no checkout and no input files were given"]
    bad: list[str] = []
    checked = 0
    if root is not None:
        try:
            tree = subprocess.run(
                ["git", "-C", root, "rev-parse", "HEAD^{tree}"],
                capture_output=True,
                text=True,
                check=True,
            ).stdout.strip()
            commit = subprocess.run(
                ["git", "-C", root, "rev-parse", "HEAD"],
                capture_output=True,
                text=True,
                check=True,
            ).stdout.strip()
        except (OSError, subprocess.CalledProcessError) as e:
            return "FAIL", [
                code("IDENTITY_UNREADABLE", f"cannot read the checkout identity: {e}")
            ]
        checked += 1
        if tree != rec["identity"]["tree"] or commit != rec["identity"]["commit"]:
            bad.append(
                code(
                    "COMMIT_MISMATCH",
                    "the checkout is not the commit/tree the record names",
                )
            )
    for name, path in inputs.items():
        want = rec["inputs"].get(name)
        if want is None:
            bad.append(code("INPUT_UNKNOWN", f"input {name!r} is not in the record"))
            continue
        checked += 1
        try:
            digest, size = sha256_file(path)
        except OSError as e:
            bad.append(code("INPUT_UNREADABLE", f"input {name!r} cannot be read: {e}"))
            continue
        if digest != want["sha256"] or size != want["bytes"]:
            bad.append(
                code(
                    "INPUT_DIGEST_MISMATCH",
                    f"input {name!r} does not match the digest in the record",
                )
            )
    if derived is not None:
        checked += 1
        units = {d["unit"] for d in derived.values()}
        have = {o["id"]: o["coverage"] for o in rec["objects"] if o["unit"] in units}
        for oid in sorted(set(have) - set(derived)):
            bad.append(
                code(
                    "REDERIVATION_MISMATCH",
                    f"{oid}: in the record but not derivable from the inputs",
                )
            )
        for oid, want in sorted(derived.items()):
            got = have.get(oid)
            if got is None:
                bad.append(
                    code(
                        "REDERIVATION_MISMATCH",
                        f"{oid}: derivable from the inputs but not in the record",
                    )
                )
            elif not _cmp_cov(want["coverage"], got):
                bad.append(
                    code(
                        "REDERIVATION_MISMATCH",
                        f"{oid}: the record says {_brief(got)}, the inputs give {_brief(want['coverage'])}",
                    )
                )
    if bad:
        return "FAIL", bad
    return ("PASS" if checked else "NOT_CHECKED"), []


def _brief(c: dict) -> str:
    """A short text form of a coverage field for messages ("MEASURED 3/5" or "STATE
    (reason)").
    """
    return f"{c.get('state')}" + (
        f" {c.get('covered')}/{c.get('total')}"
        if c.get("state") == MEASURED
        else f" ({c.get('reason')})"
    )


def derive_go_coverage(
    unit: str,
    module: str,
    inventory: dict,
    blocks: list[tuple],
    repeats: list[list[tuple]] | None = None,
) -> dict:
    """The Go coverage of every object, derived from the inventory and the cover profile by a SECOND, different
    algorithm than go_objects (a binary search over the sorted function starts instead of a scan of all functions),
    so that a defect of the first one is not repeated here. Returns {object id: {"unit", "coverage"}}.
    """
    import bisect

    by_file: dict[str, list[tuple]] = {}
    for b in blocks:
        by_file.setdefault(b[0], []).append(b)
    out: dict[str, dict] = {}
    for fdump in inventory["files"]:
        rel = fdump["file"]
        funcs = sorted(
            (
                (
                    fn["start"]["line"],
                    fn["start"]["col"],
                    fn["end"]["line"],
                    fn["end"]["col"],
                    go_object_id(module, rel, fn, fdump.get("build", "")),
                )
                for fn in fdump["funcs"]
            )
        )
        starts = [(f[0], f[1]) for f in funcs]
        key = module + "/" + rel
        runs = [by_file.get(key, [])] + [
            [b for b in rep if b[0] == key] for rep in (repeats or [])
        ]
        per_run: list[dict[str, list]] = []
        for blks in runs:
            acc: dict[str, list] = {
                f[4]: [0, 0, 0] for f in funcs
            }  # total, covered, partial
            for b in blks:
                bs, be = (b[1], b[2]), (b[3], b[4])
                i = bisect.bisect_right(starts, bs) - 1
                if (
                    i >= 0
                    and be <= (funcs[i][2], funcs[i][3])
                    and (i + 1 >= len(funcs) or be <= starts[i + 1])
                ):
                    acc[funcs[i][4]][0] += b[5]
                    acc[funcs[i][4]][1] += b[5] if b[6] > 0 else 0
                else:
                    for f in funcs:  # not inside one function: does it cut some?
                        if not (be <= (f[0], f[1]) or bs >= (f[2], f[3])):
                            acc[f[4]][2] += 1
            per_run.append(acc)
        in_profile = bool(runs[0])
        for f in funcs:
            oid = f[4]
            t, c, p = per_run[0][oid]
            cov: dict
            if not in_profile:
                cov = {"state": UNVERIFIABLE, "reason": "FILE_NOT_IN_PROFILE"}
            elif p:
                cov = {"state": UNVERIFIABLE, "reason": "PARTIAL_BLOCK"}
            elif t == 0:
                cov = {"state": NOT_APPLICABLE, "reason": "NO_STATEMENTS"}
            elif len({r[oid][1] for r in per_run}) > 1:
                cov = {"state": UNVERIFIABLE, "reason": "NONDETERMINISTIC"}
            else:
                cov = {
                    "state": MEASURED,
                    "kind": "statement",
                    "total": t,
                    "covered": c,
                    "runs": len(per_run),
                    "determinism": (
                        "OBSERVED_STABLE"
                        if len(per_run) >= STABLE_MIN_RUNS
                        else "NOT_SHOWN"
                    ),
                }
            out[oid] = {"unit": unit, "coverage": cov}
    return out


def derive_rust_coverage(
    llvm: dict, demangled: dict[str, str], root: str | None, prefix: str = "/w/"
) -> dict:
    """The Rust region coverage of every object, re-derived from the llvm-cov export (the id contract and the
    test-region rule are shared; the aggregation is written again, per file first)."""
    out: dict[str, dict] = {}
    per_key: dict[str, list] = {}
    cfg: dict[str, list] = {}
    for fn in llvm["data"][0]["functions"]:
        rel = (
            fn["filenames"][0][len(prefix) :]
            if fn["filenames"][0].startswith(prefix)
            else fn["filenames"][0]
        )
        regs = [r for r in fn.get("regions", []) if len(r) >= 8 and r[7] == 0]
        first = min((r[0] for r in regs), default=0)
        parts = rel.split("/")
        test = "tests" in parts[:-1] or "benches" in parts[:-1]
        if not test and root and rel.endswith(".rs"):
            if rel not in cfg:
                p = Path(root) / rel
                cfg[rel] = (
                    cfg_test_spans(p.read_text(encoding="utf-8")) if p.is_file() else []
                )
            test = any(a <= first <= b for a, b in cfg[rel])
        if test:
            continue
        per_key.setdefault(rust_object_key(demangled[fn["name"]]), []).append(
            (rel, regs)
        )
    for key, insts in per_key.items():
        files = {i[0] for i in insts}
        total = sum(len(r) for _, r in insts)
        covered = sum(1 for _, rs in insts for r in rs if r[4] > 0)
        cov: dict
        if len(files) != 1:
            cov = {"state": UNVERIFIABLE, "reason": "AMBIGUOUS_MAPPING"}
        elif not total:
            cov = {"state": NOT_APPLICABLE, "reason": "NO_STATEMENTS"}
        else:
            cov = {
                "state": MEASURED,
                "kind": "region",
                "total": total,
                "covered": covered,
                "runs": 1,
                "determinism": "NOT_SHOWN",
            }
        out[key] = {"unit": "rust", "coverage": cov}
    return out


_FUNC_RE = re.compile(
    r"^func\s*(?:\(\s*(?:[A-Za-z_]\w*\s+)?\*?\s*([A-Za-z_]\w*)\s*(?:\[[^\]]*\])?\s*\)\s*)?([A-Za-z_]\w*)\s*(?:\[[^\]]*\])?\s*\("
)
_BUILD_RE = re.compile(r"^//go:build\s+(.+?)\s*$")


def independent_go_ids(
    root: str, module: str, module_dir: str, files: list[str]
) -> set[str]:
    """The object ids of Go source files, enumerated WITHOUT go/ast (so not by tools/objinventory): the column-0
    `func` lines of the source, with the same key format (object-id/1). `files` are repo-relative; `module_dir` is the
    directory of the go.mod ("." or "" for the root module)."""
    ids: set[str] = set()
    for f in files:
        rel = f if module_dir in ("", ".") else f[len(module_dir) + 1 :]
        build, in_header, inits = "", True, 0
        for line in (Path(root) / f).read_text(encoding="utf-8").splitlines():
            if in_header:
                m = _BUILD_RE.match(line)
                if m and not build:
                    build = m.group(1)
                if line.startswith("package "):
                    in_header = False
                continue
            m = _FUNC_RE.match(line)
            if not m:
                continue
            recv, name = m.group(1), m.group(2)
            fn: dict = {
                "name": name,
                "kind": "method" if recv else "func",
                "recv": recv or "",
            }
            if name == "init" and not recv:
                inits += 1
                fn.update(kind="init", ord=inits)
            ids.add(go_object_id(module, rel, fn, build))
    return ids


def independent_rust_free_functions(text: str, crate: str) -> tuple[set[str], int]:
    """An independent enumeration of the FREE functions of one Rust source file (no llvm-cov, no cargo-mutants):
    `fn` items at nesting depth 0 outside the #[cfg(test)] items. Returns (object ids, unmapped).

    `unmapped` counts what this enumeration cannot name: `impl` blocks, `trait` blocks and inline `mod` blocks (their
    functions carry a type or module path it does not derive) and every nested `fn` (llvm-cov lists it separately). A non-zero `unmapped` means the file is NOT proven
    complete: the caller must keep the Rust verdict PARTIAL. (The comment/string/char scanner and the test-region rule
    are shared with the object builder; that sharing is a stated limit of the independence.)
    """
    spans = cfg_test_spans(text)
    code_lines: dict[int, list[str]] = {}
    for _i, c, ln in _scan(text):
        code_lines.setdefault(ln, []).append(c)
    ids: set[str] = set()
    unmapped = 0
    depth = 0
    fn_re = re.compile(
        r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:const\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+\"[^\"]*\"\s+)?fn\s+([A-Za-z_]\w*)"
    )
    blk_re = re.compile(
        r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?(impl|trait|mod)\b"
    )
    for ln in sorted(code_lines):
        line = "".join(code_lines[ln])
        in_test = any(a <= ln <= b for a, b in spans)
        if not in_test:
            m = fn_re.match(line)
            n_fn = len(re.findall(r"\bfn\s+[A-Za-z_]", line))
            if m and depth == 0:
                ids.add(f"rust:{crate}::{m.group(1)}")
                unmapped += (
                    n_fn - 1
                )  # further `fn` items on the same line are nested: llvm-cov lists them, this scan cannot name them
            elif n_fn:  # a nested fn (inside a function, an impl, a trait or a mod)
                unmapped += n_fn
            elif depth == 0 and blk_re.match(line) and "{" in line:
                unmapped += 1
        depth += line.count("{") - line.count("}")
    return ids, unmapped


def go_func_lines(root: str, files: list[str]) -> int:
    """An independent count of top-level Go declarations: lines that start with `func ` in column 0."""
    n = 0
    for f in files:
        for line in (Path(root) / f).read_text(encoding="utf-8").splitlines():
            if line.startswith("func "):
                n += 1
    return n


def _diff(have: set[str], want: set[str]) -> str:
    """Describe how two id sets differ - the count and first few missing and unexpected
    ids.
    """
    miss, extra = sorted(want - have), sorted(have - want)
    return f"{len(miss)} missing {miss[:3]}, {len(extra)} unexpected {extra[:3]}"


def verify_l3(
    rec: dict,
    root: str | None,
    go_files: dict[str, list[str]] | None,
    mutants: list[dict] | None,
    go_independent: dict[str, set[str]] | None = None,
    rust_independent: set[str] | None = None,
    required_units: set[str] | None = None,
) -> tuple[str, list[str], list[str]]:
    """Completeness against an independent enumeration: (COMPLETE_FOR_SCOPE | PARTIAL | FAIL, proven, unproven/findings).

    COMPLETE_FOR_SCOPE only if EVERY unit is proven by SET identity (not by a count). Whatever cannot be proven is
    listed and makes the verdict PARTIAL: PARTIAL is never renamed to a pass."""
    proven: list[str] = []
    unproven: list[str] = []
    bad: list[str] = []
    objs = rec["objects"]
    for unit in sorted(required_units or ()):
        if not any(o["unit"] == unit for o in objs):
            bad.append(
                code(
                    "MISSING_UNIT",
                    f"the scope requires unit {unit!r} but the record has no object of it",
                )
            )
    if go_independent:
        for unit, want in sorted(go_independent.items()):
            have = {o["id"] for o in objs if o["lang"] == "go" and o["unit"] == unit}
            if have != want:
                bad.append(
                    code("SET_MISMATCH", f"go unit {unit!r}: {_diff(have, want)}")
                )
            else:
                proven.append(
                    f"go unit {unit!r}: the {len(have)} object ids are IDENTICAL to the independent enumeration (a set, not a count)"
                )
    elif root is not None and go_files:
        for unit, files in sorted(go_files.items()):
            have_n = sum(1 for o in objs if o["lang"] == "go" and o["unit"] == unit)
            want_n = go_func_lines(root, files)
            if have_n != want_n:
                bad.append(
                    code(
                        "COUNT_MISMATCH",
                        f"go unit {unit!r}: {have_n} objects but {want_n} top-level `func` lines in the source",
                    )
                )
            else:
                proven.append(
                    f"go unit {unit!r}: {have_n} objects equal the independent `func` line count"
                )
                unproven.append(
                    f"go unit {unit!r}: only the COUNT was compared (no independent id set): a swap would not be seen"
                )
    else:
        unproven.append(
            "go: no source tree was given, the object list was not compared with an independent enumeration"
        )
    rust = {o["id"] for o in objs if o["lang"] == "rust"}
    if rust_independent is not None:
        if rust != rust_independent:
            bad.append(code("SET_MISMATCH", f"rust: {_diff(rust, rust_independent)}"))
        else:
            proven.append(
                f"rust: the {len(rust)} object ids are IDENTICAL to the independent enumeration (a set, not a count)"
            )
    else:
        unproven.append(
            "rust: no independent object inventory was given, so a function with neither a mutant nor a coverage entry cannot be enumerated independently"
        )
    if mutants is not None:
        names = {(o["file"], o["name"]) for o in objs if o["lang"] == "rust"}
        miss = sorted(
            {
                (m["file"], m["function"]["function_name"].split("::")[-1])
                for m in mutants
                if m.get("function")
            }
            - names
        )
        if miss:
            bad.append(
                code(
                    "MUTANT_FUNCTION_MISSING",
                    f"rust: {len(miss)} function(s) known to cargo-mutants have no object, e.g. {miss[0]}",
                )
            )
        else:
            proven.append("rust: every function that cargo-mutants lists has an object")
    if bad:
        return "FAIL", proven, bad
    return ("PARTIAL" if unproven else "COMPLETE_FOR_SCOPE"), proven, unproven


def verify_record(
    rec: dict,
    root=None,
    inputs=None,
    go_files=None,
    mutants=None,
    derived=None,
    go_independent=None,
    rust_independent=None,
    required_units=None,
    tool_pins=None,
    quality=False,
) -> dict:
    """All three levels, each on its own. There is deliberately no combined verdict."""
    l1 = verify_l1(rec)
    res = {"L1": {"verdict": "FAIL" if l1 else "INTEGRITY_OK", "findings": l1}}
    res["tools"] = verify_tools(rec, tool_pins, quality)
    if l1:  # the later levels read the record: do not trust a record that is not intact
        res["L2"] = {
            "verdict": "NOT_CHECKED",
            "findings": ["the record is not intact (L1)"],
        }
        res["L3"] = {
            "verdict": "NOT_CHECKED",
            "proven": [],
            "unproven": ["the record is not intact (L1)"],
        }
        return res
    v2, f2 = verify_l2(rec, root, inputs or {}, derived)
    if v2 == "PASS" and derived is None:
        # the digests are right, but nothing re-derived the VALUES: that is only part of the binding
        res["L2"] = {
            "verdict": "PARTIAL",
            "findings": [],
            "unproven": [
                "the measured values were not re-derived from the input files"
            ],
        }
    else:
        res["L2"] = {"verdict": {"PASS": "BOUND_TO_INPUTS"}.get(v2, v2), "findings": f2}
    v3, proven, other = verify_l3(
        rec, root, go_files, mutants, go_independent, rust_independent, required_units
    )
    res["L3"] = {
        "verdict": v3,
        "proven": proven,
        "unproven" if v3 != "FAIL" else "findings": other,
    }
    res["stability"] = stability(rec)
    return res


# --------------------------------------------------------------------------- CLI


def _load(path: str):
    """Read a JSON file; ModelError if it is missing, unreadable or not JSON."""
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        raise ModelError(f"{path}: missing, unreadable or not JSON: {e}") from e


def demangle(symbols: list[str]) -> tuple[dict[str, str], str]:
    """Demangle Rust v0 symbols with binutils c++filt (in the pinned image); returns (map, version)."""
    try:
        ver = subprocess.run(
            ["c++filt", "--version"], capture_output=True, text=True, check=True
        ).stdout.splitlines()[0]
        out = subprocess.run(
            ["c++filt"],
            input="\n".join(symbols) + "\n",
            capture_output=True,
            text=True,
            check=True,
        ).stdout
    except (OSError, subprocess.CalledProcessError) as e:
        raise ModelError(f"c++filt is not available: {e}") from e
    names = out.splitlines()
    if len(names) != len(symbols):
        raise ModelError("c++filt returned a different number of lines")
    return dict(zip(symbols, names)), ver


def _git(root: str, *args: str) -> str:
    """Run git in root and return its stripped stdout; ModelError if it fails."""
    try:
        return subprocess.run(
            ["git", "-C", root, *args], capture_output=True, text=True, check=True
        ).stdout.strip()
    except (OSError, subprocess.CalledProcessError) as e:
        raise ModelError(f"git {' '.join(args)} failed in {root}: {e}") from e


def cmd_build(a: argparse.Namespace) -> int:
    """Build the evidence record from the real tool outputs (report-only)."""
    scope = _load(a.scope)
    check_scope(scope)
    tracked = _git(a.root, "ls-files").splitlines()
    inputs: dict[str, dict] = {}

    def note(name: str, path: str) -> None:
        digest, size = sha256_file(path)
        inputs[name] = {"sha256": digest, "bytes": size}

    note("scope", a.scope)
    objects: list[dict] = []
    attribution: dict = {}
    # every tracked Go/Rust file must be classified: an unclassified file is an error (N8)
    scope_files(scope, "go", tracked)
    scope_files(scope, "rust", tracked)
    for spec in a.go_unit or []:
        name, mod_dir, inv, cover, *opt = spec.split(",")
        module = go_module_path(
            Path(a.root, mod_dir, "go.mod").read_text(encoding="utf-8")
        )
        blocks = parse_coverprofile(Path(cover).read_text(encoding="utf-8"))
        reps = [
            parse_coverprofile(Path(r.split("=", 1)[1]).read_text(encoding="utf-8"))
            for r in (a.go_repeat or [])
            if r.split("=", 1)[0] == name
        ]
        objs, st = go_objects(
            name,
            module,
            _load(inv),
            blocks,
            repeats=reps or None,
            detail="detail" in opt,
        )
        objects += objs
        attribution[f"go:{name}"] = st
        note(f"go-inventory:{name}", inv)
        note(f"go-coverprofile:{name}", cover)
        for i, r in enumerate(
            (r for r in (a.go_repeat or []) if r.split("=", 1)[0] == name), 1
        ):
            note(f"go-coverprofile-repeat:{name}:{i}", r.split("=", 1)[1])
    if a.llvm_cov:
        llvm = _load(a.llvm_cov)
        syms = sorted({f["name"] for f in llvm["data"][0]["functions"]})
        names, cxxfilt = demangle(syms)
        mutants = _load(a.rust_list) if a.rust_list else None
        verdicts: dict[str, str] | None = None
        if a.rust_outcomes:
            verdicts = {}
            for path in a.rust_outcomes:
                for k, v in qg.rust_verdicts(_load(path), path).items():
                    if k in verdicts:
                        raise ModelError(
                            f"mutant {k!r} has a verdict in two outcome files"
                        )
                    verdicts[k] = v
                note(f"rust-outcomes:{Path(path).parent.parent.name}", path)
        objs, st = rust_objects(
            llvm, names, a.root, mutants, verdicts, prefix=a.rust_prefix
        )
        objects += objs
        attribution["rust"] = st
        note("llvm-cov-json", a.llvm_cov)
        if a.rust_list:
            note("rust-mutant-list", a.rust_list)
        a.tool = (a.tool or []) + [f"c++filt={cxxfilt}"]
    tools = dict(kv.split("=", 1) for kv in a.tool or [])
    rec = build_record(
        objects,
        _git(a.root, "rev-parse", "HEAD"),
        _git(a.root, "rev-parse", "HEAD^{tree}"),
        tools,
        inputs,
        scope,
        attribution,
    )
    # the commit and tree name the committed state; uncommitted paths are counted, not hidden
    rec["identity"]["uncommitted_paths"] = len(
        _git(a.root, "status", "--porcelain").splitlines()
    )
    rec["digest"] = canonical_digest({k: v for k, v in rec.items() if k != "digest"})
    Path(a.out).write_text(
        json.dumps(rec, indent=1, sort_keys=True) + "\n", encoding="utf-8"
    )
    sm = rec["summary"]
    print(f"{EVIDENCE_SCHEMA}: {sm['objects']} objects, digest {rec['digest'][:16]}")
    for unit, u in sm["by_unit"].items():
        print(
            f"  unit {unit}: {u['objects']} objects, coverage {u['fields']['coverage']}"
        )
    return 0


def cmd_scope_list(a: argparse.Namespace) -> int:
    """The tracked files of one scope class, one per line (what the inventory tool is run on)."""
    scope = _load(a.scope)
    check_scope(scope)
    tracked = _git(a.root, "ls-files").splitlines()
    for f in sorted(scope_files(scope, a.lang, tracked).get(a.cls, [])):
        print(f)
    return 0


def _crate_name(root: str, rel: str) -> str:
    """The crate a Rust file belongs to: the [package] name of the nearest Cargo.toml above it ('-' becomes '_')."""
    d = Path(root, rel).parent
    while True:
        m = d / "Cargo.toml"
        if m.is_file():
            mt = re.search(
                r'^\s*name\s*=\s*"([^"]+)"', m.read_text(encoding="utf-8"), re.M
            )
            if mt and "[package]" in m.read_text(encoding="utf-8"):
                return mt.group(1).replace("-", "_")
        if d == Path(root) or d == d.parent:
            raise ModelError(f"no Cargo.toml above {rel}")
        d = d.parent


def build_context(rec: dict, root: str, scope: dict, inputs: dict[str, str]) -> dict:
    """Everything the verifier needs besides the record, DERIVED FROM THE CHECKOUT AND THE INPUT FILES:
    the independent Go object sets, the re-derived coverage values, the independent Rust set (only when it is
    complete), the units the scope requires. Returns the keyword arguments of verify_record.
    """
    tracked = _git(root, "ls-files").splitlines()
    go_groups = scope_files(scope, "go", tracked)
    rust_groups = scope_files(scope, "rust", tracked)
    units: dict[str, tuple[str, list[str]]] = {
        "root": (".", go_groups.get("production", []))
    }
    for u, fs in go_groups.items():
        if u.startswith("separate:"):
            units[u.split(":", 1)[1]] = (
                os.path.commonpath([os.path.dirname(f) for f in fs]) if fs else ".",
                fs,
            )
    go_independent: dict[str, set[str]] = {}
    derived: dict[str, dict] = {}
    for unit, (mod_dir, files) in units.items():
        if not files:
            continue
        module = go_module_path(
            Path(root, mod_dir, "go.mod").read_text(encoding="utf-8")
        )
        go_independent[unit] = independent_go_ids(root, module, mod_dir, files)
        inv, prof = inputs.get(f"go-inventory:{unit}"), inputs.get(
            f"go-coverprofile:{unit}"
        )
        if inv and prof:
            reps = [
                parse_coverprofile(Path(path).read_text(encoding="utf-8"))
                for name, path in sorted(inputs.items())
                if name.startswith(f"go-coverprofile-repeat:{unit}:")
            ]
            derived.update(
                derive_go_coverage(
                    unit,
                    module,
                    _load(inv),
                    parse_coverprofile(Path(prof).read_text(encoding="utf-8")),
                    repeats=reps or None,
                )
            )
    rust_files = rust_groups.get("production", [])
    rust_independent: set[str] | None = None
    if inputs.get("llvm-cov-json"):
        llvm = _load(inputs["llvm-cov-json"])
        names, _ = demangle(sorted({f["name"] for f in llvm["data"][0]["functions"]}))
        derived.update(
            derive_rust_coverage(llvm, names, root, prefix=str(Path(root)) + "/")
        )
    if rust_files:
        ids: set[str] = set()
        unmapped = 0
        for f in rust_files:
            found, cannot_name = independent_rust_free_functions(
                Path(root, f).read_text(encoding="utf-8"), _crate_name(root, f)
            )
            ids |= found
            unmapped += cannot_name
        rust_independent = (
            ids if unmapped == 0 else None
        )  # an impl/trait/mod/nested fn cannot be named: NOT proven
    required = {u for u, (_, fs) in units.items() if fs} | (
        {"rust"} if rust_files else set()
    )
    return {
        "go_independent": go_independent or None,
        "derived": derived or None,
        "rust_independent": rust_independent,
        "required_units": required,
    }


def cmd_verify(a: argparse.Namespace) -> int:
    """verify: check an evidence record at every level (L1 to L3, plus the tool pins when
    given) and print the verdicts; returns 1 if any level fails.
    """
    rec = _load(a.record)
    inputs = dict(kv.split("=", 1) for kv in a.input or [])
    mutants = _load(a.rust_list) if a.rust_list else None
    ctx: dict = {}
    if a.root and a.scope:
        scope = _load(a.scope)
        check_scope(scope)
        ctx = build_context(rec, a.root, scope, inputs)
    pins = load_tool_pins(a.tool_pins) if a.tool_pins else None
    res = verify_record(
        rec,
        a.root,
        inputs,
        None,
        mutants,
        tool_pins=pins,
        quality=a.mode == "quality",
        **ctx,
    )
    for lvl in ("L1", "L2", "L3"):
        r = res[lvl]
        print(f"{lvl} {r['verdict']}")
        for k in ("findings", "proven", "unproven"):
            for line in r.get(k, []):
                print(f"   {k}: {line}")
    st = res.get("stability")
    if st:
        print(
            f"stability {st['verdict']}: {st['observed_stable']}/{st['measured']} measured coverages seen identical in >= "
            f"{STABLE_MIN_RUNS} runs, {st['not_shown']} not shown, nondeterministic objects: {len(st['nondeterministic_objects'])}"
        )
    tl = res.get("tools")
    if tl:
        print(f"tools {tl['verdict']}")
        for line in tl["findings"]:
            print(f"   findings: {line}")
    print(
        "(no combined 'verified': a digest does not prove where a measurement came from)"
    )
    failed = any(res[lvl]["verdict"] == "FAIL" for lvl in ("L1", "L2", "L3")) or (
        tl is not None and tl["verdict"] in ("UNUSABLE", "UNVERIFIABLE")
    )
    return 1 if failed else 0


def main(argv: list[str] | None = None) -> int:
    """Run the build or verify subcommand; return its exit status."""
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    sub = ap.add_subparsers(dest="command", required=True)
    p = sub.add_parser("build")
    p.add_argument("--root", required=True, help="checkout of the measured tree")
    p.add_argument("--scope", required=True)
    p.add_argument("--out", required=True)
    p.add_argument(
        "--go-unit",
        action="append",
        help="NAME,MODULE_DIR,INVENTORY.json,COVERPROFILE[,detail]",
    )
    p.add_argument(
        "--go-repeat",
        action="append",
        help="UNIT=COVERPROFILE of a repeated measurement",
    )
    p.add_argument("--llvm-cov", help="cargo llvm-cov --json export")
    p.add_argument("--rust-list", help="cargo mutants --list --json")
    p.add_argument(
        "--rust-outcomes",
        action="append",
        help="outcomes.json of a (shard) run, repeatable",
    )
    p.add_argument(
        "--rust-prefix",
        default="/w/",
        help="the checkout path prefix of the file names in the llvm-cov export",
    )
    p.add_argument(
        "--tool", action="append", help="NAME=VERSION of a measuring tool, repeatable"
    )
    p.set_defaults(fn=cmd_build)

    p = sub.add_parser("scope-list")
    p.add_argument("--root", required=True)
    p.add_argument("--scope", required=True)
    p.add_argument("--lang", required=True, choices=["go", "rust"])
    p.add_argument(
        "--class",
        dest="cls",
        required=True,
        help="production | separate:<unit> | test | ...",
    )
    p.set_defaults(fn=cmd_scope_list)

    p = sub.add_parser("verify")
    p.add_argument("--record", required=True)
    p.add_argument(
        "--root", help="checkout of the measured tree (L2 identity, L3 source count)"
    )
    p.add_argument(
        "--scope", help="the measurement scope (needed with --root for the L3 Go count)"
    )
    p.add_argument(
        "--input", action="append", help="NAME=PATH of an input file (L2 digest)"
    )
    p.add_argument("--rust-list", help="cargo mutants --list --json output (L3, Rust)")
    p.add_argument(
        "--mode",
        required=True,
        choices=["quality", "diagnostic"],
        help="quality: the result decides a quality verdict, so the tool pins are REQUIRED (missing = UNVERIFIABLE, exit 1); "
        "diagnostic: reading a record, the tool pins are optional (NOT_CHECKED is not a pass)",
    )
    p.add_argument(
        "--tool-pins",
        help=".measurement-tools.json: the pinned tool versions (fail closed on a different, missing or unpinned tool)",
    )
    p.set_defaults(fn=cmd_verify)
    a = ap.parse_args(argv)
    try:
        return a.fn(a)
    except (ModelError, qg.GateError) as e:
        print(f"::error::{e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
