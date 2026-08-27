package codemapmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/limits"
	"codemap/topology"
)

func TestHandleGetTopologyAndModuleContext(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	var builtRoot string
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(_ context.Context, gotRoot string) (*topology.Graph, topology.CacheIdentity, error) {
		builtRoot = gotRoot
		node := topology.Node{
			ID:       topology.ID("jvm:settings.gradle.kts:app"),
			Kind:     topology.NodeKind("gradle-project"),
			Name:     "app",
			Manifest: "settings.gradle.kts",
			Root:     "app",
			Provider: "jvm",
		}
		return &topology.Graph{
			Nodes:        map[topology.ID]topology.Node{node.ID: node},
			Dependencies: map[topology.ID][]topology.Edge{},
			Dependents:   map[topology.ID][]topology.Edge{},
			Members:      map[topology.ID][]string{node.ID: {"app/src/Main.kt"}},
			Owners:       map[string][]topology.ID{"app/src/Main.kt": {node.ID}},
			Coverage:     topology.Coverage{Status: topology.CoverageComplete},
		}, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildTopologyGraphOnly = original })

	graphResult, structured, err := handleGetTopology(context.Background(), nil, TopologyInput{Path: nested, Ecosystem: "jvm"})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if builtRoot != wantRoot {
		t.Fatalf("built topology at %q, want %q", builtRoot, wantRoot)
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "jvm:settings.gradle.kts:app") {
		t.Fatalf("missing structured topology: %s", encoded)
	}
	if got := resultText(t, graphResult); !strings.Contains(got, "PROJECT TOPOLOGY") ||
		!strings.Contains(got, "jvm:settings.gradle.kts:app") {
		t.Fatalf("unexpected get_topology output:\n%s", got)
	}

	moduleResult, _, err := handleGetModuleContext(context.Background(), nil, ModuleContextInput{
		Path: root,
		File: "app/src/Main.kt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(t, moduleResult); !strings.Contains(got, "Module: app") {
		t.Fatalf("unexpected get_module_context output:\n%s", got)
	}
	_, moduleStructured, err := handleGetModuleContext(context.Background(), nil, ModuleContextInput{Path: root, Module: "app"})
	if err != nil {
		t.Fatal(err)
	}
	moduleJSON, err := json.Marshal(moduleStructured)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(moduleJSON), `"file"`) || !strings.Contains(string(moduleJSON), `"hub":false`) {
		t.Fatalf("module structured fields are ambiguous: %s", moduleJSON)
	}
}

func TestTopologyHandlerRejectsUnsafeProjectBeforeBuilding(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	called := false
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		called = true
		return &topology.Graph{}, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildTopologyGraphOnly = original })
	result, _, err := handleGetTopology(context.Background(), nil, TopologyInput{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || called {
		t.Fatalf("unsafe project: IsError=%v builderCalled=%v", result.IsError, called)
	}
}

func TestTopologyStructuredContentIsBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := &topology.Graph{
		Nodes:        make(map[topology.ID]topology.Node),
		Dependencies: make(map[topology.ID][]topology.Edge),
		Dependents:   make(map[topology.ID][]topology.Edge),
		Members:      make(map[topology.ID][]string),
		Owners:       make(map[string][]topology.ID),
		Coverage: topology.Coverage{Status: topology.CoveragePartial, Issues: []topology.Issue{
			{Provider: "go", Code: "metadata", Message: "module metadata was incomplete"},
		}},
	}
	for i := 0; i < 256; i++ {
		id := topology.ID(fmt.Sprintf("go:module-%03d", i))
		graph.Nodes[id] = topology.Node{ID: id, Name: strings.Repeat("module", 512), Provider: "go"}
		for member := 0; member < 32; member++ {
			file := fmt.Sprintf("module-%03d/%s-%03d.go", i, strings.Repeat("path", 64), member)
			graph.Members[id] = append(graph.Members[id], file)
			graph.Owners[file] = []topology.ID{id}
		}
	}
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return graph, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildTopologyGraphOnly = original })

	_, structured, err := handleGetTopology(context.Background(), nil, TopologyInput{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > limits.MaxContextOutputBytes {
		t.Fatalf("structured topology is %d bytes, limit %d", len(encoded), limits.MaxContextOutputBytes)
	}
	if !strings.Contains(string(encoded), `"truncated":true`) ||
		!strings.Contains(string(encoded), `"total_nodes":256`) ||
		!strings.Contains(string(encoded), `"total_coverage_issues":1`) {
		t.Fatalf("bounded topology omits truncation metadata: %s", encoded)
	}
}

func TestStatusListsTopologyTools(t *testing.T) {
	result, _, err := handleStatus(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, result)
	for _, name := range []string{"get_topology", "get_module_context"} {
		if !strings.Contains(text, name) {
			t.Fatalf("status omits %s:\n%s", name, text)
		}
	}
}

func TestHandleGetModuleContextRejectsConflictingSelectors(t *testing.T) {
	result, _, err := handleGetModuleContext(context.Background(), nil, ModuleContextInput{
		Path:   "/repo",
		Module: "app",
		File:   "app/src/Main.kt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "either module or file") {
		t.Fatalf("unexpected selector result: error=%v text=%q", result.IsError, resultText(t, result))
	}
}
