# Dependency resolution

This doc answers: how does codemap decide what imports what, how much of each answer does it stand behind, and which languages does it cover?

`--deps` and `--importers` resolve imports using each ecosystem's own rules rather than guessing from paths:

| Ecosystem | Resolved via |
|-----------|--------------|
| **Go** | module path from `go.mod`; stdlib and third-party imports are not fuzzy-matched into local files |
| **Rust** | `cargo metadata`: workspace membership, target kinds (lib/bin/test/bench/example/build), and `dev-dependencies` reachable from `#[cfg(test)]` blocks |
| **JS/TS** | `package.json` `exports`/`imports` maps, npm/pnpm/Bun workspaces, Deno import maps, and `tsconfig` `rootDir`/`outDir` remapping (including `extends`) |
| **Dart/Flutter** | `pubspec.yaml` package names and declared dependencies; `package:` URIs resolve within the owning package's `lib/`, while undeclared or duplicate package names fail closed |
| **Everything else** | ast-grep import extraction with suffix and directory matching |

## The coverage contract

Every dependency answer reports how much of it codemap actually stands behind:

```bash
codemap --json --deps . | jq .coverage
```

```json
{
  "status": "partial",
  "sources": [
    { "name": "ast-grep", "status": "authoritative",
      "languages": ["bash", "c", "cpp", "csharp", "dart", "elixir", "go", "java", "javascript", "kotlin", "lua", "php", "python", "ruby", "rust", "scala", "solidity", "swift", "typescript"] },
    { "name": "cargo-metadata", "status": "mixed",
      "detail": "2 of 5 Cargo manifests used fallback topology",
      "languages": ["rust"] }
  ],
  "issues": [
    { "code": "unresolved-import", "path": "src/app.ts",
      "message": "import ./gone resolved to no file in the project" }
  ]
}
```

`issues` lists each project-relative import that resolved to no file, one entry per reference. The key is omitted when there are none.

- `status` is `complete`, `partial`, or `unavailable`.
- Each source reports `authoritative`, `mixed`, `fallback`, `timeout`, `unavailable`, or `failed`.
- `languages` names the languages a source can affect. Per-file answers (`--importers`, `get_importers`, `get_file_context`, the importer sections of `blast-radius`) keep only the sources tagged with the file's language, plus untagged sources, which apply everywhere. A Go file in a Go+Rust repo reads `complete` with no Rust caveat; `--deps` keeps the scan-level rollup.
- A timed-out or failed scan returns an **empty result with provenance**, not a silent empty graph and not a hard error, so an agent can tell "nothing imports this" apart from "I couldn't tell".

The JSON payload is versioned (`schema_version: codemap.analysis/v1`) so consumers can depend on its shape.

## Supported languages

21 ast-grep language rules for dependency analysis: Go, Python, JavaScript, JSX, TypeScript, TSX, Rust, Ruby, C, C++, Java, Swift, Dart, Kotlin, C#, PHP, Bash, Lua, Scala, Elixir, Solidity. Dart projects, including Flutter apps and packages, also get `pubspec.yaml` dependency discovery. CUE files also contribute module-scoped package edges through lexical import extraction; CUE is not an ast-grep rule.

> Powered by [ast-grep](https://ast-grep.github.io/). Installed automatically with the Homebrew formula.

To add a language, see [CONTRIBUTING.md](../CONTRIBUTING.md#adding-a-new-language).

Next: [every command, flag and mode](COMMANDS.md)
