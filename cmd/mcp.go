package cmd

import (
	"context"
	"fmt"
	"os"

	codemapmcp "codemap/mcp"
)

// RunMCP runs the shared stdio server and returns its process exit status.
func RunMCP() int {
	return runMCP(codemapmcp.Run)
}

func runMCP(runner func(context.Context) error) int {
	if err := runner(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return 1
	}
	return 0
}
