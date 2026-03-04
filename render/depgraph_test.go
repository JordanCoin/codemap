package render

import (
	"bytes"
	"strings"
	"testing"

	"codemap/scanner"
)

func TestTitleCase(t *testing.T) {
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

func TestGetSystemName(t *testing.T) {
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

func TestDepgraphNoFiles(t *testing.T) {
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

func TestDepgraphRendersExternalDepsAndSummary(t *testing.T) {
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
