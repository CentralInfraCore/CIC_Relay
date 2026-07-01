#!/usr/bin/env python3
"""
Go AST extractor — augments go.meta.gen.py with precise AST metadata.

Extracts:
  - position (start_line, end_line)
  - signature (params, returns) for funcs/methods
  - calls (functions called within a func/method body)
  - fields (struct field names, types, tags)
  - methods (interface method signatures)
  - interfaces (interfaces implemented by a type — via Named.Interface pattern)
  - exported (whether the symbol is exported)

Output is a JSON object suitable for merging into go.meta.gen.py object dicts.
"""

import re
from pathlib import Path


def extract_ast(go_file: Path) -> dict:
    """Extract AST metadata from a Go source file.

    Returns {object_key: ObjectMeta, ...} where object_key is:
      - "FuncName" for functions
      - "TypeName" for types/structs/interfaces
      - "ReceiverType.MethodName" for methods (matched to go.meta.gen.py's receiver field)

    Returns empty dict if file doesn't exist or can't be read.
    """
    try:
        source = go_file.read_text(encoding="utf-8")
    except (FileNotFoundError, IsADirectoryError, PermissionError):
        return {}

    lines = source.split("\n")
    result = {}

    # Extract all definitions with line numbers
    funcs = _extract_funcs(source, lines)
    types = _extract_types(source, lines)

    for func_info in funcs:
        key = _make_key(func_info)
        result[key] = {
            "kind": func_info["kind"],
            "signature": func_info.get("signature"),
            "calls": func_info.get("calls", []),
            "position": func_info["position"],
            "exported": func_info["exported"],
        }

    for type_info in types:
        key = type_info["name"]
        obj = {
            "kind": type_info["kind"],
            "position": type_info["position"],
            "exported": type_info["exported"],
        }
        if "fields" in type_info:
            obj["fields"] = type_info["fields"]
        if "methods" in type_info:
            obj["methods"] = type_info["methods"]
        result[key] = obj

    return result


def _make_key(func_info: dict) -> str:
    """Make a key for a function/method matching go.meta.gen.py's object_key format."""
    if func_info["kind"] == "method":
        return func_info["receiver"] + "." + func_info["name"]
    else:
        return func_info["name"]


def _extract_funcs(source: str, lines: list) -> list:
    """Extract all function and method declarations with positions and signatures."""
    funcs = []

    # Pattern: func [receiver] name(params) returns { ... }
    # Simplified: match func declarations, extract receiver (optional), name, params, returns
    pattern = re.compile(
        r"^\s*func\s+"
        r"(?:\(\s*(\w+)\s+\*?(\w+)\s*\)\s*)?"  # optional receiver: ($var $Type)
        r"(\w+)\s*"  # func name
        r"\(([^)]*(?:\([^)]*\)[^)]*)*)\)"  # params (with nested parens handling)
        r"(.*?)(?={)",  # return types
        re.MULTILINE,
    )

    for m in pattern.finditer(source):
        recv_var, recv_type, name, params_str, returns_str = m.groups()
        line_num = source[: m.start()].count("\n") + 1

        # Find the closing brace to get end line
        brace_pos = source.find("{", m.end())
        if brace_pos == -1:
            end_line = line_num
        else:
            end_line = source[:brace_pos:].count("\n") + 1
            # Find closing brace by brace matching
            depth = 1
            pos = brace_pos + 1
            while pos < len(source) and depth > 0:
                if source[pos] == "{":
                    depth += 1
                elif source[pos] == "}":
                    depth -= 1
                pos += 1
            end_line = source[:pos].count("\n") + 1

        func_info = {
            "name": name,
            "kind": "method" if recv_type else "func",
            "position": {"start_line": line_num, "end_line": end_line},
            "exported": name[0].isupper(),
            "signature": {
                "params": _parse_params(params_str),
                "returns": _parse_returns(returns_str),
            },
        }

        if recv_type:
            func_info["receiver"] = recv_type

        # Extract calls from function body
        if brace_pos != -1:
            body_start = brace_pos + 1
            depth = 1
            pos = body_start
            while pos < len(source) and depth > 0:
                if source[pos] == "{":
                    depth += 1
                elif source[pos] == "}":
                    depth -= 1
                pos += 1
            body = source[body_start : pos - 1]
            func_info["calls"] = _extract_calls(body)
        else:
            func_info["calls"] = []

        funcs.append(func_info)

    return funcs


def _extract_types(source: str, lines: list) -> list:
    """Extract all type/struct/interface declarations."""
    types = []

    # Pattern: type Name struct { ... } or type Name interface { ... }
    struct_pattern = re.compile(
        r"^\s*type\s+(\w+)\s+struct\s*\{(.*?)^\s*\}", re.MULTILINE | re.DOTALL
    )

    iface_pattern = re.compile(
        r"^\s*type\s+(\w+)\s+interface\s*\{(.*?)^\s*\}", re.MULTILINE | re.DOTALL
    )

    # Structs
    for m in struct_pattern.finditer(source):
        name, body = m.groups()
        start_line = source[: m.start()].count("\n") + 1
        end_line = source[: m.end()].count("\n") + 1

        types.append(
            {
                "name": name,
                "kind": "struct",
                "position": {"start_line": start_line, "end_line": end_line},
                "exported": name[0].isupper(),
                "fields": _parse_struct_fields(body),
            }
        )

    # Interfaces
    for m in iface_pattern.finditer(source):
        name, body = m.groups()
        start_line = source[: m.start()].count("\n") + 1
        end_line = source[: m.end()].count("\n") + 1

        types.append(
            {
                "name": name,
                "kind": "interface",
                "position": {"start_line": start_line, "end_line": end_line},
                "exported": name[0].isupper(),
                "methods": _parse_interface_methods(body),
            }
        )

    # Type aliases (type Name TypeExpr, but not struct/interface)
    type_pattern = re.compile(
        r"^\s*type\s+(\w+)\s+(?!struct|interface)(\S+)", re.MULTILINE
    )

    for m in type_pattern.finditer(source):
        name = m.group(1)
        # Skip if already added as struct/interface
        if any(t["name"] == name for t in types):
            continue

        start_line = source[: m.start()].count("\n") + 1
        types.append(
            {
                "name": name,
                "kind": "type",
                "position": {"start_line": start_line, "end_line": start_line},
                "exported": name[0].isupper(),
            }
        )

    return types


def _parse_params(params_str: str) -> list:
    """Parse function parameters into [{'name': ..., 'type': ...}, ...]"""
    if not params_str or not params_str.strip():
        return []

    params = []
    # Simplified: split by comma, but respect nested parens/brackets
    parts = _split_params(params_str)

    for part in parts:
        part = part.strip()
        if not part:
            continue

        # Try to match "name type" pattern
        m = re.match(r"(\w+)\s+(.+)", part)
        if m:
            params.append({"name": m.group(1), "type": m.group(2)})
        else:
            # No name, just type
            params.append({"name": "", "type": part})

    return params


def _parse_returns(returns_str: str) -> list:
    """Parse function return types into ['type1', 'type2', ...]"""
    if not returns_str or not returns_str.strip():
        return []

    returns_str = returns_str.strip()

    # If starts with (, it's a parenthesized return list
    if returns_str.startswith("("):
        returns_str = returns_str[1:]
        if returns_str.endswith(")"):
            returns_str = returns_str[:-1]
        parts = _split_params(returns_str)
    else:
        parts = [returns_str]

    result = []
    for part in parts:
        part = part.strip()
        # Named return: name type → extract type
        if m := re.match(r"(\w+)\s+(.+)", part):
            result.append(m.group(2))
        elif part:
            result.append(part)

    return result


def _split_params(params_str: str) -> list:
    """Split parameters by comma, respecting nested brackets/parens."""
    parts = []
    current: list = []
    depth = 0

    for char in params_str:
        if char in "([{":
            depth += 1
        elif char in ")]}":
            depth -= 1
        elif char == "," and depth == 0:
            parts.append("".join(current))
            current = []
            continue
        current.append(char)

    if current:
        parts.append("".join(current))

    return parts


def _extract_calls(body: str) -> list:
    """Extract function calls from a function body."""
    calls = set()

    # Pattern: identifier(args) or identifier.identifier(args)
    pattern = re.compile(r"(\w+)(?:\.(\w+))?\s*\(")

    for m in pattern.finditer(body):
        if m.group(2):
            calls.add(m.group(1) + "." + m.group(2))
        else:
            # Filter out keywords
            if m.group(1) not in {
                "if",
                "for",
                "switch",
                "defer",
                "go",
                "make",
                "len",
                "cap",
                "range",
                "return",
                "panic",
                "recover",
                "close",
                "copy",
                "delete",
                "append",
                "real",
                "imag",
                "complex",
            }:
                calls.add(m.group(1))

    return sorted(list(calls))


def _parse_struct_fields(body: str) -> list:
    """Extract struct fields from struct body."""
    fields = []

    # Pattern: name type `tag` or embedded type (matched inline per line below).
    for line in body.split("\n"):
        line = line.strip()
        if not line or line.startswith("//"):
            continue

        m = re.match(r"(\w+)?\s+(.+?)(?:\s+`([^`]*)`)?$", line)
        if m:
            name = m.group(1) or m.group(2)  # name or embedded type
            field_type = m.group(2) if m.group(1) else m.group(2)
            tag = m.group(3) or ""

            fields.append(
                {
                    "name": name.strip(),
                    "type": field_type.strip(),
                    "tag": tag.strip() if tag else "",
                }
            )

    return fields


def _parse_interface_methods(body: str) -> list:
    """Extract interface methods from interface body."""
    methods = []

    # Pattern: name(params) returntype(s) (matched inline per line below).
    for line in body.split("\n"):
        line = line.strip()
        if not line or line.startswith("//"):
            continue

        m = re.match(r"^(\w+)\s*\(([^)]*)\)(?:\s+(.+))?$", line)
        if m:
            name = m.group(1)
            params = _split_params(m.group(2)) if m.group(2) else []
            returns = _split_params(m.group(3)) if m.group(3) else []

            methods.append(
                {
                    "name": name,
                    "params": [p.strip() for p in params if p.strip()],
                    "returns": [r.strip() for r in returns if r.strip()],
                }
            )

    return methods


if __name__ == "__main__":
    import json
    import sys

    if len(sys.argv) < 2:
        print("usage: go_ast_extract.py <go-file>", file=sys.stderr)
        sys.exit(1)

    go_file = Path(sys.argv[1])
    result = extract_ast(go_file)
    print(json.dumps(result, indent=2))
