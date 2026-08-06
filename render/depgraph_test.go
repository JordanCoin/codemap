package render

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/analysis"
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
	Depgraph(context.Background(), &buf, project)

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
	Depgraph(context.Background(), &buf, project)
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

func writeDepgraphFile(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Regression for PR #105: Depgraph must build the file graph from the caller's
// analyses instead of re-running the ast-grep scan, so an internal dep edge
// renders without any scanner binary present.
func TestDepgraphBuildsGraphFromAnalysesWithoutRescanning(t *testing.T) {
	root := t.TempDir()
	writeDepgraphFile(t, root, "a.go", "package a\n")
	writeDepgraphFile(t, root, "b.go", "package b\n")

	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "a.go", Language: "go", Functions: []string{"A"}, Imports: []string{"./b"}},
			{Path: "b.go", Language: "go", Functions: []string{"B"}},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	output := buf.String()
	if !strings.Contains(output, "a ───▶ b") {
		t.Fatalf("expected internal dep edge resolved from analyses, got:\n%s", output)
	}
}

// Regression for PR #105: a degraded scan must surface its coverage in text
// output instead of reading as a complete, empty graph.
func TestDepgraphRendersDegradedCoverage(t *testing.T) {
	root := t.TempDir()
	writeDepgraphFile(t, root, "main.go", "package main\n")
	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "main.go", Language: "go", Functions: []string{"main"}},
		},
		Coverage: analysis.Coverage{
			Status:  analysis.CoverageUnavailable,
			Sources: []analysis.Source{{Name: "ast-grep", Status: analysis.SourceTimeout, Detail: "scan timed out"}},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	output := buf.String()
	if !strings.Contains(output, "Coverage: unavailable") || !strings.Contains(output, "scan timed out") {
		t.Fatalf("expected degraded coverage in rendered deps, got:\n%s", output)
	}
}

func TestDepgraphEmptyFilesSurfacesCoverage(t *testing.T) {
	project := scanner.DepsProject{
		Root:  t.TempDir(),
		Files: nil,
		Coverage: analysis.Coverage{
			Status:  analysis.CoverageUnavailable,
			Sources: []analysis.Source{{Name: "ast-grep", Status: analysis.SourceFailed, Detail: "scanner failed"}},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	output := buf.String()
	if !strings.Contains(output, "No source files found.") || !strings.Contains(output, "Coverage: unavailable") {
		t.Fatalf("expected empty-files message plus degraded coverage, got:\n%s", output)
	}
}

func TestDepgraphCompleteCoverageOmitsLine(t *testing.T) {
	project := scanner.DepsProject{
		Root: t.TempDir(),
		Files: []scanner.FileAnalysis{
			{Path: "main.go", Language: "go", Functions: []string{"main"}},
		},
		Coverage: analysis.Coverage{
			Status:  analysis.CoverageComplete,
			Sources: []analysis.Source{{Name: "ast-grep", Status: analysis.SourceAuthoritative}},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	if strings.Contains(buf.String(), "Coverage:") {
		t.Fatalf("complete coverage must not render a coverage line, got:\n%s", buf.String())
	}
}

// Regression for PR #105: a canceled ctx must stop the graph build inside
// Depgraph instead of running an unobservable second scan; the renderer still
// emits the file list but no internal dependency edges.
func TestDepgraphHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	writeDepgraphFile(t, root, "a.go", "package a\n")
	writeDepgraphFile(t, root, "b.go", "package b\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "a.go", Language: "go", Functions: []string{"A"}, Imports: []string{"./b"}},
			{Path: "b.go", Language: "go", Functions: []string{"B"}},
		},
	}

	var buf bytes.Buffer
	Depgraph(ctx, &buf, project)
	output := buf.String()
	if strings.Contains(output, "a ───▶ b") {
		t.Fatalf("canceled Depgraph must not render graph edges, got:\n%s", output)
	}
	if !strings.Contains(output, "2 files") || !strings.Contains(output, "2 functions") {
		t.Fatalf("canceled Depgraph must still render the file summary, got:\n%s", output)
	}
}

func TestDepgraphRendersPartialCoverageWithNote(t *testing.T) {
	root := t.TempDir()
	writeDepgraphFile(t, root, "main.go", "package main\n")
	project := scanner.DepsProject{
		Root: root,
		Files: []scanner.FileAnalysis{
			{Path: "main.go", Language: "go", Functions: []string{"main"}},
		},
		Coverage: analysis.Coverage{
			Status: analysis.CoveragePartial,
			Sources: []analysis.Source{
				{Name: "ast-grep", Status: analysis.SourceAuthoritative},
				{Name: "rust-cargo", Status: analysis.SourceMixed, Detail: "Rust dependency resolution is partial"},
			},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	output := buf.String()
	if !strings.Contains(output, "Coverage: partial") || !strings.Contains(output, "Rust dependency resolution is partial") {
		t.Fatalf("expected partial coverage with note, got:\n%s", output)
	}
}

func TestDepgraphRendersPartialCoverageWithoutDetail(t *testing.T) {
	root := t.TempDir()
	writeDepgraphFile(t, root, "main.go", "package main\n")
	project := scanner.DepsProject{
		Root:  root,
		Files: []scanner.FileAnalysis{{Path: "main.go", Language: "go", Functions: []string{"main"}}},
		Coverage: analysis.Coverage{
			Status:  analysis.CoveragePartial,
			Sources: []analysis.Source{{Name: "rust-cargo", Status: analysis.SourceMixed}},
		},
	}

	var buf bytes.Buffer
	Depgraph(context.Background(), &buf, project)
	if !strings.Contains(buf.String(), "Coverage: partial") {
		t.Fatalf("expected bare partial coverage line, got:\n%s", buf.String())
	}
}
