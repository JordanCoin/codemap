#!/usr/bin/env bash
set -euo pipefail

if command -v codemap >/dev/null 2>&1 && codemap --help 2>/dev/null | grep -q "codemap mcp"; then
  exec codemap mcp "$@"
fi

if command -v codemap-mcp >/dev/null 2>&1; then
  exec codemap-mcp "$@"
fi

echo "Codemap MCP is unavailable. Install a codemap build that supports 'codemap mcp' or add 'codemap-mcp' to PATH." >&2
exit 1
