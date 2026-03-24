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

func TestDepgraphRendersInternalChainsAndHubs(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.23\n",
		"pkg/root.go": `package pkg
import (
	_ "example.com/demo/pkg/a"
	_ "example.com/demo/pkg/b"
)
func Root() {}
`,
		"pkg/a/a.go": `package a
import (
	_ "example.com/demo/pkg/c"
	_ "example.com/demo/pkg/d"
	_ "example.com/demo/pkg/e"
	_ "example.com/demo/pkg/f"
	_ "example.com/demo/pkg/common"
)
func A() {}
`,
		"pkg/b/b.go": `package b
import _ "example.com/demo/pkg/common"
func B() {}
`,
		"pkg/c/c.go":         "package c\nfunc C() {}\n",
		"pkg/d/d.go":         "package d\nfunc D() {}\n",
		"pkg/e/e.go":         "package e\nfunc E() {}\n",
		"pkg/f/f.go":         "package f\nfunc F() {}\n",
		"pkg/common/util.go": "package common\nfunc Util() {}\n",
	}

	for rel, content := range files {
		fullPath := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", fullPath, err)
		}
	}

	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "pkg/root.go", Functions: []string{"Root"}},
			{Path: "pkg/a/a.go", Functions: []string{"A"}},
			{Path: "pkg/b/b.go", Functions: []string{"B"}},
			{Path: "pkg/c/c.go", Functions: []string{"C"}},
			{Path: "pkg/d/d.go", Functions: []string{"D"}},
			{Path: "pkg/e/e.go", Functions: []string{"E"}},
			{Path: "pkg/f/f.go", Functions: []string{"F"}},
			{Path: "pkg/common/util.go", Functions: []string{"Util"}},
		},
	}

	var buf bytes.Buffer
	Depgraph(&buf, project)
	output := buf.String()

	expectedSnippets := []string{
		"Pkg",
		"root ───▶ pkg/a/a, pkg/b/b",
		"a ──┬──▶ pkg/c/c",
		"└──▶ pkg/common/util",
		"HUBS: pkg/common/util (2←)",
		"8 files · 8 functions · 8 deps",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected output to contain %q, got:\n%s", snippet, output)
		}
	}
}
