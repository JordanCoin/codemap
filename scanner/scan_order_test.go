package scanner

import (
	"context"
	"sort"
	"testing"
)

// blast-radius caps the deps section to the first N analyses, so the analyses
// order decides which files get counted. ast-grep emits matches in worker
// completion order, and the same repository scanned twice produced "298
// functions", then "286", then "239" for one diff (#153). The scan must hand
// back analyses in path order so every consumer sees the same first N.
func TestScanForDepsAnalysesSortedByPath(t *testing.T) {
	const runs = 6
	for run := 0; run < runs; run++ {
		outcome, err := ScanForDeps(context.Background(), "../testdata/deterministic-edges", Filters{})
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var paths []string
		for _, a := range outcome.Analyses {
			paths = append(paths, a.Path)
		}
		if !sort.StringsAreSorted(paths) {
			t.Fatalf("run %d analyses = %v, want path order", run, paths)
		}
	}
}
