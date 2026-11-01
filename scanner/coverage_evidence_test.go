package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/analysis"
)

var goFixture = map[string]string{"go.mod": "module mod\n", "main.go": "package main\nfunc main() {}\n"}

func evidenceGraph(t *testing.T, files ...map[string]string) *FileGraph {
	t.Helper()
	dir := t.TempDir()
	for _, set := range files {
		for name, body := range set {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	graph, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return graph
}

func assertFileCoverage(t *testing.T, graph *FileGraph, file string, want analysis.CoverageStatus, fragment string) {
	t.Helper()
	coverage := graph.Coverage.ForFile(file)
	if got := coverage.EffectiveStatus(); got != want {
		t.Fatalf("%s coverage = %s, want %s (notes = %v)", file, got, want, coverage.Notes)
	}
	if fragment != "" && !strings.Contains(strings.Join(coverage.Notes, "\n"), fragment) {
		t.Fatalf("%s notes = %v, want one containing %q", file, coverage.Notes, fragment)
	}
}

// evidenceCase is a repo that must read partial for file, alone and beside Go.
type evidenceCase struct {
	files    map[string]string
	file     string
	fragment string
}

func runEvidenceCases(t *testing.T, cases map[string]evidenceCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, tc.files), tc.file, analysis.CoveragePartial, tc.fragment)
			mixed := evidenceGraph(t, tc.files, goFixture)
			assertFileCoverage(t, mixed, tc.file, analysis.CoveragePartial, tc.fragment)
			assertFileCoverage(t, mixed, "main.go", analysis.CoverageComplete, "")
		})
	}
}

func runPlainCases(t *testing.T, file string, cases map[string]map[string]string) {
	t.Helper()
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, files), file, analysis.CoverageComplete, "")
		})
	}
}

func TestPHPAutoloadEvidenceDowngradesCoverage(t *testing.T) {
	runEvidenceCases(t, map[string]evidenceCase{
		"composer autoload": {map[string]string{
			"composer.json": `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
			"src/a.php":     "<?php\n$x = 1;\n",
		}, "src/a.php", "composer.json has an autoload section"},
		"unresolved use": {map[string]string{
			"src/a.php": "<?php\nuse Vendor\\Thing;\n",
		}, "src/a.php", `src/a.php -> Vendor\Thing`},
		"namespace statement": {map[string]string{
			"src/a.php": "<?php\nnamespace App;\n$x = new Thing();\n",
		}, "src/a.php", "src/a.php has a namespace statement"},
	})
}

func TestPHPWithoutAutoloadEvidenceStaysComplete(t *testing.T) {
	runPlainCases(t, "a.php", map[string]map[string]string{
		"require only":              {"a.php": "<?php\nrequire_once 'b.php';\n", "b.php": "<?php\n$x = 1;\n"},
		"composer without autoload": {"composer.json": `{"name":"a/b","autoload":{}}`, "a.php": "<?php\n$x = 1;\n"},
		"namespace in a comment":    {"a.php": "<?php\n// namespace App;\n$x = 1;\n"},
	})
	// composer.json with no PHP file in scope says nothing about the graph.
	graph := evidenceGraph(t, map[string]string{"composer.json": `{"autoload":{"psr-4":{"A\\":"src/"}}}`}, goFixture)
	assertFileCoverage(t, graph, "main.go", analysis.CoverageComplete, "")
	if got := graph.Coverage.EffectiveStatus(); got != analysis.CoverageComplete {
		t.Fatalf("rollup = %s, want complete (notes = %v)", got, graph.Coverage.Notes)
	}
}
