package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/analysis"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// An import that points into the project and resolves to nothing is a
// missing edge, and a missing edge must be declared: a graph that drops it
// and still says complete cannot be told apart from a checked, empty one (#183).
func TestUnresolvedProjectImportsAreDeclared(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		note  string
	}{
		"typescript alias and relative": {
			files: map[string]string{
				"tsconfig.json": `{"compilerOptions":{"paths":{"@app/*":["src/*"]}}}`,
				"src/a.ts":      "import { x } from '@app/missing';\nimport { y } from './gone';\nimport React from 'react';\nimport './style.css';\nexport const z = x + y;\n",
				"src/b.ts":      "export const b = 1;\n",
			},
			note: "TypeScript: 2 of 2 project-relative imports did not resolve to a file",
		},
		"javascript emitted mts": {
			files: map[string]string{
				"a.mjs":      "import { h } from './helper.mjs';\nimport _ from 'lodash';\n",
				"helper.mts": "export const h = 1;\n",
			},
			note: "JavaScript: 1 of 1 project-relative imports did not resolve to a file",
		},
		"python relative and package": {
			files: map[string]string{
				"pkg/__init__.py": "",
				"pkg/a.py":        "from .missing import x\nfrom pkg.gone import y\nfrom .b import ok\nimport requests\n",
				"pkg/b.py":        "ok = 1\n",
			},
			note: "Python: 2 of 3 project-relative imports did not resolve to a file",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := BuildFileGraph(context.Background(), writeRepo(t, c.files), Filters{})
			if err != nil {
				t.Fatal(err)
			}
			if got := g.Coverage.EffectiveStatus(); got != analysis.CoveragePartial {
				t.Fatalf("coverage = %v, want partial (notes = %v)", got, g.Coverage.Notes)
			}
			if !hasNotePrefix(g.Coverage.Notes, c.note) {
				t.Fatalf("notes = %v, want one starting %q", g.Coverage.Notes, c.note)
			}
		})
	}
}

func hasNotePrefix(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}
