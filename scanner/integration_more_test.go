package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeScannerDepsFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":             "module example.com/demo\n\ngo 1.22\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
		"a/a.go":             "package a\n\nimport _ \"example.com/demo/pkg/types\"\n\nfunc UseA() {}\n",
		"b/b.go":             "package b\n\nimport _ \"example.com/demo/pkg/types\"\n\nfunc UseB() {}\n",
		"c/c.go":             "package c\n\nimport _ \"example.com/demo/pkg/types\"\n\nfunc UseC() {}\n",
		"main.go":            "package main\n\nimport _ \"example.com/demo/pkg/types\"\n\nfunc main() {}\n",
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

func TestScanForDepsBuildFileGraphAndAnalyzeImpact(t *testing.T) {
	if !NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeScannerDepsFixture(t, root)

	analyses, err := ScanForDeps(root)
	if err != nil {
		t.Fatalf("ScanForDeps() error: %v", err)
	}
	byPath := make(map[string]FileAnalysis, len(analyses))
	for _, analysis := range analyses {
		byPath[analysis.Path] = analysis
	}
	if got := byPath["a/a.go"].Imports; len(got) != 1 || got[0] != "example.com/demo/pkg/types" {
		t.Fatalf("expected a/a.go to import types package, got %+v", got)
	}
	if got := byPath["main.go"].Functions; len(got) != 1 || got[0] != "main" {
		t.Fatalf("expected main.go to expose main(), got %+v", got)
	}

	fg, err := BuildFileGraph(root)
	if err != nil {
		t.Fatalf("BuildFileGraph() error: %v", err)
	}
	if fg.Module != "example.com/demo" {
		t.Fatalf("file graph module = %q, want example.com/demo", fg.Module)
	}
	if got := fg.Importers["pkg/types/types.go"]; len(got) != 4 {
		t.Fatalf("expected 4 importers for pkg/types/types.go, got %+v", got)
	}
	if !fg.IsHub("pkg/types/types.go") {
		t.Fatal("expected pkg/types/types.go to be detected as a hub")
	}

	impacts := AnalyzeImpact(root, []FileInfo{{Path: "pkg/types/types.go"}})
	if len(impacts) == 0 {
		t.Fatal("expected AnalyzeImpact to report at least one impacted file")
	}

	maxUsedBy := 0
	for _, impact := range impacts {
		if impact.UsedBy > maxUsedBy {
			maxUsedBy = impact.UsedBy
		}
	}
	if maxUsedBy < 4 {
		t.Fatalf("expected impacted file usage count >= 4, got %+v", impacts)
	}
}

func TestDependencyAndGraphScansRespectConfiguredFilters(t *testing.T) {
	if !NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	files := map[string]string{
		".codemap/config.json": `{"only":["go"],"exclude":["excluded"]}`,
		"go.mod":               "module example.com/filtered\n\ngo 1.22\n",
		"pkg/types/types.go":   "package types\n\ntype Item struct{}\n",
		"a/a.go":               "package a\n\nimport _ \"example.com/filtered/pkg/types\"\n",
		"b/b.go":               "package b\n\nimport _ \"example.com/filtered/pkg/types\"\n",
		"c/c.go":               "package c\n\nimport _ \"example.com/filtered/pkg/types\"\n",
		"schema.ts":            "import './blocked'\n",
		"excluded/d/d.go":      "package d\n\nimport _ \"example.com/filtered/pkg/types\"\n",
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

	analyses, err := ScanForDeps(root)
	if err != nil {
		t.Fatalf("ScanForDeps() error: %v", err)
	}
	for _, analysis := range analyses {
		if analysis.Path == "schema.ts" || strings.HasPrefix(analysis.Path, "excluded/") {
			t.Fatalf("configured-out dependency analysis returned: %s", analysis.Path)
		}
	}

	fg, err := BuildFileGraph(root)
	if err != nil {
		t.Fatalf("BuildFileGraph() error: %v", err)
	}
	if got := fg.Importers["pkg/types/types.go"]; len(got) != 3 {
		t.Fatalf("filtered hub importers = %#v, want exactly 3", got)
	}
	if _, ok := fg.Imports["excluded/d/d.go"]; ok {
		t.Fatal("excluded Go file remains in graph")
	}
	if _, ok := fg.Imports["schema.ts"]; ok {
		t.Fatal("extension-filtered file remains in graph")
	}
}

func TestConfiguredScanHelpers(t *testing.T) {
	if !MatchesFilters("main.go", ".go", []string{"go"}, nil) {
		t.Fatal("expected exported filter helper to accept configured extension")
	}
	if MatchesFilters("main.ts", ".ts", []string{"go"}, nil) {
		t.Fatal("expected exported filter helper to reject unconfigured extension")
	}

	root := t.TempDir()
	analyses := []FileAnalysis{{Path: "schema.ts"}}
	got := filterConfiguredAnalyses(root, analyses)
	if len(got) != 1 || got[0].Path != "schema.ts" {
		t.Fatalf("unconfigured analysis filter = %+v, want original analysis", got)
	}

	if _, err := ScanConfiguredFiles(filepath.Join(root, "missing"), nil); err == nil {
		t.Fatal("expected configured scan of missing root to fail")
	}
}

func TestScanForDepsPropagatesScanError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	binDir := t.TempDir()
	fakeBinary := filepath.Join(binDir, "ast-grep")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'ast-grep 0.40.0'; exit 0; fi\nprintf '[invalid'\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	if _, err := ScanForDeps(t.TempDir()); err == nil {
		t.Fatal("expected malformed ast-grep output to propagate a scan error")
	}
}

func TestScanForDepsPropagatesSetupError(t *testing.T) {
	invalidTemp := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(invalidTemp, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", invalidTemp)
	t.Setenv("TMP", invalidTemp)
	t.Setenv("TEMP", invalidTemp)

	if _, err := ScanForDeps(t.TempDir()); err == nil {
		t.Fatal("expected invalid temporary directory to propagate a scanner setup error")
	}
}
