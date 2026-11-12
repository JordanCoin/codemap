# Config, skills and integrations

This doc answers: how do I tune what codemap shows an agent, add skills, and feed its context to tools that are not MCP clients?

## Project config

Per-project defaults in `.codemap/config.json`, so you don't pass `--only`/`--exclude`/`--depth` every time. Hooks respect it too.

```bash
codemap config init   # auto-detect top extensions, write config
codemap config show   # display current config
```

```json
{
  "only": ["rs", "sh", "sql", "toml", "yml"],
  "exclude": ["docs/reference", "docs/research"],
  "depth": 4,
  "max_files": 100000,
  "mode": "auto",
  "guidance": {
    "missing_extension_hints": true,
    "ignored_extensions": []
  },
  "budgets": {
    "session_start_bytes": 30000,
    "diff_bytes": 15000,
    "max_hubs": 8
  },
  "routing": {
    "retrieval": { "strategy": "keyword", "top_k": 3 },
    "subsystems": [
      {
        "id": "watching",
        "paths": ["watch/**"],
        "keywords": ["hook", "daemon", "events"],
        "docs": ["docs/HOOKS.md"],
        "agents": ["codemap-hook-triage"]
      }
    ]
  },
  "drift": {
    "enabled": true,
    "recent_commits": 10,
    "require_docs_for": ["watching"]
  }
}
```

All fields are optional. `--only` and `--depth` override the config's values for one run; `--exclude` adds to the config's `exclude` list, so a config exclude keeps applying when you pass more patterns on the command line. Exclude patterns follow `.gitignore` anchors: a bare name (`tool`) matches at any depth, `/tool` only the root entry, `tool/` only directories, and `/tool/` only the root directory, so a built binary at the root can be excluded without losing `cmd/tool/`. When an MCP file search finds real matches hidden by `only`, codemap reports the paths and suggests which extensions to add; set `guidance.missing_extension_hints: false` to disable.

## Skills

Markdown files that give agents context-aware guidance, matched against intent, mentioned files, and project languages.

```bash
codemap skill list
codemap skill show hub-safety
codemap skill init            # custom skill template
```

| Builtin | Activates when |
|---------|---------------|
| `hub-safety` | Editing hub files (3+ importers) |
| `refactor` | Restructuring, renaming, moving code |
| `test-first` | Writing tests, TDD workflows |
| `explore` | Understanding how code works |
| `handoff` | Switching between AI agents |
| `config-setup` | `.codemap/config.json` is missing, boilerplate, or mismatched to the stack |

Drop a `.md` file with YAML frontmatter in `.codemap/skills/` to add your own. Project-local skills override builtins, no Go code required:

```yaml
---
name: my-skill
description: When this skill should activate
keywords: ["relevant", "keywords"]
languages: ["go"]
---

# Instructions for the AI agent
```

## Context protocol

One command that gives any AI tool codemap's full intelligence:

```bash
codemap context                       # full JSON envelope
codemap context --for "refactor auth" # with pre-classified intent + matched skills
codemap context --compact             # minimal, for token-constrained agents
```

Returns a `ContextEnvelope` with project metadata, dependency-graph evidence, intent classification, working set, matched skills, and a handoff reference. If fresh graph evidence is unavailable, hub counts are `null` and risk is `unknown` instead of being inferred from stale state. Anything that can shell out gets code-aware context.

## HTTP API

```bash
codemap serve --port 9471
```

| Endpoint | Returns |
|----------|---------|
| `GET /api/context?intent=refactor+auth` | Full context envelope |
| `GET /api/context?compact=true` | Minimal envelope |
| `GET /api/skills` | All skills with metadata |
| `GET /api/skills?language=go&category=refactor` | Filtered skill matches |
| `GET /api/skills/<name>` | Full skill body |
| `GET /api/working-set` | Current session's active files |
| `GET /api/health` | Health check |

Binds to `127.0.0.1`; use `--host 0.0.0.0` to expose.

## Cross-agent handoff

When you switch agents (Claude → Codex → Cursor), codemap tracks who worked and what they touched:

```json
{
  "agent_history": [
    {"agent_id": "claude-code", "files_edited": ["cmd/hooks.go", "main.go"], "ended_at": "..."},
    {"agent_id": "codex", "files_edited": ["scanner/types.go"], "ended_at": "..."}
  ]
}
```

Agent detection is automatic via environment variables. History carries across sessions, capped at 20 entries, in `.codemap/handoff.latest.json`.

Next: [MCP tools and Codex](MCP.md)
