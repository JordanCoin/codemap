# Codemap MCP Server

Run codemap as an MCP (Model Context Protocol) server for deep Claude integration.

## Setup

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
      "command": "/path/to/codemap-mcp",
      "args": []
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
      "command": "/path/to/codemap-mcp"
    }
  }
}
```

## Available Tools

| Tool | Description |
|------|-------------|
| `status` | Verify MCP connection and local filesystem access |
| `list_projects` | Discover projects in a parent directory (with optional filter) |
| `get_structure` | Project tree view with file sizes and language detection |
| `get_dependencies` | Dependency flow with imports, functions, and hub files |
| `get_diff` | Changed files with line counts and impact analysis |
| `find_file` | Find files by name pattern |
| `get_importers` | Find all files that import a specific file |
| `get_handoff` | Build/read layered handoff artifact (`prefix` + `delta`) with lazy file detail loading |

## Usage

Once configured, Claude can use these tools automatically. Try asking:

- "What's the structure of this project?"
- "Show me the dependency flow"
- "What files import utils.go?"
- "What changed since the last commit?"
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
