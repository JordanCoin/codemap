package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEntrypointsMatchConfigFallback(t *testing.T) {
	binDir := t.TempDir()
	cli := buildFallbackTestBinary(t, filepath.Join(binDir, "codemap"), ".")
	standalone := buildFallbackTestBinary(t, filepath.Join(binDir, "codemap-mcp"), "./cmd/codemap-mcp")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema.proto"), []byte("syntax = \"proto3\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cliResult := probeFallbackTestBinary(t, cli, []string{"mcp"}, root)
	standaloneResult := probeFallbackTestBinary(t, standalone, nil, root)
	if cliResult != standaloneResult {
		t.Fatalf("find_file differs:\ncodemap mcp: %s\ncodemap-mcp: %s", cliResult, standaloneResult)
	}
	if !strings.Contains(cliResult, "Matches excluded by `only` config:") {
		t.Fatalf("fallback guidance missing: %s", cliResult)
	}

	cliResult = probeFallbackTestBinary(t, cli, []string{"mcp", "ignored"}, root)
	standaloneResult = probeFallbackTestBinary(t, standalone, []string{"ignored"}, root)
	if cliResult != standaloneResult {
		t.Fatalf("legacy argument handling differs:\ncodemap mcp: %s\ncodemap-mcp: %s", cliResult, standaloneResult)
	}
}

func buildFallbackTestBinary(t *testing.T, output, pkg string) string {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, pkg)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, data)
	}
	return output
}

func probeFallbackTestBinary(t *testing.T, binary string, args []string, root string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "fallback-parity-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, args...)}, nil)
	if err != nil {
		t.Fatalf("connect %s: %v", binary, err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "find_file",
		Arguments: map[string]any{"path": root, "pattern": "schema"},
	})
	if err != nil {
		t.Fatalf("find_file from %s: %v", binary, err)
	}
	var text []string
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text = append(text, item.Text)
		}
	}
	return strings.Join(text, "\n")
}
