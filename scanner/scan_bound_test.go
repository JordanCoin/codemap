package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/analysis"
)

func writeChainRepo(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("import { x } from './f%d';\nexport const x = %d;\n", (i+1)%n, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.ts", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A repository above the file bound used to be walked and graphed in full,
// and a machine running two such scans ran out of memory (#186). The walk
// now keeps the first N files and reports the whole count.
func TestScanFilesBoundedStopsAtLimit(t *testing.T) {
	dir := writeChainRepo(t, 5)
	files, total, err := ScanFilesBounded(context.Background(), dir, nil, Filters{MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || total != 5 {
		t.Fatalf("files = %d, total = %d, want 3 of 5", len(files), total)
	}
	files, total, err = ScanFilesBounded(context.Background(), dir, nil, Filters{MaxFiles: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 || total != 5 {
		t.Fatalf("unbounded: files = %d, total = %d, want 5 of 5", len(files), total)
	}
}

// Above the bound the dependency scan is not attempted at all, so no graph is
// allocated, and every consumer of the coverage line sees why.
func TestScanForDepsRefusesAboveBound(t *testing.T) {
	dir := writeChainRepo(t, 3)
	outcome, err := ScanForDeps(context.Background(), dir, Filters{MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 0 {
		t.Fatalf("analyses = %d, want none above the bound", len(outcome.Analyses))
	}
	coverage := CoverageFromSources(outcome.Sources)
	if coverage.Status != analysis.CoverageUnavailable {
		t.Fatalf("status = %v, want unavailable (sources %v)", coverage.Status, outcome.Sources)
	}
	want := "bounded: scanned 0 of 3 files (limit 2; raise with --max-files or config max_files)"
	found := false
	for _, source := range outcome.Sources {
		if source.Name == "scan-bound" && source.Detail == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("sources = %v, want scan-bound with detail %q", outcome.Sources, want)
	}

	graph, err := BuildFileGraph(context.Background(), dir, Filters{MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Imports) != 0 || graph.Coverage.EffectiveStatus() != analysis.CoverageUnavailable || !strings.Contains(strings.Join(graph.Coverage.Notes, "\n"), "bounded: scanned 0 of 3 files") {
		t.Fatalf("graph above bound: imports = %d, status = %v, notes = %v", len(graph.Imports), graph.Coverage.EffectiveStatus(), graph.Coverage.Notes)
	}

	// Under the bound nothing changes.
	outcome, err = ScanForDeps(context.Background(), dir, Filters{MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 3 || CoverageFromSources(outcome.Sources).Status != analysis.CoverageComplete {
		t.Fatalf("at bound: analyses = %d, status = %v", len(outcome.Analyses), CoverageFromSources(outcome.Sources).Status)
	}
}
