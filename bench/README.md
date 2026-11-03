# Import-accuracy benchmark

Measures how well a released `codemap` binary recovers file-to-file import edges, against truth produced by each language's own toolchain. One command, pinned inputs, full census, pre-registered repositories.

```bash
make bench            # downloads and verifies codemap v4.6.4, clones, builds truth, runs codemap, scores
bench/run.sh --group held_out
bench/run.sh --repo expressjs__express
CODEMAP=./codemap bench/run.sh      # score a local build against the same truth
```

Results land in `bench/results/v<version>.md` and `.json` (committed). Clones, virtualenvs, the downloaded binary and per-repo edge files live in `bench/cache/` and `bench/results/<version>/` (gitignored).

## What is measured

An **edge** is `source file -> target file`, both in the repository, both source files of the language under test, written as repository-relative paths with forward slashes. For each repository the benchmark builds two edge sets over the same universe of source files, then counts:

- **recall** = truth edges codemap also reports, over all truth edges
- **precision** = codemap edges that are in truth, over all codemap edges
- **95% intervals** are Wilson score intervals on the full edge census. There is no sampling; `n` is the number of edges in each set. Per-language pooled rows sum counts across the group's repos.

Scope rules are applied identically to both sides by `bench/score.py`: the source must be in the truth generator's source-file list, and the target must be an in-repo file with the language's target extensions (`.go`; `.py`; for TypeScript and JavaScript any of `.ts .tsx .mts .cts .js .jsx .mjs .cjs`, never `.d.ts`, never under `node_modules`). Externals (standard library, third-party packages, Node builtins) are not edges on either side.

## How truth is built

Never a regex. Each generator writes `truth.edges` (`src<TAB>dst` lines), `truth.edges.files` (the source universe) and `truth.edges.meta.json`.

| language | truth | scope decisions |
|---|---|---|
| Go | `go list -e -json -deps ./...` with `GOFLAGS=-mod=mod GOWORK=off` (`bench/truth/go.py`) | Go imports name packages. If package P imports in-module package Q, every non-test file of P gets an edge to every non-test file of Q. Test files are out of scope on both sides because `go list` without `-test` reports no test imports. Files excluded by build constraints at the default GOOS/GOARCH are not in `GoFiles`, so they are out of scope. |
| TypeScript | TypeScript compiler API 5.9.3, `ts.resolveModuleName` with each `tsconfig.json`'s own options (`moduleResolution`, `paths`, `baseUrl`, `rootDirs`, `extends`), per package in monorepos (`bench/truth/ts.mjs`) | Sources are the files listed by some tsconfig in the repo (outside `node_modules`), excluding `.d.ts`. Specifiers come from import declarations, `export ... from`, `import x = require()`, `require("literal")` and `import("literal")`. Type-only imports count. Triple-slash references do not. A workspace package reached through a `node_modules` symlink is resolved to its real in-repo path. Dependencies are installed from the repo's lockfile with scripts disabled before resolution. |
| JavaScript | Node 22's own resolver (`bench/truth/js.mjs`): `require.resolve` through `createRequire(file)` for CommonJS, `import.meta.resolve(spec, parentURL)` for ESM (needs `--experimental-import-meta-resolve`). Specifiers are extracted by the TypeScript parser in JS mode. | A file's module type is what Node uses: `.mjs` ESM, `.cjs` CommonJS, `.js` by the nearest `package.json` `"type"`. Sources are all in-repo `.js .mjs .cjs .jsx` files outside `node_modules`, `dist`, `build`, `vendor`, `coverage`. Resolutions to `.json` or to builtins are out of scope. Dependencies installed as for TypeScript. |
| Python | A virtualenv with the repo installed as documented (`pip install -e .`, falling back to `--no-deps` and recording which), then `ast` to walk import statements and `importlib.util.find_spec` inside that interpreter (`bench/truth/py_walk.py`) | Sources are all in-repo `.py` files outside build, dist, venvs and site-packages. The target is the final module a statement names: `import a.b.c` -> `a/b/c.py` or `a/b/c/__init__.py`; `from a.b import c` -> `a/b/c.py` when `a.b.c` is a module, else `a/b/__init__.py` or `a/b.py`. Relative imports resolve against the file's package (directories containing `__init__.py`). Parent packages Python also executes on the way are not edges. Imports inside functions, `try` blocks and `if TYPE_CHECKING:` count: they are import statements. Namespace packages without `__init__.py` are not handled. |

## How codemap's edges are collected

codemap v4.6.4's `--deps --json` carries raw import strings, not resolved targets, so it cannot be scored. The resolved graph is exposed per file through the public `codemap --json --importers <file>` contract, whose `imports` array lists the in-repo files that file resolves to. `bench/codemap_edges.py` queries every source file in the truth universe once, from the clone root, in parallel, with default configuration: no `.codemap/config.json` is written or tuned by the benchmark. codemap itself auto-writes one on first run in a git checkout, which is the behaviour a new user gets, and the runner leaves it alone.

The binary is the released asset, as a new user would install it: `codemap-full_<version>_linux_amd64.tar.gz` downloaded from the GitHub release and verified against the release's `checksums.txt` before use. The `-full` asset bundles the ast-grep the release was built against; the plain asset expects ast-grep on `PATH`.

## Repositories

`bench/manifest.json` is the pre-registered set. It was written by `bench/select.py` from one committed GitHub search page per language (`bench/selection/`, `language:<lang> fork:false archived:false stars:>3000`, sorted by stars, 100 results) and committed before any codemap run, so the history proves the order of events. The rule is mechanical and every candidate's decision is in the manifest's `screen_log`:

1. in star order, skip the tuned list (below)
2. pin the SHA of the default branch head (`git ls-remote`) and list the tree at that SHA
3. require the language's toolchain manifest at the root (`go.mod`; `package.json` plus a `tsconfig.json`; `package.json`; `pyproject.toml`, `setup.py` or `setup.cfg`)
4. require 100 to 3000 source files, counted by extension outside `vendor`, `node_modules`, `third_party`, `dist`, `build`
5. the first three that pass per language are held out; the next two are alternates, used in order only when a held-out repo's toolchain truth cannot be built, and any substitution is reported in the results

The **tuned** group (`sindresorhus/ky`, `excalidraw/excalidraw`, `trpc/trpc`, `expressjs/express`, `eslint/eslint`, `NodeBB/NodeBB`, `pytest-dev/pytest`, `pallets/werkzeug`) is the set of repositories earlier codemap fixes were developed against. It is pinned and scored the same way but reported separately and never pooled into the held-out numbers. There is no tuned Go repository.

## Spot check

`bench/score.py` draws five disagreeing edges per language (seed 20261103, mixing false negatives and false positives) from the held-out pool. Each is classified by hand in the results as **codemap wrong**, **truth wrong** or **scope ambiguity**, with the reason, so a reader can judge whether the truth generators are themselves trustworthy. When a spot check shows a generator is wrong, the generator is fixed and the run is repeated; codemap is never changed to improve its score.

## Tool versions

Recorded in each run's `truth.edges.meta.json` and in the results JSON: Go, Node, TypeScript, Python and codemap versions. The run that produced `results/v4.6.4.md` used Go 1.24.7, Node v22.22.0, TypeScript 5.9.3, Python 3.13.16, ast-grep 0.42.1 (bundled in the release asset).

## Limitations

- Linux amd64 only (the release asset the runner downloads).
- Go truth uses the default build tags; platform-specific files for other OSes are out of scope rather than counted against codemap.
- TypeScript sources are limited to files some tsconfig includes; scripts outside every tsconfig are not scored.
- JavaScript dynamic imports with non-literal specifiers are not edges on either side.
- Python truth needs the repository to install; a repo whose `pip install -e .` fails even with `--no-deps` is skipped and replaced by the next alternate, which the results state.
- Truth reflects the toolchain's view at the default configuration. A repo whose build depends on code generation that was not run will have unresolvable specifiers; those are not edges in truth, and a codemap edge to a checked-in file with the same name would count as a false positive. The spot check is where such cases are classified.
- The per-file `--importers` query rebuilds codemap's graph on every call; the benchmark reports wall time but does not benchmark speed.
