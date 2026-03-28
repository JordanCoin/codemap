---
name: codemap
description: Analyze codebase structure, dependencies, changes, cross-agent handoffs, and get code-aware intelligence. Use when user asks about project structure, where code is located, how files connect, what changed, how to resume work, before starting any coding task, when you need risk analysis and skill guidance, or when Codemap should tune project config before analysis.
---

# Codemap

Codemap gives you instant architectural context about any codebase. It classifies your intent, detects risk, matches relevant skills, and tracks your working set — all automatically via hooks.

Codemap should also keep its own per-project config healthy. On first use in a repo, or when output is obviously noisy, tune `.codemap/config.json` before doing deeper analysis so future calls stay code-first instead of asset-first.

## Commands

```bash
codemap .                       # Project structure and top files
codemap --deps                  # Dependency flow (imports/functions/hubs)
codemap --diff                  # Changes vs main branch
codemap --diff --ref <branch>   # Changes vs specific branch
codemap --importers <file>      # Who imports this file? Is it a hub?
codemap handoff .               # Build + save handoff artifact
codemap handoff --latest .      # Read latest saved handoff
codemap handoff --json .        # Machine-readable handoff payload
codemap skill list              # Show available skills with descriptions
codemap skill show <name>       # Get full skill instructions
codemap skill init              # Create custom skill template
codemap config show             # Show current project config
codemap context                 # Universal JSON context envelope
codemap context --for "prompt"  # With pre-classified intent + matched skills
codemap context --compact       # Minimal for token-constrained agents
codemap serve --port 9471       # HTTP API for non-MCP integrations
```

## First-Use Setup

Before deeper Codemap analysis in a repo:

1. Check `.codemap/config.json`.
2. If it is missing, clearly boilerplate, or obviously too noisy for the stack, run `codemap skill show config-setup` and follow it.
3. After writing or improving config, rerun `codemap .` and `codemap --deps`.

Treat config as repo memory. Once tuned, future Codemap calls should benefit automatically.

Signals that config needs setup or tuning:
- `.codemap/config.json` is missing
- config only contains generic auto-detected `only` values with no real project shaping
- large non-code directories dominate the tree output
- stack-specific noise is overwhelming source structure (`.xcassets`, screenshots, PDFs, training-data, fixtures, generated files, models, vendor directories)
- the repo stack is obvious, but the config does not reflect it

## When to Use

### ALWAYS run `codemap .` when:
- Starting any new task or feature
- User asks "where is X?" or "what files handle Y?"
- User asks about project structure or organization
- You need to understand the codebase before making changes

### ALWAYS run `codemap --deps` when:
- User asks "how does X work?" or "what uses Y?"
- Refactoring or moving code
- Need to trace imports or dependencies
- Finding hub files (most-imported)

### ALWAYS run `codemap --diff` when:
- User asks "what changed?" or "what did I modify?"
- Reviewing changes before commit
- Summarizing work done on a branch

### ALWAYS run `codemap --importers <file>` when:
- About to edit a file — check if it's a hub
- Need to know the blast radius of a change
- Deciding whether to refactor or leave alone

### Run `codemap skill show <name>` when:
- The prompt-submit hook shows matched skills in `<!-- codemap:skills [...] -->`
- You need guidance for a specific task (hub editing, refactoring, testing)
- Risk level is medium or high

### Run `codemap skill show config-setup` when:
- The repo has no `.codemap/config.json`
- The config looks like a bare bootstrap and not a real project policy
- Codemap output is cluttered by large non-code directories
- You want Codemap to make better future decisions for this specific repo

### Run `codemap context` when:
- Piping codemap intelligence to another tool
- Need a structured JSON summary of the project state
- Building automation that consumes code-aware context

### Run `codemap handoff` when:
- Switching between agents (Claude, Codex, Cursor)
- Resuming work after a break
- User asks "what should the next agent know?"

## Hook Output

The prompt-submit hook fires on every message and provides:

```
<!-- codemap:intent {"category":"refactor","risk":"high",...} -->
<!-- codemap:skills [{"name":"hub-safety","score":5},...] -->
Skills matched: hub-safety, refactor — run `codemap skill show <name>` for guidance
```

- **Intent categories**: refactor, bugfix, feature, explore, test, docs
- **Risk levels**: low (no hubs), medium (1 hub), high (2+ hubs or 8+ importers)
- **Skills are pull-based**: only names are shown, run `codemap skill show` for full body

## Builtin Skills

| Skill | When to Pull |
|-------|-------------|
| `config-setup` | Missing, boilerplate, or noisy `.codemap/config.json` |
| `hub-safety` | Editing files imported by 3+ others |
| `refactor` | Restructuring, renaming, moving code |
| `test-first` | Writing tests, TDD workflows |
| `explore` | Understanding how code works |
| `handoff` | Switching between AI agents |

## Output Interpretation

### Tree View (`codemap .`)
- Stars (⭐) indicate top 5 largest source files
- Directories flattened when containing single subdirectory

### Dependency Flow (`codemap --deps`)
- External dependencies grouped by language
- Internal import chains showing how files connect
- HUBS section shows most-imported files (3+ importers)

### Diff Mode (`codemap --diff`)
- `(new)` = untracked, `✎` = modified, `(+N -M)` = lines changed
- Warning icons show hub files (high impact)

### Importers (`codemap --importers <file>`)
- Shows all files that import this file
- Flags hub status (3+ importers = high impact)

### Context Envelope (`codemap context`)
- JSON with project metadata, intent, working set, matched skills, handoff ref
- `--compact` strips skills and limits working set for token savings

## MCP Tools

If codemap MCP server is configured, these tools are available:

| Tool | Use For |
|------|---------|
| `get_structure` | Project tree |
| `get_dependencies` | Dependency flow + hubs |
| `get_diff` | Changed files with impact |
| `find_file` | Search by filename |
| `get_importers` | Who imports a file |
| `get_hubs` | List all hub files |
| `get_file_context` | Full context for one file |
| `get_handoff` | Build/read handoff artifact |
| `get_working_set` | Files edited this session |
| `list_skills` | Available skills (metadata) |
| `get_skill` | Full skill instructions |
| `get_activity` | Recent coding activity |
| `start_watch` / `stop_watch` | Control daemon |
| `status` | Verify MCP connection |
| `list_projects` | Discover projects |

## HTTP API

When `codemap serve` is running:

| Endpoint | Returns |
|----------|---------|
| `GET /api/context?intent=...` | Context envelope |
| `GET /api/skills` | All skills metadata |
| `GET /api/skills/<name>` | Full skill body |
| `GET /api/working-set` | Current working set |
| `GET /api/health` | Server health |
