# codemap 🗺️

> **codemap — structural ground truth for coding agents.**
> Resolves what your code actually imports, tells you what breaks if you change it, and is explicit about what it couldn't figure out.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.24+-00ADD8.svg)
![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/JordanCoin/6ffe3276ddb8a7a7f08d50d649e567bd/raw/codemap-coverage.json)
[![Run in Smithery](https://smithery.ai/badge/skills/jordancoin)](https://smithery.ai/skills?ns=jordancoin&utm_source=github&utm_medium=badge)

![codemap screenshot](assets/codemap.png)

## What it's for

An agent reading your repo can see what a file *says*. It can't cheaply see what depends on that file — that answer lives in `go.mod`, Cargo workspace membership, `package.json` `exports` maps, and `tsconfig` path aliases, not in the source text.

codemap computes three things:

| | |
|---|---|
| **Orientation** | A structure map with the most-imported files called out. Cheap cold start, useful when an agent has no memory of the last hour. |
| **Dependency graph** | Imports resolved through each ecosystem's real rules — not string matching. |
| **Blast radius** | Who breaks if you change this file. |

And one thing that matters more than any of them: **it tells you when it doesn't know.** Every dependency answer carries a coverage status, so a partial graph never reads as a complete one.

```bash
codemap .                        # structure + hubs
codemap --importers path/to/file # who depends on this
codemap --diff                   # what changed vs main
```

## Install

```bash
# macOS/Linux
brew tap JordanCoin/tap && brew install codemap

# Windows
scoop bucket add codemap https://github.com/JordanCoin/scoop-codemap
scoop install codemap
```

> Other options: [Releases](https://github.com/JordanCoin/codemap/releases) | `go install` | build from source

### CI / tarball install

Release tarballs ship `codemap` and the bundled rules but not the `ast-grep` executable, which `--deps` needs. Either install it separately:

```bash
apk add --no-cache curl jq bash python3 py3-pip

ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

CODEMAP_VERSION=$(curl -fsSL https://api.github.com/repos/JordanCoin/codemap/releases/latest | jq -r '.tag_name' | tr -d 'v')
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap

python3 -m pip install --no-cache-dir ast-grep-cli
```

…or use the self-contained `codemap-full` artifact, which bundles `codemap`, `ast-grep`, and `sg`:

```bash
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap-full_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap ast-grep sg
```

## Setup

Run setup anywhere inside your git repo. Repo-scoped commands such as
`setup`, `doctor`, `config`, `watch`, `skill`, `context`, `serve`, and
managed hooks resolve the nearest git root automatically, including linked
worktrees with a `.git` file.

```bash
cd /path/to/your/project
codemap setup
```

`codemap setup` configures Claude Code and Codex by default:

- creates `.codemap/config.json` with auto-detected language filters
- merges hooks into `.claude/settings.local.json` and `.codex/hooks.json`
- configures MCP in `.mcp.json` and `.codex/config.toml`
- hooks start and read daemon state at session start

Managed entries record the verified absolute path of the running `codemap`, so agents don't depend on your shell `PATH`. Rerun setup if that path changes.

```bash
codemap setup --agent claude   # one agent only
codemap setup --agent codex
codemap setup --global         # user-scope, applies to every project
```

### Verify

```bash
codemap doctor            # validate this project's integrations
codemap doctor --global   # validate user-scope configuration
```

Doctor checks project scope and falls back to user scope, reporting which one satisfied each check. For Codex, trust the hooks from `/hooks` in CLI or Settings → Hooks in Desktop, then start a new session.

## Dependency resolution

`--deps` and `--importers` resolve imports using each ecosystem's own rules rather than guessing from paths:

| Ecosystem | Resolved via |
|-----------|--------------|
| **Go** | module path from `go.mod`; stdlib and third-party imports are not fuzzy-matched into local files |
| **Rust** | `cargo metadata` — workspace membership, target kinds (lib/bin/test/bench/example/build), and `dev-dependencies` reachable from `#[cfg(test)]` blocks |
| **JS/TS** | `package.json` `exports`/`imports` maps, npm/pnpm/Bun workspaces, Deno import maps, and `tsconfig` `rootDir`/`outDir` remapping (including `extends`) |
| **Everything else** | ast-grep import extraction with suffix and directory matching |

### The coverage contract

Every dependency answer reports how much of it codemap actually stands behind:

```bash
codemap --json --deps . | jq .coverage
```

```json
{
  "status": "partial",
  "sources": [
    { "name": "ast-grep", "status": "authoritative" },
    { "name": "cargo-metadata", "status": "mixed",
      "detail": "2 of 5 Cargo manifests used fallback topology" }
  ],
  "issues": []
}
```

- `status` is `complete`, `partial`, or `unavailable`.
- Each source reports `authoritative`, `mixed`, `fallback`, `timeout`, `unavailable`, or `failed`.
- A timed-out or failed scan returns an **empty result with provenance**, not a silent empty graph and not a hard error — so an agent can tell "nothing imports this" apart from "I couldn't tell".

The JSON payload is versioned (`schema_version: codemap.analysis/v1`) so consumers can depend on its shape.

### Supported languages

20 language rules for dependency analysis: Go, Python, JavaScript, JSX, TypeScript, TSX, Rust, Ruby, C, C++, Java, Swift, Kotlin, C#, PHP, Bash, Lua, Scala, Elixir, Solidity.

> Powered by [ast-grep](https://ast-grep.github.io/). Installed automatically with the Homebrew formula.

## Commands

```bash
codemap .              # structure view (respects .codemap/config.json)
codemap --diff         # what changed vs main
codemap --deps .       # dependency flow
codemap --importers f  # who imports a file
codemap blast-radius   # review bundle: diff + deps + importers
codemap handoff .      # save layered handoff for cross-agent continuation
codemap context        # machine-readable project context JSON
codemap doctor         # validate agent integrations
codemap skill list     # available agent skills
codemap watch start    # background daemon for live graph state
codemap serve          # HTTP API for non-MCP integrations
codemap mcp            # MCP server on stdio
codemap --version
```

### Options

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

`-C`/`--project-root` selects the repository Codemap operates on.
`--setup-root` explicitly reuses `<repository>/.codemap` policy and runtime state
from another checkout. Both accept a repository or subdirectory; relative setup
paths resolve from the project root.

| Flag | Description |

| Flag | Description |
|------|-------------|
| `-C, --project-root <repo>` | Operate on code in `<repo>` |
| `--setup-root <repo>` | Explicitly reuse policy and runtime state from `<repo>/.codemap` |
| `--depth, -d <n>` | Limit tree depth (0 = unlimited) |
| `--only <exts>` | Only include files with these extensions |
| `--exclude <patterns>` | Exclude files matching patterns |
| `--diff` | Show files changed vs main branch |
| `--ref <branch>` | Branch to compare against (with `--diff`) |
| `--deps` | Dependency flow mode |
| `--importers <file>` | Check who imports a file |
| `--skyline` | City skyline visualization |
| `--animate` | Animate the skyline (with `--skyline`) |
| `--json` | Output JSON |

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
   Imported by 21 files - changes have wide impact!

   Dependents:
   • cmd/hooks.go
   • mcp/find_guidance.go
   ...
```

For a review bundle in one command — Markdown, text, or a single JSON object:

```bash
codemap blast-radius --ref main .
codemap blast-radius --json --ref main .
codemap blast-radius --text --ref main .
```

### Skyline

```bash
codemap --skyline --animate
```

![codemap skyline](assets/skyline-animated.gif)

### Remote repos

Analyze any public GitHub or GitLab repo without cloning it yourself:

```bash
codemap github.com/anthropics/anthropic-cookbook
codemap gitlab.com/user/repo
```

Shallow-clones to a temp directory and cleans up. If you already have the repo locally, codemap uses your copy.

## Agent integration

### Hooks

Automatic context at session start, before and after edits, and at compaction.
→ See [docs/HOOKS.md](docs/HOOKS.md)

The prompt-submit hook classifies intent, surfaces hub-file risk, shows your working set, matches relevant skills, and emits structured markers (`<!-- codemap:intent -->`) for tool consumption.

### MCP

`codemap mcp` serves 16 tools over stdio:

| Category | Tools |
|----------|-------|
| Structure | `get_structure`, `find_file`, `get_hubs`, `get_file_context` |
| Dependencies | `get_dependencies`, `get_importers`, `get_diff` |
| Session | `get_working_set`, `get_activity`, `get_handoff` |
| Daemon | `start_watch`, `stop_watch`, `status` |
| Skills | `list_skills`, `get_skill` |
| Discovery | `list_projects` |

`get_structure`, `get_diff`, `get_importers`, `get_dependencies`, and `get_handoff` declare an `OutputSchema` and return typed structured content alongside the text response, so callers get parseable results instead of prose.

### Codex

`codemap setup` configures Codex alongside Claude. For Codex only:

```bash
codemap setup --agent codex          # project hooks + MCP
codemap plugin install               # global plugin (MCP + skills), activated by default
codemap doctor --agent codex         # validate; reports CLI and Desktop runtimes separately
```

**After upgrading the codemap binary**, agent integrations do not update themselves:

```bash
codemap plugin install   # Codex only, once per Codex environment
cd /path/to/project && codemap setup && codemap doctor   # both agents, per project
```

`codemap plugin install` refreshes the plugin for CLI and Desktop sharing a Codex environment, and migrates the current project when run inside one — but it does not discover every configured project. Start a new task or session afterward, and re-check hook trust if Codex asks.

> `codemap doctor` probes executables recorded in project-local config (`.codex/config.toml`, `.mcp.json`), so running it inside an untrusted repo executes a repo-chosen path. Doctor bounds this by requiring absolute paths and a recognized argument shape, but treat it like any command that honors project-local config.

## Project config

Per-project defaults in `.codemap/config.json`, so you don't pass `--only`/`--exclude`/`--depth` every time. Hooks respect it too.

```bash
codemap config init   # auto-detect top extensions, write config
codemap config show   # display current config
```

```json
{
  "only": ["rs", "sh", "sql", "toml", "yml"],
  "exclude": ["docs/reference", "docs/research"],
  "depth": 4,
  "mode": "auto",
  "guidance": {
    "missing_extension_hints": true,
    "ignored_extensions": []
  },
  "budgets": {
    "session_start_bytes": 30000,
    "diff_bytes": 15000,
    "max_hubs": 8
  },
  "routing": {
    "retrieval": { "strategy": "keyword", "top_k": 3 },
    "subsystems": [
      {
        "id": "watching",
        "paths": ["watch/**"],
        "keywords": ["hook", "daemon", "events"],
        "docs": ["docs/HOOKS.md"],
        "agents": ["codemap-hook-triage"]
      }
    ]
  },
  "drift": {
    "enabled": true,
    "recent_commits": 10,
    "require_docs_for": ["watching"]
  }
}
```

All fields are optional; CLI flags always override config. When an MCP file search finds real matches hidden by `only`, codemap reports the paths and suggests which extensions to add — set `guidance.missing_extension_hints: false` to disable.

## Skills

Markdown files that give agents context-aware guidance, matched against intent, mentioned files, and project languages.

```bash
codemap skill list
codemap skill show hub-safety
codemap skill init            # custom skill template
```

| Builtin | Activates when |
|---------|---------------|
| `hub-safety` | Editing hub files (3+ importers) |
| `refactor` | Restructuring, renaming, moving code |
| `test-first` | Writing tests, TDD workflows |
| `explore` | Understanding how code works |
| `handoff` | Switching between AI agents |
| `config-setup` | `.codemap/config.json` is missing, boilerplate, or mismatched to the stack |

Drop a `.md` file with YAML frontmatter in `.codemap/skills/` to add your own — project-local skills override builtins, no Go code required:

```yaml
---
name: my-skill
description: When this skill should activate
keywords: ["relevant", "keywords"]
languages: ["go"]
---

# Instructions for the AI agent
```

## Context protocol

One command that gives any AI tool codemap's full intelligence:

```bash
codemap context                       # full JSON envelope
codemap context --for "refactor auth" # with pre-classified intent + matched skills
codemap context --compact             # minimal, for token-constrained agents
```

Returns a `ContextEnvelope` with project metadata, intent classification, working set, matched skills, and a handoff reference. Anything that can shell out gets code-aware context.

## HTTP API

```bash
codemap serve --port 9471
```

| Endpoint | Returns |
|----------|---------|
| `GET /api/context?intent=refactor+auth` | Full context envelope |
| `GET /api/context?compact=true` | Minimal envelope |
| `GET /api/skills` | All skills with metadata |
| `GET /api/skills?language=go&category=refactor` | Filtered skill matches |
| `GET /api/skills/<name>` | Full skill body |
| `GET /api/working-set` | Current session's active files |
| `GET /api/health` | Health check |

Binds to `127.0.0.1`; use `--host 0.0.0.0` to expose.

## Cross-agent handoff

When you switch agents (Claude → Codex → Cursor), codemap tracks who worked and what they touched:

```json
{
  "agent_history": [
    {"agent_id": "claude-code", "files_edited": ["cmd/hooks.go", "main.go"], "ended_at": "..."},
    {"agent_id": "codex", "files_edited": ["scanner/types.go"], "ended_at": "..."}
  ]
}
```

Agent detection is automatic via environment variables. History carries across sessions, capped at 20 entries, in `.codemap/handoff.latest.json`.

## Roadmap

Shipped: diff/skyline/deps modes, project config, Claude + Codex hooks and MCP, cross-agent handoff, remote repos, intent routing, skills framework, context protocol, HTTP API, build-system-aware resolution for Go/Rust/JS/TS, and the versioned coverage contract.

Next:

- [ ] Per-query coverage — report only the gaps that affect *this* answer, not the whole scan ([#111](https://github.com/JordanCoin/codemap/issues/111))
- [ ] Per-edge provenance — which resolver produced each edge, so "why does codemap think A imports B?" is answerable
- [ ] Community skill registry (`codemap skill add <name>`)
- [ ] Enhanced analysis (entry points, key types)

## Contributing

Fork → branch → commit → PR. See [CONTRIBUTING.md](CONTRIBUTING.md) before adding a new language.

## License

MIT
