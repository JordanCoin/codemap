#!/usr/bin/env python3
"""C and C++ truth: the preprocessor's own include tree per translation unit.

1. Configure the repository with cmake into a build directory outside the
   source tree, with the repository's default options and clang as the
   compiler, exporting compile_commands.json.
2. For every translation unit in compile_commands.json, rerun its own
   command with `-fsyntax-only -H -w` (plus `-MM`-style output suppressed):
   clang prints to stderr one line per header it enters, prefixed by as
   many dots as the include depth. Depth 1 lines are the unit's direct
   includes; a depth-n line's parent is the nearest preceding depth n-1
   line. That is exactly the include graph the compiler followed under this
   configuration.

Scope (see bench/README.md):
  universe = in-repo translation units plus every in-repo file that appears
             in any include tree, excluding the build directory
  edges    = file -> file it directly includes, union over all units
  A unit whose preprocessing stops at a missing header (a dependency this
  environment lacks) contributes what was seen before the error and is
  counted in meta.units_with_errors.

usage: cc.py <repo-root> <out-edges-file> [--build-dir <dir>] [--jobs N]
"""
import json
import os
import shlex
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

SOURCE_EXTS = (".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inc", ".inl", ".ipp", ".tcc", ".m", ".mm")
DROP_FLAGS_WITH_ARG = {"-o", "-MF", "-MT", "-MQ"}
DROP_FLAGS = {"-c", "-M", "-MM", "-MD", "-MMD", "-MG", "-MP"}


def configure(root, build_dir):
    os.makedirs(build_dir, exist_ok=True)
    env = dict(os.environ, CC="clang", CXX="clang++")
    cmd = ["cmake", "-S", root, "-B", build_dir, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON", "-DCMAKE_BUILD_TYPE=Release"]
    proc = subprocess.run(cmd, capture_output=True, text=True, env=env, timeout=1800)
    log = proc.stdout + proc.stderr
    return proc.returncode == 0 and os.path.exists(os.path.join(build_dir, "compile_commands.json")), log


def unit_args(entry):
    if "arguments" in entry:
        args = list(entry["arguments"])
    else:
        args = shlex.split(entry["command"])
    out, skip = [], False
    for a in args:
        if skip:
            skip = False
            continue
        if a in DROP_FLAGS_WITH_ARG:
            skip = True
            continue
        if a in DROP_FLAGS:
            continue
        if a.startswith("-o") and len(a) > 2 and not a.startswith("-objc"):
            continue
        out.append(a)
    return out


def run_unit(entry, root, build_dir):
    args = unit_args(entry) + ["-fsyntax-only", "-H", "-w", "-Wno-error"]
    cwd = entry.get("directory") or build_dir
    try:
        proc = subprocess.run(args, cwd=cwd, capture_output=True, text=True, timeout=600)
    except (subprocess.TimeoutExpired, OSError) as e:
        return entry["file"], None, None, f"{type(e).__name__}"
    stack = []  # stack[d-1] = file at depth d
    edges = set()
    src = os.path.realpath(os.path.join(cwd, entry["file"]))
    files = {src}
    for line in proc.stderr.splitlines():
        if not line.startswith("."):
            continue
        depth = len(line) - len(line.lstrip("."))
        path = line[depth:].strip()
        if not path:
            continue
        path = os.path.realpath(os.path.join(cwd, path))
        parent = src if depth == 1 else (stack[depth - 2] if len(stack) >= depth - 1 else None)
        if parent is None:
            continue
        del stack[depth - 1:]
        stack.append(path)
        edges.add((parent, path))
        files.add(path)
    err = None
    if proc.returncode != 0:
        for line in proc.stderr.splitlines():
            if "fatal error:" in line or "error:" in line:
                err = line.strip()[:200]
                break
        err = err or f"exit {proc.returncode}"
    return src, edges, files, err


def rel_in_repo(path, root, build_dir):
    if path == build_dir or path.startswith(build_dir + os.sep):
        return None
    if path == root or path.startswith(root + os.sep):
        rel = os.path.relpath(path, root).replace(os.sep, "/")
        if ".git/" in rel + "/":
            return None
        return rel
    return None


def main(argv):
    build_dir = None
    jobs = os.cpu_count() or 2
    if "--build-dir" in argv:
        i = argv.index("--build-dir"); build_dir = argv[i + 1]; del argv[i:i + 2]
    if "--jobs" in argv:
        i = argv.index("--jobs"); jobs = int(argv[i + 1]); del argv[i:i + 2]
    if len(argv) != 3:
        sys.exit("usage: cc.py <repo-root> <out-edges-file> [--build-dir <dir>] [--jobs N]")
    root = os.path.realpath(argv[1]); out_path = argv[2]
    build_dir = os.path.realpath(build_dir or os.path.join(os.path.dirname(out_path), "build"))
    ok, log = configure(root, build_dir)
    with open(out_path + ".configure.log", "w") as fh:
        fh.write(log)
    meta = {"configure_ok": ok, "build_dir": build_dir, "clang": subprocess.run(["clang", "--version"], capture_output=True, text=True).stdout.split("\n")[0],
            "cmake": subprocess.run(["cmake", "--version"], capture_output=True, text=True).stdout.split("\n")[0]}
    if not ok:
        meta["error"] = "cmake configure failed (see truth.edges.configure.log)"
        json.dump(meta, open(out_path + ".meta.json", "w"), indent=1)
        print(json.dumps(meta))
        sys.exit(1)
    db = json.load(open(os.path.join(build_dir, "compile_commands.json")))
    units = [e for e in db if e.get("file", "").endswith(SOURCE_EXTS)]
    all_edges, all_files, errors, unit_count = set(), set(), {}, 0
    with ThreadPoolExecutor(max_workers=jobs) as pool:
        for src, edges, files, err in pool.map(lambda e: run_unit(e, root, build_dir), units):
            unit_count += 1
            if edges is None:
                errors[src] = err
                continue
            if err:
                errors[src] = err
            all_edges |= edges
            all_files |= files
    sources, edges_rel = set(), set()
    for f in all_files:
        rel = rel_in_repo(f, root, build_dir)
        if rel is not None and rel.endswith(SOURCE_EXTS):
            sources.add(rel)
    for a, b in all_edges:
        ra, rb = rel_in_repo(a, root, build_dir), rel_in_repo(b, root, build_dir)
        if ra is not None and rb is not None and ra != rb and ra in sources and rb in sources:
            edges_rel.add((ra, rb))
    with open(out_path, "w") as fh:
        for a, b in sorted(edges_rel):
            fh.write(f"{a}\t{b}\n")
    with open(out_path + ".files", "w") as fh:
        for f in sorted(sources):
            fh.write(f + "\n")
    meta.update({"translation_units": unit_count, "units_with_errors": len(errors),
                 "error_samples": dict(sorted(errors.items())[:8]), "source_files": len(sources), "edges": len(edges_rel)})
    json.dump(meta, open(out_path + ".meta.json", "w"), indent=1)
    print(json.dumps({k: v for k, v in meta.items() if k != "error_samples"}))


if __name__ == "__main__":
    main(sys.argv)
