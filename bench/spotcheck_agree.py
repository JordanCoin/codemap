#!/usr/bin/env python3
"""Sample agreeing edges (true positives) per repository and print the source
lines that justify each, so a person can confirm that agreement is not two
tools making the same mistake.

For every repository of one language in the manifest, draws n edges (fixed
seed) from the intersection of truth and codemap edges (scope applied as in
score.py) and prints, per edge, the import or require statements in the
source file that name the target's module (or its package directory for an
__init__.py target). Nothing is written; the classification is done by hand.

usage: spotcheck_agree.py <manifest.json> <results-dir> <repos-cache-dir> <lang> [n] [seed]
"""
import json
import os
import random
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from score import TARGET_EXTS, in_scope, read_edges, read_lines  # noqa: E402


def stem_pattern(dst):
    stem = os.path.splitext(os.path.basename(dst))[0]
    if stem == "__init__":
        stem = os.path.basename(os.path.dirname(dst))
    return re.compile(r"^\s*(from\s+\S*\b" + re.escape(stem) + r"\b|import\s+.*\b" + re.escape(stem) + r"\b|.*\b(require|import)\(.*\b" + re.escape(stem) + r"\b)")


def main(manifest_path, results_dir, repos_dir, lang, n=5, seed=20261103):
    manifest = json.load(open(manifest_path))
    rng = random.Random(int(seed))
    for group in ("held_out", "tuned"):
        for repo in manifest["groups"][group].get(lang, []):
            slug = repo["slug"]
            base = os.path.join(results_dir, slug)
            sources = read_lines(os.path.join(base, "truth.edges.files"))
            exts = TARGET_EXTS[lang]
            truth = {e for e in read_edges(os.path.join(base, "truth.edges")) if in_scope(e, sources, exts)}
            codemap = {e for e in read_edges(os.path.join(base, "codemap.edges")) if in_scope(e, sources, exts)}
            tp = sorted(truth & codemap)
            sample = rng.sample(tp, min(int(n), len(tp)))
            print(f"## {repo['repo']} ({group}): {len(tp)} agreeing edges, sampling {len(sample)}")
            for src, dst in sample:
                print(f"- `{src}` -> `{dst}`")
                pat = stem_pattern(dst)
                try:
                    lines = [(i, l.rstrip()) for i, l in enumerate(open(os.path.join(repos_dir, slug, src), errors="replace"), 1) if pat.search(l)]
                except OSError as e:
                    print(f"    cannot read source: {e}")
                    continue
                for i, l in lines[:4]:
                    print(f"    {src}:{i}: {l[:150]}")
                if not lines:
                    print("    (no import line names the target's module; inspect by hand)")
            print()


if __name__ == "__main__":
    if len(sys.argv) < 5:
        sys.exit(__doc__)
    main(*sys.argv[1:])
