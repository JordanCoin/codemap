---
name: hub-safety
description: Safety checks and procedures when modifying hub files (files imported by 3+ others). Activated when editing files with high importer counts.
priority: 10
keywords: ["hub", "refactor", "breaking-change", "api", "interface"]
languages: ["go", "typescript", "python", "java", "rust"]
---

# Hub Safety Protocol

Hub files are imported by 3+ other files. Changes here ripple across the codebase.

## Before editing a hub file

1. Run `codemap --importers <file>` to see all dependents
2. Understand the public API surface — which functions/types are used externally
3. Check if tests exist for dependents, not just the hub itself

## While editing

- Prefer additive changes over breaking changes
- If renaming or removing a public symbol, update all importers in the same commit
- If changing a function signature, check all call sites

## After editing

- Run the full test suite, not just the hub's tests
- Run `codemap --deps .` to verify the dependency graph is intact
- If the hub has 8+ importers, consider a staged rollout (change hub, test, update callers separately)

## Risk escalation

| Importers | Risk | Action |
|-----------|------|--------|
| 3-5 | Medium | Run package tests + direct importer tests |
| 6-10 | High | Run full test suite, review all importers |
| 10+ | Critical | Consider splitting the hub or using an interface |
