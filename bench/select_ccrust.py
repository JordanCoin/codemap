#!/usr/bin/env python3
"""Pre-registered mechanical selection of the C, C++ and Rust held-out repos.

Input: bench/selection/search-c-cpp-rust.json, one GitHub search page per
language (q = "language:<L> stars:>5000 archived:false fork:false
pushed:>2025-11-04", sort = stars desc, per_page = 40), fetched once with
three spaced calls and committed. This script never calls the GitHub API.

Rule, applied to each language's 40 candidates in star order, C first, then
C++, then Rust:
  1. skip a repo already picked for another language (GitHub files a C++
     repo under C or the reverse; it is scored once, under the language it
     was picked for)
  2. skip a repo carrying any of the collection topics
       algorithm, algorithms, education, interview, interview-preparation,
       awesome, awesome-list, snippets, learning-resources, learn, tutorial,
       tutorials, roadmap
  3. pin SHA = `git ls-remote <url> HEAD` at selection time
  4. list the tree at that SHA with a blobless, no-checkout clone; a
     truncated listing is a skip
  5. require the toolchain manifest at the repo root:
       c, cpp: CMakeLists.txt (the truth generator reads cmake's
               compile_commands.json; `bear` is not available here, so a
               make-only build cannot produce one)
       rust:   Cargo.toml
  6. require 100 <= N <= 3000 source files, N counted by extension
       (c: .c .h; cpp: .cpp .cc .cxx .hpp .hh .hxx .h; rust: .rs) outside
       vendor, third_party, thirdparty, external, deps, node_modules, dist,
       build and .git
  7. the first three that pass are the held-out set; there are no
     alternates and no substitution: a held-out repo whose toolchain truth
     cannot be built is reported as truth unavailable
Every candidate's decision is written to the screen log.

usage: select_ccrust.py <search-json> <manifest.json> [clone-cache-dir]
"""
import datetime
import json
import os
import subprocess
import sys
import time

LANGS = ["c", "cpp", "rust"]
N_HELD_OUT = 3
MIN_FILES, MAX_FILES = 100, 3000
COLLECTION_TOPICS = {"algorithm", "algorithms", "education", "interview", "interview-preparation", "awesome", "awesome-list",
                     "snippets", "learning-resources", "learn", "tutorial", "tutorials", "roadmap"}
EXTS = {
    "c": {".c", ".h"},
    "cpp": {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h"},
    "rust": {".rs"},
}
SKIP_DIRS = {"vendor", "third_party", "thirdparty", "external", "deps", "node_modules", "dist", "build", ".git"}


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
        out2 = run(["git", "rev-parse", "HEAD"], cwd=dest)
        sha = out2.stdout.strip()
        out = run(["git", "ls-tree", "-r", "--name-only", sha], cwd=dest)
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:200])
    return sha, out.stdout.splitlines()


def manifest_ok(lang, paths):
    root = {p for p in paths if "/" not in p}
    if lang in ("c", "cpp"):
        return "CMakeLists.txt" in root
    return "Cargo.toml" in root


def count_source(lang, paths):
    n = 0
    for p in paths:
        parts = p.split("/")
        if any(part in SKIP_DIRS for part in parts[:-1]):
            continue
        name = parts[-1]
        dot = name.rfind(".")
        if dot >= 0 and name[dot:] in EXTS[lang]:
            n += 1
    return n


def screen(lang, items, cache_dir, taken):
    accepted, log = [], []
    for rank, it in enumerate(items, 1):
        if len(accepted) >= N_HELD_OUT:
            break
        full = it["full_name"]
        url = f"https://github.com/{full}"
        entry = {"rank": rank, "repo": full, "url": url, "stars": it["stargazers_count"], "default_branch": it["default_branch"], "topics": it.get("topics") or []}
        if full in taken:
            entry["decision"] = "excluded: picked for another language"
            log.append(entry); print(lang, full, entry["decision"], flush=True); continue
        bad = sorted(set(entry["topics"]) & COLLECTION_TOPICS)
        if bad:
            entry["decision"] = f"excluded: collection topic {bad}"
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
            entry["decision"] = "excluded: no CMakeLists.txt at root" if lang != "rust" else "excluded: no Cargo.toml at root"
        elif n < MIN_FILES:
            entry["decision"] = f"excluded: {n} source files < {MIN_FILES}"
        elif n > MAX_FILES:
            entry["decision"] = f"excluded: {n} source files > {MAX_FILES}"
        else:
            entry["decision"] = "accepted"
            accepted.append(entry)
            taken.add(full)
        log.append(entry)
        print(lang, full, n, entry["decision"], flush=True)
        time.sleep(1.0)
    return accepted, log


def main(search_path, manifest_path, cache_dir):
    search = json.load(open(search_path))
    manifest = json.load(open(manifest_path))
    os.makedirs(cache_dir, exist_ok=True)
    taken = set()
    screen_log = {}
    for lang in LANGS:
        items = search["results"][lang]["items"]
        accepted, log = screen(lang, items, cache_dir, taken)
        screen_log[lang] = log
        manifest["groups"]["held_out"][lang] = [
            {"repo": a["repo"], "url": a["url"], "sha": a["sha"], "slug": a["repo"].replace("/", "__"), "stars": a["stars"], "source_files": a["source_files"]}
            for a in accepted]
        if len(accepted) < N_HELD_OUT:
            print(f"{lang}: only {len(accepted)} candidates passed within the top {len(items)}", flush=True)
    manifest.setdefault("extensions", {})["c_cpp_rust"] = {
        "applied_on": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d"),
        "rule": __doc__.split("Rule, applied", 1)[1].split("Every candidate", 1)[0].strip(),
        "search": os.path.basename(search_path),
        "tuned": [],
        "substitution": "none",
        "screen_log": screen_log,
    }
    json.dump(manifest, open(manifest_path, "w"), indent=1)
    print("wrote", manifest_path)


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "bench/cache/select")
