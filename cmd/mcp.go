package cmd

import (
	"context"
	"fmt"
	"os"

	codemapmcp "codemap/mcp"
)

// RunMCP parses MCP runtime options and runs the shared stdio server.
func RunMCP(args []string) int {
	return RunMCPScoped(args, false)
}

// RunMCPScoped is RunMCP for a server launched with --project-root.
func RunMCPScoped(args []string, projectScoped bool) int {
	options, err := codemapmcp.ParseRuntimeOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid MCP options: %v\n", err)
		return 2
	}
	options.ProjectScoped = projectScoped
	return runMCP(func(ctx context.Context) error { return codemapmcp.Run(ctx, options) })
}

func runMCP(runner func(context.Context) error) int {
	if err := runner(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return 1
	}
	return 0
}
