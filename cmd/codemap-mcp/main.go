package main

import (
	"context"
	"fmt"
	"os"

	codemapmcp "codemap/mcp"
)

func main() {
	if err := codemapmcp.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
