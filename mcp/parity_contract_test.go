package codemapmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStructureDepthPrecedence(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"a/one/b/c.go": "package b\n",
		"a/two/d/e.go": "package d\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{"only":["go"],"depth":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	configured, _, err := handleGetStructure(context.Background(), nil, StructureInput{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	explicitOne, _, err := handleGetStructure(context.Background(), nil, StructureInput{Path: root, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	explicitDeep, _, err := handleGetStructure(context.Background(), nil, StructureInput{Path: root, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resultText(t, configured), resultText(t, explicitOne); got != want {
		t.Fatalf("configured depth did not match explicit depth 1\nconfigured:\n%s\nexplicit:\n%s", got, want)
	}
	if resultText(t, explicitDeep) == resultText(t, explicitOne) {
		t.Fatal("explicit request depth did not override configured depth")
	}
}

func TestMCPDepthSchemasDescribeEffectiveContract(t *testing.T) {
	session := connectParitySession(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"get_dependencies", "get_hubs", "list_skills"} {
		tool := findParityTool(t, tools.Tools, name)
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		schema := strings.ToLower(string(encoded))
		if !strings.Contains(schema, `"depth"`) || !strings.Contains(schema, "deprecated") || !strings.Contains(schema, "ignored") {
			t.Fatalf("%s depth schema is not compatibility-marked: %s", name, schema)
		}
	}
	structure := findParityTool(t, tools.Tools, "get_structure")
	encoded, err := json.Marshal(structure.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if schema := strings.ToLower(string(encoded)); !strings.Contains(schema, `"depth"`) || strings.Contains(schema, "deprecated") {
		t.Fatalf("get_structure depth schema = %s", schema)
	}
}

func connectParitySession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(RuntimeOptions{}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "parity-contract-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func findParityTool(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
