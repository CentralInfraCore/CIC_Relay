#!/usr/bin/env python3
"""rs.meta.gen.py — Companion YAML skeleton generator for Rust source files.

The Rust counterpart of go.meta.gen.py. Reads Rust through tools/rsastdump (a
`syn` front end, workspace member) and writes a .yaml companion next to each
.rs file following rs.meta.schema.yaml.

Auto-filled fields:
  - crate, module, entrypoint
  - description: the file's inner doc (//!)
  - objects: name, kind, visibility, receiver, trait, derives, implements,
    variants, methods, supertraits, references, calls, test
  - objects[].description: outer doc comments (///)
  - tags: suggested from file path, dependencies, and naming (review before use)

Semantic fields left empty for human completion:
  - category, used_in, related_nodes

References keep only paths into dependency crates (Cargo.toml) and into other
modules of the same crate (crate::…); std/core/alloc and same-module paths are
dropped. `implements` is recomputed on every run from the `impl Trait for Type`
blocks of all companions of the crate, so it is an auto field, not a manual one.

Usage:
    python tools/rs.meta.gen.py <file.rs> [<file.rs> ...]
    python tools/rs.meta.gen.py --dir <directory> [--recursive] [--merge]

Options:
    --dir         Process all .rs files in directory (default: non-recursive;
                  target/ and testdata/ are never entered)
    --recursive   Recurse into subdirectories (use with --dir)
    --overwrite   Overwrite existing .yaml files (default: skip)
    --merge       Refresh auto fields, keep manual ones (see _merge_data)
    --skip-tests  Leave out #[cfg(test)] / #[test] items and tests/ files
    --dry-run     Print what would be generated without writing files

Environment:
    RS_META_RSASTDUMP  Command that runs rsastdump (default: build it with
                       `cargo build -p rsastdump --locked` in this workspace).
"""

import argparse
import json
import os
import shlex
import shutil
import subprocess  # nosec B404 - drives cargo / rsastdump, argv lists only
import sys
import tomllib
from pathlib import Path

try:
    import yaml
except ImportError:
    print("PyYAML not found. Install with: pip install pyyaml", file=sys.stderr)
    sys.exit(1)

# Allowed tag values from rs.meta.schema.yaml (same vocabulary as Go).
_ALLOWED_TAGS = {
    "relay",
    "compliance",
    "workflow",
    "cictor",
    "schema",
    "parser",
    "guard",
    "core",
    "doc",
    "interface",
    "gateway",
    "builder",
    "test",
    "meta",
    "orchestrator",
    "decision",
    "reflector",
    "context",
    "validator",
    "executor",
    "hook",
    "template",
    "fallback",
    "session",
    "metrics",
    "storage",
    "loader",
    "renderer",
    "legal",
    "license",
    "platform-engineering",
    "ci-cd",
    "ecosystem",
}

# Crates of the Rust distribution: never part of the reference graph.
_STD_ROOTS = {"std", "core", "alloc", "proc_macro", "test"}

# Prelude names: a call through one of these is std, not a graph edge.
_PRELUDE = {
    "Some",
    "None",
    "Ok",
    "Err",
    "Box",
    "Vec",
    "String",
    "Option",
    "Result",
    "Default",
    "From",
    "Into",
    "TryFrom",
    "TryInto",
    "ToString",
    "ToOwned",
    "Clone",
    "Iterator",
    "IntoIterator",
    "drop",
    # primitive types: `str::lines`, `u8::from` are std
    "str",
    "bool",
    "char",
    "u8",
    "u16",
    "u32",
    "u64",
    "u128",
    "usize",
    "i8",
    "i16",
    "i32",
    "i64",
    "i128",
    "isize",
    "f32",
    "f64",
}

_SKIP_DIRS = {"target", "testdata", ".git", "p_venv", "source"}

_KIND_RANK = {
    "mod": 0,
    "const": 1,
    "static": 2,
    "type": 3,
    "struct": 4,
    "enum": 4,
    "union": 4,
    "trait": 5,
    "impl": 6,
    "method": 6,
    "fn": 7,
    "macro": 8,
}

_TAG_DEPS = {
    "interface": {"axum", "hyper", "reqwest", "tonic", "actix_web", "warp", "ureq"},
    "storage": {"sqlx", "rusqlite", "diesel", "sled", "redb"},
    "metrics": {"prometheus", "metrics", "opentelemetry"},
    "parser": {"syn", "nom", "pest"},
    "schema": {"jsonschema", "schemars"},
}


# ---------------------------------------------------------------------------
# Crate layout (Cargo.toml)
# ---------------------------------------------------------------------------


class CrateInfo:
    """What the generator needs from the owning Cargo.toml."""

    def __init__(self, root: Path, name: str, deps: set[str]):
        self.root = root
        self.name = name
        self.deps = deps


_crate_cache: dict[Path, CrateInfo | None] = {}


def _dep_names(manifest: dict) -> set[str]:
    """Local (in-code) names of every dependency table of a manifest."""
    tables = [manifest]
    tables += [t for t in manifest.get("target", {}).values() if isinstance(t, dict)]
    names: set[str] = set()
    for t in tables:
        for key in ("dependencies", "dev-dependencies", "build-dependencies"):
            names.update(n.replace("-", "_") for n in t.get(key, {}))
    return names


def _find_crate(rs_file: Path) -> CrateInfo | None:
    """The nearest Cargo.toml with a [package] above rs_file."""
    for parent in rs_file.resolve().parents:
        if parent in _crate_cache:
            return _crate_cache[parent]
        manifest = parent / "Cargo.toml"
        if not manifest.exists():
            continue
        try:
            data = tomllib.loads(manifest.read_text(encoding="utf-8"))
        except (OSError, tomllib.TOMLDecodeError):
            _crate_cache[parent] = None
            return None
        pkg = data.get("package")
        if not isinstance(pkg, dict):
            continue  # a virtual workspace manifest: keep walking
        info = CrateInfo(parent, pkg["name"].replace("-", "_"), _dep_names(data))
        _crate_cache[parent] = info
        return info
    return None


def _module_path(rs_file: Path, crate_root: Path | None) -> list[str]:
    """Module path of a file inside its crate target, `crate` excluded.

    src/lib.rs, src/main.rs, src/bin/x.rs, tests/x.rs, examples/x.rs,
    benches/x.rs and build.rs are target roots (`[]`); src/a/b.rs and
    src/a/b/mod.rs are `["a", "b"]`. `#[path]` attributes are not followed.
    """
    if crate_root is None:
        return []
    try:
        parts = list(rs_file.resolve().relative_to(crate_root).parts)
    except ValueError:
        return []
    if parts == ["build.rs"]:
        return []
    if parts and parts[0] == "src":
        parts = parts[1:]
        if parts in (["lib.rs"], ["main.rs"]):
            return []
        if parts and parts[0] == "bin":
            parts = parts[2:] if len(parts) > 2 else []
            if parts == ["main.rs"]:
                return []
    elif parts and parts[0] in ("tests", "examples", "benches"):
        parts = parts[2:] if len(parts) > 2 else []
        if parts == ["main.rs"]:
            return []
    if not parts:
        return []
    last = parts[-1].removesuffix(".rs")
    return parts[:-1] if last == "mod" else parts[:-1] + [last]


# ---------------------------------------------------------------------------
# Path classification
# ---------------------------------------------------------------------------


def _normalize(path: str, module: list[str]) -> str:
    """Make self::/super:: paths absolute (crate::…); others are unchanged."""
    segs = path.split("::")
    if segs[0] == "self":
        return "::".join(["crate", *module, *segs[1:]])
    if segs[0] == "super":
        base = list(module)
        while segs and segs[0] == "super":
            segs = segs[1:]
            if base:
                base.pop()
        return "::".join(["crate", *base, *segs])
    return path


def _is_external(first: str, crate: CrateInfo | None) -> bool:
    if crate is None:
        return False
    return first in crate.deps or first == crate.name


def _local_rest(path: str, module: list[str], file_mods: set[str]) -> str | None:
    """If path points into this file (its module and the inline modules in it),
    the file-relative rest ("Foo::new", "tests::helper"); None otherwise.

    `module` is the file's module path; `file_mods` are the out-of-line modules
    the file declares (`mod x;`): a path into one of those is cross-module."""
    segs = path.split("::")
    cur = ["crate", *module]
    if segs[: len(cur)] != cur or len(segs) == len(cur):
        return None
    if segs[len(cur)] in file_mods:
        return None
    return "::".join(segs[len(cur) :])


def _keep_ref(
    path: str, module: list[str], file_mods: set[str], crate: CrateInfo | None
) -> bool:
    first = path.split("::")[0]
    if first == "crate":
        return _local_rest(path, module, file_mods) is None
    return _is_external(first, crate)


def _format_call(path: str, module: list[str], file_mods: set[str]) -> str | None:
    first = path.split("::")[0]
    if first in _STD_ROOTS or first in _PRELUDE:
        return None
    if first == "crate":
        return _local_rest(path, module, file_mods) or path
    return path


# ---------------------------------------------------------------------------
# rsastdump front end
# ---------------------------------------------------------------------------

_WORKSPACE = Path(__file__).resolve().parents[1]
_DUMP_CHUNK = 400  # files per rsastdump invocation (argv length)
_dump_cmd: list[str] | None = None


def _dump_command() -> list[str] | None:
    """Return the command that runs rsastdump, or None if it cannot be had.

    RS_META_RSASTDUMP overrides it with a prebuilt binary (or any command).
    Otherwise the workspace member is built once with the `cargo` on PATH.
    """
    global _dump_cmd
    if _dump_cmd is not None:
        return _dump_cmd
    override = os.environ.get("RS_META_RSASTDUMP")
    if override:
        _dump_cmd = shlex.split(override)
        return _dump_cmd
    cargo = shutil.which("cargo")
    if not cargo or not (_WORKSPACE / "tools" / "rsastdump" / "Cargo.toml").exists():
        return None
    res = subprocess.run(
        [cargo, "build", "-q", "-p", "rsastdump", "--locked"],
        cwd=_WORKSPACE,  # nosec B603 - argv list, no shell, builds this repo's own helper
        capture_output=True,
        text=True,
    )
    if res.returncode != 0:
        print(f"rsastdump build failed:\n{res.stderr}", file=sys.stderr)
        return None
    target = Path(os.environ.get("CARGO_TARGET_DIR") or _WORKSPACE / "target")
    if not target.is_absolute():
        target = _WORKSPACE / target
    _dump_cmd = [str(target / "debug" / "rsastdump")]
    return _dump_cmd


def _rs_dump(files: list[Path]) -> dict[Path, dict]:
    """Run rsastdump over files; returns {resolved path: per-file dump}."""
    cmd = _dump_command()
    if cmd is None:
        raise RuntimeError("rsastdump unavailable (no cargo on PATH)")
    result: dict[Path, dict] = {}
    resolved = [f.resolve() for f in files]
    for i in range(0, len(resolved), _DUMP_CHUNK):
        chunk = resolved[i : i + _DUMP_CHUNK]
        res = subprocess.run(
            cmd
            + [
                str(f) for f in chunk
            ],  # nosec B603 - argv list, no shell; cmd is the helper or RS_META_RSASTDUMP
            capture_output=True,
            text=True,
        )
        if res.returncode != 0:
            raise RuntimeError(f"rsastdump failed: {res.stderr.strip()}")
        for entry in json.loads(res.stdout)["files"]:
            result[Path(entry["file"])] = entry
    return result


# ---------------------------------------------------------------------------
# Dump -> companion data
# ---------------------------------------------------------------------------


def _qualified(item: dict) -> str:
    return "::".join([*item.get("mod_path", []), item["name"]])


def _objects_from_dump(
    dump: dict,
    module: list[str],
    crate: CrateInfo | None,
    skip_tests: bool = False,
) -> list[dict]:
    """Turn an rsastdump file entry into the companion's objects list."""
    inline = {i["mod_path"][0] for i in dump["items"] if i.get("mod_path")}
    file_mods = set(dump.get("mods", [])) - inline  # out-of-line `mod x;` only
    items = [i for i in dump["items"] if not (skip_tests and i.get("test"))]

    # same-file trait impls: Type -> {trait, ...}
    impls: dict[str, set[str]] = {}
    for it in items:
        if it["kind"] == "impl" and it.get("trait") and not it["name"].startswith("!"):
            mod = module + it.get("mod_path", [])
            recv = it.get("receiver", "").split("::")[-1]
            impls.setdefault(recv, set()).add(_normalize(it["trait"], mod))

    objects: list[dict] = []
    for it in items:
        kind = it["kind"]
        mod = module + it.get("mod_path", [])
        refs: list[str] = []
        for r in it.get("refs", []):
            n = _normalize(r, mod)
            if _keep_ref(n, module, file_mods, crate) and n not in refs:
                refs.append(n)
        calls = sorted(
            {
                c
                for c in (
                    _format_call(_normalize(x, mod), module, file_mods)
                    for x in it.get("calls", [])
                )
                if c
            }
        )
        obj: dict = {"name": _qualified(it), "kind": kind}
        if kind in ("method", "impl"):
            obj["receiver"] = it.get("receiver", "")
            if it.get("trait"):
                obj["trait"] = _normalize(it["trait"], mod)
        if "visibility" in it:
            obj["visibility"] = it["visibility"]
        obj["description"] = it.get("doc", "")
        if kind in ("struct", "enum", "union"):
            obj["derives"] = [_normalize(d, mod) for d in it.get("derives") or []]
            obj["implements"] = sorted(impls.get(it["name"], set()))
        if kind == "enum":
            obj["variants"] = it.get("variants") or []
        if kind == "trait":
            obj["methods"] = it.get("methods") or []
            obj["supertraits"] = [
                _normalize(s, mod) for s in it.get("supertraits") or []
            ]
        if kind not in ("mod", "macro"):
            obj["references"] = refs
        if kind in ("fn", "method", "trait"):
            obj["calls"] = calls
        if it.get("test"):
            obj["test"] = True
        objects.append(obj)
    objects.sort(key=lambda o: _KIND_RANK.get(o["kind"], 99))  # stable
    return objects


def _suggest_tags(
    rs_file: Path, objects: list[dict], crate: CrateInfo | None
) -> list[str]:
    """Suggest tags from file path, used dependencies and naming conventions.

    These are suggestions — review and trim before committing the YAML.
    """
    tags: set[str] = set()
    path_str = str(rs_file).replace("\\", "/")
    if path_str.endswith("/src/main.rs") or "/src/bin/" in path_str:
        tags.update(["core", "gateway"])
    # an inline #[cfg(test)] mod is routine in Rust: only test-only files count
    if "/tests/" in path_str or (objects and all(o.get("test") for o in objects)):
        tags.add("test")
    if "/tools/" in path_str or rs_file.name == "build.rs":
        tags.add("builder")
    if "/core/" in path_str:
        tags.add("core")

    used = {
        r.split("::")[0]
        for o in objects
        for r in o.get("references", []) + o.get("calls", [])
    }
    for tag, deps in _TAG_DEPS.items():
        if used & deps:
            tags.add(tag)

    for o in objects:
        name = o["name"].split("::")[-1]
        if name.startswith(("validate", "Validate")) or name.endswith("Validator"):
            tags.add("validator")
        if name.startswith(("execute", "Execute")) or name.endswith("Executor"):
            tags.add("executor")
        if name.startswith(("load", "Load")) or name.endswith("Loader"):
            tags.add("loader")
        if name.endswith(("Handler", "Server", "Client")):
            tags.add("interface")
        if name.endswith(("Store", "Repository")):
            tags.add("storage")
        if "metric" in name.lower():
            tags.add("metrics")
        if "schema" in name.lower():
            tags.add("schema")
        if name.startswith(("parse", "Parse")) or name.endswith("Parser"):
            tags.add("parser")
        if name.startswith(("hook", "Hook")) or name.endswith("Hook"):
            tags.add("hook")
    return sorted(tags & _ALLOWED_TAGS)


def generate(rs_file: Path, dump: dict, skip_tests: bool = False) -> dict:
    """Build the companion data for one Rust file from its rsastdump entry."""
    crate = _find_crate(rs_file)
    module = _module_path(rs_file, crate.root if crate else None)
    objects = _objects_from_dump(dump, module, crate, skip_tests)
    is_lib = rs_file.name == "lib.rs" and rs_file.parent.name == "src"
    entrypoint = (
        not module
        and not is_lib
        and any(o["kind"] == "fn" and o["name"] == "main" for o in objects)
    ) or (rs_file.name == "build.rs" and not module)
    return {
        "crate": crate.name if crate else "",
        "module": "::".join(["crate", *module]),
        "description": dump.get("inner_doc", ""),
        "tags": _suggest_tags(rs_file, objects, crate),
        "category": [],
        "used_in": [],
        "entrypoint": entrypoint,
        "related_nodes": [],
        "objects": objects,
    }


# ---------------------------------------------------------------------------
# Merge and cross-file implements
# ---------------------------------------------------------------------------


def _obj_key(o: dict) -> tuple[str, str, str]:
    return (o.get("kind", ""), o.get("receiver", ""), o.get("name", ""))


def _merge_data(new_data: dict, old_data: dict) -> dict:
    """Merge freshly generated data into an existing YAML.

    Auto fields (always taken from source): crate, module, entrypoint and every
    structural object field (kind, visibility, receiver, trait, derives,
    implements, variants, methods, supertraits, references, calls, test).

    Description fields: the doc comment wins when there is one, otherwise the
    existing (hand-written) description is kept.

    Manual fields (never touched): tags, category, used_in, related_nodes.

    Objects are matched by (kind, receiver, name) — Rust methods share names
    across types (new, fmt, from) — new ones are added, vanished ones dropped.
    """
    merged = dict(new_data)
    merged["tags"] = old_data.get("tags", new_data.get("tags", []))
    merged["category"] = old_data.get("category", [])
    merged["used_in"] = old_data.get("used_in", [])
    merged["related_nodes"] = old_data.get("related_nodes", [])
    if not merged.get("description"):
        merged["description"] = old_data.get("description", "")

    old_by_key = {_obj_key(o): o for o in old_data.get("objects", [])}
    objects = []
    for new_obj in new_data.get("objects", []):
        obj = dict(new_obj)
        old_obj = old_by_key.get(_obj_key(new_obj))
        if old_obj is not None and not obj.get("description"):
            obj["description"] = old_obj.get("description", "")
        objects.append(obj)
    merged["objects"] = objects
    return merged


def _companions(crate_root: Path) -> list[Path]:
    """Companion YAMLs of every .rs file of a crate (target/ etc. skipped)."""
    out = []
    for rs in sorted(crate_root.rglob("*.rs")):
        rel = rs.relative_to(crate_root).parts
        if any(p in _SKIP_DIRS for p in rel):
            continue
        y = rs.with_suffix(".yaml")
        if y.exists():
            out.append(y)
    return out


def _detect_implements(yaml_files: list[Path]) -> dict[str, set[str]]:
    """{type name: {trait, ...}} from the impl objects of the given companions.

    Types are matched by their last path segment: two same-named types in one
    crate share the result (no name resolution across files).
    """
    result: dict[str, set[str]] = {}
    for yf in yaml_files:
        try:
            data = yaml.safe_load(yf.read_text(encoding="utf-8")) or {}
        except (
            Exception
        ):  # nosec B112 - one malformed companion must not abort the scan
            continue
        for o in data.get("objects", []):
            if o.get("kind") == "impl" and o.get("trait") and "!" not in o["name"]:
                recv = str(o.get("receiver", "")).split("::")[-1]
                result.setdefault(recv, set()).add(o["trait"])
    return result


def _apply_implements(yaml_files: list[Path]) -> int:
    """Rewrite `implements` of every type in yaml_files; returns files changed."""
    impl_map = _detect_implements(yaml_files)
    changed_files = 0
    for yf in yaml_files:
        try:
            data = yaml.safe_load(yf.read_text(encoding="utf-8")) or {}
        except (
            Exception
        ):  # nosec B112 - one malformed companion must not abort the pass
            continue
        changed = False
        for o in data.get("objects", []):
            if o.get("kind") in ("struct", "enum", "union"):
                impls = sorted(impl_map.get(o["name"].split("::")[-1], set()))
                if o.get("implements") != impls:
                    o["implements"] = impls
                    changed = True
        if changed:
            _write_yaml(data, yf)
            changed_files += 1
    return changed_files


def _write_yaml(data: dict, path: Path) -> None:
    with open(path, "w", encoding="utf-8") as f:
        f.write("---\n")
        yaml.dump(
            data, f, allow_unicode=True, default_flow_style=False, sort_keys=False
        )


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _collect(args: argparse.Namespace) -> list[Path]:
    files: list[Path] = [Path(f) for f in args.files]
    if args.dir:
        d = Path(args.dir)
        found = d.rglob("*.rs") if args.recursive else d.glob("*.rs")
        files += [
            f
            for f in found
            if not any(p in _SKIP_DIRS for p in f.relative_to(d).parts[:-1])
        ]
    return sorted(set(files))


def _is_test_file(rs_file: Path) -> bool:
    return "tests" in rs_file.parts[:-1]


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Generate companion YAML skeletons for Rust source files.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "files", nargs="*", metavar="FILE", help="Rust source files to process"
    )
    parser.add_argument("--dir", metavar="DIR", help="Process all .rs files in DIR")
    parser.add_argument(
        "--recursive",
        action="store_true",
        help="Recurse into subdirectories (use with --dir)",
    )
    parser.add_argument(
        "--overwrite",
        action="store_true",
        help="Overwrite existing YAML files completely",
    )
    parser.add_argument(
        "--merge",
        action="store_true",
        help="Refresh auto fields while preserving manual ones (tags, category, "
        "used_in, related_nodes, hand-written descriptions). Implies --overwrite "
        "for existing files.",
    )
    parser.add_argument(
        "--skip-tests",
        action="store_true",
        help="Leave out #[cfg(test)]/#[test] items and files under tests/",
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="Print actions without writing files"
    )
    args = parser.parse_args()

    files = _collect(args)
    if not files:
        parser.print_help()
        sys.exit(1)

    todo = [
        f
        for f in files
        if f.exists()
        and not (args.skip_tests and _is_test_file(f))
        and (args.overwrite or args.merge or not f.with_suffix(".yaml").exists())
    ]
    try:
        dumps = _rs_dump(todo) if todo else {}
    except RuntimeError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        sys.exit(2)

    generated = merged_count = skipped = errors = 0
    crates_touched: set[Path] = set()
    for rs_file in files:
        if not rs_file.exists():
            print(f"NOT FOUND: {rs_file}", file=sys.stderr)
            errors += 1
            continue
        if args.skip_tests and _is_test_file(rs_file):
            continue
        yaml_file = rs_file.with_suffix(".yaml")
        existing = yaml_file.exists()
        if existing and not args.overwrite and not args.merge:
            print(f"SKIP (exists): {yaml_file}")
            skipped += 1
            continue

        dump = dumps.get(rs_file.resolve())
        if dump is None or dump.get("error"):
            reason = dump["error"] if dump else "no rsastdump output"
            print(f"ERROR: {rs_file}: {reason}", file=sys.stderr)
            errors += 1
            continue
        data = generate(rs_file, dump, args.skip_tests)

        if existing and args.merge:
            try:
                old_data = yaml.safe_load(yaml_file.read_text(encoding="utf-8")) or {}
            except Exception as e:
                print(f"ERROR reading existing {yaml_file}: {e}", file=sys.stderr)
                errors += 1
                continue
            data = _merge_data(data, old_data)
            action = "MERGED"
            merged_count += 1
        else:
            action = "GENERATED"
            generated += 1

        obj_count = len(data["objects"])
        ref_count = sum(len(o.get("references", [])) for o in data["objects"])
        if args.dry_run:
            print(
                f"DRY-RUN ({action}): {yaml_file}  [{obj_count} objects, {ref_count} refs]"
            )
        else:
            _write_yaml(data, yaml_file)
            print(f"{action}: {yaml_file}  [{obj_count} objects, {ref_count} refs]")
            crate = _find_crate(rs_file)
            if crate is not None:
                crates_touched.add(crate.root)

    print(
        f"\nDone: {generated} generated, {merged_count} merged, "
        f"{skipped} skipped, {errors} errors"
    )

    # implements across files: impl blocks often live apart from the type
    for root in sorted(crates_touched):
        n = _apply_implements(_companions(root))
        if n:
            print(f"IMPLEMENTS: {root.name} — {n} companion(s) updated")

    if errors:
        sys.exit(1)


if __name__ == "__main__":
    main()
