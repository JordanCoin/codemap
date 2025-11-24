# codemap 🗺️

> A CLI tool that generates a compact, visually structured "brain map" of your codebase for LLM context.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.21+-00ADD8.svg)
![Python](https://img.shields.io/badge/python-3.8+-3776AB.svg)

**codemap** is designed to give Large Language Models (LLMs) instant architectural context of your project without burning thousands of tokens. It produces a dense, tree-like structure that highlights important files and hides clutter.

## Features

- 🧠 **Brain Map Output**: Visualizes your codebase structure in a single, pasteable block.
- 📉 **Token Efficient**: Clusters files and strips extensions to save vertical space.
- ⭐️ **Smart Highlighting**: Automatically flags the top 5 largest source code files.
- 📂 **Smart Flattening**: Merges empty intermediate directories (e.g., `src/main/java`).
- 🎨 **Rich Context**: Color-coded by language for easy scanning.
- 🚫 **Noise Reduction**: Automatically ignores `.git`, `node_modules`, and assets (images, binaries).

## Installation

### Homebrew

```bash
brew tap jordancoin/codemap
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

## Example Output

```text
codemap
├── 📂 .claude/ (100.0B)
│   └── ⭐️ settings.local.json
├── 📂 renderer/ (12.3KB)
│   └── ⭐️ render.py
├── 📂 scanner/ (3 files, 3.7KB)
│   └── ⭐️ go.mod ⭐️ main.go
│       go.sum              
└── ⭐️ Makefile
```

## License

MIT
