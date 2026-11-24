# SPDX-License-Identifier: AGPL-3.0-or-later
"""Writes pkg/cidag/testdata/vectors.json: the vectors both implementations of the CI Merkle DAG contract read.

The expected values come from the independent Python implementation (cidag_independent.py), the ORACLE, not
from the Go package: the Go package is measured against these vectors, never the other way round. One case is
also fixed by hand (docs/hu/CI-MERKLE-DAG-CONTRACT.md, worked example) so that the oracle itself is checked
with `printf | sha256sum`.

Run:  python3 tests/test_tools/cidag_make_vectors.py [OUTPUT]
"""

import copy
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cidag_independent as ci  # type: ignore[import-not-found]  # noqa: E402


def d(label):
    """A realistic digest made from a short label."""
    return "sha256:" + hashlib.sha256(label.encode()).hexdigest()


def step(i, label, needs=()):
    return {"id": i, "definition_digest": d("def:" + label), "needs": list(needs)}


def ev(status="success", result=None, inputs=None):
    if result is None and status not in ci.NULL_DIGEST_STATUSES:
        result = d("result")
    return {"inputs": inputs or {}, "result": {"digest": result, "status": status}}


POSITIVE = {}

POSITIVE["single"] = (
    {"workflow": "ci", "steps": [step("build", "build")]},
    {
        "build": {
            "inputs": {"source": d("src")},
            "result": {"digest": d("out"), "status": "success"},
        }
    },
)

POSITIVE["linear"] = (
    {
        "workflow": "ci",
        "steps": [
            step("build", "build"),
            step("test", "test", ["build"]),
            step("release", "release", ["test"]),
        ],
    },
    {
        "build": {
            "inputs": {"source": d("src"), "compiler": d("go1.26.11")},
            "result": {"digest": d("bin"), "status": "success"},
        },
        "test": {
            "inputs": {
                "tests": d("tests"),
                "retries": 2,
                "gate_enabled": True,
                "mode": "full",
            },
            "result": {"digest": d("verdict"), "status": "success"},
        },
        "release": {
            "inputs": {"channel": "stable"},
            "result": {"digest": d("rel"), "status": "success"},
        },
    },
)

POSITIVE["diamond_parallel"] = (
    {
        "workflow": "ci",
        "steps": [
            step("join", "join", ["lint", "unit"]),
            step("unit", "unit", ["setup"]),
            step("lint", "lint", ["setup"]),
            step("setup", "setup"),
        ],
    },
    {
        "setup": {
            "inputs": {"source": d("src")},
            "result": {"digest": d("s"), "status": "success"},
        },
        "unit": {
            "inputs": {"tests": d("u")},
            "result": {"digest": d("u-out"), "status": "success"},
        },
        "lint": {
            "inputs": {"rules": d("l")},
            "result": {"digest": d("l-out"), "status": "success"},
        },
        "join": {"inputs": {}, "result": {"digest": d("j"), "status": "success"}},
    },
)

POSITIVE["failed_skipped_cancelled"] = (
    {
        "workflow": "ci",
        "steps": [
            step("build", "build"),
            step("test", "test", ["build"]),
            step("lint", "lint", ["build"]),
            step("deploy", "deploy", ["test", "lint"]),
            step("notify", "notify", ["deploy"]),
        ],
    },
    {
        "build": {
            "inputs": {"source": d("src")},
            "result": {"digest": d("bin"), "status": "success"},
        },
        "test": {
            "inputs": {"tests": d("tests")},
            "result": {"digest": d("fail-log"), "status": "failure"},
        },
        "lint": {"inputs": {}, "result": {"digest": None, "status": "cancelled"}},
        "deploy": {"inputs": {}, "result": {"digest": None, "status": "skipped"}},
        "notify": {
            "inputs": {"always": True},
            "result": {"digest": d("n"), "status": "neutral"},
        },
    },
)

POSITIVE["dynamic_branch"] = (
    {
        "workflow": "ci",
        "steps": [
            step("decide", "decide"),
            step("branch_a", "a", ["decide"]),
            step("branch_b", "b", ["decide"]),
            step("done", "done", ["branch_a", "branch_b"]),
        ],
    },
    {
        "decide": {
            "inputs": {"policy": d("policy"), "change_set": d("changes")},
            "result": {"digest": d("chose-a"), "status": "success"},
        },
        "branch_a": {
            "inputs": {},
            "result": {"digest": d("a-out"), "status": "success"},
        },
        "branch_b": {"inputs": {}, "result": {"digest": None, "status": "skipped"}},
        "done": {"inputs": {}, "result": {"digest": d("d"), "status": "success"}},
    },
)

POSITIVE["empty_inputs_all_statuses"] = (
    {
        "workflow": "wf/main@1",
        "steps": [
            step("s1", "s1"),
            step("s2", "s2"),
            step("s3", "s3"),
            step("s4", "s4"),
        ],
    },
    {
        "s1": {"inputs": {}, "result": {"digest": d("1"), "status": "timed_out"}},
        "s2": {"inputs": {}, "result": {"digest": d("2"), "status": "action_required"}},
        "s3": {"inputs": {}, "result": {"digest": d("3"), "status": "stale"}},
        "s4": {
            "inputs": {"n": -9007199254740991, "m": 9007199254740991},
            "result": {"digest": d("4"), "status": "success"},
        },
    },
)

CANON = [
    ("empty object", {}),
    ("empty list", []),
    ("null", None),
    ("true", True),
    ("false", False),
    ("zero", 0),
    ("negative", -17),
    ("max int", 9007199254740991),
    ("string with space", "a b c"),
    ("slash is not escaped", "a/b"),
    ("nested keys sorted", {"b": 1, "a": {"d": [], "c": None}}),
    ("list keeps order", [3, 1, 2]),
    ("digest string", d("x")),
]


def build_vectors():
    out = {
        "version": 1,
        "profile": "CI step profile v1",
        "canon": [],
        "positive": [],
        "negative_build": [],
        "negative_verify": [],
        "parse": [],
    }
    for name, value in CANON:
        out["canon"].append(
            {
                "name": name,
                "value": value,
                "canonical": ci.canonical(value).decode("ascii"),
            }
        )
    for name, (t, e) in POSITIVE.items():
        b = ci.build(t, e)
        out["positive"].append(
            {
                "name": name,
                "template": t,
                "evidence": e,
                "expect": {
                    "canonical_objects": {
                        i: ci.canonical(o).decode("ascii")
                        for i, o in b["objects"].items()
                    },
                    "step_roots": b["step_roots"],
                    "template_digest": b["template_digest"],
                    "ci_root": b["ci_root"],
                },
            }
        )

    base_t, base_e = POSITIVE["linear"]

    def neg(name, mutate, kind):
        t, e = copy.deepcopy(base_t), copy.deepcopy(base_e)
        mutate(t, e)
        try:
            ci.build(t, e)
        except ci.CidagError as exc:
            assert exc.kind == kind, (name, exc.kind, kind)
        else:
            raise AssertionError("not refused: " + name)
        out["negative_build"].append(
            {"name": name, "template": t, "evidence": e, "error": kind}
        )

    neg("template extra member", lambda t, e: t.update(extra=1), "TEMPLATE_SHAPE")
    neg("template without workflow", lambda t, e: t.pop("workflow"), "TEMPLATE_SHAPE")
    neg(
        "template with a renamed member",
        lambda t, e: (t.pop("workflow"), t.update(flow="ci")),
        "TEMPLATE_SHAPE",
    )
    neg(
        "step with a renamed member",
        lambda t, e: (t["steps"][0].pop("needs"), t["steps"][0].update(deps=[])),
        "TEMPLATE_SHAPE",
    )
    neg("no steps", lambda t, e: t.update(steps=[]), "TEMPLATE_EMPTY")
    neg("step extra member", lambda t, e: t["steps"][0].update(x=1), "TEMPLATE_SHAPE")
    neg(
        "needs is not a list",
        lambda t, e: t["steps"][1].update(needs="build"),
        "TEMPLATE_SHAPE",
    )
    neg(
        "workflow id with a space", lambda t, e: t.update(workflow="a b"), "TEMPLATE_ID"
    )
    neg("empty step id", lambda t, e: t["steps"][0].update(id=""), "TEMPLATE_ID")
    neg(
        "step id with a quote",
        lambda t, e: t["steps"][0].update(id='b"x'),
        "TEMPLATE_ID",
    )
    neg(
        "step id with non-ASCII",
        lambda t, e: t["steps"][0].update(id="bé"),
        "TEMPLATE_ID",
    )
    neg(
        "step id too long",
        lambda t, e: t["steps"][0].update(id="a" * 129),
        "TEMPLATE_ID",
    )
    neg(
        "definition digest wrong form",
        lambda t, e: t["steps"][0].update(definition_digest="sha256:ABC"),
        "TEMPLATE_DIGEST",
    )
    neg(
        "definition digest upper case",
        lambda t, e: t["steps"][0].update(definition_digest="sha256:" + "A" * 64),
        "TEMPLATE_DIGEST",
    )
    neg(
        "duplicate step id",
        lambda t, e: t["steps"][1].update(id="build"),
        "TEMPLATE_DUPLICATE_ID",
    )
    neg(
        "unknown need",
        lambda t, e: t["steps"][1].update(needs=["nope"]),
        "TEMPLATE_NEED_UNKNOWN",
    )
    neg(
        "need with an empty identifier",
        lambda t, e: t["steps"][1].update(needs=[""]),
        "TEMPLATE_ID",
    )
    neg(
        "need that is not a string",
        lambda t, e: t["steps"][1].update(needs=[5]),
        "TEMPLATE_ID",
    )
    neg(
        "duplicate need",
        lambda t, e: t["steps"][1].update(needs=["build", "build"]),
        "TEMPLATE_NEED_DUPLICATE",
    )
    neg(
        "two-step cycle",
        lambda t, e: t["steps"][0].update(needs=["test"]),
        "TEMPLATE_CYCLE",
    )
    neg(
        "self need",
        lambda t, e: t["steps"][0].update(needs=["build"]),
        "TEMPLATE_CYCLE",
    )
    neg("evidence missing for a step", lambda t, e: e.pop("test"), "EVIDENCE_MISSING")
    neg(
        "evidence for an unknown step",
        lambda t, e: e.update(ghost=copy.deepcopy(e["build"])),
        "EVIDENCE_EXTRA",
    )
    neg(
        "evidence step without result",
        lambda t, e: e["build"].pop("result"),
        "EVIDENCE_SHAPE",
    )
    neg(
        "evidence step with extra member",
        lambda t, e: e["build"].update(x=1),
        "EVIDENCE_SHAPE",
    )
    neg(
        "evidence step with a renamed member",
        lambda t, e: (e["build"].pop("inputs"), e["build"].update(params={})),
        "EVIDENCE_SHAPE",
    )
    neg(
        "result with a renamed member",
        lambda t, e: (
            e["build"]["result"].pop("digest"),
            e["build"]["result"].update(hash="x"),
        ),
        "RESULT_SHAPE",
    )
    neg(
        "inputs is not an object",
        lambda t, e: e["build"].update(inputs=[]),
        "EVIDENCE_SHAPE",
    )
    neg(
        "input name with a space",
        lambda t, e: e["build"]["inputs"].update({"a b": "x"}),
        "INPUT_NAME",
    )
    neg(
        "empty input name",
        lambda t, e: e["build"]["inputs"].update({"": "x"}),
        "INPUT_NAME",
    )
    neg("input null", lambda t, e: e["build"]["inputs"].update(k=None), "INPUT_VALUE")
    neg(
        "input nested object",
        lambda t, e: e["build"]["inputs"].update(k={"a": 1}),
        "INPUT_VALUE",
    )
    neg("input list", lambda t, e: e["build"]["inputs"].update(k=[1]), "INPUT_VALUE")
    neg(
        "input empty string",
        lambda t, e: e["build"]["inputs"].update(k=""),
        "INPUT_VALUE",
    )
    neg(
        "input string with <",
        lambda t, e: e["build"]["inputs"].update(k="a<b"),
        "INPUT_VALUE",
    )
    neg(
        "input string with &",
        lambda t, e: e["build"]["inputs"].update(k="a&b"),
        "INPUT_VALUE",
    )
    neg(
        "input string with a quote",
        lambda t, e: e["build"]["inputs"].update(k='a"b'),
        "INPUT_VALUE",
    )
    neg(
        "input string with a backslash",
        lambda t, e: e["build"]["inputs"].update(k="a\\b"),
        "INPUT_VALUE",
    )
    neg(
        "input string with a tab",
        lambda t, e: e["build"]["inputs"].update(k="a\tb"),
        "INPUT_VALUE",
    )
    neg(
        "input string with non-ASCII",
        lambda t, e: e["build"]["inputs"].update(k="café"),
        "INPUT_VALUE",
    )
    neg(
        "input string too long",
        lambda t, e: e["build"]["inputs"].update(k="a" * 257),
        "INPUT_VALUE",
    )
    neg(
        "input integer beyond 2^53-1",
        lambda t, e: e["build"]["inputs"].update(k=9007199254740992),
        "INPUT_VALUE",
    )
    neg(
        "input integer below -(2^53-1)",
        lambda t, e: e["build"]["inputs"].update(k=-9007199254740992),
        "INPUT_VALUE",
    )
    neg(
        "result extra member",
        lambda t, e: e["build"]["result"].update(x=1),
        "RESULT_SHAPE",
    )
    neg(
        "result without digest",
        lambda t, e: e["build"]["result"].pop("digest"),
        "RESULT_SHAPE",
    )
    neg(
        "result is not an object",
        lambda t, e: e["build"].update(result="success"),
        "RESULT_SHAPE",
    )
    neg(
        "unknown status",
        lambda t, e: e["build"]["result"].update(status="ok"),
        "RESULT_STATUS",
    )
    neg(
        "status in upper case",
        lambda t, e: e["build"]["result"].update(status="SUCCESS"),
        "RESULT_STATUS",
    )
    neg(
        "success with a null digest",
        lambda t, e: e["build"]["result"].update(digest=None),
        "RESULT_DIGEST",
    )
    neg(
        "skipped with a digest",
        lambda t, e: e["build"]["result"].update(status="skipped"),
        "RESULT_DIGEST",
    )
    neg(
        "cancelled with a digest",
        lambda t, e: e["build"]["result"].update(status="cancelled"),
        "RESULT_DIGEST",
    )
    neg(
        "result digest wrong form",
        lambda t, e: e["build"]["result"].update(digest="sha256:1234"),
        "RESULT_DIGEST",
    )
    neg(
        "result digest not a string",
        lambda t, e: e["build"]["result"].update(digest=7),
        "RESULT_DIGEST",
    )

    # published bundles tampered after the build: the verifier must name the refusal
    good = ci.build(base_t, base_e)
    bundle = {
        "template": base_t,
        "objects": good["objects"],
        "step_roots": good["step_roots"],
        "ci_root": good["ci_root"],
    }
    out["verify_base"] = {"name": "linear", "bundle": bundle}

    def ver(name, ops, kind):
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
        try:
            ci.verify(b)
        except ci.CidagError as exc:
            assert exc.kind == kind, (name, exc.kind, kind)
        else:
            raise AssertionError("not refused: " + name)
        out["negative_verify"].append({"name": name, "ops": ops, "error": kind})

    P = lambda *a: list(a)  # noqa: E731
    ver("bundle extra member", [{"path": P("x"), "set": 1}], "BUNDLE_SHAPE")
    ver(
        "bundle without ci_root",
        [{"path": P("ci_root"), "delete": True}],
        "BUNDLE_SHAPE",
    )
    ver(
        "object extra member",
        [{"path": P("objects", "build", "x"), "set": 1}],
        "OBJECT_SHAPE",
    )
    ver(
        "object without inputs",
        [{"path": P("objects", "test", "inputs"), "delete": True}],
        "OBJECT_SHAPE",
    )
    ver(
        "object contract changed",
        [{"path": P("objects", "build", "contract"), "set": "cic-ci-step/2"}],
        "OBJECT_CONTRACT_MISMATCH",
    )
    ver(
        "object step id changed",
        [{"path": P("objects", "build", "step", "id"), "set": "other"}],
        "OBJECT_STEP_MISMATCH",
    )
    ver(
        "object definition digest changed",
        [{"path": P("objects", "build", "step", "definition_digest"), "set": d("x")}],
        "OBJECT_STEP_MISMATCH",
    )
    ver(
        "two objects exchanged",
        [
            {"path": P("objects", "build"), "set": good["objects"]["test"]},
            {"path": P("objects", "test"), "set": good["objects"]["build"]},
        ],
        "OBJECT_STEP_MISMATCH",
    )
    ver(
        "dependency removed from an object",
        [{"path": P("objects", "test", "dependencies"), "set": []}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "dependency replaced by another root",
        [
            {
                "path": P("objects", "release", "dependencies"),
                "set": [good["step_roots"]["build"]],
            }
        ],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "edge removed from the template",
        [{"path": P("template", "steps", 1, "needs"), "set": []}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    # tampering with the LAST step is caught by its own root; tampering with an earlier step is caught one hop
    # downstream, because the published dependency root of the dependent no longer matches the recomputed one
    ver(
        "input value changed in the last step",
        [{"path": P("objects", "release", "inputs", "channel"), "set": "beta"}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "input added to the last step",
        [{"path": P("objects", "release", "inputs", "extra"), "set": "x"}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "input removed from the last step",
        [{"path": P("objects", "release", "inputs", "channel"), "delete": True}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "result digest changed in the last step",
        [{"path": P("objects", "release", "result", "digest"), "set": d("evil")}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "result status changed in the last step",
        [{"path": P("objects", "release", "result", "status"), "set": "neutral"}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "input value changed in a middle step",
        [{"path": P("objects", "test", "inputs", "mode"), "set": "delta"}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "input removed from the first step",
        [{"path": P("objects", "build", "inputs", "compiler"), "delete": True}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "result digest changed in the first step",
        [{"path": P("objects", "build", "result", "digest"), "set": d("evil")}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "result status changed in the first step",
        [{"path": P("objects", "build", "result", "status"), "set": "neutral"}],
        "OBJECT_DEPENDENCIES_MISMATCH",
    )
    ver(
        "a step root changed",
        [{"path": P("step_roots", "build"), "set": d("evil")}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "a step root removed",
        [{"path": P("step_roots", "release"), "delete": True}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "a step root added",
        [{"path": P("step_roots", "ghost"), "set": d("evil")}],
        "STEP_ROOT_MISMATCH",
    )
    ver(
        "ci root changed",
        [{"path": P("ci_root"), "set": d("evil")}],
        "CI_ROOT_MISMATCH",
    )
    ver(
        "workflow renamed",
        [{"path": P("template", "workflow"), "set": "other"}],
        "CI_ROOT_MISMATCH",
    )
    ver(
        "object removed",
        [{"path": P("objects", "release"), "delete": True}],
        "EVIDENCE_MISSING",
    )
    ver(
        "object for an unknown step",
        [{"path": P("objects", "ghost"), "set": good["objects"]["build"]}],
        "EVIDENCE_EXTRA",
    )
    ver(
        "step added to the template without an object",
        [
            {
                "path": P("template", "steps"),
                "set": base_t["steps"]
                + [{"id": "late", "definition_digest": d("l"), "needs": []}],
            }
        ],
        "EVIDENCE_MISSING",
    )
    ver(
        "cycle introduced in the template",
        [{"path": P("template", "steps", 0, "needs"), "set": ["release"]}],
        "TEMPLATE_CYCLE",
    )
    ver(
        "input value turned invalid",
        [{"path": P("objects", "build", "inputs", "source"), "set": None}],
        "INPUT_VALUE",
    )

    for name, text in (
        ("duplicate member name", '{"a":1,"a":2}'),
        ("float", '{"a":1.5}'),
        ("exponent", '{"a":1e3}'),
        ("integer with a fraction of zero", '{"a":1.0}'),
        ("NaN", '{"a":NaN}'),
        ("Infinity", "[Infinity]"),
        ("leading zero", '{"a":01}'),
        ("not JSON", "{"),
        ("duplicate in a nested object", '{"o":{"k":1,"k":1}}'),
        ("integer beyond int64", '{"a":9223372036854775808}'),
        ("nesting deeper than 64", "[" * 65 + "]" * 65),
    ):
        try:
            ci.parse_strict(text)
        except ci.CidagError as exc:
            assert exc.kind == "PARSE", (name, exc.kind)
        else:
            raise AssertionError("not refused: " + name)
        out["parse"].append({"name": name, "text": text, "error": "PARSE"})
    out["parse_ok"] = [
        {
            "name": "plain object",
            "text": '{"b":[1,-2,0],"a":{"x":null,"y":true}}',
            "canonical": ci.canonical(
                ci.parse_strict('{"b":[1,-2,0],"a":{"x":null,"y":true}}')
            ).decode("ascii"),
        }
    ]
    return out


def main(argv):
    dest = (
        argv[1]
        if len(argv) > 1
        else os.path.join(
            os.path.dirname(os.path.abspath(__file__)),
            "..",
            "..",
            "pkg",
            "cidag",
            "testdata",
            "vectors.json",
        )
    )
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    text = json.dumps(build_vectors(), sort_keys=True, indent=1) + "\n"
    with open(dest, "w", encoding="utf-8") as f:
        f.write(text)
    print("wrote %s (%d bytes)" % (os.path.normpath(dest), len(text.encode())))


if __name__ == "__main__":
    main(sys.argv)
