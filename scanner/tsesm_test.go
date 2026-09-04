package scanner

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// Under ESM and NodeNext, TypeScript requires the specifier to name the
// emitted JavaScript file, so "./helper.js" appears in a project whose only
// on-disk counterpart is helper.ts. The specifier already carries an
// extension, so the extension-appending search never matched and most imports
// in such a project were lost.
func TestTypeScriptESMSpecifiersResolve(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/typescript-esm-specifiers", Filters{})
	if err != nil {
		t.Fatalf("build typescript fixture graph: %v", err)
	}

	for _, tc := range []struct {
		file string
		want []string
		why  string
	}{
		{"src/helper.ts", []string{"src/a_js_to_ts.ts", "src/d_extensionless.ts"}, "./helper.js and ./helper both reach helper.ts"},
		{"src/widget.tsx", []string{"src/b_jsx_to_tsx.ts"}, "./widget.jsx reaches widget.tsx"},
		// A real .js file must win over a same-named .ts: the specifier names
		// a file that exists, so rewriting it to TypeScript would be a wrong
		// edge rather than a missing one.
		{"src/real.js", []string{"src/c_real_js_wins.ts"}, "an existing .js beats the .ts counterpart"},
		{"src/real.ts", nil, "the .ts counterpart must not steal the edge"},
	} {
		got := append([]string(nil), graph.Importers[tc.file]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s importers = %v, want exactly %v (%s)", tc.file, got, tc.want, tc.why)
		}
	}
}

// A specifier naming a file that exists in neither form resolves to nothing.
func TestTypeScriptESMMissingSpecifierResolvesToNothing(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/typescript-esm-specifiers", Filters{})
	if err != nil {
		t.Fatalf("build typescript fixture graph: %v", err)
	}
	if got := graph.Imports["src/e_missing.ts"]; len(got) != 0 {
		t.Fatalf("src/e_missing.ts imports = %v, want none", got)
	}
}

func TestTypeScriptSourceCandidates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     string
		language string
		want     []string
	}{
		{"js maps to ts, tsx and a declaration", "src/helper.js", "typescript",
			[]string{"src/helper.ts", "src/helper.tsx", "src/helper.d.ts"}},
		{"jsx maps to tsx", "src/widget.jsx", "typescript", []string{"src/widget.tsx", "src/widget.d.ts"}},
		{"javascript importers get the same mapping", "src/helper.js", "javascript",
			[]string{"src/helper.ts", "src/helper.tsx", "src/helper.d.ts"}},
		{"uppercase extension still maps", "src/helper.JS", "typescript",
			[]string{"src/helper.ts", "src/helper.tsx", "src/helper.d.ts"}},
		// Anything without an emitted extension is the ordinary
		// extension-appending search's job, not this one's.
		{"extensionless is left alone", "src/helper", "typescript", nil},
		{"a ts specifier is left alone", "src/helper.ts", "typescript", nil},
		// mts and cts are not recognized source extensions, so mjs and cjs
		// deliberately map to nothing rather than to files that can never be
		// indexed.
		{"mjs has no reachable counterpart", "src/mod.mjs", "typescript", nil},
		{"cjs has no reachable counterpart", "src/mod.cjs", "typescript", nil},
		// Only JS-family importers use TypeScript emit semantics.
		{"go importers are left alone", "src/helper.js", "go", nil},
		{"python importers are left alone", "src/helper.js", "python", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := typescriptSourceCandidates(tc.path, tc.language)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("typescriptSourceCandidates(%q, %q) = %v, want %v", tc.path, tc.language, got, tc.want)
			}
		})
	}
}
