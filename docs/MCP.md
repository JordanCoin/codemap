# Codemap MCP Server

Run codemap as an MCP (Model Context Protocol) server for deep Claude integration.

## Setup

Preferred when `codemap` is already installed:

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
- handoff payload includes deterministic hashes (`prefix_hash`, `delta_hash`, `combined_hash`)
- handoff payload includes cache metrics (`reuse_ratio`, `unchanged_bytes`, etc.)
