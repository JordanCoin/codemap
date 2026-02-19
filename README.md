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

## Quick Start

```bash
codemap .                    # Project tree
codemap --only swift .       # Just Swift files
codemap --exclude .xcassets,Fonts,.png .  # Hide assets
codemap --depth 2 .          # Limit depth
codemap --diff               # What changed vs main
codemap --deps .             # Dependency flow
codemap handoff .            # Save cross-agent handoff summary
codemap github.com/user/repo # Remote GitHub repo
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
codemap handoff .                 # Build + save .codemap/handoff.latest.json
codemap handoff --latest .        # Read latest saved artifact
codemap handoff --json .          # Machine-readable handoff payload
codemap handoff --since 2h .      # Limit timeline lookback window
```

What it captures:
- changed files (branch + working tree + staged + untracked text files)
- high-impact changed files (`risk_files`) when dependency context is available
- recent edit timeline from daemon state (when available)
- next steps and open questions

Hook integration:
- `session-stop` writes `.codemap/handoff.latest.json`
- `session-start` shows a compact recent handoff summary (24h freshness window)

**CLAUDE.md** — Add to your project root to teach Claude when to run codemap:
```bash
cp /path/to/codemap/CLAUDE.md your-project/
```

## Roadmap

- [x] Diff mode, Skyline mode, Dependency flow
- [x] Tree depth limiting (`--depth`)
- [x] File filtering (`--only`, `--exclude`)
- [x] Claude Code hooks & MCP server
- [x] Cross-agent handoff artifact (`.codemap/handoff.latest.json`)
- [x] Remote repo support (GitHub, GitLab)
- [ ] Enhanced analysis (entry points, key types)

## Contributing

1. Fork → 2. Branch → 3. Commit → 4. PR

## License

MIT
