package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEntrypointsMatch(t *testing.T) {
	binDir := t.TempDir()
	cli := buildTestBinary(t, filepath.Join(binDir, "codemap"), ".")
	standalone := buildTestBinary(t, filepath.Join(binDir, "codemap-mcp"), "./cmd/codemap-mcp")

	tests := []struct {
		name string
		args []string
	}{
		{name: "no options"},
		{name: "managed options", args: []string{"--configured-version", "test-version", "--integration", "codex-setup"}},
		{name: "partial options", args: []string{"--configured-version", "test-version"}},
		{name: "invalid option", args: []string{"--invalid"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliResult := runTestBinary(t, cli, append([]string{"mcp"}, tt.args...)...)
			standaloneResult := runTestBinary(t, standalone, tt.args...)
			if cliResult != standaloneResult {
				t.Fatalf("entrypoints differ:\ncodemap mcp:  %+v\ncodemap-mcp:  %+v", cliResult, standaloneResult)
			}
		})
	}

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
	managedArgs := []string{"--configured-version", "test-version", "--integration", "codex-setup"}
	cliSnapshot := probeMCP(t, cli, append([]string{"mcp"}, managedArgs...), root)
	standaloneSnapshot := probeMCP(t, standalone, managedArgs, root)
	if cliSnapshot != standaloneSnapshot {
		t.Fatalf("MCP protocol behavior differs:\ncodemap mcp:\n%s\ncodemap-mcp:\n%s", cliSnapshot, standaloneSnapshot)
	}
}

type testCommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func buildTestBinary(t *testing.T, output, pkg string) string {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, pkg)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
	return output
}

func runTestBinary(t *testing.T, binary string, args ...string) testCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := exec.Command(binary, args...)
	command.Stdin = bytes.NewReader(nil)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s: %v", binary, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return testCommandResult{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}
}

func probeMCP(t *testing.T, binary string, args []string, root string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "entrypoint-parity-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, args...)}, nil)
	if err != nil {
		t.Fatalf("connect %s: %v", binary, err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools from %s: %v", binary, err)
	}
	toolNames := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames = append(toolNames, tool.Name+":"+tool.Description)
	}
	status, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("status from %s: %v", binary, err)
	}
	find, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "find_file",
		Arguments: map[string]any{"path": root, "pattern": "schema"},
	})
	if err != nil {
		t.Fatalf("find_file from %s: %v", binary, err)
	}
	return strings.Join(toolNames, "\n") + "\n--- status ---\n" + mcpResultText(t, status) + "\n--- find_file ---\n" + mcpResultText(t, find)
}

func mcpResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var text []string
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text = append(text, item.Text)
		}
	}
	return strings.Join(text, "\n")
}
