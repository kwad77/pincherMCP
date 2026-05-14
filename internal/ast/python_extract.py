"""Pincher Python AST extractor (embedded via //go:embed).

Reads source from stdin, takes <relpath> as argv[1]. Emits a single JSON
object on stdout:

    {"symbols": [...], "edges": [...], "module": "..."}

or, on SyntaxError:

    {"error": "..."}

Exits 0 in both cases so the Go caller distinguishes parse failure from
process failure by inspecting the JSON. Pure stdlib, Python 3.8+.
"""

import ast
import json
import sys


def build_line_offsets(source):
    """Byte offset of each 1-indexed line. Slot 0 unused; slot 1 = 0."""
    offsets = [0, 0]
    for i, b in enumerate(source):
        if b == 0x0A:
            offsets.append(i + 1)
    return offsets


def byte_offset(line_offsets, lineno, col_offset):
    if lineno < 1 or lineno >= len(line_offsets):
        return 0
    return line_offsets[lineno] + (col_offset or 0)


def module_qn(relpath):
    """Match moduleQN() in extractor.go: strip extension, slashes → dots."""
    base = relpath
    dot = base.rfind(".")
    if dot > 0:
        base = base[:dot]
    return base.replace("/", ".").replace("\\", ".")


def is_test_name(name):
    return name.startswith("test_") or name.startswith("Test")


def collect_dunder_all(tree):
    """Module-level `__all__ = [...]`: return the set of names, or None."""
    for node in tree.body:
        if not isinstance(node, ast.Assign):
            continue
        for target in node.targets:
            if isinstance(target, ast.Name) and target.id == "__all__":
                names = _literal_string_seq(node.value)
                if names is not None:
                    return names
    return None


def _literal_string_seq(value):
    if not isinstance(value, (ast.List, ast.Tuple)):
        return None
    names = set()
    for elt in value.elts:
        if isinstance(elt, ast.Constant) and isinstance(elt.value, str):
            names.add(elt.value)
        else:
            return None
    return names


def safe_unparse(node):
    """ast.unparse on 3.9+; empty string on older Python."""
    if node is None:
        return ""
    try:
        return ast.unparse(node)
    except (AttributeError, NotImplementedError):
        return ""


def build_signature(node):
    """Reconstruct the def/class line with decorators, async, annotations."""
    lines = []
    for dec in getattr(node, "decorator_list", []) or []:
        lines.append("@" + safe_unparse(dec))
    if isinstance(node, ast.ClassDef):
        parts = [safe_unparse(b) for b in node.bases]
        parts += [
            (kw.arg + "=" if kw.arg else "**") + safe_unparse(kw.value)
            for kw in node.keywords
        ]
        suffix = "(" + ", ".join(parts) + ")" if parts else ""
        lines.append("class " + node.name + suffix + ":")
    else:
        prefix = "async def" if isinstance(node, ast.AsyncFunctionDef) else "def"
        args = safe_unparse(node.args) if node.args else ""
        ret = ""
        if node.returns is not None:
            ret = " -> " + safe_unparse(node.returns)
        lines.append(prefix + " " + node.name + "(" + args + ")" + ret + ":")
    return "\n".join(lines)


def collect(node, parent_qn, dunder_all, line_offsets, source_len,
            in_class, symbols):
    """Walk child nodes; emit one record per FunctionDef/AsyncFunctionDef/ClassDef."""
    for child in ast.iter_child_nodes(node):
        if not isinstance(
            child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)
        ):
            continue
        name = child.name
        qn = parent_qn + "." + name if parent_qn else name

        if isinstance(child, ast.ClassDef):
            kind = "Class"
        elif in_class:
            kind = "Method"
        else:
            kind = "Function"

        start_line = child.lineno
        # Account for decorator lines so StartByte points at the first @decorator.
        if getattr(child, "decorator_list", None):
            first_dec = child.decorator_list[0]
            start_line = min(start_line, first_dec.lineno)
        start_byte = byte_offset(line_offsets, start_line, 0)

        end_line = getattr(child, "end_lineno", None) or start_line
        end_col = getattr(child, "end_col_offset", 0) or 0
        end_byte = byte_offset(line_offsets, end_line, end_col)
        if end_byte > source_len:
            end_byte = source_len

        if dunder_all is not None:
            is_exp = name in dunder_all
        else:
            is_exp = not name.startswith("_")

        symbols.append({
            "name": name,
            "qualified_name": qn,
            "kind": kind,
            "parent": parent_qn if kind == "Method" else "",
            "signature": build_signature(child),
            "docstring": ast.get_docstring(child, clean=True) or "",
            "is_exported": is_exp,
            "is_test": is_test_name(name),
            "start_byte": start_byte,
            "end_byte": end_byte,
            "start_line": start_line,
            "end_line": end_line,
        })

        collect(
            child, qn, dunder_all, line_offsets, source_len,
            in_class=isinstance(child, ast.ClassDef), symbols=symbols,
        )


def collect_imports(tree, module):
    edges = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                edges.append({
                    "from_qn": module,
                    "to_name": alias.name,
                    "kind": "IMPORTS",
                    "confidence": 1.0,
                })
        elif isinstance(node, ast.ImportFrom):
            base = node.module or ""
            if node.level:
                base = ("." * node.level) + base
            for alias in node.names:
                target = base + "." + alias.name if base else alias.name
                edges.append({
                    "from_qn": module,
                    "to_name": target,
                    "kind": "IMPORTS",
                    "confidence": 1.0,
                })
    return edges


def main():
    relpath = sys.argv[1] if len(sys.argv) > 1 else ""
    module = module_qn(relpath)
    source = sys.stdin.buffer.read()

    try:
        tree = ast.parse(source)
    except SyntaxError as e:
        json.dump({"error": str(e)}, sys.stdout)
        return

    line_offsets = build_line_offsets(source)
    dunder_all = collect_dunder_all(tree)
    symbols = []

    # Emit one Module symbol per file so IMPORTS edges have a stable
    # endpoint on both sides (matches the Go extractor's convention at
    # extractor.go:432-448). Without this, every Python IMPORTS edge
    # would lack a resolvable from-side and stay in pending_edges.
    last_line = max(1, len(line_offsets) - 1)
    short_name = module.rsplit(".", 1)[-1] if module else ""
    symbols.append({
        "name": short_name,
        "qualified_name": module,
        "kind": "Module",
        "parent": "",
        "signature": "",
        "docstring": ast.get_docstring(tree, clean=True) or "",
        "is_exported": True,
        "is_test": False,
        "start_byte": 0,
        "end_byte": len(source),
        "start_line": 1,
        "end_line": last_line,
    })

    collect(
        tree, parent_qn=module, dunder_all=dunder_all,
        line_offsets=line_offsets, source_len=len(source),
        in_class=False, symbols=symbols,
    )
    edges = collect_imports(tree, module)

    json.dump(
        {"symbols": symbols, "edges": edges, "module": module},
        sys.stdout,
    )


if __name__ == "__main__":
    main()
