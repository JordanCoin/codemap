package scanner

import (
	"context"
	"slices"
	"testing"

	"codemap/analysis"
)

// A tsconfig "*" catch-all makes every bare specifier alias-shaped, and a
// package declared in package.json is external by definition: openclaw
// reported 3,732 missing edges that were all npm dependencies.
func TestDeclaredPackagesAreNotUnresolvedProjectImports(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"tsconfig.json": `{"compilerOptions":{"paths":{"*":["./src/*"]}}}`,
		"package.json":  `{"name":"app","dependencies":{"@scope/pkg":"1.0.0"},"devDependencies":{"lodash":"4"}}`,
		"src/a.ts":      "import { p } from '@scope/pkg';\nimport _ from 'lodash';\nimport { m } from './missing';\nimport { b } from './b';\nexport const z = p + _ + m + b;\n",
		"src/b.ts":      "export const b = 1;\n",
	})
	g, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(g.Coverage.Issues); n != 1 || g.Coverage.Issues[0].Message != "import ./missing resolved to no file in the project" {
		t.Fatalf("issues = %+v, want exactly the ./missing one", g.Coverage.Issues)
	}
	if !hasNotePrefix(g.Coverage.Notes, "TypeScript: 1 of 2 project-relative imports did not resolve") {
		t.Fatalf("notes = %v", g.Coverage.Notes)
	}
}

// "import pkg.mod as alias" reached the resolver and the tally as
// "pkg.mod as alias": the edge was lost and the miss was reported with the
// alias text (docling, 3 false misses on a clean repo).
func TestPythonImportAsAliasResolves(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"pkg/__init__.py": "",
		"pkg/mod.py":      "x = 1\n",
		"pkg/a.py":        "import pkg.mod as alias\n",
	})
	g, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(g.Imports["pkg/a.py"], "pkg/mod.py") {
		t.Fatalf("imports = %v, want pkg/mod.py", g.Imports["pkg/a.py"])
	}
	if got := g.Coverage.EffectiveStatus(); got != analysis.CoverageComplete || len(g.Coverage.Issues) != 0 {
		t.Fatalf("coverage = %v issues = %+v, want complete and none", got, g.Coverage.Issues)
	}

	dir = writeRepo(t, map[string]string{
		"pkg/__init__.py": "",
		"pkg/a.py":        "import pkg.gone as g\n",
	})
	g, err = BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(g.Coverage.Issues); n != 1 || g.Coverage.Issues[0].Message != "import pkg.gone resolved to no file in the project" {
		t.Fatalf("issues = %+v, want one for pkg.gone without alias text", g.Coverage.Issues)
	}
}
