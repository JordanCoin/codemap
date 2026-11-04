#!/usr/bin/env python3
"""Go truth: file-level import edges from the Go toolchain.

Two toolchain sources, combined:
  * `go list -e -json -deps ./...` gives every package in the module: its
    directory, its non-test Go files at the default build configuration
    (GoFiles + CgoFiles) and which import path resolves to which package.
  * `go/parser` (bench/truth/goimports, ImportsOnly) gives the import paths
    each file itself declares. `go list` only reports imports per package,
    and the edge rule is per file.

Edge rule (see bench/README.md): Go imports name packages. If file F
declares an import of in-module package Q, then F -> every non-test Go file
of Q. Test files are out of scope on both sides because `go list` without
-test reports no test imports. Files `go list` excludes at the default
GOOS/GOARCH (build constraints) are not in GoFiles, so they are out of scope
as sources and as targets.

Output: `src\tdst` lines (repo-relative, forward slashes), `<out>.files`
listing the in-scope files, `<out>.meta.json`.
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def go_list(root):
    env = dict(os.environ, GOFLAGS=os.environ.get("GOFLAGS", "") + " -mod=mod", GOWORK="off")
    proc = subprocess.run(["go", "list", "-e", "-json", "-deps", "./..."], cwd=root, env=env, capture_output=True, text=True)
    if proc.returncode != 0 and not proc.stdout.strip():
        sys.stderr.write(proc.stderr)
        sys.exit(f"go list failed in {root}")
    pkgs, dec, buf, i = [], json.JSONDecoder(), proc.stdout, 0
    while i < len(buf):
        while i < len(buf) and buf[i].isspace():
            i += 1
        if i >= len(buf):
            break
        obj, i = dec.raw_decode(buf, i)
        pkgs.append(obj)
    return pkgs


def file_imports(paths):
    """Per-file import paths via go/parser (bench/truth/goimports)."""
    helper = os.path.join(HERE, "goimports")
    proc = subprocess.run(["go", "run", "."], cwd=helper, input="\n".join(paths) + "\n", capture_output=True, text=True)
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        sys.exit("goimports helper failed")
    out = {}
    for line in proc.stdout.splitlines():
        rec = json.loads(line)
        out[rec["file"]] = rec
    return out


def main(root, out_path):
    root = os.path.realpath(root)
    pkgs = go_list(root)

    def in_module(p):
        mod = p.get("Module")
        if not mod or mod.get("Main") is not True:
            return False
        d = os.path.realpath(p.get("Dir", ""))
        return d == root or d.startswith(root + os.sep)

    local = {p["ImportPath"]: p for p in pkgs if in_module(p)}

    def files(p):
        d = os.path.realpath(p["Dir"])
        rel = os.path.relpath(d, root)
        return [(f if rel == "." else f"{rel}/{f}").replace(os.sep, "/") for f in p.get("GoFiles", []) + p.get("CgoFiles", [])]

    sources, errors, abs_of = set(), [], {}
    for ip, p in local.items():
        if p.get("Error"):
            errors.append(f"{ip}: {p['Error'].get('Err', '')[:200]}")
        for rel in files(p):
            sources.add(rel)
            abs_of[rel] = os.path.join(root, rel.replace("/", os.sep))

    parsed = file_imports([abs_of[r] for r in sorted(sources)])
    parse_errors = 0
    edges = set()
    for rel in sorted(sources):
        rec = parsed.get(abs_of[rel])
        if rec is None or rec.get("error"):
            parse_errors += 1
            continue
        for imp in rec["imports"]:
            q = local.get(imp)
            if q is None:
                continue
            for dst in files(q):
                if dst != rel:
                    edges.add((rel, dst))

    with open(out_path, "w") as fh:
        for src, dst in sorted(edges):
            fh.write(f"{src}\t{dst}\n")
    with open(out_path + ".files", "w") as fh:
        for f in sorted(sources):
            fh.write(f + "\n")
    meta = {
        "packages_in_module": len(local),
        "source_files": len(sources),
        "edges": len(edges),
        "parse_errors": parse_errors,
        "package_errors": errors,
        "go_version": subprocess.run(["go", "version"], capture_output=True, text=True).stdout.strip(),
    }
    with open(out_path + ".meta.json", "w") as fh:
        json.dump(meta, fh, indent=1)
    print(json.dumps(meta))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: go.py <repo-root> <out-edges-file>")
    main(sys.argv[1], sys.argv[2])
