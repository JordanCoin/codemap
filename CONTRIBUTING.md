# Contributing to codemap

Thanks for your interest in contributing! Here's how to get involved.

## Quick Contributions

- **Bug reports**: Open an issue with reproduction steps
- **Feature ideas**: Open an issue to discuss first
- **Documentation**: PRs welcome for README, examples, etc.

## Adding a New Language

Want to add support for a language like Clojure, Zig, Haskell, etc.? `--deps` runs [ast-grep](https://ast-grep.github.io/) with YAML rules that are embedded in the binary (`//go:embed sg-rules/*.yml` in `scanner/astgrep.go`), so there is no grammar to build or ship. The language must be one ast-grep can parse. Here's what's needed:

### 1. Add a rule file

Create `scanner/sg-rules/<lang>.yml` with an imports rule and a functions rule. Rule IDs start with a prefix followed by `-`, for example `lua-imports` and `lua-functions`:

```yaml
id: lua-imports
language: lua
rule:
  any:
    - kind: function_call
      pattern: require($$$)
---
id: lua-functions
language: lua
rule:
  kind: function_declaration
```

Use the existing files in `scanner/sg-rules/` as examples, and the [ast-grep playground](https://ast-grep.github.io/playground.html) to find node kinds.

### 2. Map the rule prefix and the extensions

- `scanner/astgrep.go`: add the rule-ID prefix to `ruleIDToLang`.
- `scanner/types.go`: add each file extension to `extToLang`, the language to `resolverLanguageFamilies` (imports only resolve between languages in the same family), and its display name to `LangDisplay`.

### 3. Add a conformance case

`scanner/conformance_test.go` fails for any language in `extToLang` without an entry in `conformanceCases`: a two-file fixture, the edge that must (or must not) exist, and the expected coverage status. Run `go test ./scanner/` with ast-grep installed.

If suffix and directory matching cannot resolve the language's imports, add a resolver in `scanner/filegraph.go` (see `resolveLuaModule`) with tests.

### 4. Open a PR

Release packaging copies `scanner/sg-rules/*`, so nothing else changes for a release.

## Development Setup

```bash
git clone https://github.com/JordanCoin/codemap.git
cd codemap

# Build the binary
go build -o codemap .

# --deps mode needs ast-grep on PATH (as ast-grep or sg)
brew install ast-grep        # or: pip install ast-grep-cli

# Test it
./codemap .
./codemap --deps .
go test ./...
```

## Code Style

- Keep it simple - this is a CLI tool, not a framework
- Run `go fmt` before committing
- Test your changes with `./codemap` on a real project


## Every line codemap prints

Output is reviewed against [docs/OUTPUT-STANDARD.md](docs/OUTPUT-STANDARD.md): sourced, precise, bounded, useful. A PR that adds or changes a printed line quotes it and says which tests it passes.

## Questions?

Open an issue or reach out. We're happy to help!
