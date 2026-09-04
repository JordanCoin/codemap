package scanner

import (
	"context"
	"sort"
	"testing"
)

func sortedImporters(t *testing.T, graph *FileGraph, file string) []string {
	t.Helper()
	got := append([]string(nil), graph.Importers[file]...)
	sort.Strings(got)
	return got
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// The reported case: a CommonJS project written in the single quotes Prettier
// emits by default. js-imports matched only double-quoted literals, so every
// require in such a project was invisible and --importers answered a confident
// zero for a file with many requirers.
func TestCommonJSSingleQuoteImportersResolve(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/commonjs-single-quotes", Filters{})
	if err != nil {
		t.Fatalf("build commonjs fixture graph: %v", err)
	}

	for _, tc := range []struct {
		file string
		want []string
		why  string
	}{
		{"services/layoutInputService.js", []string{"routes/admin.js", "routes/members.js"}, "single-quoted require, both ../ and ./../ forms"},
		{"routes/members.js", []string{"app.js"}, "double-quoted require still resolves"},
		{"routes/admin.js", []string{"app.js"}, "single-quoted ESM import in a .js file"},
		{"app.js", nil, "entry point is imported by nothing"},
	} {
		got := sortedImporters(t, graph, tc.file)
		if !equalStrings(got, tc.want) {
			t.Errorf("%s importers = %v, want exactly %v (%s)", tc.file, got, tc.want, tc.why)
		}
	}
}

// A require whose argument is a variable cannot name a file. It has to resolve
// to nothing: a fabricated edge is worse than a missing one.
func TestDynamicRequireResolvesToNothing(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/commonjs-single-quotes", Filters{})
	if err != nil {
		t.Fatalf("build commonjs fixture graph: %v", err)
	}
	got := append([]string(nil), graph.Imports["app.js"]...)
	sort.Strings(got)
	want := []string{"routes/admin.js", "routes/members.js"}
	if !equalStrings(got, want) {
		t.Fatalf("app.js imports = %v, want exactly %v — require(which) must add no edge", got, want)
	}
}

// extractImportPath is what makes the structural rule quote-agnostic, since
// the rule binds no $PATH metavariable. Its fail-closed behavior on a
// non-literal argument is what keeps a dynamic require from inventing a path.
func TestExtractImportPathQuoteStyles(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"double-quoted require", `require("./routes/members")`, "./routes/members"},
		{"single-quoted require", `require('./routes/members')`, "./routes/members"},
		{"template-literal require", "require(`./routes/members`)", "./routes/members"},
		{"double-quoted esm import", `import x from "./mod";`, "./mod"},
		{"single-quoted esm import", `import x from './mod';`, "./mod"},
		{"bare esm import", `import './side-effect';`, "./side-effect"},
		{"dynamic require yields nothing", `require(someVariable)`, ""},
		{"computed require yields nothing", `require(base + '/x')`, "/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractImportPath(tc.text); got != tc.want {
				t.Fatalf("extractImportPath(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
