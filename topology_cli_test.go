package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/topology"
)

func TestCanonicalTopologyRootSelectsRepositoryFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalTopologyRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("topology root = %q, want %q", got, want)
	}
}

func TestRunTopologyModeRendersGraphAndModule(t *testing.T) {
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
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

	var graphOut bytes.Buffer
	if err := runTopologyMode(context.Background(), "/repo", "", "", "", false, &graphOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(graphOut.String(), "PROJECT TOPOLOGY") ||
		!strings.Contains(graphOut.String(), "jvm:settings.gradle.kts:app") {
		t.Fatalf("unexpected topology output:\n%s", graphOut.String())
	}

	var moduleOut bytes.Buffer
	if err := runTopologyMode(context.Background(), "/repo", "app", "", "", false, &moduleOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moduleOut.String(), "Module: app") {
		t.Fatalf("unexpected module output:\n%s", moduleOut.String())
	}

	var fileOut bytes.Buffer
	if err := runTopologyMode(context.Background(), "/repo", "", "app/src/Main.kt", "jvm", false, &fileOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileOut.String(), "Module: app") {
		t.Fatalf("unexpected file-owner output:\n%s", fileOut.String())
	}
}

func TestTopologyCLIFileSelectorAndEcosystemContract(t *testing.T) {
	root := t.TempDir()
	output, err := runCodemap("--topology", "--ecosystem", "jvm", root)
	if err != nil {
		t.Fatalf("ecosystem invocation failed: %v\n%s", err, output)
	}
	output, err = runCodemap("--module", "app", "--module-file", "app/src/Main.kt", root)
	if err == nil || !strings.Contains(output, "either --module or --module-file") {
		t.Fatalf("conflicting selectors: err=%v output=%q", err, output)
	}
}

func TestTopologyCLIRejectsEcosystemOutsideTopology(t *testing.T) {
	output, err := runCodemap("--ecosystem", "jvm", t.TempDir())
	if err == nil || !strings.Contains(output, "--ecosystem requires") {
		t.Fatalf("unexpected result: err=%v output=%q", err, output)
	}
}

func TestRunTopologyModeJSONIsValid(t *testing.T) {
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return &topology.Graph{
			Nodes:        map[topology.ID]topology.Node{},
			Dependencies: map[topology.ID][]topology.Edge{},
			Dependents:   map[topology.ID][]topology.Edge{},
			Members:      map[topology.ID][]string{},
			Owners:       map[string][]topology.ID{},
			Coverage:     topology.Coverage{Status: topology.CoverageUnavailable},
		}, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildTopologyGraphOnly = original })

	var out bytes.Buffer
	if err := runTopologyMode(context.Background(), "/repo", "", "", "", true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "{") || !strings.Contains(out.String(), `"coverage"`) {
		t.Fatalf("unexpected JSON output: %s", out.String())
	}
}
