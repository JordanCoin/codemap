---
name: test-first
description: TDD workflow using codemap's impact analysis. Write tests before implementation, verify with dependency awareness.
priority: 7
keywords: ["test", "tdd", "coverage", "testing", "spec"]
languages: ["go", "typescript", "python", "java", "rust"]
---

# Test-First Development with Code Intelligence

## The cycle

1. **Write a failing test** for the behavior you want
2. **Run the test** to confirm it fails for the right reason
3. **Implement** the minimum code to make it pass
4. **Run all tests** (not just the new one)
5. **Check hub impact** — if you touched a hub file, run importer tests too

## Using codemap for test planning

- `codemap --importers <file>` — which files depend on what you're testing?
- `codemap --deps .` — understand the dependency chain before writing mocks
- Working set shows what you've already touched — test those files

## What to test when editing hub files

Hub files affect many dependents. Test:
1. The hub file's own tests
2. Direct importers' tests
3. Integration tests that cross the hub boundary

## Test file conventions

| Language | Convention |
|----------|-----------|
| Go | `*_test.go` in same package |
| TypeScript | `*.test.ts` or `*.spec.ts` |
| Python | `test_*.py` or `*_test.py` |
| Rust | `#[cfg(test)]` module or `tests/` dir |
