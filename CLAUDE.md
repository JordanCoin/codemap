# 🛑 STOP — Run codemap before ANY task

## Repo Root Requirement (Critical)

Run codemap from the git repository root. Hooks and context files resolve from the current working directory, so running from a subdirectory can break hook context.

```bash
cd "$(git rev-parse --show-toplevel)"
```

`codemap` expects these at repo root:
- `.git/`
- `.codemap/`
- `.claude/settings.local.json` (project-local hooks)

```bash
codemap .                     # Project structure
codemap --deps                # How files connect
codemap --diff                # What changed vs main
codemap --diff --ref <branch> # Changes vs specific branch
```

## Required Usage

**BEFORE starting any task**, run `codemap .` first.

Treat codemap as part of the execution loop, not as optional reference material:

- Before the first real code exploration in a task: `codemap .`
- Before editing any file: `codemap --importers <file>`
- Before refactors, moves, or dependency-heavy changes: `codemap --deps`
- Before summarizing, reviewing, or committing: `codemap --diff`
- If a codemap hook prints `Next codemap:` or `Run now:`, do that before continuing

Use `rg` for exact string lookup after codemap has established structure or blast radius.

**ALWAYS run `codemap --deps` when:**
- User asks how something works
- Refactoring or moving code
- Tracing imports or dependencies

**ALWAYS run `codemap --diff` when:**
- Reviewing or summarizing changes
- Before committing code
- User asks what changed
- Use `--ref <branch>` when comparing against something other than main
