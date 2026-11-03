#!/usr/bin/env python3
"""Collect codemap's resolved file-level import edges for one repository.

codemap v4.6.4 exposes resolved edges only per file, through the public
`codemap --json --importers <file>` contract (its `imports` array is the list
of in-repo files the queried file resolves to). `codemap --deps --json`
carries raw import strings, not resolved targets, so it cannot be scored.
This runner queries every in-scope source file once, in parallel, and writes
`src\tdst` lines in the same format as the truth generators.

Default config only: no .codemap/config.json is written or tuned by this
script. codemap itself auto-writes one on first run in a git checkout (that
is the released behaviour a new user gets) and the runner leaves it alone.

usage: codemap_edges.py <codemap-binary> <repo-root> <files-list> <out-edges-file> [jobs]
"""
import json
import os
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor


def query(binary, root, rel):
    cmd = [binary, "--json", "--importers", rel, "."]
    try:
        proc = subprocess.run(cmd, cwd=root, capture_output=True, text=True, timeout=600)
    except subprocess.TimeoutExpired:
        return rel, None, "timeout"
    if proc.returncode != 0:
        return rel, None, proc.stderr.strip()[:200] or f"exit {proc.returncode}"
    try:
        data = json.loads(proc.stdout)
    except json.JSONDecodeError:
        return rel, None, "non-JSON output"
    return rel, data, None


def main(binary, root, files_list, out_path, jobs):
    root = os.path.realpath(root)
    files = [line.strip() for line in open(files_list) if line.strip()]
    edges = set()
    errors = {}
    coverage = {}
    not_indexed = 0
    started = time.time()
    with ThreadPoolExecutor(max_workers=jobs) as pool:
        for rel, data, err in pool.map(lambda f: query(binary, root, f), files):
            if err:
                errors[rel] = err
                continue
            coverage[data.get("coverage_status", "")] = coverage.get(data.get("coverage_status", ""), 0) + 1
            if data.get("not_indexed"):
                not_indexed += 1
            for dst in data.get("imports") or []:
                dst = dst.replace(os.sep, "/")
                if dst != rel:
                    edges.add((rel, dst))
    with open(out_path, "w") as fh:
        for src, dst in sorted(edges):
            fh.write(f"{src}\t{dst}\n")
    version = subprocess.run([binary, "--version"], capture_output=True, text=True).stdout.strip()
    meta = {
        "codemap": version,
        "queried_files": len(files),
        "errors": len(errors),
        "error_samples": dict(list(errors.items())[:5]),
        "not_indexed": not_indexed,
        "coverage_status_counts": coverage,
        "edges": len(edges),
        "seconds": round(time.time() - started, 1),
        "jobs": jobs,
    }
    with open(out_path + ".meta.json", "w") as fh:
        json.dump(meta, fh, indent=1)
    print(json.dumps(meta))


if __name__ == "__main__":
    if len(sys.argv) < 5:
        sys.exit("usage: codemap_edges.py <codemap-binary> <repo-root> <files-list> <out-edges-file> [jobs]")
    main(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5]) if len(sys.argv) > 5 else os.cpu_count() or 2)
