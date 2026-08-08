# Contributing to codemap

Thanks for your interest in contributing! Here's how to get involved.

## Quick Contributions

- **Bug reports**: Open an issue with reproduction steps
- **Feature ideas**: Open an issue to discuss first
- **Documentation**: PRs welcome for README, examples, etc.

## Adding a New Language

Want to add support for a language like Clojure, Zig, Haskell, etc.? Here's what's needed:

### 1. Add grammar to `release.yml`

In `.github/workflows/release.yml`, add a line to the `GRAMMARS` env var:

```yaml
env:
  GRAMMARS: |
    go:tree-sitter/tree-sitter-go:master:src
    # ... existing grammars ...
    clojure:sogaiu/tree-sitter-clojure:main:src   # <- add yours
```

Format: `name:github_org/repo:branch:src_dir`

Most grammars use `src` as the source directory, but check the repo structure.

### 2. Create a query file

Create `scanner/queries/<lang>.scm` to define what to capture:

```scm
; Functions - capture function/method definitions
(function_definition
  name: (identifier) @function)

; Imports - capture import/require statements
(import_clause
  (identifier) @import)
```

**Finding the right node types:**
- Use [tree-sitter playground](https://tree-sitter.github.io/tree-sitter/7-playground.html)
- Check the grammar repo's `grammar.js` for node names
- Look at existing queries in `scanner/queries/` for examples

### 3. Add extension mapping

In `scanner/grammar.go`, add to the `extToLang` map:

```go
var extToLang = map[string]string{
    // ... existing mappings ...
    ".clj":  "clojure",
    ".cljs": "clojure",
    ".cljc": "clojure",
}
```

### 4. Open a PR

That's it from your side! Open a PR with these 3 changes.

**What happens next:**
- Maintainer reviews the PR
- Maintainer updates the Homebrew formula (`codemap.rb`) with the new grammar
- On next release, all platforms get the new language automatically

## Development Setup

```bash
git clone https://github.com/JordanCoin/codemap.git
cd codemap

# Build the binary
go build -o codemap .

# Build grammars for --deps mode (requires clang/gcc)
make deps

# Test it
./codemap .
./codemap --deps .
```

## Code Style

- Keep it simple - this is a CLI tool, not a framework
- Run `go fmt` before committing
- Test your changes with `./codemap` on a real project

## Questions?

Open an issue or reach out. We're happy to help!
