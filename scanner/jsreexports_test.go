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

func assertJSImports(t *testing.T, imports map[string][]string, file string, want ...string) {
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
	assertJSImports(t, imports, "src/barrel.ts",
		"src/lib/named.ts", "src/lib/star.ts", "src/lib/namespaced.ts", "src/lib/types.ts", "src/lib/commented.ts")
	assertJSImports(t, imports, "src/barrel.js", "src/lib/js-named.js", "src/lib/js-star.js")
	assertJSImports(t, imports, "src/re.jsx", "src/lib/jsx-star.js")
}

// Non-literal arguments have to add nothing, and a string inside a comment
// must not stand in for the path.
func TestDynamicImportLiteralsAreEdges(t *testing.T) {
	imports := jsFixtureImports(t, "js-reexports")
	assertJSImports(t, imports, "src/lazy.ts",
		"src/lib/dyn-single.ts", "src/lib/dyn-double.ts", "src/lib/dyn-tick.ts", "src/lib/dyn-hinted.ts", "src/lib/dyn-opts.ts")
	assertJSImports(t, imports, "src/lazy.jsx", "src/lib/js-dyn.js")
	assertJSImports(t, imports, "src/view.tsx", "src/lib/tsx-named.ts", "src/lib/tsx-dyn.ts")
}

func TestRepoRootDirectoryImportResolvesToIndex(t *testing.T) {
	imports := jsFixtureImports(t, "js-root-import")
	for _, file := range []string{"lib/deep/slash.js", "lib/deep/noslash.js", "lib/oneup.js", "lib/oneup-noslash.js", "dotslash.js", "dot.js"} {
		assertJSImports(t, imports, file, "index.js")
	}
	// Above the scan root is not the root.
	assertJSImports(t, imports, "above.js")
	assertJSImports(t, imports, "above-noslash.js")
}
