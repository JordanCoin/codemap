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
   target is the module file the path imports from, as for the other
   languages in this benchmark (the file a specifier resolves to, not the
   file that happens to define a re-exported name): for `mod name;` the
   module's file; for `use` leaves and paths, the file of the deepest
   segment that names a module or crate root (the last segment when it is
   itself a module, otherwise the segment before it, which rust-analyzer
   resolves to the module's file, a crate's lib.rs or main.rs, or the file
   holding an inline `mod name { }`): the path is walked from its last
   segment backwards and the first segment whose definition is a module
   gives the target; for `include!` literals the file named relative to
   the including file, kept only when it is in the same crate's dep-info.
   A path none of whose segments names a module (`Foo::new()`) depends on
   the `use` that brought `Foo` in, which is already an edge, so it yields
   none. Targets outside the repository (registry crates, the standard
   library) are dropped. The first scored pass used the file defining the
   last segment instead; see bench/README.md for the disclosed before and
   after.

usage: rust_la.py <repo-root> <out-edges-file> --rustsyn <binary> [--target-dir <dir>] [--jobs N]
"""
import glob
import json
import os
import pathlib
import re
import subprocess
import urllib.parse
import sys
import threading
import time

RS_EXTS = (".rs",)
MOD_ITEM = re.compile(r"^\s*(pub(\([^)]*\))?\s+)?(unsafe\s+)?mod\s+r?#?\w+\s*\{")


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
    def __init__(self, root, stderr_path=None):
        self.proc = subprocess.Popen(["rust-analyzer"], cwd=root, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open(stderr_path or os.devnull, "w"))
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
    return pathlib.Path(path).as_uri()


def uri_to_path(u):
    """The filesystem path of a file URI, or None for any other scheme."""
    parsed = urllib.parse.urlparse(u)
    if parsed.scheme != "file":
        return None
    return urllib.parse.unquote(parsed.path)


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
    # A repository may pin its toolchain (rust-toolchain.toml); rustup's
    # proxies resolve it from the working directory, so every tool below is
    # run from the repository root, and the rust-analyzer component is added
    # to whichever toolchain that resolves to.
    run(["rustup", "component", "add", "rust-analyzer"], root, timeout=1800)
    meta = {"rustc": run(["rustc", "--version"], root).stdout.strip(), "cargo": run(["cargo", "--version"], root).stdout.strip(),
            "rust_analyzer": run(["rust-analyzer", "--version"], root).stdout.strip(),
            "toolchain": run(["rustup", "show", "active-toolchain"], root).stdout.strip().split("\n")[0]}
    md = run(["cargo", "metadata", "--format-version", "1", "--no-deps"], root, env=env)
    if md.returncode != 0:
        meta["error"] = "cargo metadata failed: " + md.stderr.strip()[-300:]
        json.dump(meta, open(out_path + ".meta.json", "w"), indent=1); print(json.dumps(meta)); sys.exit(1)
    metadata = json.loads(md.stdout)
    meta["workspace_members"] = len(metadata["workspace_members"])
    member_ids = set(metadata["workspace_members"])
    crate_names = set()
    for pkg in metadata["packages"]:
        if pkg["id"] in member_ids:
            crate_names.add(pkg["name"].replace("-", "_"))
            for tgt in pkg.get("targets", []):
                crate_names.add(tgt["name"].replace("-", "_"))
    meta["workspace_crates"] = sorted(crate_names)
    # --keep-going: a crate whose build script fails (a packaging-only
    # prerequisite, say) must not stop rustc from checking the other crates,
    # whose dep-info is the universe.
    chk = run(["cargo", "check", "--workspace", "--all-targets", "--keep-going", "-j", str(jobs)], root, env=env, timeout=7200)
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

    lsp = LSP(root, out_path + ".rust-analyzer.log")
    init = lsp.request("initialize", {
        "processId": os.getpid(), "rootUri": uri(root), "capabilities": {"textDocument": {"definition": {"linkSupport": False}}, "window": {"workDoneProgress": True}},
        "initializationOptions": {"cargo": {"buildScripts": {"enable": True}, "targetDir": target_dir, "allTargets": True}, "procMacro": {"enable": True},
                                   "checkOnSave": False, "cachePriming": {"enable": True}},
        "workspaceFolders": [{"uri": uri(root), "name": "repo"}]}, timeout=600)
    lsp.notify("initialized", {})
    meta["rust_analyzer_ready"] = lsp.wait_quiescent(timeout=3600)
    edges, stats = set(), {"resolved": 0, "unresolved": 0, "external": 0, "include": 0, "errors": 0, "skipped_no_module": 0}
    _line_cache = {}

    def file_lines(path):
        if path not in _line_cache:
            _line_cache[path] = open(path, encoding="utf-8", errors="replace").read().split("\n")
        return _line_cache[path]
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
            segs = ref["segments"]
            if kind == "path" and len(segs) < 2:
                continue

            def definition(seg):
                try:
                    resp = lsp.request("textDocument/definition", {"textDocument": {"uri": uri(abs_path)}, "position": seg["pos"]}, timeout=120)
                except TimeoutError:
                    return None
                res = resp.get("result")
                if not res:
                    return []
                return res if isinstance(res, list) else [res]

            def located(loc):
                raw = uri_to_path(loc.get("uri") or loc.get("targetUri") or "")
                if raw is None:
                    return None, None
                path = os.path.realpath(raw)
                rng = loc.get("range") or loc.get("targetSelectionRange") or {}
                start = rng.get("start") or {}
                return path, (start.get("line", -1), start.get("character", -1))

            def names_module(path, start):
                """A definition is a module when it is a file's start (a file
                module or crate root) or an inline `mod name {` item."""
                if start == (0, 0):
                    return True
                try:
                    line = file_lines(path)[start[0]]
                except (IndexError, OSError):
                    return False
                return MOD_ITEM.match(line) is not None

            # Walk the path from its last segment backwards; the edge target is
            # the deepest segment that names a module (or crate root). A path
            # whose segments name no module at all (`Foo::new()`) depends on the
            # `use` that brought `Foo` in, which is already an edge, so it is
            # skipped. For `mod name;` the single segment is the module.
            hit = False
            errored = False
            for seg in reversed(segs):
                locs = definition(seg)
                if locs is None:
                    errored = True
                    break
                for loc in locs:
                    path, start = located(loc)
                    if path is None:
                        continue
                    if kind != "mod" and not names_module(path, start):
                        continue
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
                    break
            if errored:
                stats["errors"] += 1
                continue
            if not hit:
                stats["skipped_no_module"] += 1
                continue
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
