package cmd

import (
	"context"
	"fmt"
	"os"

	codemapmcp "codemap/mcp"
)

// RunMCP parses MCP runtime options and runs the shared stdio server.
func RunMCP(args []string) int {
	options, err := codemapmcp.ParseRuntimeOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid MCP options: %v\n", err)
		return 2
	}
	if err := codemapmcp.Run(context.Background(), options); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return 1
	}
	return 0
}
