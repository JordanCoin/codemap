---
name: explore
description: Systematic codebase exploration using codemap's analysis tools. Use when understanding how code works, tracing dependencies, or onboarding to a new area.
priority: 6
keywords: ["explore", "understand", "how", "where", "find", "trace", "architecture"]
languages: ["go", "typescript", "python", "java", "rust", "ruby", "swift"]
---

# Systematic Codebase Exploration

## Start with the big picture

1. `codemap .` — project tree with file counts and top extensions
2. `codemap --deps .` — dependency flow showing how packages connect
3. Hub files — these are the most important files to understand first

## Trace a feature

1. Find the entry point (CLI command, API route, event handler)
2. `codemap --importers <entry-file>` — who calls this?
3. `codemap --deps .` — follow the import chain
4. Read hub files in the chain first — they define the shared types/interfaces

## Understand a subsystem

1. Check `.codemap/config.json` for routing subsystems — they define logical boundaries
2. Each subsystem has paths, keywords, and associated docs
3. Read the subsystem's docs first, then trace the code

## Find what you're looking for

| Question | Command |
|----------|---------|
| Where is X defined? | `codemap --deps .` → find the file |
| Who uses X? | `codemap --importers <file>` |
| What's the architecture? | `codemap --deps .` → look at hub files |
| What changed recently? | `codemap --diff` |
| What was I working on? | Check working set in prompt-submit output |
