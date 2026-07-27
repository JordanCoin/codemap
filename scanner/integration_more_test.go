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

	fromAnalyses, err := BuildFileGraphFromAnalyses(root, analyses)
	if err != nil {
		t.Fatalf("BuildFileGraphFromAnalyses() error: %v", err)
	}
	if got := fromAnalyses.Importers["pkg/types/types.go"]; len(got) != 3 {
		t.Fatalf("precomputed filtered hub importers = %#v, want exactly 3", got)
	}
	if _, ok := fromAnalyses.Imports["excluded/d/d.go"]; ok {
		t.Fatal("configured precomputed graph retained excluded Go file")
	}
	if _, ok := fromAnalyses.Imports["schema.ts"]; ok {
		t.Fatal("configured precomputed graph retained extension-filtered file")
	}
}

func TestExplicitDependencyFiltersOverrideRepositoryConfig(t *testing.T) {
	if !NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	t.Run("only", func(t *testing.T) {
		root := t.TempDir()
		files := map[string]string{
			".codemap/config.json": `{"only":["ts"]}`,
			"go.mod":               "module example.com/explicit\n\ngo 1.22\n",
			"pkg/types/types.go":   "package types\n\ntype Item struct{}\n",
			"a/a.go":               "package a\n\nimport _ \"example.com/explicit/pkg/types\"\n",
			"b/b.go":               "package b\n\nimport _ \"example.com/explicit/pkg/types\"\n",
			"schema.ts":            "export const schema = true\n",
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

		analyses, err := ScanForDepsWithFilters(root, Filters{Only: []string{"go"}})
		if err != nil {
			t.Fatalf("ScanForDepsWithFilters() error: %v", err)
		}
		for _, analysis := range analyses {
			if filepath.Ext(analysis.Path) != ".go" {
				t.Fatalf("explicit only filter returned %q", analysis.Path)
			}
		}

		configured, err := ScanForDeps(root)
		if err != nil {
			t.Fatalf("ScanForDeps() error: %v", err)
		}
		for _, analysis := range configured {
			if filepath.Ext(analysis.Path) == ".go" {
				t.Fatalf("configured wrapper ignored repository only filter: %q", analysis.Path)
			}
		}

		graph, err := BuildFileGraphWithFilters(root, Filters{Only: []string{"go"}})
		if err != nil {
			t.Fatalf("BuildFileGraphWithFilters() error: %v", err)
		}
		if got := graph.Importers["pkg/types/types.go"]; len(got) != 2 {
			t.Fatalf("explicit only graph importers = %#v, want both Go callers", got)
		}
		if _, ok := graph.Packages["example.com/explicit/pkg/types"]; !ok {
			t.Fatal("explicit only graph file index omitted the Go package")
		}
	})

	t.Run("exclude", func(t *testing.T) {
		root := t.TempDir()
		files := map[string]string{
			".codemap/config.json": `{"exclude":["blocked"]}`,
			"go.mod":               "module example.com/explicit\n\ngo 1.22\n",
			"pkg/types/types.go":   "package types\n\ntype Item struct{}\n",
			"keep/keep.go":         "package keep\n\nimport _ \"example.com/explicit/pkg/types\"\n",
			"blocked/blocked.go":   "package blocked\n\nimport _ \"example.com/explicit/pkg/types\"\n",
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

		filters := Filters{Exclude: []string{"keep"}}
		analyses, err := ScanForDepsWithFilters(root, filters)
		if err != nil {
			t.Fatalf("ScanForDepsWithFilters() error: %v", err)
		}
		for _, analysis := range analyses {
			if strings.HasPrefix(analysis.Path, "keep/") {
				t.Fatalf("explicit exclude filter returned %q", analysis.Path)
			}
		}
		if !containsAnalysisPath(analyses, "blocked/blocked.go") {
			t.Fatal("explicit exclude filter inherited repository config")
		}

		graph, err := BuildFileGraphFromFilteredAnalyses(root, analyses, filters)
		if err != nil {
			t.Fatalf("BuildFileGraphFromFilteredAnalyses() error: %v", err)
		}
		if got := graph.Importers["pkg/types/types.go"]; len(got) != 1 || got[0] != "blocked/blocked.go" {
			t.Fatalf("explicit exclude graph importers = %#v, want blocked caller only", got)
		}
		if _, ok := graph.Packages["example.com/explicit/keep"]; ok {
			t.Fatal("explicit exclude graph file index retained excluded package")
		}
	})
}

func containsAnalysisPath(analyses []FileAnalysis, want string) bool {
	for _, analysis := range analyses {
		if analysis.Path == want {
			return true
		}
	}
	return false
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

	if _, err := BuildFileGraphFromFilteredAnalyses(filepath.Join(root, "missing"), nil, Filters{}); err == nil {
		t.Fatal("expected explicit-filter graph of missing root to propagate a file scan error")
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

	if _, err := BuildFileGraphWithFilters(t.TempDir(), Filters{}); err == nil {
		t.Fatal("expected explicit-filter graph to propagate a dependency scan error")
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
