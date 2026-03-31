package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

func TestDepgraphTitleCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty", input: "", expected: ""},
		{name: "single word", input: "hello", expected: "Hello"},
		{name: "multiple words", input: "hello world", expected: "Hello World"},
		{name: "extra spaces", input: "  hello   world  ", expected: "Hello World"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := titleCase(tt.input)
			if got != tt.expected {
				t.Fatalf("titleCase(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDepgraphGetSystemName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "skips generic prefix", input: "src/payment_service", expected: "Payment Service"},
		{name: "supports windows separators", input: "internal\\auth-module", expected: "Auth Module"},
		{name: "falls back to last segment", input: "src", expected: "Src"},
		{name: "root marker", input: ".", expected: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSystemName(tt.input)
			if got != tt.expected {
				t.Fatalf("getSystemName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDepgraphNoSourceFiles(t *testing.T) {
	project := scanner.DepsProject{
		Root:  t.TempDir(),
		Files: nil,
	}

	var buf bytes.Buffer
	Depgraph(&buf, project)

	output := buf.String()
	if !strings.Contains(output, "No source files found.") {
		t.Fatalf("expected no files message, got:\n%s", output)
	}
}

func TestDepgraphRendersExternalDepsAndSummarySection(t *testing.T) {
	project := scanner.DepsProject{
		Root: t.TempDir(),
		Files: []scanner.FileAnalysis{
			{
				Path:      "src/main.go",
				Functions: []string{"main"},
			},
		},
		ExternalDeps: map[string][]string{
			"go":         {"github.com/acme/module/v2", "github.com/acme/pkg", "github.com/acme/pkg"},
			"javascript": {"react", "react"},
		},
	}

	var buf bytes.Buffer
	Depgraph(&buf, project)
	output := buf.String()

	expectedSnippets := []string{
		"Dependency Flow",
		"Go: module, pkg",
		"JavaScript: react",
		"Src",
		"+1 standalone files",
		"1 files",
		"1 functions",
		"0 deps",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected output to contain %q, got:\n%s", snippet, output)
		}
	}
}

func writeDepgraphFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":                "module example.com/demo\n\ngo 1.24.0\n",
		"app/main.go":           "package app\n\nimport (\n\t\"example.com/demo/core/extra1\"\n\t\"example.com/demo/core/extra2\"\n\t\"example.com/demo/core/leaf\"\n\t\"example.com/demo/core/mid\"\n\t\"example.com/demo/core/root\"\n)\n\nfunc Main() {\n\textra1.Extra1()\n\textra2.Extra2()\n\tleaf.Leaf()\n\tmid.Mid()\n\troot.Root()\n}\n",
		"core/root/root.go":     "package root\n\nimport \"example.com/demo/core/mid\"\n\nfunc Root() {\n\tmid.Mid()\n}\n",
		"core/mid/mid.go":       "package mid\n\nimport \"example.com/demo/core/leaf\"\n\nfunc Mid() {\n\tleaf.Leaf()\n}\n",
		"core/leaf/leaf.go":     "package leaf\n\nfunc Leaf() {}\n",
		"core/extra1/extra1.go": "package extra1\n\nimport \"example.com/demo/core/leaf\"\n\nfunc Extra1() {\n\tleaf.Leaf()\n}\n",
		"core/extra2/extra2.go": "package extra2\n\nimport \"example.com/demo/core/leaf\"\n\nfunc Extra2() {\n\tleaf.Leaf()\n}\n",
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
}

func TestDepgraphRendersChainsFanoutAndHubs(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeDepgraphFixture(t, root)

	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "app/main.go", Functions: []string{"Main"}},
			{Path: "core/root/root.go", Functions: []string{"Root"}},
			{Path: "core/mid/mid.go", Functions: []string{"Mid"}},
			{Path: "core/leaf/leaf.go", Functions: []string{"Leaf"}},
			{Path: "core/extra1/extra1.go", Functions: []string{"Extra1"}},
			{Path: "core/extra2/extra2.go", Functions: []string{"Extra2"}},
		},
		ExternalDeps: map[string][]string{
			"go": {"example.com/very/long/module/name/v2"},
		},
	}

	var buf bytes.Buffer
	Depgraph(&buf, project)
	output := buf.String()

	expectedSnippets := []string{
		"Dependency Flow",
		"Go: name",
		"App",
		"Core",
		"main ──┬──▶ core/extra1/extra1",
		"└──▶ core/root/root",
		"root ───▶ core/mid/mid",
		"HUBS: core/leaf/leaf (4←), core/mid/mid (2←)",
		"6 files · 6 functions · 9 deps",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected output to contain %q, got:\n%s", snippet, output)
		}
	}
}
