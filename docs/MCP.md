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
| `get_handoff` | Build/read cross-agent handoff artifact (optionally save to `.codemap/handoff.latest.json`) |

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
- `save=true` to persist generated artifact to `.codemap/handoff.latest.json`

By default, `get_handoff` does **not** write to disk unless `save=true` is set.
