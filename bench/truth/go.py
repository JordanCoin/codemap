#!/usr/bin/env python3
"""Go truth: file-level import edges from `go list -json -deps ./...`.

Edge rule (see bench/README.md): Go imports name packages. If package P
(any of its non-test Go files F) imports in-module package Q, then F -> every
non-test Go file of Q. Test files are out of scope on both sides because
`go list` without -test reports no test imports.

Output: one `src\tdst` line per edge, paths relative to the repo root with
forward slashes. A second file, `<out>.files`, lists every in-scope source
file (the files of all in-module packages), one per line, so the scorer can
restrict codemap's edges to the same universe.
"""
import json
import os
import subprocess
import sys


def main(root, out_path):
    root = os.path.realpath(root)
    env = dict(os.environ, GOFLAGS=os.environ.get("GOFLAGS", "") + " -mod=mod", GOWORK="off")
    proc = subprocess.run(
        ["go", "list", "-e", "-json", "-deps", "./..."],
        cwd=root, env=env, capture_output=True, text=True,
    )
    if proc.returncode != 0 and not proc.stdout.strip():
        sys.stderr.write(proc.stderr)
        sys.exit(f"go list failed in {root}")
    pkgs = []
    dec = json.JSONDecoder()
    buf = proc.stdout
    i = 0
    while i < len(buf):
        while i < len(buf) and buf[i].isspace():
            i += 1
        if i >= len(buf):
            break
        obj, j = dec.raw_decode(buf, i)
        pkgs.append(obj)
        i = j

    def in_module(p):
        mod = p.get("Module")
        if not mod or mod.get("Main") is not True:
            return False
        d = os.path.realpath(p.get("Dir", ""))
        return d == root or d.startswith(root + os.sep)

    by_path = {p["ImportPath"]: p for p in pkgs}
    local = {ip: p for ip, p in by_path.items() if in_module(p)}

    def files(p):
        d = os.path.realpath(p["Dir"])
        rel = os.path.relpath(d, root)
        out = []
        for f in p.get("GoFiles", []) + p.get("CgoFiles", []):
            out.append(f if rel == "." else f"{rel}/{f}".replace(os.sep, "/"))
        return out

    sources = set()
    edges = set()
    errors = []
    for ip, p in local.items():
        if p.get("Error"):
            errors.append(f"{ip}: {p['Error'].get('Err', '')[:200]}")
        srcs = files(p)
        sources.update(srcs)
        for imp in p.get("Imports", []):
            q = local.get(imp)
            if q is None:
                continue
            for dst in files(q):
                for src in srcs:
                    edges.add((src, dst))
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
