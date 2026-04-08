# codemap 🗺️

> **codemap — a project brain for your AI.**
> Give LLMs instant architectural context without burning tokens.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-00ADD8.svg)
![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/JordanCoin/6ffe3276ddb8a7a7f08d50d649e567bd/raw/codemap-coverage.json)
[![Run in Smithery](https://smithery.ai/badge/skills/jordancoin)](https://smithery.ai/skills?ns=jordancoin&utm_source=github&utm_medium=badge)

![codemap screenshot](assets/codemap.png)

## Install

```bash
# macOS/Linux
brew tap JordanCoin/tap && brew install codemap

# Windows
scoop bucket add codemap https://github.com/JordanCoin/scoop-codemap
scoop install codemap
```

> Other options: [Releases](https://github.com/JordanCoin/codemap/releases) | `go install` | Build from source

## Tarball / CI Install

If you install `codemap` from a release tarball, also install `ast-grep` separately for `--deps`.
The tarball includes `codemap` and the bundled rules, but not the `ast-grep` executable.

Example for Alpine-based CI:

```bash
apk add --no-cache curl jq bash python3 py3-pip

ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

CODEMAP_VERSION=$(curl -fsSL https://api.github.com/repos/JordanCoin/codemap/releases/latest | jq -r '.tag_name' | tr -d 'v')
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap

python3 -m pip install --no-cache-dir ast-grep-cli
```

If you want a self-contained archive for CI/CD, use the `codemap-full` release artifact instead.
It includes `codemap`, `ast-grep`, and `sg` in one archive so `--deps` works after extraction.

```bash
apk add --no-cache curl jq bash

ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

CODEMAP_VERSION=$(curl -fsSL https://api.github.com/repos/JordanCoin/codemap/releases/latest | jq -r '.tag_name' | tr -d 'v')
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap-full_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap ast-grep sg
```

## Recommended Setup (Hooks + Daemon + Config)

No repo clone is required for normal users.
Run setup from your git repo root (not a subdirectory), or hooks may not resolve project context.

```bash
# install codemap first (package manager)
brew tap JordanCoin/tap && brew install codemap

# then run setup inside your project
cd /path/to/your/project
codemap setup
```

`codemap setup` is the default onboarding path and configures the pieces that make codemap most useful with Claude:
- creates `.codemap/config.json` (if missing) with auto-detected language filters
- installs codemap hooks into `.claude/settings.local.json` (project-local by default)
- hooks automatically start/read daemon state on session start

Use global Claude settings instead of project-local settings:

```bash
codemap setup --global
```

Windows equivalent:

```bash
scoop bucket add codemap https://github.com/JordanCoin/scoop-codemap
scoop install codemap
cd C:\path\to\your\project
codemap setup
```

Optional helper scripts (mainly for contributors running from this repo):
- macOS/Linux: `./scripts/onboard.sh /path/to/your/project`
- Windows (PowerShell): `./scripts/onboard.ps1 -ProjectRoot C:\path\to\your\project`

## Verify Setup

1. Restart Claude Code or open a new session.
2. At session start, you should see codemap project context.
3. Edit a file and confirm pre/post edit hook context appears.

## Daily Commands

```bash
codemap .          # Fast tree/context view (respects .codemap/config.json)
codemap --diff     # What changed vs main
codemap handoff .  # Save layered handoff for cross-agent continuation
codemap --deps .   # Dependency flow (requires ast-grep)
codemap skill list # Show available skills
codemap context    # Universal JSON context for any AI tool
codemap mcp        # Run Codemap MCP server on stdio
codemap plugin install # Install the Codemap Codex plugin
codemap serve      # HTTP API for non-MCP integrations
```

## Other Commands

```bash
codemap --only swift .
codemap --exclude .xcassets,Fonts,.png .
codemap --depth 2 .
codemap github.com/user/repo
```

## Options

| Flag | Description |
|------|-------------|
| `--depth, -d <n>` | Limit tree depth (0 = unlimited) |
| `--only <exts>` | Only show files with these extensions |
| `--exclude <patterns>` | Exclude files matching patterns |
| `--diff` | Show files changed vs main branch |
| `--ref <branch>` | Branch to compare against (with --diff) |
| `--deps` | Dependency flow mode |
| `--importers <file>` | Check who imports a file |
| `--skyline` | City skyline visualization |
| `--animate` | Animate the skyline (use with --skyline) |
| `--json` | Output JSON |

> **Note:** Flags must come before the path/URL: `codemap --json github.com/user/repo`

**Smart pattern matching** — no quotes needed:
- `.png` → any `.png` file
- `Fonts` → any `/Fonts/` directory
- `*Test*` → glob pattern

## Modes

### Diff Mode

See what you're working on:

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

### Dependency Flow

See how your code connects:

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

### Skyline Mode

```bash
codemap --skyline --animate
```

![codemap skyline](assets/skyline-animated.gif)

### Remote Repos

Analyze any public GitHub or GitLab repo without cloning it yourself:

```bash
codemap github.com/anthropics/anthropic-cookbook
codemap https://github.com/user/repo
codemap gitlab.com/user/repo
```

Uses a shallow clone to a temp directory (fast, no history, auto-cleanup). If you already have the repo cloned locally, codemap will use your local copy instead.

## Supported Languages

18 languages for dependency analysis: Go, Python, JavaScript, TypeScript, Rust, Ruby, C, C++, Java, Swift, Kotlin, C#, PHP, Bash, Lua, Scala, Elixir, Solidity

> Powered by [ast-grep](https://ast-grep.github.io/). Install via `brew install ast-grep` for `--deps` mode.

## Blast Radius Bundle

If you want a compact review bundle for another LLM, combine the three high-signal views:

```bash
codemap --json --diff --ref main .
codemap --json --deps --diff --ref main .
codemap --json --importers path/to/file .
```

For a reusable built-in command that emits either Markdown, text, or a single JSON object:

```bash
codemap blast-radius --ref main .
codemap blast-radius --json --ref main .
codemap blast-radius --text --ref main .
```

## Claude Integration

**Hooks (Recommended)** — Automatic context at session start, before/after edits, and more.
→ See [docs/HOOKS.md](docs/HOOKS.md)

**MCP Server** — Deep integration with project analysis + handoff tools.
→ See [docs/MCP.md](docs/MCP.md)

## Multi-Agent Handoff

codemap now supports a shared handoff artifact so you can switch between agents (Claude, Codex, MCP clients) without re-briefing.

```bash
codemap handoff .                 # Build + save layered handoff artifacts
codemap handoff --latest .        # Read latest saved artifact
codemap handoff --json .          # Machine-readable handoff payload
codemap handoff --since 2h .      # Limit timeline lookback window
codemap handoff --prefix .        # Stable prefix layer only
codemap handoff --delta .         # Recent delta layer only
codemap handoff --detail a.go .   # Lazy-load full detail for one changed file
codemap handoff --no-save .       # Build/read without writing artifacts
```

What it captures (layered for cache reuse):
- `prefix` (stable): hub summaries + repo file-count context
- `delta` (dynamic): changed file stubs (`path`, `hash`, `status`, `size`), risk files, recent events, next steps
- deterministic hashes: `prefix_hash`, `delta_hash`, `combined_hash`
- cache metrics: reuse ratio + unchanged bytes vs previous handoff

Artifacts written:
- `.codemap/handoff.latest.json` (full artifact)
- `.codemap/handoff.prefix.json` (stable prefix snapshot)
- `.codemap/handoff.delta.json` (dynamic delta snapshot)
- `.codemap/handoff.metrics.log` (append-only metrics stream, one JSON line per save)

Save defaults:
- CLI saves by default; use `--no-save` to make generation read-only.
- MCP does not save by default; set `save=true` to persist artifacts.

Compatibility note:
- legacy top-level fields (`changed_files`, `risk_files`, etc.) are still included for compatibility and will be removed in a future schema version after migration.

Why this matters:
- default transport is compact stubs (low context cost)
- full per-file context is lazy-loaded only when needed (`--detail` / `file=...`)
- output is deterministic and budgeted to reduce context churn across agent turns

Hook integration:
- `session-stop` writes `.codemap/handoff.latest.json`
- `session-start` shows a compact recent handoff summary (24h freshness window)

**CLAUDE.md** — Add to your project root to teach Claude when to run codemap:
```bash
cp /path/to/codemap/CLAUDE.md your-project/
```

## Project Config

Set per-project defaults in `.codemap/config.json` so you don't need to pass `--only`/`--exclude`/`--depth` every time. Hooks also respect this config.

```bash
codemap config init          # Auto-detect top extensions, write config
codemap config show          # Display current config
```

Example `.codemap/config.json`:
```json
{
  "only": ["rs", "sh", "sql", "toml", "yml"],
  "exclude": ["docs/reference", "docs/research"],
  "depth": 4,
  "mode": "auto",
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

All fields are optional. CLI flags always override config values.
Hook-specific policy fields are optional and bounded by safe defaults.

## Skills

codemap ships with a **skills framework** — markdown files that provide context-aware guidance to AI agents. Skills are automatically matched against your intent, the files you mention, and the languages in your project.

```bash
codemap skill list              # Show all available skills
codemap skill show hub-safety   # Print full skill content
codemap skill init              # Create a custom skill template
```

### Builtin Skills

| Skill | Activates When |
|-------|---------------|
| `hub-safety` | Editing hub files (3+ importers) |
| `refactor` | Restructuring, renaming, moving code |
| `test-first` | Writing tests, TDD workflows |
| `explore` | Understanding how code works |
| `handoff` | Switching between AI agents |

### Custom Skills

Drop a `.md` file in `.codemap/skills/` with YAML frontmatter:

```yaml
---
name: my-skill
description: When this skill should activate
keywords: ["relevant", "keywords"]
languages: ["go"]
---

# Instructions for the AI agent
```

Project-local skills override builtins. No Go code needed — just markdown.

### MCP Tools

Skills are also available via MCP: `list_skills` (metadata) and `get_skill` (full body).

## Intelligent Routing

The prompt-submit hook performs **intent classification** on every prompt — detecting whether you're refactoring, fixing a bug, exploring, testing, or building a feature. It then:

- Surfaces **risk analysis** based on hub file involvement
- Emits **exact next-step codemap commands** at transition points like “before editing” and “before refactoring”
- Shows your **working set** (files edited this session)
- Emits **structured JSON markers** (`<!-- codemap:intent -->`) for tool consumption
- Matches **relevant skills** and tells you which to pull (`codemap skill show <name>`)
- Warns about **documentation drift** when docs are stale

## Context Protocol

A single command that gives **any AI tool** codemap's full intelligence:

```bash
codemap context                       # Full JSON envelope
codemap context --for "refactor auth" # With pre-classified intent + matched skills
codemap context --compact             # Minimal for token-constrained agents
```

The output is a `ContextEnvelope` containing project metadata, intent classification, working set, matched skills, and handoff reference. Cursor, Windsurf, Codex, custom agents — anything that can shell out gets code-aware intelligence.

## HTTP API

For tools that prefer HTTP over CLI:

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
| `GET /api/health` | Server health check |

Binds to `127.0.0.1` by default. Use `--host 0.0.0.0` to expose to network.

## Agent-Aware Handoff

When you switch between AI agents (Claude → Codex → Cursor), codemap tracks who worked and what they did:

```json
{
  "agent_history": [
    {"agent_id": "claude-code", "files_edited": ["cmd/hooks.go", "main.go"], "ended_at": "..."},
    {"agent_id": "codex", "files_edited": ["scanner/types.go"], "ended_at": "..."}
  ]
}
```

Agent detection is automatic via environment variables. History is carried across sessions (capped at 20 entries) in the handoff artifact.

## Roadmap

- [x] Diff mode, Skyline mode, Dependency flow
- [x] Tree depth limiting (`--depth`)
- [x] File filtering (`--only`, `--exclude`)
- [x] Project config (`.codemap/config.json`)
- [x] Claude Code hooks & MCP server
- [x] Cross-agent handoff artifact (`.codemap/handoff.latest.json`)
- [x] Remote repo support (GitHub, GitLab)
- [x] Intelligent routing (intent classification, risk analysis, working set)
- [x] Skills framework (builtin + custom skills, CLI, MCP tools)
- [x] Context protocol (`codemap context` — universal JSON envelope for any AI tool)
- [x] HTTP API (`codemap serve` — REST endpoints for non-MCP integrations)
- [x] Agent-aware handoff (multi-agent history tracking)
- [ ] Community skill registry (GitHub-hosted, `codemap skill add <name>`)
- [ ] Enhanced analysis (entry points, key types)

## Contributing

1. Fork → 2. Branch → 3. Commit → 4. PR

## License

MIT
