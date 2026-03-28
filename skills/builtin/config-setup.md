---
name: config-setup
description: Set up or tune .codemap/config.json so Codemap focuses on code-relevant parts of the repo. Use when config is missing, boilerplate, noisy, or mismatched to the stack.
priority: 8
keywords: ["setup", "config", "tune", "initialize", "noise", "boilerplate", "exclude", "onboard"]
languages: ["go", "typescript", "javascript", "python", "rust", "ruby", "swift", "java", "kotlin", "csharp"]
---

# Codemap Config Setup

## Goal

Write or improve `.codemap/config.json` so future Codemap calls stay focused on the code that matters for this repo.

## Use this when

1. `.codemap/config.json` is missing
2. The existing config looks like a bare bootstrap instead of a real project policy
3. Codemap output is dominated by assets, fixtures, generated files, vendor trees, PDFs, screenshots, models, or training data
4. The project stack is obvious, but Codemap is not prioritizing the right parts of the repo

## Workflow

1. Inspect the repo quickly before writing config
   - Run `codemap .`
   - If needed, run `codemap --deps .`
   - Note the stack markers (`Cargo.toml`, `Package.swift`, `*.xcodeproj`, `go.mod`, `package.json`, `pyproject.toml`, etc.)
   - Identify large non-code directories and noisy extensions

2. Decide whether config is missing, boilerplate, or tuned
   - Missing: no `.codemap/config.json`
   - Boilerplate: only generic `only` values, no real shaping, no excludes despite obvious noise
   - Tuned: contains intentional project-specific includes/excludes, depth, or routing hints

3. Write a conservative code-first config
   - Keep primary source-language `only` values when they help
   - Add `exclude` entries for obvious non-code noise
   - Set a moderate `depth` when the repo is broad
   - Avoid overfitting or excluding real source directories

4. Prefer stack-aware defaults
   - Rust: focus `src`, `tests`, `benches`, `examples`; de-prioritize corpora, sample PDFs, training data, large generated artifacts
   - iOS/Swift: focus app/framework source, tests, package/project manifests; de-prioritize `.xcassets`, screenshots, snapshots, vendor/build outputs
   - TS/JS: focus `src`, `apps`, `packages`, `tests`; de-prioritize `dist`, `coverage`, Storybook assets, large fixture payloads
   - Python: focus package roots, tests, tool config; de-prioritize notebooks, data dumps, models, fixtures when they overwhelm code
   - Go: focus packages, cmd, internal, tests; de-prioritize generated assets, sample data, vendor-like noise

5. Preserve user intent
   - If config already looks curated, do not replace it wholesale
   - Make minimal edits and explain why

6. Verify immediately
   - Rerun `codemap .`
   - If the repo still looks noisy, refine `exclude` and possibly `depth`
   - Only rerun `codemap --deps .` after tree output looks reasonable

## Heuristics

- Exclude giant non-code directories before narrowing source extensions further
- Prefer directory exclusions over extension-only exclusions when the directory is clearly not core to the codebase
- Do not exclude tests just because they are large
- Do not exclude docs that are part of routing or subsystem understanding
- Keep the first config conservative; optimize further only if the output remains noisy

## Example outcomes

### Rust library with large fixture data

```json
{
  "only": ["rs", "toml", "md"],
  "exclude": ["training-data", "test-pdfs", "*.pdf"],
  "depth": 4
}
```

### iOS app with asset bloat

```json
{
  "only": ["swift", "m", "mm", "h", "plist", "md"],
  "exclude": [".xcassets", "Snapshots", "Screenshots", "DerivedData"],
  "depth": 4
}
```

## Output expectations

After tuning config, briefly report:

1. Why the old config was missing or too noisy
2. What exclusions or depth choices you added
3. Whether future Codemap runs should now be materially cleaner
