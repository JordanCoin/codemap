package scanner

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// Edges are appended in analysis order, which the scanner does not fix, so the
// same repository scanned twice produced the same edges in a different
// sequence. That made --importers output shift between identical runs and made
// an exact-list assertion impossible for any caller.
func TestFileGraphEdgeOrderIsDeterministic(t *testing.T) {
	const runs = 8
	var first *FileGraph
	for run := 0; run < runs; run++ {
		graph, err := BuildFileGraph(context.Background(), "../testdata/deterministic-edges", Filters{})
		if err != nil {
			t.Fatalf("run %d: build graph: %v", run, err)
		}
		if first == nil {
			first = graph
			continue
		}
		if !reflect.DeepEqual(graph.Importers, first.Importers) {
			t.Fatalf("run %d importers = %v, want the same order as run 0 %v", run, graph.Importers, first.Importers)
		}
		if !reflect.DeepEqual(graph.Imports, first.Imports) {
			t.Fatalf("run %d imports = %v, want the same order as run 0 %v", run, graph.Imports, first.Imports)
		}
		// Imports keep resolution order, so they must stay stable without
		// being sorted — the CUE resolver's selected-package-first ordering
		// depends on it.
	}

	// Sorted, not merely stable: a caller asserting an exact list needs to
	// know which order it will get.
	got := first.Importers["lib/shared.ts"]
	want := append([]string(nil), got...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("importers = %v, want them sorted %v", got, want)
	}
	if len(got) != 4 {
		t.Fatalf("importers = %v, want all four importers of lib/shared.ts", got)
	}
}

func TestSortEdgesHandlesNilGraph(t *testing.T) {
	var graph *FileGraph
	graph.sortEdges() // must not panic
}
