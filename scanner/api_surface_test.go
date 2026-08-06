package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestNoRetiredCompatibilityTwins guards the single-entry-point contract: the
// retired XxxContext/WithFilters/Outcome names must stay out of the scanner API.
func TestNoRetiredCompatibilityTwins(t *testing.T) {
	retired := map[string]bool{
		"AnalyzeImpactContext":                      true,
		"BuildFileGraphContext":                     true,
		"BuildFileGraphFromAnalysesContext":         true,
		"BuildFileGraphFromFilteredAnalyses":        true,
		"BuildFileGraphFromFilteredAnalysesContext": true,
		"BuildFileGraphFromOutcomeWithFilters":      true,
		"BuildFileGraphWithFilters":                 true,
		"BuildFileGraphWithFiltersContext":          true,
		"DependencyCoverageContext":                 true,
		"GitDiffFiles":                              true,
		"GitDiffFilesContext":                       true,
		"GitDiffInfoContext":                        true,
		"HasConfiguredLanguageContext":              true,
		"ReadExternalDepsContext":                   true,
		"ScanConfiguredFilesContext":                true,
		"ScanDirectoryContext":                      true,
		"ScanDirectoryOutcome":                      true,
		"ScanDirectoryOutcomeContext":               true,
		"ScanFilesContext":                          true,
		"ScanForDepsOutcome":                        true,
		"ScanForDepsOutcomeContext":                 true,
		"ScanForDepsOutcomeWithFilters":             true,
		"ScanForDepsOutcomeWithFiltersContext":      true,
		"ScanForDepsWithFilters":                    true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if fn, ok := node.(*ast.FuncDecl); ok && retired[fn.Name.Name] {
				t.Errorf("retired compatibility wrapper remains: %s", fn.Name.Name)
			}
			return true
		})
	}
}
