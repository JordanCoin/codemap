#!/usr/bin/env python3
"""First-draft mechanical selection for the import-accuracy benchmark.

Kept as the record of the draft rule described under Disclosure in
bench/README.md. The pre-registered set in bench/manifest.json was produced
by the revised rule (topic and pushed-date filters added) recorded there;
this script is not run by bench/run.sh.

Inputs: bench/selection/candidates-<lang>.json, one GitHub search page per
language (q = "language:<lang> fork:false archived:false stars:>3000",
sort = stars desc, per_page = 100), trimmed to the fields used here. The
search was run once and committed; this script never calls the GitHub API.

Rule, applied to each language's candidates in star order:
  1. skip the tuned list (repos earlier codemap fixes were tuned on)
  2. pin SHA = `git ls-remote <url> HEAD` at selection time
  3. list the tree at that SHA with a blobless, no-checkout clone
  4. require the language's toolchain manifest at the repo root
       go: go.mod; typescript: package.json at root and a tsconfig.json
       anywhere; javascript: package.json; python: pyproject.toml, setup.py
       or setup.cfg
  5. require 100 <= N <= 3000 source files, N counted by extension
       (go: .go; typescript: .ts .tsx .mts .cts minus .d.ts; javascript:
       .js .mjs .cjs .jsx; python: .py) outside vendor, node_modules,
       third_party, dist, build and .git
  6. the first three that pass are the held-out set; the next two are
       pre-registered alternates, used only if a held-out repo's toolchain
       truth cannot be built, in order, and reported as a substitution
Every candidate's decision is written to the manifest's screen log.

usage: select.py <selection-dir> <manifest-out> [clone-cache-dir]
"""
import datetime
import json
import os
import subprocess
import sys
import time

LANGS = ["go", "typescript", "javascript", "python"]
N_HELD_OUT, N_ALTERNATES = 3, 2
MIN_FILES, MAX_FILES = 100, 3000
TUNED = {
    "typescript": ["sindresorhus/ky", "excalidraw/excalidraw", "trpc/trpc"],
    "javascript": ["expressjs/express", "eslint/eslint", "NodeBB/NodeBB"],
    "python": ["pytest-dev/pytest", "pallets/werkzeug"],
    "go": [],
}
TUNED_ALL = {r for lst in TUNED.values() for r in lst}
EXTS = {
    "go": {".go"},
    "typescript": {".ts", ".tsx", ".mts", ".cts"},
    "javascript": {".js", ".mjs", ".cjs", ".jsx"},
    "python": {".py"},
}
SKIP_DIRS = {"vendor", "node_modules", "third_party", "dist", "build", ".git"}


def run(cmd, cwd=None, timeout=300):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)


def head_sha(url):
    out = run(["git", "ls-remote", url, "HEAD"])
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:200])
    return out.stdout.split()[0]


def tree_paths(url, sha, cache_dir, full):
    dest = os.path.join(cache_dir, full.replace("/", "__"))
    if not os.path.isdir(os.path.join(dest, ".git")):
        out = run(["git", "clone", "-q", "--depth", "1", "--filter=blob:none", "--no-checkout", url, dest], timeout=900)
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:200])
    out = run(["git", "ls-tree", "-r", "--name-only", sha], cwd=dest)
    if out.returncode != 0:
        # HEAD moved between ls-remote and clone: use what the clone has and report its SHA.
        out2 = run(["git", "rev-parse", "HEAD"], cwd=dest)
        sha = out2.stdout.strip()
        out = run(["git", "ls-tree", "-r", "--name-only", sha], cwd=dest)
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:200])
    return sha, out.stdout.splitlines()


def manifest_ok(lang, paths):
    root = {p for p in paths if "/" not in p}
    if lang == "go":
        return "go.mod" in root
    if lang == "typescript":
        return "package.json" in root and any(p.split("/")[-1] == "tsconfig.json" for p in paths)
    if lang == "javascript":
        return "package.json" in root
    if lang == "python":
        return bool(root & {"pyproject.toml", "setup.py", "setup.cfg"})
    return False


def count_source(lang, paths):
    n = 0
    for p in paths:
        parts = p.split("/")
        if any(part in SKIP_DIRS for part in parts[:-1]):
            continue
        name = parts[-1]
        if lang == "typescript" and name.endswith(".d.ts"):
            continue
        dot = name.rfind(".")
        if dot >= 0 and name[dot:] in EXTS[lang]:
            n += 1
    return n


def screen(lang, items, cache_dir):
    accepted, log = [], []
    for rank, it in enumerate(items, 1):
        if len(accepted) >= N_HELD_OUT + N_ALTERNATES:
            break
        full = it["full_name"]
        url = f"https://github.com/{full}"
        entry = {"rank": rank, "repo": full, "url": url, "stars": it["stargazers_count"], "default_branch": it["default_branch"]}
        if full in TUNED_ALL:
            entry["decision"] = "excluded: tuned list"
            log.append(entry); print(lang, full, entry["decision"], flush=True); continue
        try:
            sha = head_sha(url)
            sha, paths = tree_paths(url, sha, cache_dir, full)
        except Exception as e:  # noqa: BLE001
            entry["decision"] = f"skipped: git error: {e}"
            log.append(entry); print(lang, full, entry["decision"], flush=True); continue
        entry["sha"] = sha
        n = count_source(lang, paths)
        entry["source_files"] = n
        entry["manifest"] = manifest_ok(lang, paths)
        if not entry["manifest"]:
            entry["decision"] = "excluded: no toolchain manifest at root"
        elif n < MIN_FILES:
            entry["decision"] = f"excluded: {n} source files < {MIN_FILES}"
        elif n > MAX_FILES:
            entry["decision"] = f"excluded: {n} source files > {MAX_FILES}"
        else:
            entry["decision"] = "accepted"
            accepted.append(entry)
        log.append(entry)
        print(lang, full, n, entry["decision"], flush=True)
        time.sleep(1.0)
    return accepted, log


def pin_tuned(lang, cache_dir):
    out = []
    for full in TUNED[lang]:
        url = f"https://github.com/{full}"
        sha = head_sha(url)
        sha, paths = tree_paths(url, sha, cache_dir, full)
        out.append({"repo": full, "url": url, "sha": sha, "source_files": count_source(lang, paths), "slug": full.replace("/", "__")})
        time.sleep(1.0)
    return out


def main(selection_dir, manifest_out, cache_dir):
    os.makedirs(cache_dir, exist_ok=True)
    search_meta = json.load(open(os.path.join(selection_dir, "search.json")))
    groups = {"held_out": {}, "alternates": {}, "tuned": {}}
    screen_log = {}
    for lang in LANGS:
        items = json.load(open(os.path.join(selection_dir, f"candidates-{lang}.json")))
        accepted, log = screen(lang, items, cache_dir)
        for e in accepted:
            e["slug"] = e["repo"].replace("/", "__")
        groups["held_out"][lang] = accepted[:N_HELD_OUT]
        groups["alternates"][lang] = accepted[N_HELD_OUT:N_HELD_OUT + N_ALTERNATES]
        screen_log[lang] = log
        groups["tuned"][lang] = pin_tuned(lang, cache_dir)
    manifest = {
        "schema": 1,
        "created_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "created_by": "bench/select.py",
        "search": search_meta,
        "rule": {
            "order": "GitHub search results for the language, sorted by stars descending, as committed in bench/selection/",
            "exclude": "repos earlier codemap fixes were tuned on (the tuned group), forks, archived repos",
            "pin": "SHA of the default branch HEAD from git ls-remote at selection time",
            "manifest": "go: go.mod at root; typescript: package.json at root and a tsconfig.json anywhere; javascript: package.json at root; python: pyproject.toml, setup.py or setup.cfg at root",
            "size": f"{MIN_FILES} <= source files <= {MAX_FILES}, counted by extension outside vendor, node_modules, third_party, dist, build, .git",
            "pick": f"first {N_HELD_OUT} accepted per language are held out; the next {N_ALTERNATES} are alternates used in order only when a held-out repo's toolchain truth cannot be built, and any substitution is reported",
            "tuned": TUNED,
        },
        "groups": groups,
        "screen_log": screen_log,
    }
    json.dump(manifest, open(manifest_out, "w"), indent=1)
    print(json.dumps({lang: [e["repo"] for e in groups["held_out"][lang]] for lang in LANGS}, indent=1), file=sys.stderr)


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit("usage: select.py <selection-dir> <manifest-out> [clone-cache-dir]")
    main(sys.argv[1], sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else os.path.join(os.path.dirname(sys.argv[2]), "cache", "screen"))
