package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

func makeBlastRadiusGitRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.22\n",
		"pkg/math/math.go": `package math

func ComputeTotal(a, b int) int {
	return a + b
}
`,
		"internal/service/service.go": `package service

import "example.com/demo/pkg/math"

func Run() int {
	return math.ComputeTotal(2, 3)
}
`,
		"internal/worker/worker.go": `package worker

import "example.com/demo/pkg/math"

func Work() int {
	return math.ComputeTotal(4, 5)
}
`,
		"main.go": `package main

import "example.com/demo/internal/service"

func main() {
	_ = service.Run()
}
`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGitMainTestCmd(t, root, "init")
	runGitMainTestCmd(t, root, "add", ".")
	runGitMainTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitMainTestCmd(t, root, "branch", "-M", "main")

	updated := `package math

func ComputeTotal(a, b int) int {
	return a + b + 1
}
`
	if err := os.WriteFile(filepath.Join(root, "pkg", "math", "math.go"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestBlastRadiusSubcommandMarkdownAndJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := makeBlastRadiusGitRepo(t)

	markdown, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("blast-radius markdown failed: %v\nstderr=%s", err, stderr)
	}
	for _, check := range []string{
		"# Codemap Blast Radius",
		"## Affected Outside Diff",
		"## Impact Snippets",
		"`internal/service/service.go` via `pkg/math/math.go`",
		"ComputeTotal",
	} {
		if !strings.Contains(markdown, check) {
			t.Fatalf("expected %q in markdown output, got:\n%s", check, markdown)
		}
	}

	jsonOut, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("blast-radius json failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(jsonOut), &bundle); err != nil {
		t.Fatalf("expected blast-radius JSON output, got error %v with body:\n%s", err, jsonOut)
	}
	if bundle.Ref != "HEAD" {
		t.Fatalf("bundle ref = %q, want HEAD", bundle.Ref)
	}
	if bundle.Summary.ChangedFiles != 1 || bundle.Summary.ChangedFilesTotal != 1 {
		t.Fatalf("unexpected changed-file summary: %+v", bundle.Summary)
	}
	if bundle.Summary.ImpactedOutsideDiffShown == 0 {
		t.Fatalf("expected impacted files outside diff, got %+v", bundle.Summary)
	}
	if len(bundle.Snippets) == 0 {
		t.Fatalf("expected at least one impact snippet, got %+v", bundle)
	}
	if bundle.Snippets[0].MatchedTerm != "ComputeTotal" {
		t.Fatalf("expected snippet match to target ComputeTotal, got %+v", bundle.Snippets[0])
	}
}

func TestBlastRadiusSubcommandNoChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := makeMainGitRepo(t, "main")

	markdown, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("blast-radius no-changes markdown failed: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(markdown, "## No Changes") {
		t.Fatalf("expected no-changes section in markdown output, got:\n%s", markdown)
	}
	if !strings.Contains(markdown, "No files changed vs `HEAD`.") {
		t.Fatalf("expected no-changes message in markdown output, got:\n%s", markdown)
	}
	if strings.Contains(markdown, "## Diff") {
		t.Fatalf("expected no diff section when there are no changes, got:\n%s", markdown)
	}

	jsonOut, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("blast-radius no-changes json failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(jsonOut), &bundle); err != nil {
		t.Fatalf("expected no-changes blast-radius JSON output, got error %v with body:\n%s", err, jsonOut)
	}
	if bundle.Summary.ChangedFiles != 0 || bundle.Summary.ChangedFilesTotal != 0 {
		t.Fatalf("expected zero changed files in no-changes output, got %+v", bundle.Summary)
	}
	if bundle.Rendered.Diff != "No files changed vs HEAD\n" {
		t.Fatalf("unexpected no-changes diff text: %q", bundle.Rendered.Diff)
	}
	if bundle.Rendered.Deps != "No changed source files to analyze.\n" {
		t.Fatalf("unexpected no-changes deps text: %q", bundle.Rendered.Deps)
	}
}
