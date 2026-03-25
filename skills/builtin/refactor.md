---
name: refactor
description: Safe refactoring with dependency awareness. Use when restructuring, renaming, moving, extracting, or consolidating code.
priority: 8
keywords: ["refactor", "rename", "move", "extract", "split", "consolidate", "restructure"]
languages: ["go", "typescript", "python", "java", "rust", "ruby"]
---

# Safe Refactoring with Code Intelligence

## Before refactoring

1. Run `codemap --deps .` to understand the dependency graph
2. Identify hub files in the refactoring scope — they need extra care
3. Run `codemap --diff` to see what's already changed on this branch

## Refactoring checklist

- [ ] All tests pass before starting
- [ ] Hub files identified and dependents listed
- [ ] Public API changes planned (additive preferred)
- [ ] Import paths updated if moving files
- [ ] No circular dependencies introduced

## Safe rename pattern

1. Add the new name alongside the old (alias/wrapper)
2. Update all callers to use the new name
3. Remove the old name
4. Run full test suite

## Safe move pattern

1. Create the new file with the same API
2. Re-export from the old location (temporary)
3. Update all importers to use the new path
4. Remove the old file and re-export

## After refactoring

- Run `codemap --deps .` again to verify the graph is clean
- Run `codemap --importers <moved-file>` to check nothing is broken
- Full test suite must pass
