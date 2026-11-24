# SPDX-License-Identifier: AGPL-3.0-or-later
"""Independent recomputation of the CIC CI Merkle DAG (contracts cic-ci-step/1, cic-ci-root/1).

This module shares NO code with pkg/cidag (Go): it is a second implementation of the same contract,
written from docs/hu/CI-MERKLE-DAG-CONTRACT.md, standard library only. Both implementations read the
same vectors (pkg/cidag/testdata/vectors.json) and must agree on every canonical byte, every root and
every refusal class.

The canonical form is the PROFILE of the CIC Canonical Object Encoding used for CI step objects: only
strings that never need an escape (so Go's json.Marshal escaping, which the encoding inherits, never
applies), integers, booleans and null; object keys sorted by their UTF-8 bytes; no white space. For that
profile the bytes are identical to Go pkg/canonicaljson.ToJSON, to Python json.dumps(sort_keys=True,
separators=(",", ":")) and to RFC 8785.
"""

import hashlib
import json
import re

STEP_CONTRACT = "cic-ci-step/1"
ROOT_CONTRACT = "cic-ci-root/1"
TEMPLATE_CONTRACT = "cic-ci-template/1"
STEP_PREFIX = b"CICSTEPv1\x00"
TEMPLATE_PREFIX = b"CICTEMPLATEv1\x00"
ROOT_PREFIX = b"CICCIROOTv1\x00"

STATUSES = (
    "success",
    "failure",
    "cancelled",
    "skipped",
    "neutral",
    "timed_out",
    "action_required",
    "stale",
)
NULL_DIGEST_STATUSES = ("skipped", "cancelled")

ID_RE = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._:/@+=-]{0,127}\Z")
DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
INT_RE = re.compile(r"\A-?(0|[1-9][0-9]*)\Z")
MAX_INT = 2**53 - 1
MAX_PARAM = 256
MAX_DEPTH = 64
# printable ASCII without the characters a JSON writer would escape or a HTML-safe encoder would rewrite
PARAM_CHARS = frozenset(chr(c) for c in range(0x20, 0x7F)) - frozenset('"\\<>&')


class CidagError(Exception):
    """A refusal with a stable class (`kind`) that the Go implementation names the same way."""

    def __init__(self, kind, detail=""):
        super().__init__("%s: %s" % (kind, detail))
        self.kind = kind
        self.detail = detail


def _fail(kind, detail=""):
    raise CidagError(kind, detail)


# ---------------------------------------------------------------------------------------- strict reader
def parse_strict(text):
    """Parse JSON refusing duplicate member names, any non-integer number and any constant (NaN, Infinity)."""

    def pairs(items):
        seen = {}
        for k, v in items:
            if k in seen:
                _fail("PARSE", "duplicate member name %r" % k)
            seen[k] = v
        return seen

    def no_float(s):
        _fail("PARSE", "non-integer number %r" % s)

    def no_const(s):
        _fail("PARSE", "constant %r" % s)

    def to_int(s):
        if not INT_RE.match(s) or abs(int(s)) > 2**63 - 1:
            _fail("PARSE", "number %r" % s)
        return int(s)

    try:
        value = json.loads(
            text,
            object_pairs_hook=pairs,
            parse_float=no_float,
            parse_int=to_int,
            parse_constant=no_const,
        )
    except CidagError:
        raise
    except (ValueError, RecursionError) as exc:
        _fail("PARSE", str(exc))
    if _depth(value) > MAX_DEPTH:
        _fail("PARSE", "nesting deeper than %d" % MAX_DEPTH)
    return value


def _depth(v):
    if isinstance(v, dict):
        return 1 + max((_depth(x) for x in v.values()), default=0)
    if isinstance(v, list):
        return 1 + max((_depth(x) for x in v), default=0)
    return 0


# ------------------------------------------------------------------------------------- canonical writer
def _is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def _ser(v):
    if v is None:
        return "null"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if _is_int(v):
        if abs(v) > MAX_INT:
            _fail("PROFILE", "integer beyond 2^53-1")
        return str(v)
    if isinstance(v, str):
        if not all(c in PARAM_CHARS for c in v):
            _fail("PROFILE", "string outside the escape-free profile: %r" % v)
        return '"' + v + '"'
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(_ser(x) for x in v) + "]"
    if isinstance(v, dict):
        for k in v:
            if not isinstance(k, str):
                _fail("PROFILE", "non-string key")
        # keys sorted by UTF-8 bytes; the profile makes every key ASCII, so this is also code-point order
        return (
            "{"
            + ",".join(
                _ser(k) + ":" + _ser(v[k])
                for k in sorted(v, key=lambda s: s.encode("utf-8"))
            )
            + "}"
        )
    _fail("PROFILE", "unsupported type %s" % type(v).__name__)


def canonical(value):
    return _ser(value).encode("ascii")


def digest(prefix, value):
    return "sha256:" + hashlib.sha256(prefix + canonical(value)).hexdigest()


# ------------------------------------------------------------------------------------------ validation
def _id(v, kind):
    if not isinstance(v, str) or not ID_RE.match(v):
        _fail(kind, "bad identifier %r" % (v,))
    return v


def _digest(v, kind):
    if not isinstance(v, str) or not DIGEST_RE.match(v):
        _fail(kind, "bad digest %r" % (v,))
    return v


def check_template(t):
    if not isinstance(t, dict) or set(t) != {"steps", "workflow"}:
        _fail("TEMPLATE_SHAPE", "template must have exactly workflow and steps")
    _id(t["workflow"], "TEMPLATE_ID")
    steps = t["steps"]
    if not isinstance(steps, list) or not steps:
        _fail("TEMPLATE_EMPTY", "no steps")
    byid = {}
    for s in steps:
        if not isinstance(s, dict) or set(s) != {"definition_digest", "id", "needs"}:
            _fail(
                "TEMPLATE_SHAPE",
                "a step must have exactly id, definition_digest and needs",
            )
        _id(s["id"], "TEMPLATE_ID")
        _digest(s["definition_digest"], "TEMPLATE_DIGEST")
        if s["id"] in byid:
            _fail("TEMPLATE_DUPLICATE_ID", s["id"])
        if not isinstance(s["needs"], list):
            _fail("TEMPLATE_SHAPE", "needs must be a list")
        byid[s["id"]] = s
    for s in steps:
        seen = set()
        for n in s["needs"]:
            _id(n, "TEMPLATE_ID")
            if n not in byid:
                _fail("TEMPLATE_NEED_UNKNOWN", "%s needs %s" % (s["id"], n))
            if n in seen:
                _fail("TEMPLATE_NEED_DUPLICATE", "%s needs %s twice" % (s["id"], n))
            seen.add(n)
    return byid


def topo_order(byid):
    """Kahn's algorithm, ready steps taken in ascending id order: one deterministic order or TEMPLATE_CYCLE."""
    indeg = {i: len(s["needs"]) for i, s in byid.items()}
    users = {i: [] for i in byid}
    for i, s in byid.items():
        for n in s["needs"]:
            users[n].append(i)
    ready = sorted(i for i, d in indeg.items() if d == 0)
    out = []
    while ready:
        i = ready.pop(0)
        out.append(i)
        for u in users[i]:
            indeg[u] -= 1
            if indeg[u] == 0:
                ready.append(u)
        ready.sort()
    if len(out) != len(byid):
        _fail("TEMPLATE_CYCLE", "the dependency graph has a cycle")
    return out


def check_evidence(byid, evidence):
    if not isinstance(evidence, dict):
        _fail("EVIDENCE_SHAPE", "evidence must be an object keyed by step id")
    for i in sorted(byid):
        if i not in evidence:
            _fail("EVIDENCE_MISSING", i)
    for i in sorted(evidence):
        if i not in byid:
            _fail("EVIDENCE_EXTRA", i)
    for i in sorted(evidence):
        e = evidence[i]
        if not isinstance(e, dict) or set(e) != {"inputs", "result"}:
            _fail("EVIDENCE_SHAPE", "%s: exactly inputs and result" % i)
        if not isinstance(e["inputs"], dict):
            _fail("EVIDENCE_SHAPE", "%s: inputs must be an object" % i)
        for name in sorted(e["inputs"]):
            val = e["inputs"][name]
            if not isinstance(name, str) or not ID_RE.match(name):
                _fail("INPUT_NAME", "%s: %r" % (i, name))
            if isinstance(val, bool):
                continue
            if _is_int(val):
                if abs(val) > MAX_INT:
                    _fail("INPUT_VALUE", "%s.%s: integer beyond 2^53-1" % (i, name))
                continue
            if (
                isinstance(val, str)
                and 0 < len(val) <= MAX_PARAM
                and all(c in PARAM_CHARS for c in val)
            ):
                continue
            _fail("INPUT_VALUE", "%s.%s" % (i, name))
        r = e["result"]
        if not isinstance(r, dict) or set(r) != {"digest", "status"}:
            _fail("RESULT_SHAPE", "%s: exactly status and digest" % i)
        if r["status"] not in STATUSES:
            _fail("RESULT_STATUS", "%s: %r" % (i, r["status"]))
        if r["status"] in NULL_DIGEST_STATUSES:
            if r["digest"] is not None:
                _fail(
                    "RESULT_DIGEST", "%s: %s must have a null digest" % (i, r["status"])
                )
        else:
            _digest(r["digest"], "RESULT_DIGEST")


# ------------------------------------------------------------------------------------------------ build
def template_object(t):
    return {
        "contract": TEMPLATE_CONTRACT,
        "steps": {
            s["id"]: {
                "definition_digest": s["definition_digest"],
                "needs": sorted(s["needs"]),
            }
            for s in t["steps"]
        },
        "workflow": t["workflow"],
    }


def build(template, evidence):
    """Returns {"objects", "step_roots", "template_digest", "ci_root"} or raises CidagError."""
    byid = check_template(template)
    order = topo_order(byid)
    check_evidence(byid, evidence)
    roots, objects = {}, {}
    for i in order:
        s = byid[i]
        e = evidence[i]
        obj = {
            "contract": STEP_CONTRACT,
            "dependencies": sorted(roots[n] for n in s["needs"]),
            "inputs": e["inputs"],
            "result": {
                "digest": e["result"]["digest"],
                "status": e["result"]["status"],
            },
            "step": {"definition_digest": s["definition_digest"], "id": i},
        }
        objects[i] = obj
        roots[i] = digest(STEP_PREFIX, obj)
    tdigest = digest(TEMPLATE_PREFIX, template_object(template))
    root_obj = {
        "contract": ROOT_CONTRACT,
        "steps": dict(roots),
        "template_digest": tdigest,
        "workflow": template["workflow"],
    }
    return {
        "objects": objects,
        "step_roots": roots,
        "template_digest": tdigest,
        "ci_root": digest(ROOT_PREFIX, root_obj),
    }


# ---------------------------------------------------------------------------------------------- verify
def verify(bundle):
    """Recompute everything from a published bundle {template, objects, step_roots, ci_root}; the published
    objects are never trusted: only their inputs and result are read, every other member is recomputed and
    compared."""
    if not isinstance(bundle, dict) or set(bundle) != {
        "ci_root",
        "objects",
        "step_roots",
        "template",
    }:
        _fail("BUNDLE_SHAPE", "exactly template, objects, step_roots and ci_root")
    byid = check_template(bundle["template"])
    objs = bundle["objects"]
    if not isinstance(objs, dict):
        _fail("BUNDLE_SHAPE", "objects must be an object keyed by step id")
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
            _fail("OBJECT_SHAPE", i)
        evidence[i] = {"inputs": o["inputs"], "result": o["result"]}
    built = build(bundle["template"], evidence)
    for i in topo_order(byid):
        got, want = objs[i], built["objects"][i]
        if got["contract"] != want["contract"]:
            _fail("OBJECT_CONTRACT_MISMATCH", i)
        if got["step"] != want["step"]:
            _fail("OBJECT_STEP_MISMATCH", i)
        if got["dependencies"] != want["dependencies"]:
            _fail("OBJECT_DEPENDENCIES_MISMATCH", i)
    if bundle["step_roots"] != built["step_roots"]:
        _fail(
            "STEP_ROOT_MISMATCH",
            "the published step roots differ from the recomputed ones",
        )
    if bundle["ci_root"] != built["ci_root"]:
        _fail(
            "CI_ROOT_MISMATCH", "the published CI root differs from the recomputed one"
        )
    return built
