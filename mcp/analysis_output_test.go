package codemapmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/analysis"
	"codemap/scanner"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGetDependenciesReturnsTextAndStructuredContent(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte("pub struct Value;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := NewServer(RuntimeOptions{}).Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "structured-output-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "get_dependencies" && tool.OutputSchema == nil {
			t.Fatal("get_dependencies does not advertise an output schema")
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_dependencies", Arguments: map[string]any{"path": root}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(resultText(t, result), "Dependency Flow") || result.StructuredContent == nil {
		t.Fatalf("structured dependency result = %#v", result)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output scanner.DepsProject
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.SchemaVersion != analysis.SchemaVersion || output.Coverage.Status != analysis.CoveragePartial {
		t.Fatalf("structured output = %#v", output)
	}
}
