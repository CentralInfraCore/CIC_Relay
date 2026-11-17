"""Tests for tools/rs.meta.gen.py: the rsastdump (syn) front end and the
dump -> companion translation.

The tests that run rsastdump skip when there is no cargo, unless
RS_META_REQUIRE_CARGO=1 (set in CI) — then a missing toolchain is a failure,
so the syn path cannot go unverified without anyone noticing.
"""

import importlib.util
import os
import shutil
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml

_ROOT = Path(__file__).resolve().parents[2]
_TOOLS = _ROOT / "tools"
_SAMPLE = _TOOLS / "rsastdump" / "testdata" / "sample.rs"

_spec = importlib.util.spec_from_file_location("rs_meta_gen", _TOOLS / "rs.meta.gen.py")
assert _spec is not None and _spec.loader is not None
gen = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gen)

HAVE_CARGO = shutil.which("cargo") is not None or bool(
    os.environ.get("RS_META_RSASTDUMP")
)


def needs_cargo(fn):
    if os.environ.get("RS_META_REQUIRE_CARGO") == "1":
        return fn  # no skip: a missing toolchain must fail the run
    return pytest.mark.skipif(not HAVE_CARGO, reason="no cargo")(fn)


def _crate(tmp_path: Path, name: str = "demo-crate", deps: str = "") -> Path:
    (tmp_path / "Cargo.toml").write_text(
        f'[package]\nname = "{name}"\nversion = "0.1.0"\n\n'
        f"[dependencies]\n{deps}\n",
        encoding="utf-8",
    )
    (tmp_path / "src").mkdir(exist_ok=True)
    gen._crate_cache.clear()
    return tmp_path


def _item(kind, name, **kw):
    base = {"name": name, "kind": kind, "mod_path": [], "test": False, "doc": ""}
    base.update({"refs": [], "calls": []})
    base.update(kw)
    return base


def _dump(items, mods=None):
    return {"file": "x.rs", "inner_doc": "", "mods": mods or [], "items": items}


# --- no cargo needed --------------------------------------------------------


def test_module_path(tmp_path):
    root = _crate(tmp_path)
    mp = gen._module_path
    assert mp(root / "src/lib.rs", root) == []
    assert mp(root / "src/main.rs", root) == []
    assert mp(root / "src/a/b.rs", root) == ["a", "b"]
    assert mp(root / "src/a/mod.rs", root) == ["a"]
    assert mp(root / "src/bin/tool.rs", root) == []
    assert mp(root / "src/bin/tool/main.rs", root) == []
    assert mp(root / "src/bin/tool/util.rs", root) == ["util"]
    assert mp(root / "tests/it.rs", root) == []
    assert mp(root / "tests/common/mod.rs", root) == []
    assert mp(root / "build.rs", root) == []
    assert mp(root / "src/x.rs", None) == []
    assert mp(Path("/elsewhere/x.rs"), root) == []


def test_find_crate_and_deps(tmp_path):
    root = _crate(
        tmp_path,
        deps='serde-json = "1"\nvault = { package = "vault-client-rs", version = "1" }\n'
        '\n[dev-dependencies]\ntokio = "1"\n'
        "\n[target.'cfg(unix)'.dependencies]\nnix = \"0.29\"\n",
    )
    info = gen._find_crate(root / "src" / "lib.rs")
    assert info is not None
    assert info.name == "demo_crate"
    assert info.deps == {"serde_json", "vault", "tokio", "nix"}
    # cached
    assert gen._find_crate(root / "src" / "lib.rs") is info


def test_find_crate_skips_virtual_workspace(tmp_path):
    (tmp_path / "Cargo.toml").write_text('[workspace]\nmembers = ["m"]\n')
    m = tmp_path / "m"
    (m / "src").mkdir(parents=True)
    (m / "Cargo.toml").write_text('[package]\nname = "m"\nversion = "0.1.0"\n')
    gen._crate_cache.clear()
    info = gen._find_crate(m / "src" / "lib.rs")
    assert info is not None and info.name == "m"
    gen._crate_cache.clear()
    (m / "Cargo.toml").write_text("not = [toml")
    assert gen._find_crate(m / "src" / "lib.rs") is None


def test_normalize():
    n = gen._normalize
    assert n("self::a::B", ["x", "y"]) == "crate::x::y::a::B"
    assert n("super::B", ["x", "y"]) == "crate::x::B"
    assert n("super::super::B", ["x", "y"]) == "crate::B"
    assert n("super::B", []) == "crate::B"
    assert n("serde::Serialize", ["x"]) == "serde::Serialize"


def test_objects_from_dump_filters_and_formats(tmp_path):
    root = _crate(tmp_path, deps='serde = "1"\nreqwest = "0.12"\n')
    crate = gen._find_crate(root / "src" / "lib.rs")
    items = [
        _item("mod", "child", visibility="pub"),
        _item(
            "struct",
            "Boxed",
            visibility="pub",
            doc="Boxed.",
            derives=["Debug", "serde::Serialize"],
            refs=["serde::Serialize", "std::collections::HashMap", "crate::other::T"],
        ),
        _item("enum", "Kind", visibility="pub", variants=["A"], derives=[]),
        _item(
            "impl",
            "fmt::Display for Kind",
            receiver="Kind",
            trait="std::fmt::Display",
        ),
        _item("impl", "!Send for Kind", receiver="Kind", trait="Send"),
        _item(
            "method",
            "fmt",
            receiver="Kind",
            trait="std::fmt::Display",
            calls=["Ok", "String::from", "str::len", "self::Kind::name"],
        ),
        _item(
            "fn",
            "handle",
            visibility="pub",
            refs=[
                "reqwest::Client",
                "self::child::helper",
                "self::Boxed",
                "Kind::A",
                "unknown::Thing",
                "demo_crate::Api",
            ],
            calls=[
                "self::child::helper",
                "helper",
                "std::fs::read",
                "crate::Boxed::new",
                "reqwest::Client::get",
            ],
        ),
        _item(
            "trait",
            "Store",
            visibility="pub",
            methods=["get"],
            supertraits=["super::Base"],
        ),
        _item("mod", "tests", test=True),
        _item("fn", "works", mod_path=["tests"], test=True, calls=["super::helper"]),
        _item("macro", "m", visibility="private"),
    ]
    objs = gen._objects_from_dump(_dump(items, mods=["child", "tests"]), [], crate)
    by = {(o["kind"], o["name"]): o for o in objs}

    boxed = by[("struct", "Boxed")]
    # lib.rs declares every top-level module, so crate::other (undeclared here)
    # is an item of this very file: local, not a reference
    assert boxed["references"] == ["serde::Serialize"]
    assert boxed["derives"] == ["Debug", "serde::Serialize"]
    assert boxed["implements"] == []
    assert boxed["description"] == "Boxed."
    assert by[("enum", "Kind")]["implements"] == ["std::fmt::Display"]
    assert by[("enum", "Kind")]["variants"] == ["A"]

    fmt = by[("method", "fmt")]
    assert fmt["receiver"] == "Kind" and fmt["trait"] == "std::fmt::Display"
    assert "visibility" not in fmt
    assert fmt["calls"] == ["Kind::name"]  # prelude/primitive dropped, local shortened

    h = by[("fn", "handle")]
    # child is an out-of-line module (cross-module), Boxed is this file's own
    assert h["references"] == [
        "reqwest::Client",
        "crate::child::helper",
        "demo_crate::Api",
    ]
    assert h["calls"] == [
        "Boxed::new",
        "crate::child::helper",
        "helper",
        "reqwest::Client::get",
    ]
    assert by[("trait", "Store")]["supertraits"] == ["crate::Base"]
    works = by[("fn", "tests::works")]
    assert works["test"] is True
    assert works["calls"] == ["helper"]  # super:: from the inline module: same file
    assert "references" not in by[("macro", "m")]
    assert "calls" not in boxed

    # kind grouping: mods first, fns after impls
    kinds = [o["kind"] for o in objs]
    assert kinds.index("mod") < kinds.index("struct") < kinds.index("impl")
    assert kinds.index("method") < kinds.index("fn")

    skipped = gen._objects_from_dump(_dump(items, ["child"]), [], crate, True)
    assert not any(o.get("test") for o in skipped)


def test_suggest_tags(tmp_path):
    objs = [
        {"name": "validate_input", "kind": "fn", "references": ["reqwest::Client"]},
        {"name": "SchemaStore", "kind": "struct", "references": []},
        {"name": "tests::t", "kind": "fn", "test": True},
        {"name": "Hook", "kind": "trait", "calls": ["sqlx::query"]},
        {"name": "parse_x", "kind": "fn"},
        {"name": "load_cfg", "kind": "fn"},
        {"name": "MetricSink", "kind": "struct"},
        {"name": "RunExecutor", "kind": "struct"},
    ]
    tags = gen._suggest_tags(Path("/r/tools/x/src/main.rs"), objs, None)
    assert tags == sorted(
        {
            "builder",
            "core",
            "executor",
            "gateway",
            "hook",
            "interface",
            "loader",
            "metrics",
            "parser",
            "schema",
            "storage",
            "validator",
        }
    )
    assert gen._suggest_tags(Path("/r/core/build.rs"), [], None) == ["builder", "core"]
    # an inline test module alone does not make a file a test file
    mixed = [
        {"name": "f", "kind": "fn"},
        {"name": "tests", "kind": "mod", "test": True},
    ]
    assert gen._suggest_tags(Path("/r/src/a.rs"), mixed, None) == []
    only = [{"name": "t", "kind": "fn", "test": True}]
    assert gen._suggest_tags(Path("/r/src/a.rs"), only, None) == ["test"]


def test_generate_fields(tmp_path):
    root = _crate(tmp_path)
    main_rs = root / "src" / "main.rs"
    data = gen.generate(main_rs, _dump([_item("fn", "main", visibility="private")]))
    assert data["crate"] == "demo_crate"
    assert data["module"] == "crate"
    assert data["entrypoint"] is True
    lib = gen.generate(root / "src" / "lib.rs", _dump([_item("fn", "main")]))
    assert lib["entrypoint"] is False
    sub = gen.generate(root / "src" / "a.rs", _dump([]))
    assert sub["module"] == "crate::a" and sub["entrypoint"] is False
    assert gen.generate(root / "build.rs", _dump([]))["entrypoint"] is True
    assert list(data) == [
        "crate",
        "module",
        "description",
        "tags",
        "category",
        "used_in",
        "entrypoint",
        "related_nodes",
        "objects",
    ]


def test_merge_keeps_manual_and_matches_by_receiver():
    new = {
        "crate": "c",
        "module": "crate",
        "description": "",
        "tags": ["core"],
        "category": [],
        "used_in": [],
        "entrypoint": False,
        "related_nodes": [],
        "objects": [
            {"name": "new", "kind": "method", "receiver": "A", "description": ""},
            {"name": "new", "kind": "method", "receiver": "B", "description": "doc"},
            {"name": "fresh", "kind": "fn", "description": ""},
        ],
    }
    old = {
        "description": "hand written",
        "tags": ["relay"],
        "category": ["modul"],
        "used_in": ["validation"],
        "related_nodes": ["x"],
        "objects": [
            {"name": "new", "kind": "method", "receiver": "A", "description": "A's"},
            {"name": "new", "kind": "method", "receiver": "B", "description": "old"},
            {"name": "gone", "kind": "fn", "description": "x"},
        ],
    }
    m = gen._merge_data(new, old)
    assert m["tags"] == ["relay"] and m["category"] == ["modul"]
    assert m["used_in"] == ["validation"] and m["related_nodes"] == ["x"]
    assert m["description"] == "hand written"
    descs = [
        (o["receiver"] if "receiver" in o else "", o["description"])
        for o in m["objects"]
    ]
    assert descs == [("A", "A's"), ("B", "doc"), ("", "")]


def test_cross_file_implements(tmp_path):
    root = _crate(tmp_path)
    a = root / "src" / "a.yaml"
    b = root / "src" / "b.yaml"
    (root / "src" / "a.rs").write_text("")
    (root / "src" / "b.rs").write_text("")
    (root / "target").mkdir()
    (root / "target" / "x.rs").write_text("")
    (root / "target" / "x.yaml").write_text("objects: []")
    gen._write_yaml(
        {"objects": [{"name": "Foo", "kind": "struct", "implements": ["Stale"]}]}, a
    )
    gen._write_yaml(
        {
            "objects": [
                {
                    "name": "T for Foo",
                    "kind": "impl",
                    "receiver": "crate::a::Foo",
                    "trait": "crate::T",
                },
                {
                    "name": "!Send for Foo",
                    "kind": "impl",
                    "receiver": "Foo",
                    "trait": "Send",
                },
                {"name": "Foo", "kind": "impl", "receiver": "Foo"},
            ]
        },
        b,
    )
    (root / "src" / "bad.rs").write_text("")
    (root / "src" / "bad.yaml").write_text(": : [")
    files = gen._companions(root)
    assert root / "target" / "x.yaml" not in files
    assert gen._apply_implements(files) == 1
    data = yaml.safe_load(a.read_text())
    assert data["objects"][0]["implements"] == ["crate::T"]
    assert gen._apply_implements(files) == 0  # idempotent


def test_dump_command_override(monkeypatch):
    monkeypatch.setattr(gen, "_dump_cmd", None)
    monkeypatch.setenv("RS_META_RSASTDUMP", "/bin/echo -n")
    assert gen._dump_command() == ["/bin/echo", "-n"]
    monkeypatch.setattr(gen, "_dump_cmd", None)
    monkeypatch.delenv("RS_META_RSASTDUMP")
    monkeypatch.setattr(gen.shutil, "which", lambda _: None)
    assert gen._dump_command() is None
    with pytest.raises(RuntimeError, match="unavailable"):
        gen._rs_dump([Path("x.rs")])
    monkeypatch.setattr(gen, "_dump_cmd", None)


def test_rs_dump_failure(monkeypatch):
    monkeypatch.setattr(gen, "_dump_cmd", ["false"])
    with pytest.raises(RuntimeError, match="rsastdump failed"):
        gen._rs_dump([Path("x.rs")])
    monkeypatch.setattr(gen, "_dump_cmd", None)


# --- rsastdump (cargo) ------------------------------------------------------


@needs_cargo
def test_sample_end_to_end(tmp_path):
    root = _crate(
        tmp_path,
        deps='serde = "1"\nserde_json = "1"\nreqwest = "0.12"\nanyhow = "1"\n',
    )
    rs = root / "src" / "graph" / "sample.rs"
    rs.parent.mkdir()
    shutil.copy(_SAMPLE, rs)
    dumps = gen._rs_dump([rs])
    data = gen.generate(rs, dumps[rs.resolve()])
    assert data["module"] == "crate::graph::sample"
    assert data["description"].startswith("Sample exercises")
    by = {(o["kind"], o["name"]): o for o in data["objects"]}
    assert by[("struct", "Boxed")]["references"] == [
        "crate::graph::Node",
        "serde_json::Value",
        "serde::Serialize",
        "serde::Deserialize",
    ]
    assert by[("enum", "Kind")]["implements"] == ["Eq", "std::fmt::Display"]
    assert by[("method", "fmt")]["calls"] == [
        "crate::graph::sibling::Thing::default",
        "serde_json::to_string",
    ]
    assert by[("fn", "handle")]["calls"] == [
        "Boxed::touch",
        "Kind::describe",
        "crate::graph::sample::child::helper",
        "helper",
        "make",
        "reqwest::Client::get",
    ]
    assert by[("fn", "tests::works")]["test"] is True


@needs_cargo
def test_cli_generate_merge_and_skip(tmp_path, monkeypatch, capsys):
    root = _crate(tmp_path, deps='serde = "1"\n')
    lib = root / "src" / "lib.rs"
    lib.write_text(
        "//! Lib.\npub mod a;\nuse serde::Serialize;\n"
        "#[derive(Serialize)]\npub struct S;\n"
        "#[cfg(test)]\nmod tests { #[test] fn t() {} }\n"
    )
    (root / "src" / "a.rs").write_text(
        "impl Clone for crate::S { fn clone(&self) -> Self { crate::S } }\n"
    )
    (root / "src" / "broken.rs").write_text("fn x( {\n")
    (root / "tests").mkdir()
    (root / "tests" / "it.rs").write_text("#[test]\nfn it() {}\n")
    (root / "target").mkdir()
    (root / "target" / "gen.rs").write_text("fn g() {}\n")

    monkeypatch.chdir(root)

    def run(*args):
        """main() in-process (so it is measured); returns (code, out, err)."""
        monkeypatch.setattr(sys, "argv", ["rs.meta.gen.py", *args])
        gen._crate_cache.clear()
        code = 0
        try:
            gen.main()
        except SystemExit as e:
            code = int(e.code or 0)
        out, err = capsys.readouterr()
        return SimpleNamespace(returncode=code, stdout=out, stderr=err)

    res = run("--dir", ".", "--recursive", "--skip-tests")
    assert res.returncode == 1, res.stdout + res.stderr  # broken.rs
    assert "broken.rs" in res.stderr
    assert not (root / "target" / "gen.yaml").exists()
    assert not (root / "tests" / "it.yaml").exists()
    data = yaml.safe_load((root / "src" / "lib.yaml").read_text())
    assert [o["name"] for o in data["objects"]] == ["a", "S"]  # tests skipped
    assert data["objects"][1]["implements"] == ["Clone"]  # from a.rs

    data["tags"] = ["relay"]
    data["objects"][1]["description"] = "hand"
    gen._write_yaml(data, root / "src" / "lib.yaml")
    (root / "src" / "broken.rs").unlink()
    res = run("--dir", "src", "--merge")
    assert res.returncode == 0, res.stderr
    assert "MERGED" in res.stdout
    data = yaml.safe_load((root / "src" / "lib.yaml").read_text())
    assert data["tags"] == ["relay"]
    s = next(o for o in data["objects"] if o["name"] == "S")
    assert s["description"] == "hand"
    assert s["implements"] == ["Clone"]
    assert any(o["name"] == "tests::t" for o in data["objects"])  # no --skip-tests

    res = run("--dir", "src", "--dry-run")
    assert "SKIP (exists)" in res.stdout
    res = run("src/missing.rs")
    assert res.returncode == 1 and "NOT FOUND" in res.stderr
    res = run()
    assert res.returncode == 1
