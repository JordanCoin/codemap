#!/usr/bin/env bash
# Import-accuracy benchmark for a released codemap binary.
#
#   make bench                 # everything: download + verify codemap, clone, truth, codemap, score
#   bench/run.sh --group held_out
#   bench/run.sh --repo expressjs__express
#   CODEMAP=/path/to/codemap bench/run.sh   # score another build with the same truth
#
# Environment:
#   CODEMAP_VERSION  release to download when CODEMAP is unset (default 4.6.4)
#   CODEMAP          path to a codemap binary (default: the verified release download)
#   BENCH_CACHE      clone / venv / truth cache, gitignored (default bench/cache)
#   JOBS             parallel codemap queries per repo (default: nproc)
#   BENCH_RESULTS_STEM  results file stem (default v<version>); BENCH_SCORE_LANG
#                    restricts scoring to one language or to c-cpp-rust
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CODEMAP_VERSION="${CODEMAP_VERSION:-4.6.4}"
BENCH_CACHE="${BENCH_CACHE:-$HERE/cache}"
JOBS="${JOBS:-$(nproc 2>/dev/null || echo 2)}"
MANIFEST="$HERE/manifest.json"
RESULTS="$HERE/results"
BENCH_GROUPS="held_out tuned"
ONLY_REPO=""
ONLY_LANG=""
METHOD="${METHOD:-daemon}"

while [ $# -gt 0 ]; do
  case "$1" in
    --group) BENCH_GROUPS="$2"; shift 2 ;;
    --repo) ONLY_REPO="$2"; shift 2 ;;
    --lang) ONLY_LANG="$2"; shift 2 ;;
    --method) METHOD="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

mkdir -p "$BENCH_CACHE" "$RESULTS"
log() { printf '[bench %s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# ---------------------------------------------------------------- codemap
if [ -z "${CODEMAP:-}" ]; then
  REL="$BENCH_CACHE/codemap-$CODEMAP_VERSION"
  if [ ! -x "$REL/codemap" ]; then
    log "downloading codemap-full v$CODEMAP_VERSION (bundles the ast-grep the release was built against)"
    mkdir -p "$REL"
    base="https://github.com/JordanCoin/codemap/releases/download/v$CODEMAP_VERSION"
    asset="codemap-full_${CODEMAP_VERSION}_linux_amd64.tar.gz"
    curl -fsSL -o "$REL/$asset" "$base/$asset"
    curl -fsSL -o "$REL/checksums.txt" "$base/checksums.txt"
    (cd "$REL" && grep " $asset\$" checksums.txt | sha256sum -c -)
    tar xzf "$REL/$asset" -C "$REL"
  fi
  CODEMAP="$REL/codemap"
  export PATH="$REL:$PATH"
fi
CODEMAP="$(cd "$(dirname "$CODEMAP")" && pwd)/$(basename "$CODEMAP")"
VERSION="$("$CODEMAP" --version | awk '{print $2}')"
log "codemap $VERSION at $CODEMAP"

# ------------------------------------------------------------ tool setup
if [ ! -d "$HERE/tools/node_modules/typescript" ]; then
  log "installing pinned truth tools (bench/tools/package.json)"
  (cd "$HERE/tools" && npm ci --no-audit --no-fund --silent)
fi

# ------------------------------------------------------------- helpers
clone_at() { # url sha dest
  local url="$1" sha="$2" dest="$3"
  if [ -d "$dest/.git" ] && [ "$(git -C "$dest" rev-parse HEAD 2>/dev/null)" = "$sha" ]; then return 0; fi
  rm -rf "$dest"; mkdir -p "$dest"
  git -C "$dest" init -q
  git -C "$dest" remote add origin "$url"
  git -C "$dest" fetch -q --depth 1 origin "$sha"
  git -C "$dest" checkout -q FETCH_HEAD
}

install_node_deps() { # repo-dir -> 0 on success
  # The package manager named by the repo's lockfile is fetched from the npm
  # registry with npx (corepack's shims download yarn/pnpm from
  # repo.yarnpkg.com, which some networks block). Scripts never run.
  local dir="$1"
  export COREPACK_ENABLE_DOWNLOAD_PROMPT=0 COREPACK_ENABLE_STRICT=0
  if [ -f "$dir/bun.lock" ] || [ -f "$dir/bun.lockb" ]; then
    (cd "$dir" && bun install --frozen-lockfile --ignore-scripts >/dev/null 2>&1)
  elif [ -f "$dir/pnpm-lock.yaml" ]; then
    local pm; pm="$(sed -n 's/.*"packageManager": *"\(pnpm@[0-9.]*\)".*/\1/p' "$dir/package.json" | head -1)"
    (cd "$dir" && npx --yes "${pm:-pnpm@10}" install --frozen-lockfile --ignore-scripts >/dev/null 2>&1)
  elif [ -f "$dir/yarn.lock" ]; then
    if grep -q '__metadata' "$dir/yarn.lock"; then
      local yv; yv="$(sed -n 's/.*"packageManager": *"yarn@\([0-9.]*\).*/\1/p' "$dir/package.json" | head -1)"
      (cd "$dir" && YARN_ENABLE_SCRIPTS=false npx --yes --package="@yarnpkg/cli-dist@${yv:-4}" yarn install --immutable >/dev/null 2>&1)
    else
      (cd "$dir" && npx --yes yarn@1.22.22 install --frozen-lockfile --ignore-scripts --non-interactive >/dev/null 2>&1)
    fi
  elif [ -f "$dir/package-lock.json" ]; then
    (cd "$dir" && npm ci --ignore-scripts --no-audit --no-fund --silent >/dev/null 2>&1)
  else
    (cd "$dir" && npm install --ignore-scripts --no-audit --no-fund --silent >/dev/null 2>&1)
  fi
}

write_status() { # dir status reason
  printf '{"status":"%s","reason":"%s","finished_at":"%s"}\n' "$2" "$3" "$(date -u +%FT%TZ)" > "$1/status.json"
}

run_repo() { # group lang repo url sha slug
  local group="$1" lang="$2" repo="$3" url="$4" sha="$5" slug="$6"
  local dir="$BENCH_CACHE/repos/$slug" out="$RESULTS/$VERSION/$slug"
  mkdir -p "$out"
  if [ -f "$out/status.json" ] && [ -f "$out/codemap.edges" ] && [ -z "$ONLY_REPO" ]; then
    log "$repo: cached result, skipping (delete $out to redo)"; return 0
  fi
  log "$repo ($lang, $group): clone at $sha"
  if ! clone_at "$url" "$sha" "$dir"; then write_status "$out" skipped "clone failed"; return 0; fi

  log "$repo: toolchain truth"
  local t0=$SECONDS ok=1
  case "$lang" in
    go)
      python3 -I "$HERE/truth/go.py" "$dir" "$out/truth.edges" > "$out/truth.log" 2>&1 || ok=0 ;;
    typescript|javascript)
      if ! install_node_deps "$dir"; then
        log "$repo: dependency install failed; resolving without node_modules"
        echo '{"dependency_install":"failed"}' > "$out/install.json"
      else
        echo '{"dependency_install":"ok"}' > "$out/install.json"
      fi
      if [ "$lang" = typescript ]; then
        node "$HERE/truth/ts.mjs" "$dir" "$out/truth.edges" > "$out/truth.log" 2>&1 || ok=0
      else
        node --experimental-import-meta-resolve "$HERE/truth/js.mjs" "$dir" "$out/truth.edges" > "$out/truth.log" 2>&1 || ok=0
      fi ;;
    c|cpp)
      python3 -I "$HERE/truth/cc.py" "$dir" "$out/truth.edges" --build-dir "$BENCH_CACHE/build/$slug" --jobs "$JOBS" > "$out/truth.log" 2>&1 || ok=0 ;;
    rust)
      if [ ! -x "$HERE/truth/rustsyn/target/release/codemap-bench-rustsyn" ]; then
        (cd "$HERE/truth/rustsyn" && cargo build --release -q)
      fi
      python3 -I "$HERE/truth/rust_la.py" "$dir" "$out/truth.edges" --rustsyn "$HERE/truth/rustsyn/target/release/codemap-bench-rustsyn" --target-dir "$BENCH_CACHE/cargo-target/$slug" --jobs "$JOBS" > "$out/truth.log" 2>&1 || ok=0 ;;
    python)
      local venv="$BENCH_CACHE/venv/$slug"
      [ -x "$venv/bin/python" ] || python3 -m venv "$venv"
      if "$venv/bin/pip" install -q -e "$dir" > "$out/install.log" 2>&1; then
        echo '{"dependency_install":"ok"}' > "$out/install.json"
      elif "$venv/bin/pip" install -q --no-deps -e "$dir" >> "$out/install.log" 2>&1; then
        log "$repo: pip install -e . failed, installed with --no-deps"
        echo '{"dependency_install":"no-deps"}' > "$out/install.json"
      else
        # Not a package (an application run from its root); the truth generator
        # always has the repo root first on sys.path, so resolution still works.
        log "$repo: not pip-installable; resolving from the repo root only"
        echo '{"dependency_install":"failed"}' > "$out/install.json"
      fi
      "$venv/bin/python" -I "$HERE/truth/py_walk.py" "$dir" "$out/truth.edges" > "$out/truth.log" 2>&1 || ok=0 ;;
  esac
  if [ $ok != 1 ] || [ ! -s "$out/truth.edges.files" ]; then
    write_status "$out" skipped "toolchain truth unavailable (see truth.log)"; return 0
  fi
  log "$repo: truth $(wc -l < "$out/truth.edges") edges over $(wc -l < "$out/truth.edges.files") files in $((SECONDS - t0))s"

  log "$repo: codemap edges via $METHOD (default config)"
  t0=$SECONDS
  if ! python3 -I "$HERE/codemap_edges.py" "$CODEMAP" "$dir" "$out/truth.edges.files" "$out/codemap.edges" "$JOBS" --method "$METHOD" > "$out/codemap.log" 2>&1; then
    # codemap produced no graph: scored as zero edges, with the reason.
    : > "$out/codemap.edges"
    reason="$(grep -o 'RuntimeError: .*' "$out/codemap.log" | tail -1 | cut -c15- | tr -d '"' | cut -c1-300)"
    write_status "$out" codemap_failed "${reason:-see codemap.log}"; return 0
  fi
  log "$repo: codemap $(wc -l < "$out/codemap.edges") edges in $((SECONDS - t0))s"
  write_status "$out" ok ""
}

# ---------------------------------------------------------------- main
for group in $BENCH_GROUPS; do
  for lang in go typescript javascript python c cpp rust; do
    [ -n "$ONLY_LANG" ] && [ "$ONLY_LANG" != "$lang" ] && continue
    python3 -c "
import json,sys
m=json.load(open('$MANIFEST'))
for r in m['groups']['$group'].get('$lang',[]):
    print(r['repo'], r['url'], r['sha'], r['slug'])
" | while read -r repo url sha slug; do
      [ -n "$ONLY_REPO" ] && [ "$ONLY_REPO" != "$slug" ] && continue
      run_repo "$group" "$lang" "$repo" "$url" "$sha" "$slug"
    done
  done
done

log "scoring"
STEM="${BENCH_RESULTS_STEM:-v$VERSION}"
# shellcheck disable=SC2086
python3 -I "$HERE/score.py" "$MANIFEST" "$RESULTS/$VERSION" "$RESULTS/$STEM.json" "$RESULTS/$STEM.md" "$VERSION" ${BENCH_SCORE_LANG:+--lang $BENCH_SCORE_LANG} > /dev/null
log "wrote $RESULTS/$STEM.md and $RESULTS/$STEM.json"
