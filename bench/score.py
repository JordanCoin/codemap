#!/usr/bin/env python3
"""Score codemap's edges against toolchain truth, per repository.

Scope is applied identically to both sides: an edge counts only if both its
source and its target are in the truth generator's file universe and the
target carries one of the language's target extensions (see bench/README.md).

Metrics per repo: n_truth, n_codemap, tp, fn, fp, recall, precision, and
95% Wilson score intervals. Aggregates per group are pooled (sum of counts),
which is the only aggregate that has a single-trial interpretation; per-repo
rows carry their own intervals. A random sample of disagreeing edges per
language is drawn with a fixed seed for the spot check.

usage: score.py <manifest.json> <results-dir> <out.json> <out.md> <codemap-version> [--lang <lang>]

--lang restricts the report to one language (a partial rerun of one language
against another codemap build); the output then says so in its heading.
"""
import json
import math
import os
import random
import sys

TARGET_EXTS = {
    "go": (".go",),
    "typescript": (".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"),
    "javascript": (".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts"),
    "python": (".py",),
    "c": (".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inc", ".inl", ".ipp", ".tcc"),
    "cpp": (".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inc", ".inl", ".ipp", ".tcc"),
    "rust": (".rs",),
}
ALL_LANGS = ("go", "typescript", "javascript", "python", "c", "cpp", "rust")


def wilson(k, n, z=1.959964):
    if n == 0:
        return (0.0, 0.0, 0.0)
    p = k / n
    denom = 1 + z * z / n
    centre = (p + z * z / (2 * n)) / denom
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / denom
    return (p, max(0.0, centre - half), min(1.0, centre + half))


def read_edges(path):
    edges = set()
    if not os.path.exists(path):
        return edges
    for line in open(path):
        line = line.rstrip("\n")
        if not line:
            continue
        src, dst = line.split("\t", 1)
        edges.add((src, dst))
    return edges


def read_lines(path):
    if not os.path.exists(path):
        return set()
    return {line.strip() for line in open(path) if line.strip()}


def in_scope(edge, sources, exts):
    """Both endpoints must be in the truth generator's file universe, and the
    target must carry one of the language's target extensions."""
    src, dst = edge
    return src in sources and dst in sources and dst.endswith(exts) and not dst.endswith(".d.ts") and "node_modules" not in dst.split("/")


def score_repo(lang, slug, results_dir):
    base = os.path.join(results_dir, slug)
    truth = read_edges(os.path.join(base, "truth.edges"))
    sources = read_lines(os.path.join(base, "truth.edges.files"))
    codemap = read_edges(os.path.join(base, "codemap.edges"))
    exts = TARGET_EXTS[lang]
    truth = {e for e in truth if in_scope(e, sources, exts)}
    # codemap edges whose source is in scope but whose target the toolchain
    # does not consider part of the build (for Go: files excluded by build
    # constraints on this platform). Scoped out, but reported.
    codemap_out = {e for e in codemap if e[0] in sources and not in_scope(e, sources, exts)}
    codemap = {e for e in codemap if in_scope(e, sources, exts)}
    tp = truth & codemap
    fn = truth - codemap
    fp = codemap - truth
    # TypeScript/JavaScript: false positives where the compiler resolved an
    # import from the same source file to a built declaration file (.d.ts)
    # inside a package directory and codemap's target lies in that package.
    # Reported separately as "fp via workspace package" so the reader can see
    # how much of the precision gap is this one disagreement.
    fp_workspace = set()
    dts_path = os.path.join(base, "truth.edges.dts")
    if os.path.exists(dts_path):
        pkg_roots = {}
        for line in open(dts_path):
            line = line.rstrip("\n")
            if not line:
                continue
            src, dts = line.split("\t", 1)
            parts = dts.split("/")
            root_parts = parts[:parts.index("dist")] if "dist" in parts else parts[:-1]
            pkg_roots.setdefault(src, set()).add("/".join(root_parts) + "/" if root_parts else "")
        for s_, t_ in fp:
            for root in pkg_roots.get(s_, ()):
                if root == "" or t_.startswith(root):
                    fp_workspace.add((s_, t_))
                    break
    recall = wilson(len(tp), len(truth))
    precision = wilson(len(tp), len(codemap))
    meta = {}
    for name in ("truth.edges.meta.json", "codemap.edges.meta.json"):
        p = os.path.join(base, name)
        if os.path.exists(p):
            meta[name.split(".")[0]] = json.load(open(p))
    return {
        "sources": len(sources),
        "n_truth": len(truth), "n_codemap": len(codemap),
        "tp": len(tp), "fn": len(fn), "fp": len(fp),
        "codemap_out_of_universe": len(codemap_out),
        "fp_workspace_package": len(fp_workspace),
        "recall": recall, "precision": precision,
        "fn_edges": sorted(fn), "fp_edges": sorted(fp),
        "meta": meta,
    }


def fmt_ci(t):
    p, lo, hi = t
    return f"{p:.3f} [{lo:.3f}, {hi:.3f}]"


def main(manifest_path, results_dir, out_json, out_md, version, only_lang=None):
    manifest = json.load(open(manifest_path))
    rng = random.Random(20261103)
    langs = [l for l in ALL_LANGS if only_lang in (None, l) or (only_lang == "c-cpp-rust" and l in ("c", "cpp", "rust"))]
    report = {"codemap": version, "languages": langs, "groups": {}, "spot_check_samples": {}}
    md = [f"# codemap {version} import-accuracy benchmark results" + (f" ({only_lang} only)" if only_lang else ""), ""]
    md.append("Scope: in-repo source file to in-repo source file edges, as defined in bench/README.md. "
              "Recall = truth edges codemap found. Precision = codemap edges that are in truth. "
              "95% intervals are Wilson score intervals on the full census (no sampling).")
    md.append("")
    disagreements = {}
    for group in ("held_out", "tuned"):
        rows = []
        pooled = {}
        for lang in langs:
            for repo in manifest["groups"][group].get(lang, []):
                slug = repo["slug"]
                status_path = os.path.join(results_dir, slug, "status.json")
                status = json.load(open(status_path)) if os.path.exists(status_path) else {"status": "missing"}
                if status.get("status") not in ("ok", "codemap_failed"):
                    rows.append({"lang": lang, "repo": repo["repo"], "sha": repo["sha"], "status": status})
                    continue
                r = score_repo(lang, slug, results_dir)
                r.update({"lang": lang, "repo": repo["repo"], "sha": repo["sha"], "status": status})
                rows.append(r)
                agg = pooled.setdefault(lang, {"n_truth": 0, "n_codemap": 0, "tp": 0})
                agg["n_truth"] += r["n_truth"]; agg["n_codemap"] += r["n_codemap"]; agg["tp"] += r["tp"]
                d = disagreements.setdefault((group, lang), [])
                d.extend(("FN", repo["repo"], s, t) for s, t in r["fn_edges"])
                d.extend(("FP", repo["repo"], s, t) for s, t in r["fp_edges"])
        for lang, agg in pooled.items():
            agg["recall"] = wilson(agg["tp"], agg["n_truth"])
            agg["precision"] = wilson(agg["tp"], agg["n_codemap"])
        report["groups"][group] = {"repos": [{k: v for k, v in row.items() if k not in ("fn_edges", "fp_edges")} for row in rows], "pooled": pooled}
        title = "Held-out (pre-registered, never used to tune codemap)" if group == "held_out" else "Tuned (repos earlier fixes were tuned on; reported separately, never in headline numbers)"
        md += [f"## {title}", "", "| lang | repo | sha | sources | n truth | n codemap | tp | fn | fp | recall [95% CI] | precision [95% CI] | fp via workspace package | codemap edges to out-of-universe targets |", "|---|---|---|---:|---:|---:|---:|---:|---:|---|---|---:|---:|"]
        for row in rows:
            if row["status"].get("status") not in ("ok", "codemap_failed"):
                md.append(f"| {row['lang']} | {row['repo']} | {row['sha'][:12]} | | | | | | | truth unavailable: {row['status'].get('reason', row['status'].get('status'))} | | | |")
                continue
            label = row['repo'] if row["status"].get("status") == "ok" else f"{row['repo']} (codemap produced no graph: {row['status'].get('reason','')})"
            md.append(f"| {row['lang']} | {label} | {row['sha'][:12]} | {row['sources']} | {row['n_truth']} | {row['n_codemap']} | {row['tp']} | {row['fn']} | {row['fp']} | {fmt_ci(row['recall'])} | {fmt_ci(row['precision'])} | {row['fp_workspace_package']} | {row['codemap_out_of_universe']} |")
        md += ["", "Pooled per language (counts summed across the group's repos):", "", "| lang | n truth | n codemap | tp | recall [95% CI] | precision [95% CI] |", "|---|---:|---:|---:|---|---|"]
        for lang, agg in pooled.items():
            md.append(f"| {lang} | {agg['n_truth']} | {agg['n_codemap']} | {agg['tp']} | {fmt_ci(agg['recall'])} | {fmt_ci(agg['precision'])} |")
        md.append("")
    # Spot-check samples: 5 random disagreeing edges per language from the held-out group, mixing FN and FP.
    md += ["## Spot-check samples (held-out, seed 20261103)", "", "Five random disagreeing edges per language, drawn from the pooled false negatives and false positives. Classification is filled in by hand in the PR; this table is the sample.", "", "| lang | kind | repo | source | target |", "|---|---|---|---|---|"]
    for lang in langs:
        pool = disagreements.get(("held_out", lang), [])
        fns = [d for d in pool if d[0] == "FN"]
        fps = [d for d in pool if d[0] == "FP"]
        rng.shuffle(fns); rng.shuffle(fps)
        sample = []
        # alternate kinds so the sample mixes both when both exist
        while len(sample) < 5 and (fns or fps):
            if fns and (len(sample) % 2 == 0 or not fps):
                sample.append(fns.pop())
            elif fps:
                sample.append(fps.pop())
        report["spot_check_samples"][lang] = [{"kind": k, "repo": r, "source": s, "target": t} for k, r, s, t in sample]
        for k, r, s, t in sample:
            md.append(f"| {lang} | {k} | {r} | `{s}` | `{t}` |")
    md.append("")
    json.dump(report, open(out_json, "w"), indent=1)
    open(out_md, "w").write("\n".join(md))
    print("\n".join(md))


if __name__ == "__main__":
    argv = sys.argv[1:]
    only = None
    if "--lang" in argv:
        i = argv.index("--lang")
        only = argv[i + 1]
        del argv[i:i + 2]
    if len(argv) != 5:
        sys.exit("usage: score.py <manifest.json> <results-dir> <out.json> <out.md> <codemap-version> [--lang <lang>]")
    main(*argv, only_lang=only)
