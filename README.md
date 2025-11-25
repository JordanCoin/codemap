# codemap 🗺️

> **codemap — a project brain for your AI.**
> Give LLMs instant architectural context without burning tokens.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-00ADD8.svg)
![Python](https://img.shields.io/badge/python-3.8+-3776AB.svg)

![codemap screenshot](assets/codemap.png)

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

**codemap** is built for speed and structure:
1.  **Scanner (Go)**: Instantly traverses your directory, respecting `.gitignore` and ignoring junk files.
2.  **Renderer (Python)**: Consumes the raw data and renders a highly structured, color-coded ASCII tree.
3.  **Output**: A clean, dense "brain map" that is both human-readable and machine-optimized.

## ⚡ Performance

**codemap** runs instantly even on large repos (hundreds or thousands of files). This makes it ideal for LLM workflows — no lag, no multi-tool dance.

## Installation

### Homebrew

```bash
brew tap JordanCoin/tap
brew install codemap
```

### Manual

1.  Clone the repo:
    ```bash
    git clone https://github.com/JordanCoin/codemap.git
    cd codemap
    ```
2.  Install dependencies:
    ```bash
    make install
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

## Skyline Mode

Want something more visual? Run `codemap --skyline` for a cityscape visualization of your codebase:

```bash
codemap --skyline --animate
```

![codemap skyline](assets/skyline-animated.gif)

Each building represents a language in your project — taller buildings mean more code. Add `--animate` for rising buildings, twinkling stars, and shooting stars.

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

### Supported Languages

codemap supports **16 languages** for dependency analysis:

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
| Dart | .dart | import |
| R | .r, .R | library, require, source |
| Bash | .sh, .bash | source, . |

## Roadmap

- [x] **Skyline Mode** (`codemap --skyline`) — ASCII cityscape visualization
- [x] **Dependency Flow** (`codemap --deps`) — function/import analysis with 16 language support

## Contributing

We love contributions!
1.  Fork the repo.
2.  Create a branch (`git checkout -b feature/my-feature`).
3.  Commit your changes.
4.  Push and open a Pull Request.

## License

MIT
