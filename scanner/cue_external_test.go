package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codemap/scanner"
)

func TestBuildFileGraphFromAnalysesResolvesCUEPackage(t *testing.T) {
	root := t.TempDir()
	writeCUE(t, root, "cue.mod/module.cue", "module: \"example.com/acme\"\n")
	writeCUE(t, root, "main.cue", "package app\n")
	writeCUE(t, root, "types/types.cue", "package types\n")

	graph, err := scanner.BuildFileGraphFromAnalyses(context.Background(), root, []scanner.FileAnalysis{{
		Path:     "main.cue",
		Language: "cue",
		Imports:  []string{"example.com/acme/types"},
	}}, scanner.Filters{Only: []string{"cue"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["main.cue"], []string{filepath.Join("types", "types.cue")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("graph imports = %v, want %v", got, want)
	}
}

func TestBuildFileGraphScansCUEFromFileInventory(t *testing.T) {
	astScanner, err := scanner.NewAstGrepScanner()
	if err != nil || !astScanner.Available() {
		t.Skip("ast-grep not available")
	}
	astScanner.Close()

	root := t.TempDir()
	writeCUE(t, root, "cue.mod/module.cue", "module: \"example.com/acme\"\n")
	writeCUE(t, root, "main.cue", "package app\nimport \"example.com/acme/types\"\n")
	writeCUE(t, root, "types/types.cue", "package types\n")

	graph, err := scanner.BuildFileGraph(context.Background(), root, scanner.Filters{Only: []string{"cue"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["main.cue"], []string{filepath.Join("types", "types.cue")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("graph imports = %v, want %v", got, want)
	}
}

func writeCUE(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
