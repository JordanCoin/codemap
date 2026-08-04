package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeTruthFixture creates a repo shaped like the real-world failure: local
// files whose basenames collide with Go standard library packages.
func writeTruthFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                        "module example.com/m\n\ngo 1.22\n",
		"cmd/context.go":                "package cmd\n",
		"skills/embed.go":               "package skills\n",
		"internal/buildinfo/version.go": "package buildinfo\n",
		"scanner/astgrep.go":            "package scanner\n",
		"app/main.py":                   "from app.core import config\n",
		"app/core/config.py":            "VALUE = 1\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGoStdlibImportsDoNotResolveToLocalFiles(t *testing.T) {
	root := writeTruthFixture(t)

	analyses := []FileAnalysis{
		{Path: "scanner/astgrep.go", Language: "go", Imports: []string{
			"context", "embed", "fmt", "github.com/fsnotify/fsnotify",
			"example.com/m/internal/buildinfo",
		}},
	}

	fg, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraphFromFilteredAnalyses: %v", err)
	}

	if got := fg.Importers["cmd/context.go"]; len(got) != 0 {
		t.Fatalf("stdlib \"context\" import resolved to local file, importers = %v", got)
	}
	if got := fg.Importers["skills/embed.go"]; len(got) != 0 {
		t.Fatalf("stdlib \"embed\" import resolved to local file, importers = %v", got)
	}
	if got := fg.Importers["internal/buildinfo/version.go"]; len(got) != 1 || got[0] != "scanner/astgrep.go" {
		t.Fatalf("module-internal import lost, importers = %v", got)
	}
}

func TestGoFilesWithoutModuleGetNoFuzzyEdges(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"cmd/context.go": "package cmd\n",
		"main.go":        "package main\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	analyses := []FileAnalysis{
		{Path: "main.go", Language: "go", Imports: []string{"context"}},
	}
	fg, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraphFromFilteredAnalyses: %v", err)
	}
	if got := fg.Importers["cmd/context.go"]; len(got) != 0 {
		t.Fatalf("Go import fuzzy-matched a local file without module context, importers = %v", got)
	}
}

func TestNonGoResolutionUnaffectedByGoGate(t *testing.T) {
	root := writeTruthFixture(t)

	analyses := []FileAnalysis{
		{Path: "app/main.py", Language: "python", Imports: []string{"app.core.config"}},
	}
	fg, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraphFromFilteredAnalyses: %v", err)
	}
	if got := fg.Importers["app/core/config.py"]; len(got) != 1 || got[0] != "app/main.py" {
		t.Fatalf("python suffix resolution regressed, importers = %v", got)
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"watch/events_test.go", true},
		{"watch/events.go", false},
		{"src/app.test.ts", true},
		{"src/app.spec.tsx", true},
		{"src/app.ts", false},
		{"tests/test_config.py", true},
		{"pkg/attest.go", false},
		{filepath.Join("watch", "events_test.go"), true},
	}
	for _, tt := range tests {
		if got := IsTestFile(tt.path); got != tt.want {
			t.Fatalf("IsTestFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestHubStatusIgnoresTestFileImporters(t *testing.T) {
	fg := &FileGraph{Importers: map[string][]string{
		"pkg/types.go":    {"a.go", "b_test.go", "c_test.go"},
		"pkg/hub.go":      {"a.go", "b.go", "c.go"},
		"pkg/pop_test.go": {"a.go", "b.go", "c.go", "d.go"},
	}}

	if fg.IsHub("pkg/types.go") {
		t.Fatal("file with only 1 non-test importer must not be a hub")
	}
	if !fg.IsHub("pkg/hub.go") {
		t.Fatal("file with 3 non-test importers must be a hub")
	}
	if fg.IsHub("pkg/pop_test.go") {
		t.Fatal("test files must never rank as hubs")
	}

	hubs := fg.HubFiles()
	if len(hubs) != 1 || hubs[0] != "pkg/hub.go" {
		t.Fatalf("HubFiles() = %v, want only pkg/hub.go", hubs)
	}
}
