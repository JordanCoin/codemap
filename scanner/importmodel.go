package scanner

import (
	"fmt"
	"sort"
	"strings"

	"codemap/analysis"
)

// symbolLevelImportLanguages are languages whose import statements name a
// module, package or namespace rather than a file, and where two files in the
// same package reference each other with no import statement at all. The
// file-to-file edge model cannot represent intra-package structure for them:
// resolving their imports yields framework names (SwiftUI, System.Text) that
// belong to no file in the project, so the graph comes back structurally empty
// rather than empty-by-accident.
//
// Languages absent from this set express intra-project structure as a path an
// import can be resolved to (Go package paths, Python module paths, relative
// JS/TS specifiers, Rust mod declarations, C/C++ includes), so an empty graph
// there is a real finding and stays complete.
var symbolLevelImportLanguages = map[string]string{
	"csharp": "C#",
	"java":   "Java",
	"kotlin": "Kotlin",
	"scala":  "Scala",
	"swift":  "Swift",
}

// symbolLevelCoverageNote explains why a symbol-level language cannot be
// covered by file-level import resolution, in the terms a consumer needs to
// decide whether to trust a zero-dependent answer.
const symbolLevelCoverageNote = "imports name modules, not files, and same-package files need no import: intra-project edges need symbol-reference resolution and are not represented"

// ResolvesFileLevelImports reports whether import resolution can produce
// file-to-file edges for a language.
func ResolvesFileLevelImports(language string) bool {
	_, symbolLevel := symbolLevelImportLanguages[language]
	return !symbolLevel
}

// symbolLevelInventory counts scanned files per symbol-level language, keyed by
// the language's display name.
func symbolLevelInventory(files []FileInfo) map[string]int {
	counts := make(map[string]int)
	for _, file := range files {
		display, symbolLevel := symbolLevelImportLanguages[DetectLanguage(file.Path)]
		if symbolLevel {
			counts[display]++
		}
	}
	return counts
}

// symbolLevelSources renders one source per symbol-level language present, so
// the source list names which slice of the project the graph cannot see rather
// than emitting a bare "partial" with nothing to point at.
func symbolLevelSources(counts map[string]int) []analysis.Source {
	if len(counts) == 0 {
		return nil
	}
	displays := make([]string, 0, len(counts))
	for display := range counts {
		displays = append(displays, display)
	}
	sort.Strings(displays)

	sources := make([]analysis.Source, 0, len(displays))
	for _, display := range displays {
		sources = append(sources, analysis.Source{
			Name:   "symbol-imports/" + strings.ToLower(display),
			Status: analysis.SourceUnavailable,
			Detail: fmt.Sprintf("%s (%d files): %s", display, counts[display], symbolLevelCoverageNote),
		})
	}
	return sources
}

// ApplySymbolLevelImportCoverage downgrades coverage that would otherwise claim
// complete knowledge of a project whose files are in languages the file-level
// edge model does not apply to. A consumer reading "complete" next to zero
// dependents concludes a change is isolated; for these languages that
// conclusion is unfounded, so the status must not be complete and the reason
// must be attached.
//
// Coverage that is already partial or unavailable keeps its status; this only
// ever removes confidence.
func ApplySymbolLevelImportCoverage(coverage analysis.Coverage, files []FileInfo) analysis.Coverage {
	sources := symbolLevelSources(symbolLevelInventory(files))
	if len(sources) == 0 {
		return coverage
	}
	coverage.Sources = append(append([]analysis.Source(nil), coverage.Sources...), sources...)
	if coverage.Status == analysis.CoverageComplete {
		coverage.Status = analysis.CoveragePartial
	}
	return analysis.NormalizeCoverage(coverage)
}

// AddSymbolLevelImportCoverage applies the same rule to a graph's coverage, so
// --importers and blast-radius inherit it alongside --deps.
func (c *GraphCoverage) AddSymbolLevelImportCoverage(files []FileInfo) {
	if c == nil {
		return
	}
	counts := symbolLevelInventory(files)
	sources := symbolLevelSources(counts)
	if len(sources) == 0 {
		return
	}
	c.Sources = append(c.Sources, sources...)
	displays := make([]string, 0, len(counts))
	for display := range counts {
		displays = append(displays, display)
	}
	sort.Strings(displays)
	c.Notes = append(c.Notes, fmt.Sprintf("%s: %s", strings.Join(displays, ", "), symbolLevelCoverageNote))
	if c.Status == "" || c.Status == analysis.CoverageComplete {
		c.Status = analysis.CoveragePartial
	}
}
