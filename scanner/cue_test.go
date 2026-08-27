package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCueImportsHandlesAliasesCommentsAndBlocks(t *testing.T) {
	data := []byte(`package app

// import "ignored"
import "example.com/acme/types"
import alias "example.com/acme/alias"
import (
    "example.com/acme/core"
    other "example.com/acme/other"
)
message: "import \"not-an-import\""
`)
	want := []string{
		"example.com/acme/types",
		"example.com/acme/alias",
		"example.com/acme/core",
		"example.com/acme/other",
	}
	if got := cueImports(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("cueImports() = %v, want %v", got, want)
	}
}

func TestCueImportsStopAtFirstDeclaration(t *testing.T) {
	data := []byte(`package app
import "example.com/acme/real"
value: """
  import "example.com/acme/fake"
"""
snippet: ''' import "example.com/acme/fake2" '''
`)
	want := []string{"example.com/acme/real"}
	if got := cueImports(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("cueImports() = %v, want %v", got, want)
	}
}

func TestCueImportsSkipsMalformedStrings(t *testing.T) {
	data := []byte("import \"\\xZZ\"\nimport \"example.com/acme/ok\"\n")
	want := []string{"example.com/acme/ok"}
	if got := cueImports(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("cueImports() = %v, want %v", got, want)
	}
}

func TestScanCUEFilesReturnsEmptyForNonCUERepo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("not CUE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := scanCUEFiles(context.Background(), root, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Analyses != nil || outcome.Sources != nil {
		t.Fatalf("non-CUE repository returned an outcome: %+v", outcome)
	}
}

func TestScanCUEFilesHonorsFilters(t *testing.T) {
	root := t.TempDir()
	writeCueFile(t, root, "keep.cue", "package keep\n")
	writeCueFile(t, root, "vendor/drop.cue", "package drop\n")

	outcome, err := scanCUEFiles(context.Background(), root, Filters{Exclude: []string{"vendor"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 1 || outcome.Analyses[0].Path != "keep.cue" {
		t.Fatalf("filtered CUE analyses = %#v, want keep.cue only", outcome.Analyses)
	}
}

func TestScanCUEFilesReadsAllPackagesAndModule(t *testing.T) {
	root := t.TempDir()
	writeCueFile(t, root, "cue.mod/module.cue", "module: \"example.com/acme\"\n")
	writeCueFile(t, root, "main.cue", "package app\nimport \"example.com/acme/types\"\n")
	writeCueFile(t, root, "types/types.cue", "package types\nvalue: string\n")

	outcome, err := scanCUEFiles(context.Background(), root, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if got := detectCUEModule(root); got != "example.com/acme" {
		t.Fatalf("detectCUEModule() = %q", got)
	}
	if len(outcome.Analyses) != 3 || outcome.Sources[0].Name != "cue-imports" {
		t.Fatalf("unexpected CUE outcome: %+v", outcome)
	}
	for _, analysis := range outcome.Analyses {
		if analysis.Path == "main.cue" && analysis.Package != "app" {
			t.Fatalf("main package = %q, want app", analysis.Package)
		}
	}
}

func TestCueImportResolvesOnlyLocalModulePackage(t *testing.T) {
	idx := buildFileIndex([]FileInfo{
		{Path: "main.cue"},
		{Path: filepath.Join("types", "types.cue")},
		{Path: filepath.Join("other", "other.cue")},
	}, "")
	idx.cueModules = []cueModuleInfo{{path: "example.com/acme"}}
	idx.cuePackages = map[string]string{"types/types.cue": "types"}

	if got := fuzzyResolve("example.com/acme/types", "main.cue", idx, "", nil, ""); !reflect.DeepEqual(got, []string{filepath.Join("types", "types.cue")}) {
		t.Fatalf("local CUE import resolved to %v", got)
	}
	if got := fuzzyResolve("example.com/other/types", "main.cue", idx, "", nil, ""); len(got) != 0 {
		t.Fatalf("external CUE import resolved to %v", got)
	}
}

func TestCueImportResolvesNearestNestedModule(t *testing.T) {
	idx := buildFileIndex([]FileInfo{
		{Path: "outer/types/types.cue"},
		{Path: "inner/types/types.cue"},
		{Path: "inner/main.cue"},
	}, "")
	idx.cueModules = []cueModuleInfo{
		{path: "example.com/inner", root: "inner"},
		{path: "example.com/outer", root: ""},
	}
	idx.cuePackages = map[string]string{
		"outer/types/types.cue": "types",
		"inner/types/types.cue": "types",
	}
	if got := fuzzyResolve("example.com/outer/types", "inner/main.cue", idx, "", nil, ""); len(got) != 0 {
		t.Fatalf("nested CUE file crossed module boundary: %v", got)
	}
	if got := fuzzyResolve("example.com/inner/types", "inner/main.cue", idx, "", nil, ""); !reflect.DeepEqual(got, []string{"inner/types/types.cue"}) {
		t.Fatalf("nested CUE import resolved to %v", got)
	}
}

func TestCueImportFiltersPackageSelector(t *testing.T) {
	root := t.TempDir()
	writeCueFile(t, root, "cue.mod/module.cue", "module: \"example.com/acme@v0\"\n")
	writeCueFile(t, root, "main.cue", "package app\n")
	writeCueFile(t, root, "templates/one.cue", "package one\n")
	writeCueFile(t, root, "templates/default.cue", "package templates\n")
	writeCueFile(t, root, "templates/two.cue", "package two\n")

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path: "main.cue", Language: "cue", Package: "app", Imports: []string{
			"example.com/acme/templates:one", "example.com/acme/templates",
		},
	}}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"templates/one.cue", "templates/default.cue"}
	if got := graph.Imports["main.cue"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected CUE package = %v, want %v", got, want)
	}
}

func TestBuildFileGraphResolvesCUEPackageFromModule(t *testing.T) {
	root := t.TempDir()
	writeCueFile(t, root, "cue.mod/module.cue", "module: \"example.com/acme\"\n")
	writeCueFile(t, root, "main.cue", "package app\n")
	writeCueFile(t, root, "types/types.cue", "package types\n")

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path:     "main.cue",
		Language: "cue",
		Imports:  []string{"example.com/acme/types"},
	}}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("types", "types.cue")}
	if got := graph.Imports["main.cue"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("CUE graph imports = %v, want %v", got, want)
	}
}

func TestBuildFileGraphResolvesNestedCUEPackageFromModule(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join("deploy", "timoni", "modules", "hilfe")
	writeCueFile(t, root, filepath.Join(moduleRoot, "cue.mod", "module.cue"), "module: \"timoni.sh/hilfe\"\n")
	writeCueFile(t, root, filepath.Join(moduleRoot, "timoni.cue"), "package app\n")
	writeCueFile(t, root, filepath.Join(moduleRoot, "templates", "admin.cue"), "package templates\n")

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path:     filepath.Join(moduleRoot, "timoni.cue"),
		Language: "cue",
		Imports:  []string{"timoni.sh/hilfe/templates"},
	}}, Filters{Only: []string{"cue"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(moduleRoot, "templates", "admin.cue")}
	if got := graph.Imports[filepath.Join(moduleRoot, "timoni.cue")]; !reflect.DeepEqual(got, want) {
		t.Fatalf("nested CUE graph imports = %v, want %v", got, want)
	}
}

func writeCueFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
