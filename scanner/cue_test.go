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

	outcome, err := scanCUEFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Analyses != nil || outcome.Sources != nil {
		t.Fatalf("non-CUE repository returned an outcome: %+v", outcome)
	}
}

func TestScanCUEFilesReadsAllPackagesAndModule(t *testing.T) {
	root := t.TempDir()
	writeCueFile(t, root, "cue.mod/module.cue", "module: \"example.com/acme\"\n")
	writeCueFile(t, root, "main.cue", "package app\nimport \"example.com/acme/types\"\n")
	writeCueFile(t, root, "types/types.cue", "package types\nvalue: string\n")

	outcome, err := scanCUEFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := detectCUEModule(root); got != "example.com/acme" {
		t.Fatalf("detectCUEModule() = %q", got)
	}
	if len(outcome.Analyses) != 3 || outcome.Sources[0].Name != "cue-imports" {
		t.Fatalf("unexpected CUE outcome: %+v", outcome)
	}
}

func TestCueImportResolvesOnlyLocalModulePackage(t *testing.T) {
	idx := buildFileIndex([]FileInfo{
		{Path: "main.cue"},
		{Path: filepath.Join("types", "types.cue")},
		{Path: filepath.Join("other", "other.cue")},
	}, "")
	idx.cueModule = "example.com/acme"

	if got := fuzzyResolve("example.com/acme/types", "main.cue", idx, "", nil, ""); !reflect.DeepEqual(got, []string{filepath.Join("types", "types.cue")}) {
		t.Fatalf("local CUE import resolved to %v", got)
	}
	if got := fuzzyResolve("example.com/other/types", "main.cue", idx, "", nil, ""); len(got) != 0 {
		t.Fatalf("external CUE import resolved to %v", got)
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
