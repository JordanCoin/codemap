#!/usr/bin/env python3
"""Rust truth: rustc's file universe and rust-analyzer's name resolution.

1. `cargo metadata --no-deps` lists the workspace members and their targets.
2. `cargo check --workspace --all-targets` makes rustc write dep-info
   (`target/debug/deps/*.d` and `target/debug/build/*/output` are not used;
   only the `.d` files): the source files rustc actually read for each crate
   target under the default features and this host's cfg. Their union over
   workspace members, restricted to in-repo .rs files, is the universe.
3. For every file in the universe, `bench/truth/rustsyn` (a program on the
   `syn` parser) lists the references to resolve with their positions:
   `mod name;` declarations, `use` tree leaves, multi-segment paths, and
   `include!`-family literals.
4. rust-analyzer, started as a language server over stdio on the
   workspace, answers textDocument/definition for each reference. The edge
   target is the file the definition lands in: for `mod name;` the module's
   file; for `use` leaves and paths the file holding the definition of the
   last named segment (the segment before `*` for a glob); for `include!`
   literals the file named relative to the including file, kept only when
   it is in the same crate's dep-info. Targets outside the repository
   (registry crates, the standard library) are dropped.

usage: rust_la.py <repo-root> <out-edges-file> --rustsyn <binary> [--target-dir <dir>] [--jobs N]
"""
import glob
import json
import os
import subprocess
import sys
import threading
import time

RS_EXTS = (".rs",)


def run(cmd, cwd, timeout=3600, env=None):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=env)


def dep_info_files(target_dir, root):
    """Files named in rustc dep-info, restricted to in-repo .rs files."""
    files = set()
    for d in glob.glob(os.path.join(target_dir, "debug", "deps", "*.d")) + glob.glob(os.path.join(target_dir, "debug", "*.d")):
        try:
            text = open(d, errors="replace").read()
        except OSError:
            continue
        # Format: "<output>: <dep> <dep> ..." lines, then "<dep>:" lines. Spaces in paths are escaped as "\ ".
        for line in text.splitlines():
            if ":" not in line:
                continue
            head, _, rest = line.partition(":")
            for tok in rest.replace("\\ ", "\0").split():
                tok = tok.replace("\0", " ")
                if tok.endswith(RS_EXTS):
                    p = os.path.realpath(tok if os.path.isabs(tok) else os.path.join(root, tok))
                    if p.startswith(root + os.sep):
                        files.add(os.path.relpath(p, root).replace(os.sep, "/"))
    return files


class LSP:
    def __init__(self, root):
        self.proc = subprocess.Popen(["rust-analyzer"], cwd=root, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open(os.devnull, "w"))
        self.next_id = 1
        self.pending = {}
        self.lock = threading.Lock()
        self.notifications = []
        self.reader = threading.Thread(target=self._read_loop, daemon=True)
        self.reader.start()

    def _read_loop(self):
        out = self.proc.stdout
        while True:
            headers = {}
            while True:
                line = out.readline()
                if not line:
                    return
                line = line.decode("ascii", errors="replace").strip()
                if not line:
                    break
                k, _, v = line.partition(":")
                headers[k.strip().lower()] = v.strip()
            length = int(headers.get("content-length", "0"))
            body = out.read(length)
            try:
                msg = json.loads(body)
            except json.JSONDecodeError:
                continue
            if "id" in msg and ("result" in msg or "error" in msg):
                with self.lock:
                    ev = self.pending.get(msg["id"])
                    if ev is not None:
                        ev["msg"] = msg
                        ev["event"].set()
            elif "method" in msg and "id" in msg:
                # server-to-client request (e.g. workDoneProgress/create, client/registerCapability): acknowledge
                self._send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
            else:
                with self.lock:
                    self.notifications.append(msg)

    def _send(self, msg):
        data = json.dumps(msg).encode()
        with self.lock:
            self.proc.stdin.write(b"Content-Length: %d\r\n\r\n" % len(data) + data)
            self.proc.stdin.flush()

    def request(self, method, params, timeout=600):
        with self.lock:
            rid = self.next_id
            self.next_id += 1
            ev = {"event": threading.Event(), "msg": None}
            self.pending[rid] = ev
        self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        if not ev["event"].wait(timeout):
            with self.lock:
                self.pending.pop(rid, None)
            raise TimeoutError(method)
        with self.lock:
            self.pending.pop(rid, None)
        return ev["msg"]

    def notify(self, method, params):
        self._send({"jsonrpc": "2.0", "method": method, "params": params})

    def wait_quiescent(self, timeout=3600):
        """Wait until rust-analyzer reports it is idle (no running progress)."""
        deadline = time.time() + timeout
        last_change = time.time()
        active = set()
        seen_any = False
        idx = 0
        while time.time() < deadline:
            with self.lock:
                new = self.notifications[idx:]
                idx = len(self.notifications)
            for n in new:
                if n.get("method") == "$/progress":
                    tok = n["params"].get("token")
                    kind = (n["params"].get("value") or {}).get("kind")
                    if kind == "begin":
                        active.add(tok); seen_any = True
                    elif kind == "end":
                        active.discard(tok)
                    last_change = time.time()
            if seen_any and not active and time.time() - last_change > 5:
                return True
            time.sleep(0.5)
        return False

    def close(self):
        try:
            self.request("shutdown", None, timeout=30)
            self.notify("exit", None)
        except Exception:  # noqa: BLE001
            pass
        self.proc.terminate()


def uri(path):
    return "file://" + path


def main(argv):
    rustsyn = None; target_dir = None; jobs = os.cpu_count() or 2
    if "--rustsyn" in argv:
        i = argv.index("--rustsyn"); rustsyn = os.path.abspath(argv[i + 1]); del argv[i:i + 2]
    if "--target-dir" in argv:
        i = argv.index("--target-dir"); target_dir = argv[i + 1]; del argv[i:i + 2]
    if "--jobs" in argv:
        i = argv.index("--jobs"); jobs = int(argv[i + 1]); del argv[i:i + 2]
    if len(argv) != 3 or not rustsyn:
        sys.exit("usage: rust_la.py <repo-root> <out-edges-file> --rustsyn <binary> [--target-dir <dir>] [--jobs N]")
    root = os.path.realpath(argv[1]); out_path = argv[2]
    target_dir = os.path.realpath(target_dir or os.path.join(root, "target"))
    env = dict(os.environ, CARGO_TARGET_DIR=target_dir, CARGO_NET_RETRY="3")
    meta = {"rustc": run(["rustc", "--version"], root).stdout.strip(), "cargo": run(["cargo", "--version"], root).stdout.strip(),
            "rust_analyzer": run(["rust-analyzer", "--version"], root).stdout.strip()}
    md = run(["cargo", "metadata", "--format-version", "1", "--no-deps"], root, env=env)
    if md.returncode != 0:
        meta["error"] = "cargo metadata failed: " + md.stderr.strip()[-300:]
        json.dump(meta, open(out_path + ".meta.json", "w"), indent=1); print(json.dumps(meta)); sys.exit(1)
    metadata = json.loads(md.stdout)
    meta["workspace_members"] = len(metadata["workspace_members"])
    chk = run(["cargo", "check", "--workspace", "--all-targets", "-j", str(jobs)], root, env=env, timeout=7200)
    with open(out_path + ".check.log", "w") as fh:
        fh.write(chk.stdout + chk.stderr)
    meta["cargo_check_ok"] = chk.returncode == 0
    universe = dep_info_files(target_dir, root)
    if not universe:
        meta["error"] = "no dep-info produced (cargo check failed before rustc ran; see truth.edges.check.log)"
        json.dump(meta, open(out_path + ".meta.json", "w"), indent=1); print(json.dumps(meta)); sys.exit(1)
    meta["source_files"] = len(universe)

    # References per file, from the syn-based helper.
    refs = {}
    parse_errors = 0
    for rel in sorted(universe):
        p = run([rustsyn, os.path.join(root, rel)], root, timeout=120)
        try:
            d = json.loads(p.stdout)
        except json.JSONDecodeError:
            parse_errors += 1; continue
        if d.get("parse_error"):
            parse_errors += 1; continue
        refs[rel] = d["references"]
    meta["parse_errors"] = parse_errors
    meta["references"] = sum(len(v) for v in refs.values())

    lsp = LSP(root)
    init = lsp.request("initialize", {
        "processId": os.getpid(), "rootUri": uri(root), "capabilities": {"textDocument": {"definition": {"linkSupport": False}}, "window": {"workDoneProgress": True}},
        "initializationOptions": {"cargo": {"buildScripts": {"enable": True}, "targetDir": target_dir, "allTargets": True}, "procMacro": {"enable": True},
                                   "checkOnSave": False, "cachePriming": {"enable": True}},
        "workspaceFolders": [{"uri": uri(root), "name": "repo"}]}, timeout=600)
    lsp.notify("initialized", {})
    meta["rust_analyzer_ready"] = lsp.wait_quiescent(timeout=3600)
    edges, stats = set(), {"resolved": 0, "unresolved": 0, "external": 0, "include": 0, "errors": 0}
    for rel, items in refs.items():
        abs_path = os.path.join(root, rel)
        try:
            text = open(abs_path, encoding="utf-8", errors="replace").read()
        except OSError:
            continue
        lsp.notify("textDocument/didOpen", {"textDocument": {"uri": uri(abs_path), "languageId": "rust", "version": 1, "text": text}})
        for ref in items:
            kind = ref["kind"]
            if kind == "include":
                stats["include"] += 1
                target = os.path.normpath(os.path.join(os.path.dirname(rel), ref["literal"]))
                if target in universe and target != rel:
                    edges.add((rel, target))
                else:
                    stats["unresolved"] += 1
                continue
            seg = ref["segments"][-1]
            if kind == "path" and len(ref["segments"]) < 2:
                continue
            try:
                resp = lsp.request("textDocument/definition", {"textDocument": {"uri": uri(abs_path)}, "position": seg["pos"]}, timeout=120)
            except TimeoutError:
                stats["errors"] += 1; continue
            res = resp.get("result")
            if not res:
                stats["unresolved"] += 1; continue
            locs = res if isinstance(res, list) else [res]
            hit = False
            for loc in locs:
                u = loc.get("uri") or loc.get("targetUri") or ""
                if not u.startswith("file://"):
                    continue
                path = os.path.realpath(u[len("file://"):])
                if path.startswith(root + os.sep) and not path.startswith(target_dir + os.sep):
                    trel = os.path.relpath(path, root).replace(os.sep, "/")
                    if trel in universe:
                        if trel != rel:
                            edges.add((rel, trel))
                        hit = True
                else:
                    stats["external"] += 1
                    hit = True
            if hit:
                stats["resolved"] += 1
            else:
                stats["unresolved"] += 1
        lsp.notify("textDocument/didClose", {"textDocument": {"uri": uri(abs_path)}})
    lsp.close()
    with open(out_path, "w") as fh:
        for a, b in sorted(edges):
            fh.write(f"{a}\t{b}\n")
    with open(out_path + ".files", "w") as fh:
        for f in sorted(universe):
            fh.write(f + "\n")
    meta.update(stats); meta["edges"] = len(edges)
    json.dump(meta, open(out_path + ".meta.json", "w"), indent=1)
    print(json.dumps(meta))


if __name__ == "__main__":
    main(sys.argv)
