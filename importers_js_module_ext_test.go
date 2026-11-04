package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codemap/scanner"
)

// #267: .cjs/.mjs/.cts/.mts are parsed, and resolve as import targets,
// including the TS emitted-extension form (./t.cjs naming t.cts).
func TestJSModuleExtensionsResolveBothDirections(t *testing.T) {
	root, err := filepath.Abs("testdata/js-module-extensions")
	if err != nil {
		t.Fatal(err)
	}
	fg, err := scanner.BuildFileGraph(context.Background(), root, scanner.Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraph: %v", err)
	}
	for _, edge := range []struct{ from, to string }{
		{"main.cjs", "lib.cjs"},
		{"a.mjs", "x.mjs"},
		{"u.mts", "t.cts"},
		{"v.cts", "m.mts"},
	} {
		if !slices.Contains(fg.Imports[edge.from], edge.to) {
			t.Errorf("Imports[%s] = %v, want %s", edge.from, fg.Imports[edge.from], edge.to)
		}
		if !slices.Contains(fg.Importers[edge.to], edge.from) {
			t.Errorf("Importers[%s] = %v, want %s", edge.to, fg.Importers[edge.to], edge.from)
		}
	}
}

// The issue's repro through the binary. A temp copy keeps an enclosing
// repo's .codemap/config.json "only" list out of the scan.
func TestImportersCommandListsCJSRequire(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("testdata/js-module-extensions")); err != nil {
		t.Fatal(err)
	}
	out, err := runCodemap("--importers", "lib.cjs", root)
	if err != nil {
		t.Fatalf("codemap --importers lib.cjs: %v\n%s", err, out)
	}
	if !strings.Contains(out, "• main.cjs") || strings.Contains(out, "no language mapping") {
		t.Fatalf("--importers lib.cjs must list main.cjs with no mapping caveat:\n%s", out)
	}
}
