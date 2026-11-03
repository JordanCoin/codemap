package scanner

import (
	"context"
	"reflect"
	"testing"

	"codemap/analysis"
)

// Review findings on #272: each is a way the member expansion could drop a
// name, misreport coverage, double count impact, or invent an edge.
func TestPythonFromImportMemberRegressions(t *testing.T) {
	t.Run("backslash continuation keeps every name", func(t *testing.T) {
		got := pythonFromImportMembers("python", "ns", "from ns import a, \\\n    c")
		if want := []string{"ns.a", "ns.c"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("members = %v, want %v", got, want)
		}
		if got := pythonFromImportMembers("python", "ns", "from ns import a, 1bad"); got != nil {
			t.Fatalf("an unreadable name must fall back to the whole module, got %v", got)
		}
		graph := falseEdgeGraph(t, map[string]string{
			"ns/a.py": "", "ns/b.py": "", "ns/c.py": "",
			"user.py": "from ns import a, \\\n    c\n",
		})
		assertImports(t, graph, "user.py", "ns/a.py", "ns/c.py")
	})

	t.Run("namespace packages resolve without a file edge", func(t *testing.T) {
		g, err := BuildFileGraph(context.Background(), writeRepo(t, map[string]string{
			"plugins/x.py":  "",
			"ns/m.py":       "",
			"deep/a/b/c.py": "",
			"app.py":        "import plugins\nfrom ns import *\nfrom deep.a import b\n",
		}), Filters{})
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Coverage.EffectiveStatus(); got == analysis.CoveragePartial {
			t.Fatalf("coverage = partial, want complete (notes = %v)", g.Coverage.Notes)
		}
		if got := g.Imports["app.py"]; len(got) != 0 {
			t.Fatalf("app.py imports = %v, want none", got)
		}
	})

	t.Run("diff impact counts an importer once", func(t *testing.T) {
		changed := []FileInfo{{Path: "pkg/a1.py"}}
		analyses := []FileAnalysis{{Path: "user.py", Imports: []string{"pkg.a1", "pkg.a2", "pkg.a3", "pkg.a4", "pkg.a5", "pkg.a6"}}}
		got := AnalyzeImpactFromAnalyses(changed, analyses)
		if want := []ImpactInfo{{File: "pkg", UsedBy: 1}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("impact = %+v, want %+v", got, want)
		}
	})

	t.Run("stdlib package does not take a project suffix match", func(t *testing.T) {
		graph := falseEdgeGraph(t, map[string]string{
			"tools/json/decoder.py": "",
			"app/__init__.py":       "",
			"app/u.py":              "from json import decoder\n",
		})
		assertImports(t, graph, "app/u.py")
	})
}
