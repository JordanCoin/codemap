package scanner

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// Python spells a relative import as a run of dots counting package levels,
// not as path segments. Routing ".mod" through the JS-shaped resolver built
// "pkg/.mod", which matches nothing, so every intra-package edge in a Python
// project was lost and --importers answered a confident zero.
func TestPythonRelativeImportsResolve(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/python-relative-imports", Filters{})
	if err != nil {
		t.Fatalf("build python fixture graph: %v", err)
	}

	for _, tc := range []struct {
		file string
		want []string
		form string
	}{
		{"pkg/mod.py", []string{"pkg/a_dotted.py", "pkg/b_bare.py", "pkg/c_multi.py", "pkg/sub/d_parent.py"},
			"from .mod / from . import mod / aliased / from ..mod one level up"},
		{"pkg/second.py", []string{"pkg/c_multi.py"}, "second name in a multi-name import list"},
		{"pkg/sub/deep.py", []string{"pkg/e_dotted_path.py"}, "from .sub.deep, dots below the current package"},
		{"pkg/sub/__init__.py", []string{"pkg/f_package.py"}, "from .sub, a package rather than a module"},
	} {
		got := append([]string(nil), graph.Importers[tc.file]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s importers = %v, want exactly %v (%s)", tc.file, got, tc.want, tc.form)
		}
	}
}

// A relative import naming a module that does not exist has to resolve to
// nothing. Guessing at which file was meant would be a fabricated edge.
func TestPythonRelativeImportToMissingModuleResolvesToNothing(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/python-relative-imports", Filters{})
	if err != nil {
		t.Fatalf("build python fixture graph: %v", err)
	}
	if got := graph.Imports["pkg/g_missing.py"]; len(got) != 0 {
		t.Fatalf("pkg/g_missing.py imports = %v, want none", got)
	}
}

func TestPythonRelativeImportNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		text string
		want []string
	}{
		{"single name", ".", "from . import mod", []string{".mod"}},
		{"several names", ".", "from . import mod, second", []string{".mod", ".second"}},
		{"alias names the module, not the alias", ".", "from . import mod as aliased", []string{".mod"}},
		{"parent package", "..", "from .. import mod", []string{"..mod"}},
		{"parenthesised list", ".", "from . import (mod, second)", []string{".mod", ".second"}},
		{"star imports no module", ".", "from . import *", nil},
		{"trailing comment ignored", ".", "from . import mod  # keep", []string{".mod"}},
		// The path already names the module here, so the imported names are
		// symbols and must not be turned into modules.
		{"dotted path is left alone", ".mod", "from .mod import helper", nil},
		{"absolute import is left alone", "os.path", "import os.path", nil},
		{"non-python is left alone", ".", "from . import mod", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			language := "python"
			if tc.name == "non-python is left alone" {
				language = "javascript"
			}
			got := pythonRelativeImportNames(language, tc.path, tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pythonRelativeImportNames(%q, %q, %q) = %v, want %v", language, tc.path, tc.text, got, tc.want)
			}
		})
	}
}

func TestResolvePythonRelativeLevels(t *testing.T) {
	files := []FileInfo{
		{Path: "pkg/mod.py"},
		{Path: "pkg/sub/deep.py"},
		{Path: "pkg/sub/__init__.py"},
		{Path: "top.py"},
	}
	idx := buildFileIndex(files, "")

	for _, tc := range []struct {
		name    string
		imp     string
		fromDir string
		want    []string
	}{
		{"one dot is the current package", ".mod", "pkg", []string{"pkg/mod.py"}},
		{"two dots climb one package", "..mod", "pkg/sub", []string{"pkg/mod.py"}},
		{"dotted path descends", ".sub.deep", "pkg", []string{"pkg/sub/deep.py"}},
		{"package resolves to its __init__", ".sub", "pkg", []string{"pkg/sub/__init__.py"}},
		{"three dots from a nested package reach the root", "...top", "pkg/sub", []string{"top.py"}},
		{"bare dots resolve to nothing", ".", "pkg", nil},
		{"unknown module resolves to nothing", ".nope", "pkg", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolvePythonRelative(tc.imp, tc.fromDir, idx)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolvePythonRelative(%q, %q) = %v, want %v", tc.imp, tc.fromDir, got, tc.want)
			}
		})
	}
}
