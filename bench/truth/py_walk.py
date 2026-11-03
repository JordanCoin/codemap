#!/usr/bin/env python3
"""Python truth: file-level import edges resolved by the interpreter.

Run this with the venv interpreter in which the repository was installed
(`pip install -e .`), so importlib resolves exactly what that environment
would import. The import statements come from the `ast` module (a real
parser, never a regex); each name is resolved with importlib.util.find_spec
and kept only when the spec's origin is an in-repo .py file.

Scope (see bench/README.md):
  sources = every in-repo .py file outside .git, venvs, build and dist,
            node_modules and site-packages
  targets = the final module named by the statement: `import a.b.c` -> a/b/c.py
            (or a/b/c/__init__.py); `from a.b import c` -> a/b/c.py when a.b.c
            is a module, else a/b/__init__.py or a/b.py; relative imports are
            resolved against the file's package (computed by walking up
            through directories that contain __init__.py). Parent packages
            that Python also executes on the way are not edges.
  Imports inside try/except, functions and `if TYPE_CHECKING:` blocks count:
  they are import statements and the resolver sees them the same way.

usage: <venv-python> -I py_walk.py <repo-root> <out-edges-file>
"""
import ast
import importlib.util
import json
import os
import sys

SKIP = {".git", "node_modules", "site-packages", "build", "dist", "__pycache__", ".venv", "venv", ".tox", ".eggs"}


def walk(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP and not d.endswith(".egg-info"))
        for f in sorted(filenames):
            if f.endswith(".py"):
                yield os.path.join(dirpath, f)


def package_of(file, root):
    """Dotted package name for the directory containing file, or '' when the
    directory is not inside a package (no __init__.py chain)."""
    parts = []
    d = os.path.dirname(file)
    while os.path.isfile(os.path.join(d, "__init__.py")) and (d == root or d.startswith(root + os.sep)):
        parts.append(os.path.basename(d))
        d = os.path.dirname(d)
    return ".".join(reversed(parts))


def module_name_of(file, root):
    pkg = package_of(file, root)
    base = os.path.splitext(os.path.basename(file))[0]
    if base == "__init__":
        return pkg
    return f"{pkg}.{base}" if pkg else base


def spec_file(name, root):
    try:
        spec = importlib.util.find_spec(name)
    except (ImportError, ValueError, AttributeError, TypeError):
        return None
    except Exception:  # noqa: BLE001 (a package __init__ can raise anything on import of its parent)
        return None
    if spec is None or not spec.origin or spec.origin in ("built-in", "frozen", "namespace"):
        return None
    origin = os.path.realpath(spec.origin)
    if not origin.endswith(".py"):
        return None
    if origin == root or origin.startswith(root + os.sep):
        rel = os.path.relpath(origin, root).replace(os.sep, "/")
        if any(part in SKIP for part in rel.split("/")[:-1]):
            return None
        return rel
    return None


def resolve_relative(module, level, package):
    if not package:
        return None
    try:
        return importlib.util.resolve_name("." * level + (module or ""), package)
    except (ImportError, ValueError):
        return None


def main(root, out_path):
    root = os.path.realpath(root)
    sources, edges = set(), set()
    stats = {"source_files": 0, "parse_errors": 0, "statements": 0, "resolved_in_repo": 0, "unresolved": 0}
    for file in walk(root):
        rel = os.path.relpath(file, root).replace(os.sep, "/")
        sources.add(rel)
        try:
            tree = ast.parse(open(file, "rb").read(), filename=file)
        except (SyntaxError, ValueError):
            stats["parse_errors"] += 1
            continue
        package = package_of(file, root)
        # For `from . import x` inside __init__.py the package is the module itself.
        self_name = module_name_of(file, root)
        is_init = os.path.basename(file) == "__init__.py"
        for node in ast.walk(tree):
            targets = []
            if isinstance(node, ast.Import):
                for alias in node.names:
                    stats["statements"] += 1
                    targets.append(alias.name)
            elif isinstance(node, ast.ImportFrom):
                stats["statements"] += 1
                if node.level:
                    base_pkg = self_name if is_init else package
                    base = resolve_relative(node.module, node.level, base_pkg)
                else:
                    base = node.module
                if base is None:
                    stats["unresolved"] += len(node.names) or 1
                    continue
                for alias in node.names:
                    if alias.name == "*":
                        targets.append(base)
                    else:
                        sub = f"{base}.{alias.name}"
                        if spec_file(sub, root) is not None:
                            targets.append(sub)
                        else:
                            targets.append(base)
            else:
                continue
            for name in targets:
                dst = spec_file(name, root)
                if dst is None:
                    stats["unresolved"] += 1
                    continue
                if dst != rel:
                    edges.add((rel, dst))
                    stats["resolved_in_repo"] += 1
    stats["source_files"] = len(sources)
    with open(out_path, "w") as fh:
        for src, dst in sorted(edges):
            fh.write(f"{src}\t{dst}\n")
    with open(out_path + ".files", "w") as fh:
        for f in sorted(sources):
            fh.write(f + "\n")
    meta = dict(stats, edges=len(edges), python=sys.version.split()[0], executable=sys.executable)
    with open(out_path + ".meta.json", "w") as fh:
        json.dump(meta, fh, indent=1)
    print(json.dumps(meta))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: py_walk.py <repo-root> <out-edges-file>")
    main(sys.argv[1], sys.argv[2])
