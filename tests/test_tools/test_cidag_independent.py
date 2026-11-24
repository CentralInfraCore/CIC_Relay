# SPDX-License-Identifier: AGPL-3.0-or-later
"""Tests of the independent implementation of the CI Merkle DAG contract, on the vectors shared with pkg/cidag.

The vectors are the point of agreement between the two implementations: this file proves the Python side meets
them, and proves that the vector set CAN FAIL: each planted defect in the independent implementation must be
caught by at least one vector (the seeded pattern of cic-primitive-forms tools/test-evidence/seeded.py).
"""

import copy
import hashlib
import json
import os
import shutil
import subprocess
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import cidag_independent as ci  # type: ignore[import-not-found]  # noqa: E402

VECTORS = os.path.join(HERE, "..", "..", "pkg", "cidag", "testdata", "vectors.json")


@pytest.fixture(scope="module")
def vectors():
    with open(VECTORS, encoding="utf-8") as f:
        return ci.parse_strict(f.read())


def apply_ops(bundle, ops):
    b = copy.deepcopy(bundle)
    for op in ops:
        tgt = b
        for p in op["path"][:-1]:
            tgt = tgt[p]
        last = op["path"][-1]
        if op.get("delete"):
            del tgt[last]
        else:
            tgt[last] = copy.deepcopy(op["set"])
    return b


def kind_of(fn, *args):
    try:
        fn(*args)
    except ci.CidagError as exc:
        return exc.kind
    return None


def run_vectors(mod, v):
    """Every vector against `mod`; returns the list of vectors it fails (empty for a correct implementation)."""
    failed = []
    for c in v["canon"]:
        try:
            if mod.canonical(c["value"]).decode("ascii") != c["canonical"]:
                failed.append("canon: " + c["name"])
        except (
            Exception
        ) as exc:  # a defective implementation may crash: that is a failure of the vector too
            failed.append("canon: %s (%s)" % (c["name"], type(exc).__name__))
    for p in v["positive"]:
        try:
            got = mod.build(p["template"], p["evidence"])
            exp = p["expect"]
            if (
                {i: mod.canonical(o).decode("ascii") for i, o in got["objects"].items()}
                != exp["canonical_objects"]
                or got["step_roots"] != exp["step_roots"]
                or got["template_digest"] != exp["template_digest"]
                or got["ci_root"] != exp["ci_root"]
            ):
                failed.append("positive: " + p["name"])
        except Exception as exc:
            failed.append("positive: %s (%s)" % (p["name"], type(exc).__name__))
    for n in v["negative_build"]:
        if kind_of(mod.build, n["template"], n["evidence"]) != n["error"]:
            failed.append("negative_build: " + n["name"])
    base = v["verify_base"]["bundle"]
    if kind_of(mod.verify, base) is not None:
        failed.append("verify_base")
    for n in v["negative_verify"]:
        try:
            if kind_of(mod.verify, apply_ops(base, n["ops"])) != n["error"]:
                failed.append("negative_verify: " + n["name"])
        except Exception as exc:
            failed.append("negative_verify: %s (%s)" % (n["name"], type(exc).__name__))
    for n in v["parse"]:
        if kind_of(mod.parse_strict, n["text"]) != n["error"]:
            failed.append("parse: " + n["name"])
    for n in v["parse_ok"]:
        if mod.canonical(mod.parse_strict(n["text"])).decode("ascii") != n["canonical"]:
            failed.append("parse_ok: " + n["name"])
    return failed


def test_the_independent_implementation_meets_every_vector(vectors):
    assert run_vectors(ci, vectors) == []


def test_vector_set_is_not_trivial(vectors):
    assert (
        len(vectors["positive"]) >= 6
        and len(vectors["negative_build"]) >= 40
        and len(vectors["negative_verify"]) >= 25
    )
    assert len({n["error"] for n in vectors["negative_build"]}) >= 14
    assert len({n["error"] for n in vectors["negative_verify"]}) >= 9


def test_canonical_form_equals_python_json_on_the_profile(vectors):
    """On the profile the bytes equal json.dumps(sort_keys=True, separators=(",", ":")) (the release compiler's form)."""
    for c in vectors["canon"]:
        assert (
            ci.canonical(c["value"])
            == json.dumps(c["value"], sort_keys=True, separators=(",", ":")).encode()
        )
    for p in vectors["positive"]:
        for o in ci.build(p["template"], p["evidence"])["objects"].values():
            assert (
                ci.canonical(o)
                == json.dumps(o, sort_keys=True, separators=(",", ":")).encode()
            )


@pytest.mark.skipif(
    shutil.which("sha256sum") is None, reason="needs coreutils sha256sum"
)
def test_worked_example_is_reproducible_with_the_shell(vectors):
    """The `single` case, hashed by coreutils from the canonical text: the oracle is not checking itself."""
    p = next(x for x in vectors["positive"] if x["name"] == "single")
    canon = p["expect"]["canonical_objects"]["build"]
    out = (
        subprocess.run(
            ["sha256sum"],
            input=b"CICSTEPv1\x00" + canon.encode(),
            capture_output=True,
            check=True,
        )
        .stdout.split()[0]
        .decode()
    )
    assert "sha256:" + out == p["expect"]["step_roots"]["build"]
    assert canon.startswith(
        '{"contract":"cic-ci-step/1","dependencies":[],"inputs":{"source":"sha256:'
    )


def test_order_independence(vectors):
    for p in vectors["positive"]:
        t = copy.deepcopy(p["template"])
        t["steps"].reverse()
        for s in t["steps"]:
            s["needs"].reverse()
        assert ci.build(t, p["evidence"])["ci_root"] == p["expect"]["ci_root"]
        assert (
            ci.build(p["template"], dict(reversed(list(p["evidence"].items()))))[
                "ci_root"
            ]
            == p["expect"]["ci_root"]
        )


def test_domain_separation():
    v = {"a": "b"}
    assert (
        len(
            {
                ci.digest(ci.STEP_PREFIX, v),
                ci.digest(ci.TEMPLATE_PREFIX, v),
                ci.digest(ci.ROOT_PREFIX, v),
            }
        )
        == 3
    )


def test_depth_limit_is_exact():
    assert ci.parse_strict("[" * 64 + "]" * 64) is not None
    with pytest.raises(ci.CidagError):
        ci.parse_strict("[" * 65 + "]" * 65)


def test_profile_refusals():
    for bad in (1.5, "a<b", "a&b", 'a"b', "a\\b", "é", "a\tb", 2**53, object(), {1: 2}):
        with pytest.raises(ci.CidagError):
            ci.canonical(bad)
    assert (
        ci.canonical({"b": [1, True, None], "a": "x y"})
        == b'{"a":"x y","b":[1,true,null]}'
    )


# ---------------------------------------------------------------------------------------- seeded defects
# The ORIGINAL functions, taken before any patch: a planted defect calls these, never the patched module names.
ORIG = {
    n: getattr(ci, n)
    for n in ("build", "verify", "digest", "_ser", "check_evidence", "check_template")
}


def _no_domain(prefix, value):
    return "sha256:" + hashlib.sha256(ci.canonical(value)).hexdigest()


def _reversed_keys_ser(v):
    if isinstance(v, dict):
        return (
            "{"
            + ",".join(
                ORIG["_ser"](k) + ":" + _reversed_keys_ser(v[k])
                for k in reversed(list(v))
            )
            + "}"
        )
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(_reversed_keys_ser(x) for x in v) + "]"
    return ORIG["_ser"](v)


def _build_ignoring_needs(template, evidence):
    t = copy.deepcopy(template)
    return ORIG["build"](
        dict(t, steps=[dict(s, needs=[]) for s in t["steps"]]), evidence
    )


def _verify_skipping(*skip):
    """A verifier that is the real one except that it does not compare the named parts (planted defect)."""

    def verify(bundle):
        if not isinstance(bundle, dict) or set(bundle) != {
            "ci_root",
            "objects",
            "step_roots",
            "template",
        }:
            raise ci.CidagError("BUNDLE_SHAPE")
        byid = ORIG["check_template"](bundle["template"])
        objs = bundle["objects"]
        evidence = {}
        for i in sorted(objs):
            o = objs[i]
            if not isinstance(o, dict) or set(o) != {
                "contract",
                "dependencies",
                "inputs",
                "result",
                "step",
            }:
                raise ci.CidagError("OBJECT_SHAPE")
            evidence[i] = {"inputs": o["inputs"], "result": o["result"]}
        built = ORIG["build"](bundle["template"], evidence)
        for i in ci.topo_order(byid):
            got, want = objs[i], built["objects"][i]
            if "contract" not in skip and got["contract"] != want["contract"]:
                raise ci.CidagError("OBJECT_CONTRACT_MISMATCH")
            if "step" not in skip and got["step"] != want["step"]:
                raise ci.CidagError("OBJECT_STEP_MISMATCH")
            if (
                "dependencies" not in skip
                and got["dependencies"] != want["dependencies"]
            ):
                raise ci.CidagError("OBJECT_DEPENDENCIES_MISMATCH")
        if "step_roots" not in skip and bundle["step_roots"] != built["step_roots"]:
            raise ci.CidagError("STEP_ROOT_MISMATCH")
        if "ci_root" not in skip and bundle["ci_root"] != built["ci_root"]:
            raise ci.CidagError("CI_ROOT_MISMATCH")
        return built

    return verify


class _Anything:
    def __contains__(self, _):
        return True


def _evidence_ignoring_extra(byid, evidence):
    return ORIG["check_evidence"](
        byid, {i: e for i, e in evidence.items() if i in byid}
    )


SEEDED = {
    "hash without a domain prefix": {"digest": _no_domain},
    "object keys written in reverse order": {"_ser": _reversed_keys_ser},
    "dependencies ignored when building": {"build": _build_ignoring_needs},
    "published dependencies not compared": {"verify": _verify_skipping("dependencies")},
    "published step roots not compared": {"verify": _verify_skipping("step_roots")},
    "published CI root not compared": {"verify": _verify_skipping("ci_root")},
    "published step identity not compared": {"verify": _verify_skipping("step")},
    "published contract not compared": {"verify": _verify_skipping("contract")},
    "every status accepted": {"STATUSES": _Anything()},
    "evidence for an unknown step ignored": {
        "check_evidence": _evidence_ignoring_extra
    },
}


@pytest.mark.parametrize("name", sorted(SEEDED))
def test_the_vectors_catch_a_planted_defect(vectors, name):
    patches = SEEDED[name]
    saved = {k: getattr(ci, k) for k in patches}
    try:
        for k, fn in patches.items():
            setattr(ci, k, fn)
        failed = run_vectors(ci, vectors)
    finally:
        for k, fn in saved.items():
            setattr(ci, k, fn)
    assert failed, "the planted defect %r survived every vector" % name
    assert (
        run_vectors(ci, vectors) == []
    )  # the module is restored and still meets every vector
