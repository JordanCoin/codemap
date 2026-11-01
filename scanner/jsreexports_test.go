package scanner

import (
	"context"
	"sort"
	"testing"
)

func jsFixtureImports(t *testing.T, fixture string) map[string][]string {
	t.Helper()
	graph, err := BuildFileGraph(context.Background(), "../testdata/"+fixture, Filters{})
	if err != nil {
		t.Fatalf("build %s graph: %v", fixture, err)
	}
	return graph.Imports
}

func assertImports(t *testing.T, imports map[string][]string, file string, want ...string) {
	t.Helper()
	got := append([]string(nil), imports[file]...)
	sort.Strings(got)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("%s imports = %v, want exactly %v", file, got, want)
	}
}

// The not-a-source strings are exported values: a rule matching every
// export_statement would read them as paths.
func TestReExportsAreEdges(t *testing.T) {
	imports := jsFixtureImports(t, "js-reexports")
	assertImports(t, imports, "src/barrel.ts",
		"src/lib/named.ts", "src/lib/star.ts", "src/lib/namespaced.ts", "src/lib/types.ts", "src/lib/commented.ts")
	assertImports(t, imports, "src/barrel.js", "src/lib/js-named.js", "src/lib/js-star.js")
	assertImports(t, imports, "src/re.jsx", "src/lib/jsx-star.js")
}

// Non-literal arguments have to add nothing, and a string inside a comment
// must not stand in for the path.
func TestDynamicImportLiteralsAreEdges(t *testing.T) {
	imports := jsFixtureImports(t, "js-reexports")
	assertImports(t, imports, "src/lazy.ts",
		"src/lib/dyn-single.ts", "src/lib/dyn-double.ts", "src/lib/dyn-tick.ts", "src/lib/dyn-hinted.ts", "src/lib/dyn-opts.ts")
	assertImports(t, imports, "src/lazy.jsx", "src/lib/js-dyn.js")
	assertImports(t, imports, "src/view.tsx", "src/lib/tsx-named.ts", "src/lib/tsx-dyn.ts")
}
