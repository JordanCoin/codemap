package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/config"
	"codemap/scanner"
)

// makeBlastRadiusHubRepo builds a repo where a changed file (pkg/hub/hub.go) has
// three external dependents, alongside a standalone changed file (aaa/aaa.go)
// that has none. filepath.Walk is lexical, so aaa/aaa.go sorts before
// pkg/hub/hub.go: with --max-changed-files=1 the hub is the one capped out,
// which is exactly the regression in findings #1 and #2.
func makeBlastRadiusHubRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/demo\n\ngo 1.22\n",
		"pkg/hub/hub.go": "package hub\n\nfunc Helper() int {\n\treturn 1\n}\n",
		"aaa/aaa.go":     "package aaa\n\nfunc A() int {\n\treturn 0\n}\n",
		"a/a.go":         "package a\n\nimport \"example.com/demo/pkg/hub\"\n\nfunc UseA() int { return hub.Helper() }\n",
		"b/b.go":         "package b\n\nimport \"example.com/demo/pkg/hub\"\n\nfunc UseB() int { return hub.Helper() }\n",
		"c/c.go":         "package c\n\nimport \"example.com/demo/pkg/hub\"\n\nfunc UseC() int { return hub.Helper() }\n",
	}
	for relPath, content := range files {
		full := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGitMainTestCmd(t, root, "init")
	runGitMainTestCmd(t, root, "add", ".")
	runGitMainTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitMainTestCmd(t, root, "branch", "-M", "main")

	// Change both the hub and the standalone file vs HEAD.
	if err := os.WriteFile(filepath.Join(root, "pkg", "hub", "hub.go"),
		[]byte("package hub\n\nfunc Helper() int {\n\treturn 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aaa", "aaa.go"),
		[]byte("package aaa\n\nfunc A() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func requireBlastRadiusTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}
}

// Finding #1: analysis must cover the full changed set, not just the capped
// subset that gets displayed.
func TestBlastRadiusAnalyzesFullChangedSetBeyondCap(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)

	out, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD",
		"--max-changed-files", "1", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}

	if bundle.Summary.ChangedFiles != 1 || bundle.Summary.ChangedFilesTotal != 2 {
		t.Fatalf("expected 1 shown of 2 changed, got %+v", bundle.Summary)
	}
	if bundle.Summary.HighestBlastRadius == nil {
		t.Fatalf("expected highest blast radius to be found even though the hub was capped out of display")
	}
	if !strings.Contains(bundle.Summary.HighestBlastRadius.File, "hub") {
		t.Fatalf("expected hub to be highest blast radius, got %+v", bundle.Summary.HighestBlastRadius)
	}
	if bundle.Summary.FilesWithDependents < 1 {
		t.Fatalf("expected >=1 file with dependents, got %+v", bundle.Summary)
	}
	if bundle.Summary.ImpactedOutsideDiffTotal < 3 {
		t.Fatalf("expected >=3 impacted files outside diff (a,b,c), got %+v", bundle.Summary)
	}
}

// Finding #2: the diff impact footer must reflect the files actually shown, not
// an arbitrary slice of the usage-sorted impact list.
func TestBlastRadiusImpactFooterMatchesShownFiles(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)

	out, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD",
		"--max-changed-files", "1", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}

	// hub.go is capped out of the shown diff, so its impact line must not appear.
	for _, imp := range bundle.Diff.Impact {
		if strings.Contains(imp.File, "hub") {
			t.Fatalf("impact footer references hub.go which is not in the shown diff: %+v", bundle.Diff.Impact)
		}
	}
	if strings.Contains(bundle.Rendered.Diff, "hub") {
		t.Fatalf("rendered diff footer references capped-out hub:\n%s", bundle.Rendered.Diff)
	}
}

// Finding #3: --max-changed-files=0 must not claim "no files changed" when files
// did change.
func TestBlastRadiusZeroCapIsConsistent(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusGitRepo(t)

	jsonOut, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD",
		"--max-changed-files", "0", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(jsonOut), &bundle); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, jsonOut)
	}
	if bundle.Summary.ChangedFilesTotal == 0 {
		t.Fatalf("expected non-zero changed total, got %+v", bundle.Summary)
	}
	if strings.HasPrefix(bundle.Rendered.Diff, "No files changed") {
		t.Fatalf("diff falsely claims no files changed: %q", bundle.Rendered.Diff)
	}

	md, _, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD",
		"--max-changed-files", "0", root)
	if err != nil {
		t.Fatalf("blast-radius markdown failed: %v", err)
	}
	if strings.Contains(md, "## No Changes") {
		t.Fatalf("markdown falsely shows No Changes section:\n%s", md)
	}
	if strings.Contains(md, "No files changed vs") {
		t.Fatalf("markdown falsely claims no files changed:\n%s", md)
	}
}

// Finding #4: total-budget truncation must not leave an unterminated code fence.
func TestBlastRadiusMarkdownFenceBalancedWhenTruncated(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)

	md, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD",
		"--max-total-chars", "650", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(md, "omitted after total budget") {
		t.Fatalf("expected output to be truncated by total budget, got:\n%s", md)
	}
	if strings.Count(md, "```")%2 != 0 {
		t.Fatalf("unbalanced code fences after truncation:\n%s", md)
	}
}

// Finding #5: flags after the positional path must still parse.
func TestBlastRadiusFlagsAfterPath(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusGitRepo(t)

	out, stderr, err := runCodemapWithInput("", "blast-radius", root, "--json", "--ref", "HEAD")
	if err != nil {
		t.Fatalf("flags-after-path failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatalf("expected JSON when flags follow the path, got: %v\n%s", err, out)
	}
}

// Finding #6: multiple format flags resolve last-wins instead of erroring.
func TestBlastRadiusMultipleFormatFlagsLastWins(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusGitRepo(t)

	out, stderr, err := runCodemapWithInput("", "blast-radius", "--markdown", "--json", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("--markdown --json failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatalf("expected JSON to win as the last flag, got: %v\n%s", err, out)
	}

	md, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--markdown", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("--json --markdown failed: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(md, "# Codemap Blast Radius") {
		t.Fatalf("expected markdown to win as the last flag, got:\n%s", md)
	}
}

// Finding #7: an invalid --ref surfaces the actionable branch/ref hint.
func TestBlastRadiusInvalidRefHint(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusGitRepo(t)

	_, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "nonexistent-branch-xyz", root)
	if err == nil {
		t.Fatalf("expected failure for invalid ref, got success; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "valid branch/ref") {
		t.Fatalf("expected actionable ref hint, got stderr:\n%s", stderr)
	}
}

// Finding #8: the shared importer helper must preserve scan order (the
// pre-existing `codemap --importers` behavior), not alphabetize.
func TestBuildImportersReportFromGraphPreservesScanOrder(t *testing.T) {
	fg := &scanner.FileGraph{
		Root:      "/repo",
		Imports:   map[string][]string{"x.go": {"zebra.go", "apple.go", "mango.go"}},
		Importers: map[string][]string{"x.go": {"zebra.go", "apple.go", "mango.go"}},
	}
	report := buildImportersReportFromGraph("/repo", "x.go", fg)
	want := []string{"zebra.go", "apple.go", "mango.go"}
	for i, w := range want {
		if report.Importers[i] != w {
			t.Fatalf("importers reordered: got %v, want %v", report.Importers, want)
		}
		if report.Imports[i] != w {
			t.Fatalf("imports reordered: got %v, want %v", report.Imports, want)
		}
	}
}

// Finding #9: changed files with no importers must not emit empty importer
// sections.
func TestBlastRadiusOmitsEmptyImporterSections(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)

	md, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	if strings.Contains(md, "### `aaa/aaa.go`") {
		t.Fatalf("emitted importer section for a file with no importers:\n%s", md)
	}
	// No empty code blocks (``` immediately followed by ```).
	if strings.Contains(md, "```text\n\n```") {
		t.Fatalf("emitted empty importer code block:\n%s", md)
	}
}

// Finding #10: BuildFileGraphFromAnalyses / AnalyzeImpactFromAnalyses must match
// the scanning wrappers so the single-scan refactor is behavior-preserving.
func TestBlastRadiusSingleScanParity(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{"only":["go"],"exclude":["c"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load(root)
	filters := scanner.Filters{Only: cfg.Only, Exclude: cfg.Exclude}

	analyses, err := scanner.ScanForDepsWithFilters(root, filters)
	if err != nil {
		t.Fatalf("ScanForDepsWithFilters: %v", err)
	}

	fgWrap, err := scanner.BuildFileGraph(root)
	if err != nil {
		t.Fatalf("BuildFileGraph: %v", err)
	}
	fgInjected, err := scanner.BuildFileGraphFromFilteredAnalyses(root, analyses, filters)
	if err != nil {
		t.Fatalf("BuildFileGraphFromFilteredAnalyses: %v", err)
	}
	if len(fgWrap.Importers["pkg/hub/hub.go"]) != len(fgInjected.Importers["pkg/hub/hub.go"]) {
		t.Fatalf("file graph importer parity mismatch: %v vs %v",
			fgWrap.Importers["pkg/hub/hub.go"], fgInjected.Importers["pkg/hub/hub.go"])
	}

	changed := []scanner.FileInfo{{Path: "pkg/hub/hub.go"}}
	impWrap := scanner.AnalyzeImpact(root, changed)
	impInjected := scanner.AnalyzeImpactFromAnalyses(changed, analyses)
	if len(impWrap) != len(impInjected) {
		t.Fatalf("impact parity mismatch: %v vs %v", impWrap, impInjected)
	}
	for _, importer := range fgInjected.Importers["pkg/hub/hub.go"] {
		if importer == "c/c.go" {
			t.Fatalf("filtered graph retained excluded importer: %v", fgInjected.Importers["pkg/hub/hub.go"])
		}
	}

	bundle, err := buildBlastRadiusBundle(root, "HEAD", defaultBlastRadiusLimits())
	if err != nil {
		t.Fatalf("buildBlastRadiusBundle: %v", err)
	}
	for _, report := range bundle.Importers {
		for _, importer := range report.Importers {
			if importer == "c/c.go" {
				t.Fatalf("blast radius retained excluded importer: %+v", bundle.Importers)
			}
		}
	}
}

// Finding #4 (deterministic unit test): truncating a fenced block mid-content
// must still close the fence.
func TestBlastOutputBuilderClosesFenceOnTruncation(t *testing.T) {
	b := newBlastOutputBuilder(120)
	var body strings.Builder
	body.WriteString("## Diff\n\n```text\n")
	for i := 0; i < 30; i++ {
		body.WriteString("a fairly long diff content line that fills budget\n")
	}
	body.WriteString("```\n\n")
	if b.Append(body.String(), "diff section") {
		t.Fatalf("expected the block to exceed the budget and truncate")
	}
	out := b.String()
	if strings.Count(out, "```")%2 != 0 {
		t.Fatalf("unbalanced fences after truncation:\n%q", out)
	}
}
