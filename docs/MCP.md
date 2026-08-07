# Codemap MCP Server

Run codemap as an MCP server for Claude Code, Codex, or another MCP client.

## Setup

Preferred when `codemap` is already installed:

```bash
codemap setup                 # Configure Claude Code and Codex
codemap setup --agent claude  # Configure only Claude Code
codemap setup --agent codex   # Configure only Codex
```

Setup writes a managed, versioned absolute executable path. After upgrading or
moving Codemap, Codex plugin users should run `codemap plugin install` first,
then rerun setup in each configured project. Claude users skip plugin
installation but still rerun setup. Use `codemap doctor` for strict validation;
it reports Codex CLI and Desktop runtimes independently. Agent integrations do
not automatically refresh the generated local plugin or per-project setup.
For a manual, PATH-dependent Claude definition:

```bash
claude mcp add --transport stdio codemap -- codemap mcp
```

### Build

```bash
make build-mcp
```

### Claude Code

```bash
claude mcp add --transport stdio codemap -- /path/to/codemap-mcp
```

Or add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "codemap": {
      "command": "codemap",
      "args": ["mcp"]
    }
  }
}
```

### Claude Desktop

> Claude Desktop cannot see your local files by default. This MCP server runs on your machine and gives Claude that ability.

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "codemap": {
      "command": "codemap",
      "args": ["mcp"]
    }
  }
}
```

If you prefer a standalone MCP binary, keep using `/path/to/codemap-mcp`.

### Linked Worktrees and Separate Roots

A standard linked Git worktree automatically reuses `.codemap/config.json` and
project skills from its primary worktree. Create it with Git, an IDE, or any
compatible worktree manager, then pass its absolute path to normal MCP tool
calls:

```bash
git worktree add <path> -b <branch> <base>
```

No MCP launch flags, project-specific MCP stanza, or `--setup-root` is needed.
Handoff, watcher, and hook/session state stays under the linked worktree's own
`.codemap` directory, so parallel worktrees do not overwrite one another.

Independent clones cannot prove that they should share setup. For those, put
an explicit setup override before `mcp`:

```json
{
  "mcpServers": {
    "codemap": {
      "command": "codemap",
      "args": [
        "--project-root", "/tmp/independent-clone",
        "--setup-root", "/path/to/original",
        "mcp"
      ]
    }
  }
}
```

Both values may be repository roots or subdirectories. An explicit
`--setup-root` preserves the existing shared policy-and-runtime-state behavior,
and managed MCP definitions created while it is active retain it automatically.

## Available Tools (17)

### Project Analysis

| Tool | Description |
|------|-------------|
| `get_structure` | Project tree view with file sizes and language detection |
| `get_dependencies` | Dependency flow with imports, functions, and hub files |
| `get_diff` | Changed files with line counts and impact analysis |
| `find_file` | Find files by name pattern |
| `get_importers` | Find all files that import a specific file |
| `get_hubs` | List all hub files (3+ importers) with dependent counts |
| `get_file_context` | Complete dependency context for one file (imports, importers, hub status, connected files) |

### Watch Daemon

| Tool | Description |
|------|-------------|
| `start_watch` | Begin file watching for a project |
| `stop_watch` | Stop file watcher |
| `get_activity` | Recent coding activity (hot files, edits, timeline) |
| `get_working_set` | Current session's working set: files being edited, ranked by activity, with hub status |

### Skills

| Tool | Description |
|------|-------------|
| `list_skills` | List available skills with names, descriptions, keywords (metadata only) |
| `get_skill` | Load full instructions for a specific skill by name |

### Handoff & Meta

| Tool | Description |
|------|-------------|
| `get_handoff` | Build/read layered handoff artifact (`prefix` + `delta`) with lazy file detail loading |
| `status` | Verify MCP connection and local filesystem access |
| `list_projects` | Discover projects in a parent directory (with optional filter) |

## Usage

Once configured, Claude can use these tools automatically. Try asking:

- "What's the structure of this project?"
- "Show me the dependency flow"
- "What files import utils.go?"
- "Is scanner/types.go a hub file?"
- "What changed since the last commit?"
- "What have I been editing this session?"
- "What skills are available for refactoring?"
- "Build a handoff summary I can continue in another agent"

## Handoff Tool Notes

`get_handoff` supports:
- `latest=true` to read previously saved handoff artifact
- `since="2h"` and `ref="main"` to tune generation
- `json=true` for machine-readable output
- `save=true` to persist generated artifacts (`handoff.latest.json`, `handoff.prefix.json`, `handoff.delta.json`)
- `prefix=true` to return only the stable prefix snapshot
- `delta=true` to return only the recent delta snapshot
- `file="path/to/file"` to lazy-load full detail for one changed file stub

By default, `get_handoff` does **not** write to disk unless `save=true` is set.

Surface behavior note:
- MCP: read-only by default (`save=false`)
- CLI `codemap handoff`: save by default (`--no-save` to disable)

Output and budget notes:
- text responses are byte-budgeted and line-truncated to protect context
- project discovery reads bounded batches, returns at most 50 sorted projects, examines at most 200 parent entries, and reports truncation separately from cancellation; when the examination cap is exceeded it returns only the truncation notice rather than a filesystem-order-dependent partial list
- MCP dependency discovery skips individual manifests larger than 1 MiB; CLI dependency and blast-radius callers retain their legacy unbounded manifest behavior
- prefix file counts and size-based handoff budgets honor the active `.codemap/config.json` filters
- handoff payload includes deterministic hashes (`prefix_hash`, `delta_hash`, `combined_hash`)
- handoff payload includes cache metrics (`reuse_ratio`, `unchanged_bytes`, etc.)

Cancellation notes:
- project scans, dependency graphs, ast-grep, Git diff/impact work, project discovery, and generated handoff/detail work stop with the MCP request context
- with `save=true`, cancellation observed after generation but before persistence prevents `WriteLatest`; once persistence begins, the existing multi-file write completes or reports its existing error without rollback
