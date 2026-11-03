#!/usr/bin/env python3
"""Collect codemap's resolved file-level import edges for one repository.

Two methods, both public codemap surfaces, both run from the clone root with
default configuration (no .codemap/config.json is written or tuned by this
script; codemap itself auto-writes one on first run in a git checkout, which
is the released behaviour a new user gets, and the runner leaves it alone):

  daemon    (default) `codemap --watch .` runs the file-watcher daemon in
            the foreground; it builds the dependency graph once and publishes
            it to the daemon state file that codemap's own hooks and MCP
            server read (`imports`: file -> resolved in-repo targets). The
            runner waits for the graph to be published, copies the edges and
            stops the daemon.
  per-file  `codemap --json --importers <file>` for every in-scope source
            file; its `imports` array is the same resolved target list. Each
            call rescans the repository, so this costs seconds per file.

The two methods were verified to produce identical edge sets on the Go
held-out repos (see bench/README.md); per-file remains available as the
cross-check (`--method per-file`).

usage: codemap_edges.py <codemap-binary> <repo-root> <files-list> <out-edges-file> [jobs] [--method daemon|per-file]
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


def collect_per_file(binary, root, files, jobs):
    edges, errors, coverage, not_indexed = set(), {}, {}, 0
    with ThreadPoolExecutor(max_workers=jobs) as pool:
        for rel, data, err in pool.map(lambda f: query(binary, root, f), files):
            if err:
                errors[rel] = err
                continue
            cov = data.get("coverage_status", "")
            coverage[cov] = coverage.get(cov, 0) + 1
            if data.get("not_indexed"):
                not_indexed += 1
            for dst in data.get("imports") or []:
                dst = dst.replace(os.sep, "/")
                if dst != rel:
                    edges.add((rel, dst))
    return edges, {"queried_files": len(files), "errors": len(errors), "error_samples": dict(list(errors.items())[:5]),
                   "not_indexed": not_indexed, "coverage_status_counts": coverage}


def find_state(root):
    base = os.path.join(root, ".codemap", "projects")
    if not os.path.isdir(base):
        return None
    for d in sorted(os.listdir(base)):
        p = os.path.join(base, d, "state.json")
        if os.path.isfile(p):
            return p
    return None


def collect_daemon(binary, root, timeout_s=3600):
    """Run codemap's file-watcher daemon in the foreground (`codemap --watch .`,
    the documented flag) until it publishes the dependency graph, copy the
    graph's `imports`, and stop it. `codemap watch start` would spawn the
    same daemon, but it gives up after a 30 s readiness wait and kills the
    daemon on repositories whose initial scan takes longer, so the foreground
    form is used for every repository for uniformity."""
    subprocess.run([binary, "watch", "stop"], cwd=root, capture_output=True, text=True)
    # Never read a state file from an earlier daemon: remove codemap's runtime
    # state (not its config) so the graph is built fresh by this run.
    import shutil
    shutil.rmtree(os.path.join(root, ".codemap", "projects"), ignore_errors=True)
    log = open(os.path.join(root, ".codemap-bench-watch.log"), "w")
    proc = subprocess.Popen([binary, "--watch", "."], cwd=root, stdout=log, stderr=subprocess.STDOUT,
                            stdin=subprocess.DEVNULL, start_new_session=True)
    deadline = time.time() + timeout_s
    state, last_gen, stable, failed_state = None, None, 0, None
    try:
        while time.time() < deadline and proc.poll() is None:
            time.sleep(3)
            p = find_state(root)
            if not p:
                continue
            try:
                st = json.load(open(p))
            except (json.JSONDecodeError, OSError):
                continue
            status = (st.get("graph") or {}).get("status")
            if status == "failed":
                # The daemon reports a failed dependency-graph build as a
                # terminal state; waiting longer cannot change it.
                failed_state = st
                break
            if not st.get("imports") or status != "available":
                continue
            gen = st.get("generation")
            stable = stable + 1 if gen == last_gen else 0
            last_gen, state = gen, st
            if stable >= 2:
                break
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                proc.kill()
        log.close()
        try:
            os.remove(os.path.join(root, ".codemap-bench-watch.log"))
        except OSError:
            pass
    if state is None and failed_state is not None:
        raise RuntimeError("codemap's dependency graph build failed in the daemon (graph.status=failed, coverage=%s)" % json.dumps(failed_state.get("coverage"))[:300])
    if state is None:
        raise RuntimeError("daemon never published a dependency graph (exit %s)" % proc.returncode)
    edges = set()
    for src, targets in state["imports"].items():
        src = src.replace(os.sep, "/")
        for dst in targets:
            dst = dst.replace(os.sep, "/")
            if dst != src:
                edges.add((src, dst))
    cov = state.get("coverage") or {}
    return edges, {"graph_status": (state.get("graph") or {}).get("status"),
                   "coverage": {"status": cov.get("status"), "notes": cov.get("notes"), "issues": len(cov.get("issues") or [])},
                   "configured_file_count": state.get("configured_file_count"), "file_count": state.get("file_count"),
                   "generation": state.get("generation")}


def main(argv):
    method = "daemon"
    if "--method" in argv:
        i = argv.index("--method")
        method = argv[i + 1]
        del argv[i:i + 2]
    if len(argv) < 5:
        sys.exit("usage: codemap_edges.py <codemap-binary> <repo-root> <files-list> <out-edges-file> [jobs] [--method daemon|per-file]")
    binary, root, files_list, out_path = argv[1:5]
    jobs = int(argv[5]) if len(argv) > 5 else (os.cpu_count() or 2)
    root = os.path.realpath(root)
    files = [line.strip() for line in open(files_list) if line.strip()]
    started = time.time()
    if method == "per-file":
        edges, extra = collect_per_file(binary, root, files, jobs)
    else:
        edges, extra = collect_daemon(binary, root)
    with open(out_path, "w") as fh:
        for src, dst in sorted(edges):
            fh.write(f"{src}\t{dst}\n")
    version = subprocess.run([binary, "--version"], capture_output=True, text=True).stdout.strip()
    meta = {"codemap": version, "method": method, "edges": len(edges), "seconds": round(time.time() - started, 1), "jobs": jobs}
    meta.update(extra)
    with open(out_path + ".meta.json", "w") as fh:
        json.dump(meta, fh, indent=1)
    print(json.dumps(meta))


if __name__ == "__main__":
    main(sys.argv)
