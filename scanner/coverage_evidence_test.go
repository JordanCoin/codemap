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

func TestRubyAutoloadEvidenceDowngradesCoverage(t *testing.T) {
	runEvidenceCases(t, map[string]evidenceCase{
		"gemfile names rails": {map[string]string{
			"Gemfile": "source 'https://rubygems.org'\ngem 'rails', '~> 7.0'\n",
			"a.rb":    "X = 1\n",
		}, "a.rb", "Gemfile names rails"},
		"gemspec names zeitwerk": {map[string]string{
			"a.gemspec": "Gem::Specification.new do |s|\n  s.add_dependency \"zeitwerk\"\nend\n",
			"a.rb":      "X = 1\n",
		}, "a.rb", "a.gemspec names zeitwerk"},
		"config application": {map[string]string{
			"config/application.rb": "module App; end\n",
			"app/a.rb":              "X = 1\n",
		}, "app/a.rb", "config/application.rb is present"},
		"zeitwerk loader call": {map[string]string{
			"lib/a.rb": "require 'zeitwerk'\nloader = Zeitwerk::Loader.for_gem\nloader.setup\n",
		}, "lib/a.rb", "lib/a.rb calls Zeitwerk::Loader"},
	})
}

func TestRubyWithoutAutoloadEvidenceStaysComplete(t *testing.T) {
	runPlainCases(t, "a.rb", map[string]map[string]string{
		"require_relative only":     {"a.rb": "require_relative 'b'\n", "b.rb": "X = 1\n"},
		"gemfile without rails":     {"Gemfile": "gem 'rake'\n# gem 'rails'\n", "a.rb": "X = 1\n"},
		"rails as a dev dependency": {"a.gemspec": "Gem::Specification.new do |s|\n  s.add_development_dependency 'rails'\nend\n", "a.rb": "X = 1\n"},
		"zeitwerk in a comment":     {"a.rb": "# Zeitwerk::Loader is not used here\nX = 1\n"},
	})
}

func TestBashDynamicSourceDowngradesCoverage(t *testing.T) {
	runEvidenceCases(t, map[string]evidenceCase{
		"variable prefix": {map[string]string{
			"a.sh": "#!/bin/bash\n\nsource \"${DIR}/lib/b.sh\"\n",
		}, "a.sh", "first at a.sh:3, 1 in total"},
		"dot after a test": {map[string]string{
			"a.sh": "[ -f \"$f\" ] && . \"$f\"\n. $g\n",
		}, "a.sh", "first at a.sh:1, 2 in total"},
		"process substitution": {map[string]string{
			"a.sh": "source <(cmd)\n",
		}, "a.sh", "first at a.sh:1, 1 in total"},
		"bats load": {map[string]string{
			"lib.bash":   "x=1\n",
			"t/a.bats":   "load \"$BATS_TEST_DIRNAME/helper\"\n",
			"t/plain.sh": "x=1\n",
		}, "lib.bash", "first at t/a.bats:1, 1 in total"},
	})
}

func TestBashWithoutDynamicSourceStaysComplete(t *testing.T) {
	runPlainCases(t, "a.sh", map[string]map[string]string{
		"literal source": {"a.sh": "source ./b.sh\nsource \"./c.sh\"\n", "b.sh": "x=1\n", "c.sh": "x=1\n"},
		"literal load":   {"a.sh": "x=1\n", "t.bats": "load 'helper'\n"},
		"single quoted":  {"a.sh": "source '$literal'\n"},
		"commented":      {"a.sh": "# source \"$x\"\nx=1\n"},
		"not a source":   {"a.sh": "echo $x\n./run \"$x\"\nreload \"$x\"\n"},
	})
}

func TestFileCoverageOutsideTheGraphIsNotComplete(t *testing.T) {
	graph := evidenceGraph(t, goFixture, map[string]string{
		"lib/x.inc":         "#include \"a.h\"\n",
		"node_modules/a.js": "x\n",
	})
	cases := map[string]struct {
		want     analysis.CoverageStatus
		fragment string
	}{
		"main.go":           {analysis.CoverageComplete, ""},
		"lib/x.inc":         {analysis.CoveragePartial, "lib/x.inc has no language mapping"},
		"node_modules/a.js": {analysis.CoveragePartial, "node_modules/a.js is not in the scanned file set"},
		"gone.go":           {analysis.CoveragePartial, "gone.go is not in the scanned file set"},
	}
	for file, tc := range cases {
		coverage := graph.FileCoverage(file)
		if got := coverage.EffectiveStatus(); got != tc.want {
			t.Errorf("%s coverage = %s, want %s (notes = %v)", file, got, tc.want, coverage.Notes)
		}
		if tc.fragment != "" && !strings.Contains(strings.Join(coverage.Notes, "\n"), tc.fragment) {
			t.Errorf("%s notes = %v, want one containing %q", file, coverage.Notes, tc.fragment)
		}
	}
	// A per-file note must not leak into the rollup or the next file's answer.
	if got := graph.Coverage.EffectiveStatus(); got != analysis.CoverageComplete {
		t.Errorf("rollup = %s, want complete (notes = %v)", got, graph.Coverage.Notes)
	}
}

func TestFileCoverageExcludedByOnlyFilterIsNotComplete(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module mod\n", "main.go": "package main\n", "a.py": "x = 1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := BuildFileGraph(context.Background(), dir, Filters{Only: []string{"go"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.FileCoverage("a.py").EffectiveStatus(); got != analysis.CoveragePartial {
		t.Errorf("a.py coverage = %s, want partial", got)
	}
	if got := graph.FileCoverage("main.go").EffectiveStatus(); got != analysis.CoverageComplete {
		t.Errorf("main.go coverage = %s, want complete", got)
	}
}
