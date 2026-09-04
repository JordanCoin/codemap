package scanner

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// create-next-app has shipped "@/*": ["./*"] for years, so this is the most
// common TypeScript layout in the wild. The substituted target was "./lib/a1"
// while the file index holds "lib/a1", so nothing matched and a file imported
// everywhere reported no importers at all.
func TestTsconfigDotSlashAliasResolvesImporters(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/tsconfig-alias-dotslash", Filters{})
	if err != nil {
		t.Fatalf("build alias fixture graph: %v", err)
	}
	// Compare as a set: the graph appends importers in analysis order, which
	// is not deterministic across runs (see the note in the PR for #173). The
	// exactness this asserts is membership, not sequence.
	got := append([]string(nil), graph.Importers["lib/a1.ts"]...)
	sort.Strings(got)
	want := []string{"app/layout.tsx", "app/page.tsx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lib/a1.ts importers = %v, want exactly %v", got, want)
	}
}

func TestNormalizeAliasTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		want   string
	}{
		{"create-next-app default", "./lib/a1", "lib/a1"},
		{"bare wildcard target", "lib/a1", "lib/a1"},
		{"nested with dot slash", "./src/lib/s1", "src/lib/s1"},
		{"redundant separators", "./src//lib/../lib/s1", "src/lib/s1"},
		{"bare dot collapses to root", ".", ""},
		{"dot slash only", "./", ""},
		{"empty stays empty", "", ""},
		// A target escaping the project must not be silently rewritten into a
		// path that could match an unrelated file.
		{"parent traversal preserved", "../shared/x", "../shared/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeAliasTarget(tc.target); got != tc.want {
				t.Fatalf("normalizeAliasTarget(%q) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}
}

// Every alias target shape has to keep resolving, so the "./" fix cannot
// regress the forms that already worked.
func TestPathAliasTargetShapes(t *testing.T) {
	files := []FileInfo{{Path: "lib/a1.ts"}, {Path: "src/lib/s1.ts"}, {Path: "app/page.tsx"}}
	idx := buildFileIndex(files, "")

	for _, tc := range []struct {
		name    string
		imp     string
		aliases map[string][]string
		baseURL string
		want    []string
	}{
		{"dot slash wildcard", "@/lib/a1", map[string][]string{"@/*": {"./*"}}, "", []string{"lib/a1.ts"}},
		{"bare wildcard", "@/lib/a1", map[string][]string{"@/*": {"*"}}, "", []string{"lib/a1.ts"}},
		{"nested dot slash", "@/lib/s1", map[string][]string{"@/*": {"./src/*"}}, "", []string{"src/lib/s1.ts"}},
		{"nested bare", "@/lib/s1", map[string][]string{"@/*": {"src/*"}}, "", []string{"src/lib/s1.ts"}},
		{"base url with dot slash", "@/lib/a1", map[string][]string{"@/*": {"./*"}}, ".", []string{"lib/a1.ts"}},
		{"exact alias with dot slash", "@app", map[string][]string{"@app": {"./lib/a1"}}, "", []string{"lib/a1.ts"}},
		{"unmatched alias resolves to nothing", "@/nope", map[string][]string{"@/*": {"./*"}}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolvePathAlias(tc.imp, tc.aliases, tc.baseURL, idx, "typescript")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolvePathAlias(%q, %v, baseURL=%q) = %v, want %v", tc.imp, tc.aliases, tc.baseURL, got, tc.want)
			}
		})
	}
}
