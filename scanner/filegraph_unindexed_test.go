package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestExplainUnindexedNamesOneReason pins the reason text for every way a
// file can be outside a graph's answerable set (#140 part 2), and that a
// scanned source file, or a graph with no inventory at all, yields no
// explanation: the first is a real zero, the second is unknown, and neither
// may be reported as "not indexed".
func TestExplainUnindexedNamesOneReason(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/unindexed\n\ngo 1.22\n")
	write("main.go", "package main\n\nimport _ \"example.com/unindexed/pkg/types\"\n\nfunc main() {}\n")
	write("pkg/types/types.go", "package types\n\ntype Item struct{}\n")
	write("NOTES.md", "# notes\n")
	write("Makefile", "all:\n")
	write("vendor/v.go", "package v\n")
	write("ignored/x.go", "package ignored\n")
	write("skip/s.go", "package skip\n")
	write(".gitignore", "ignored/\n")

	filters := Filters{Exclude: []string{"skip"}}
	fg, err := BuildFileGraph(context.Background(), root, filters)
	if err != nil {
		t.Fatalf("BuildFileGraph() error: %v", err)
	}
	if !fg.Indexed("main.go") || !fg.Indexed("NOTES.md") {
		t.Fatalf("fixture setup: expected main.go and NOTES.md in KnownFiles, got %v", fg.KnownFiles)
	}

	for _, tc := range []struct {
		path   string
		reason string
	}{
		{"nope/missing.go", "path does not exist"},
		{"pkg", "path is a directory, not a file"},
		{"skip/s.go", "excluded by config: only/exclude in .codemap/config.json"},
		{"vendor/v.go", "under ignored directory vendor/"},
		{"ignored/x.go", "ignored by .gitignore"},
		{"NOTES.md", "unsupported file type: .md has no import analysis"},
		{"Makefile", "unsupported file type: no extension, so no import analysis"},
	} {
		why, ok := fg.ExplainUnindexed(filepath.FromSlash(tc.path), filters)
		if !ok {
			t.Errorf("ExplainUnindexed(%q) = indexed, want reason %q", tc.path, tc.reason)
			continue
		}
		if why.Reason != tc.reason {
			t.Errorf("ExplainUnindexed(%q) reason = %q, want %q", tc.path, why.Reason, tc.reason)
		}
		if why.Next == "" {
			t.Errorf("ExplainUnindexed(%q) has no next action", tc.path)
		}
	}

	for _, indexed := range []string{"main.go", "pkg/types/types.go"} {
		if why, ok := fg.ExplainUnindexed(filepath.FromSlash(indexed), filters); ok {
			t.Errorf("ExplainUnindexed(%q) = %q, want indexed", indexed, why.Reason)
		}
	}

	// A graph without an inventory (e.g. rebuilt from a daemon cache) cannot
	// tell "never scanned" from "scanned, zero edges", so it must not claim
	// either: the caller keeps its ordinary output.
	noInventory := &FileGraph{Root: root, Imports: map[string][]string{}, Importers: map[string][]string{}}
	if why, ok := noInventory.ExplainUnindexed("nope/missing.go", filters); ok {
		t.Errorf("graph without KnownFiles explained %q as %q, want unknown", "nope/missing.go", why.Reason)
	}
	var nilGraph *FileGraph
	if _, ok := nilGraph.ExplainUnindexed("main.go", filters); ok {
		t.Error("nil graph produced an explanation")
	}
}
