package scanner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Coverage.Issues was declared, normalized and serialized but never
// populated, so every answer carried `"issues": []`, which reads as "nothing
// was dropped" even when imports failed to resolve (#137). Each unresolved
// project-relative import is now one issue, and a clean graph omits the key
// rather than publishing an empty list that looks like a finding.
func TestCoverageIssuesListUnresolvedProjectImports(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.ts", "import { x } from './gone';\nimport { y } from './b';\n")
	write("b.ts", "export const y = 1;\n")

	graph, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	coverage := graph.Coverage.Contract()
	if len(coverage.Issues) != 1 {
		t.Fatalf("issues = %+v, want exactly one for a.ts -> ./gone", coverage.Issues)
	}
	issue := coverage.Issues[0]
	if issue.Code != "unresolved-import" || issue.Path != "a.ts" || !strings.Contains(issue.Message, "./gone") {
		t.Fatalf("issue = %+v, want code unresolved-import, path a.ts, message naming ./gone", issue)
	}
	if issue.Line != 0 {
		t.Fatalf("issue.Line = %d; no line is known for an import, and ast-grep ranges are 0-based so a guessed one would be off by one", issue.Line)
	}
}

func TestCoverageIssuesOmittedWhenClean(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("import { y } from './b';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.ts"), []byte("export const y = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(graph.Coverage.Contract())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"issues"`) {
		t.Fatalf("clean coverage must omit the issues key, got %s", encoded)
	}
}
