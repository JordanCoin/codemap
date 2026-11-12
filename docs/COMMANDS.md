# Commands

This doc answers: what does every codemap command, flag and mode do?

```bash
codemap .              # structure view (respects .codemap/config.json)
codemap --diff         # what changed vs main
codemap --deps .       # dependency flow
codemap --importers f  # who imports a file
codemap blast-radius   # review bundle: diff + deps + importers
codemap collide        # rank open PRs by shared-file merge-order hazard
codemap find "<query>" # where is X defined: rank by path, symbol names, then body text ("(body)" tag), with importer counts
codemap handoff .      # save layered handoff for cross-agent continuation
codemap context        # machine-readable project context JSON
codemap doctor         # validate agent integrations
codemap skill list     # available agent skills
codemap watch start    # background daemon for live graph state
codemap serve          # HTTP API for non-MCP integrations
codemap mcp            # MCP server on stdio
codemap --version
```

## Options

Standard linked Git worktrees automatically reuse the primary worktree's
`.codemap/config.json` and project skills. Create the worktree with Git, an IDE,
or any manager that uses standard linked-worktree metadata, then give the agent
its absolute path:

```bash
git worktree add <path> -b <branch> <base>
codemap -C /tmp/feature-worktree context
```

Normal CLI and plugin MCP calls need no `--setup-root`: central config and skills
come from the primary worktree, while handoffs, watcher files, and hook/session
state remain in the linked worktree. Independent clones have no trusted Git
metadata linking them, so sharing setup between them still requires an explicit
override:

```bash
codemap -C /tmp/independent-clone --setup-root /path/to/original context
```

`-C`/`--project-root` operates on exactly the named directory, like `git -C`;
it may be a subdirectory of a repository, and storage and config discovery still
walk up to the git root. `--setup-root` explicitly reuses `<repository>/.codemap`
policy and runtime state from another checkout. Both accept a repository or
subdirectory; relative setup paths resolve from the project root.

| Flag | Description |
|------|-------------|
| `-C, --project-root <dir>` | Operate on exactly `<dir>` (may be a subdirectory of a repository; storage and config discovery still walk up to the git root) |
| `--setup-root <repo>` | Explicitly reuse policy and runtime state from `<repo>/.codemap` |
| `--depth, -d <n>` | Limit tree depth (0 = unlimited) |
| `--only <exts>` | Only include files with these extensions |
| `--exclude <patterns>` | Exclude files matching patterns |
| `--max-files <n>` | Scan bound: files kept by the tree, and the largest repo a dependency scan runs on (default 100000, or config `max_files`; `-1` = unbounded). Above it the output says `bounded: scanned N of M files` instead of presenting a prefix as the repo |
| `--diff` | Show files changed vs main branch |
| `--ref <branch>` | Branch to compare against (with `--diff`) |
| `--include-untracked` | With `--diff --ref`: also list untracked files |
| `--deps` | Dependency flow mode |
| `--stdin` | Read JSON file manifest from stdin (use with `--deps`) |
| `--importers <file>` | Check who imports a file |
| `--hubs` | List hub files (3+ importers) |
| `--file <file>` | Imports, importers, and hub status for one file |
| `--topology` | Project/module dependency topology |
| `--module <id\|name>` | Context for one module ID or unique name |
| `--module-file <file>` | Context for the module owning a file |
| `--ecosystem <provider>` | Filter topology by provider |
| `--skyline` | City skyline visualization |
| `--animate` | Animate the skyline (with `--skyline`) |
| `--json` | Output JSON |
| `--debug` | Show scanner and path diagnostics |
| `--color <mode>` | `auto` (default): ANSI colour only when stdout is a terminal and `NO_COLOR` is unset; `always` keeps colour when piping into `less -R`; `never` turns it off |

> Flags come before the path/URL: `codemap --json github.com/user/repo`

**Pattern matching** needs no quotes: `.png` matches any `.png` file, `Fonts` matches any `/Fonts/` directory, `*Test*` is a glob.

## Modes

### Diff

```bash
codemap --diff
codemap --diff --ref develop
```

```
╭─────────────────────────── myproject ──────────────────────────╮
│ Changed: 4 files | +156 -23 lines vs main                      │
╰────────────────────────────────────────────────────────────────╯
├── api/
│   └── (new) auth.go         ✎ handlers.go (+45 -12)
└── ✎ main.go (+29 -3)

⚠ handlers.go is used by 3 other files
```

### Dependency flow

```bash
codemap --deps .
```

```
╭──────────────────────────────────────────────────────────────╮
│                    MyApp - Dependency Flow                   │
├──────────────────────────────────────────────────────────────┤
│ Go: chi, zap, testify                                        │
╰──────────────────────────────────────────────────────────────╯

Backend ════════════════════════════════════════════════════
  server ───▶ validate ───▶ rules, config
  api ───▶ handlers, middleware

HUBS: config (12←), api (8←), utils (5←)
```

### Blast radius

Who breaks if you change a file:

```bash
codemap --importers config/config.go
```

```
⚠️  HUB FILE: config/config.go
   Imported by 42 files (package codemap/config: Go imports name packages, not files) - changes have wide impact!

   Dependents:
   • blast_radius.go
   • blast_radius_fixes_test.go
   ...
```

For a review bundle in one command, as Markdown, text, or a single JSON object:

```bash
codemap blast-radius --ref main .
codemap blast-radius --json --ref main .
codemap blast-radius --text --ref main .
```

### Skyline

```bash
codemap --skyline --animate
```

![codemap skyline](../assets/skyline-animated.gif)

### Remote repos

Analyze any public GitHub or GitLab repo without cloning it yourself:

```bash
codemap github.com/anthropics/anthropic-cookbook
codemap gitlab.com/user/repo
```

Shallow-clones to a temp directory and cleans up. If you already have the repo locally, codemap uses your copy.

Next: [tune what the agent sees](CONFIG.md)
