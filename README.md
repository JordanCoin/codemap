# codemap 🗺️

[![Run in Smithery](https://smithery.ai/badge/skills/jordancoin)](https://smithery.ai/skills?ns=jordancoin&utm_source=github&utm_medium=badge)


> **codemap — a project brain for your AI.**
> Give LLMs instant architectural context without burning tokens.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-00ADD8.svg)

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

## Recommended Setup (Hooks + Daemon + Config)

No repo clone is required for normal users.

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

## Roadmap

- [x] Diff mode, Skyline mode, Dependency flow
- [x] Tree depth limiting (`--depth`)
- [x] File filtering (`--only`, `--exclude`)
- [x] Project config (`.codemap/config.json`)
- [x] Claude Code hooks & MCP server
- [x] Cross-agent handoff artifact (`.codemap/handoff.latest.json`)
- [x] Remote repo support (GitHub, GitLab)
- [ ] Enhanced analysis (entry points, key types)

## Contributing

1. Fork → 2. Branch → 3. Commit → 4. PR

## License

MIT
