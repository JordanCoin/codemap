#!/usr/bin/env python3
"""Show the evidence behind one disagreeing edge so a person can classify it.

For a sampled edge (repo slug, source, target) it prints: the import lines in
the source file that mention the target's module name, whether the target
is in the truth universe, codemap's auto-written config `only`/`exclude`
lists, the truth generator's resolution notes for that source, and codemap's
own answer for the source (`--json --importers`).

usage: spotcheck.py <results-dir> <repos-cache-dir> <codemap-binary> <slug> <source> <target>
"""
import json
import os
import re
import subprocess
import sys


def main(results, repos, binary, slug, src, dst):
    root = os.path.join(repos, slug)
    base = os.path.join(results, slug)
    binary = os.path.abspath(binary)
    print(f"== {slug}: {src} -> {dst}")
    sources = {l.strip() for l in open(os.path.join(base, "truth.edges.files"))}
    print(f"   source in universe: {src in sources}; target in universe: {dst in sources}; target exists: {os.path.exists(os.path.join(root, dst))}")
    cfg = os.path.join(root, ".codemap", "config.json")
    if os.path.exists(cfg):
        c = json.load(open(cfg))
        print(f"   codemap auto config only={c.get('only')} exclude={c.get('exclude')}")
    stem = os.path.splitext(os.path.basename(dst))[0]
    if stem == "__init__":  # a package: its import lines name the directory
        stem = os.path.basename(os.path.dirname(dst))
    pat = re.compile(r"(import|require|from)\b.*" + re.escape(stem) + r"|" + re.escape(stem) + r".*\b(import|require)\b")
    try:
        for i, line in enumerate(open(os.path.join(root, src), errors="replace"), 1):
            if pat.search(line):
                print(f"   {src}:{i}: {line.rstrip()[:160]}")
    except OSError as e:
        print(f"   cannot read source: {e}")
    try:
        out = subprocess.run([binary, "--json", "--importers", src, "."], cwd=root, capture_output=True, text=True, timeout=600)
        d = json.loads(out.stdout)
        imps = d.get("imports") or []
        print(f"   codemap --importers {src}: imports={len(imps)} coverage={d.get('coverage_status')} not_indexed={d.get('not_indexed')} target_listed={dst in imps}")
        if d.get("coverage_notes"):
            print("   notes:", "; ".join(d["coverage_notes"])[:300])
    except Exception as e:  # noqa: BLE001
        print(f"   codemap query failed: {e}")


if __name__ == "__main__":
    if len(sys.argv) != 7:
        sys.exit(__doc__)
    main(*sys.argv[1:])
