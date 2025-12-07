# codemap 🗺️

> **codemap — a project brain for your AI.**
> Give LLMs instant architectural context without burning tokens.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-00ADD8.svg)

![codemap screenshot](assets/codemap.png)

## Table of Contents

- [Why codemap exists](#why-codemap-exists)
- [Features](#features)
- [How It Works](#%EF%B8%8F-how-it-works)
- [Performance](#-performance)
- [Installation](#installation)
- [Usage](#usage)
- [Diff Mode](#diff-mode)
- [Dependency Flow Mode](#dependency-flow-mode)
- [Skyline Mode](#skyline-mode)
- [Supported Languages](#supported-languages)
- [Claude Integrations](#claude-integrations)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

## Why codemap exists

Modern LLMs are powerful, but blind. They can write code — but only after you ask them to burn tokens searching or manually explain your entire project structure.

That means:
*   🔥 **Burning thousands of tokens**
*   🔁 **Repeating context**
*   📋 **Pasting directory trees**
*   ❓ **Answering “where is X defined?”**

**codemap fixes that.**

One command → a compact, structured “brain map” of your codebase that LLMs can instantly understand.

## Features

- 🧠 **Brain Map Output**: Visualizes your codebase structure in a single, pasteable block.
- 📉 **Token Efficient**: Clusters files and simplifies names to save vertical space.
- ⭐️ **Smart Highlighting**: Automatically flags the top 5 largest source code files.
- 📂 **Smart Flattening**: Merges empty intermediate directories (e.g., `src/main/java`).
- 🎨 **Rich Context**: Color-coded by language for easy scanning.
- 🚫 **Noise Reduction**: Automatically ignores `.git`, `node_modules`, and assets (images, binaries).

## ⚙️ How It Works

**codemap** is a fast Go binary with minimal dependencies:
1.  **Scanner**: Instantly traverses your directory, respecting `.gitignore` and ignoring junk.
2.  **Analyzer**: Uses [ast-grep](https://ast-grep.github.io/) to parse imports/functions across 18 languages.
3.  **Renderer**: Outputs a clean, dense "brain map" that is both human-readable and LLM-optimized.

## ⚡ Performance

**codemap** runs instantly even on large repos (hundreds or thousands of files). This makes it ideal for LLM workflows — no lag, no multi-tool dance.

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap JordanCoin/tap
brew install codemap
```

### Scoop (Windows)

```powershell
scoop bucket add codemap https://github.com/JordanCoin/scoop-codemap
scoop install codemap
```

### Winget (Windows)

```powershell
winget install JordanCoin.codemap
```

> **Note:** Winget availability depends on Microsoft approval. Use Scoop if not yet available.

### Download Binary

Pre-built binaries are available for all platforms on the [Releases page](https://github.com/JordanCoin/codemap/releases):

- **macOS**: `codemap-darwin-amd64.tar.gz` (Intel) or `codemap-darwin-arm64.tar.gz` (Apple Silicon)
- **Linux**: `codemap-linux-amd64.tar.gz` or `codemap-linux-arm64.tar.gz`
- **Windows**: `codemap-windows-amd64.zip`

```bash
# Example: download and install on Linux/macOS
curl -L https://github.com/JordanCoin/codemap/releases/latest/download/codemap-linux-amd64.tar.gz | tar xz
sudo mv codemap-linux-amd64/codemap /usr/local/bin/
```

> **Note:** The `--deps` feature requires [ast-grep](https://ast-grep.github.io/). Install via `brew install ast-grep`, `pip install ast-grep-cli`, or `cargo install ast-grep`.

### From source

```bash
git clone https://github.com/JordanCoin/codemap.git
cd codemap
go build -o codemap .
```

## Usage

Run `codemap` in any directory:

```bash
codemap
```

Or specify a path:

```bash
codemap /path/to/my/project
```

### AI Usage Example

**The Killer Use Case:**

1.  Run codemap and copy the output:
    ```bash
    codemap . | pbcopy
    ```

2.  Or simply tell Claude, Codex, or Cursor:
    > "Use codemap to understand my project structure."

## Diff Mode

See what you're working on with `--diff`:

```bash
codemap --diff
```

```
╭─────────────────────────── myproject ──────────────────────────╮
│ Changed: 4 files | +156 -23 lines vs main                      │
│ Top Extensions: .go (3), .tsx (1)                              │
╰────────────────────────────────────────────────────────────────╯
myproject
├── api/
│   └── (new) auth.go         ✎ handlers.go (+45 -12)
├── web/
│   └── ✎ Dashboard.tsx (+82 -8)
└── ✎ main.go (+29 -3)

⚠ handlers.go is used by 3 other files
⚠ api is used by 2 other files
```

**What it shows:**
- 📊 **Change summary**: Total files and lines changed vs main branch
- ✨ **New vs modified**: `(new)` for untracked files, `✎` for modified
- 📈 **Line counts**: `(+45 -12)` shows additions and deletions per file
- ⚠️ **Impact analysis**: Which changed files are imported by others

Compare against a different branch:
```bash
codemap --diff --ref develop
```

## Dependency Flow Mode

See how your code connects with `--deps`:

```bash
codemap --deps /path/to/project
```

```
╭──────────────────────────────────────────────────────────────╮
│                    MyApp - Dependency Flow                   │
├──────────────────────────────────────────────────────────────┤
│ Go: chi, zap, testify                                        │
│ Py: fastapi, pydantic, httpx                                 │
╰──────────────────────────────────────────────────────────────╯

Backend ════════════════════════════════════════════════════
  server ───▶ validate ───▶ rules, config
  api ───▶ handlers, middleware

Frontend ═══════════════════════════════════════════════════
  App ──┬──▶ Dashboard
        ├──▶ Settings
        └──▶ api

HUBS: config (12←), api (8←), utils (5←)
45 files · 312 functions · 89 deps
```

**What it shows:**
- 📦 **External dependencies** grouped by language (from go.mod, requirements.txt, package.json, etc.)
- 🔗 **Internal dependency chains** showing how files import each other
- 🎯 **Hub files** — the most-imported files in your codebase

## Skyline Mode

Want something more visual? Run `codemap --skyline` for a cityscape visualization of your codebase:

```bash
codemap --skyline --animate
```

![codemap skyline](assets/skyline-animated.gif)

Each building represents a language in your project — taller buildings mean more code. Add `--animate` for rising buildings, twinkling stars, and shooting stars.

## Supported Languages

codemap supports **18 languages** for dependency analysis (powered by [ast-grep](https://ast-grep.github.io/)):

| Language | Extensions | Import Detection |
|----------|------------|------------------|
| Go | .go | import statements |
| Python | .py | import, from...import |
| JavaScript | .js, .jsx, .mjs | import, require |
| TypeScript | .ts, .tsx | import, require |
| Rust | .rs | use, mod |
| Ruby | .rb | require, require_relative |
| C | .c, .h | #include |
| C++ | .cpp, .hpp, .cc | #include |
| Java | .java | import |
| Swift | .swift | import |
| Kotlin | .kt, .kts | import |
| C# | .cs | using |
| PHP | .php | use, require, include |
| Bash | .sh, .bash | source, . |
| Lua | .lua | require, dofile |
| Scala | .scala, .sc | import |
| Elixir | .ex, .exs | import, alias, use, require |
| Solidity | .sol | import |

## Claude Integrations

codemap provides four ways to integrate with Claude:

### Claude Code Hooks (Recommended)

The most seamless integration. Hooks run automatically at the right moments — no commands to remember.

```
Session starts  → Claude sees project structure + hub files
Before editing  → Claude is warned if the file is high-impact
After editing   → Claude sees what depends on the changed file
Before compact  → Hub state is saved so Claude remembers what matters
Session ends    → Summary of all changes and their impact
```

**Quick setup:** Tell Claude to `@HOOKS.md` and "add these hooks to my settings". Or see [HOOKS.md](HOOKS.md) for the full setup.

### CLAUDE.md

Add the included `CLAUDE.md` to your project root. Claude Code reads it and knows when to run codemap:

```bash
cp /path/to/codemap/CLAUDE.md your-project/
```

This teaches Claude to:
- Run `codemap .` before starting tasks
- Run `codemap --deps` when refactoring
- Run `codemap --diff` when reviewing changes

### Claude Code Skill

For automatic invocation, install the codemap skill:

```bash
# Copy to your project
cp -r /path/to/codemap/.claude/skills/codemap your-project/.claude/skills/

# Or install globally
cp -r /path/to/codemap/.claude/skills/codemap ~/.claude/skills/
```

Skills are model-invoked — Claude automatically decides when to use codemap based on your questions, no explicit commands needed.

### MCP Server

For the deepest integration, run codemap as an MCP server:

```bash
# Build the MCP server
make build-mcp

# Add to Claude Code
claude mcp add --transport stdio codemap -- /path/to/codemap-mcp
```

Or add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "codemap": {
      "command": "/path/to/codemap-mcp",
      "args": []
    }
  }
}
```

**Claude Desktop:**

> ⚠️ Claude Desktop cannot see your local files by default. This MCP server runs on your machine and gives Claude that ability.

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "codemap": {
      "command": "/path/to/codemap-mcp"
    }
  }
}
```

**MCP Tools:**
| Tool | Description |
|------|-------------|
| `status` | Verify MCP connection and local filesystem access |
| `list_projects` | Discover projects in a parent directory (with optional filter) |
| `get_structure` | Project tree view with file sizes and language detection |
| `get_dependencies` | Dependency flow with imports, functions, and hub files |
| `get_diff` | Changed files with line counts and impact analysis |
| `find_file` | Find files by name pattern |
| `get_importers` | Find all files that import a specific file |

## Roadmap

- [x] **Diff Mode** (`codemap --diff`) — show changed files with impact analysis
- [x] **Skyline Mode** (`codemap --skyline`) — ASCII cityscape visualization
- [x] **Dependency Flow** (`codemap --deps`) — function/import analysis with 14 language support
- [x] **Claude Code Skill** — automatic invocation based on user questions
- [x] **MCP Server** — deep integration with 7 tools for codebase analysis
- [ ] **Enhanced Analysis** — entry points, key types, exported function counts for richer LLM context

## Contributing

We love contributions!
1.  Fork the repo.
2.  Create a branch (`git checkout -b feature/my-feature`).
3.  Commit your changes.
4.  Push and open a Pull Request.

## License

MIT
