package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// goMultiFilePackageFixture is the repro from #191: one module whose packages
// differ only in how many non-test files they hold.
const goMultiFilePackageFixture = "../testdata/go-multi-file-package"

// An import of a Go package links the importer to every non-test file of the
// package. On main before this test, a package with two or more files produced
// no edge at all, so --importers multi/a.go answered "No files import" with
// complete coverage (#191).
func TestGoMultiFilePackageImportsResolve(t *testing.T) {
	fg, err := BuildFileGraph(context.Background(), goMultiFilePackageFixture, Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraph: %v", err)
	}

	wantImporters := map[string][]string{
		"multi/a.go":       {"app/main.go"},
		"multi/b.go":       {"app/main.go"},
		"named/named.go":   {"app/main.go"},
		"named/other.go":   {"app/main.go"},
		"single/single.go": {"app/main.go"},
		"single2/impl.go":  {"app/main.go"},
		// Negative cases: nothing imports package orphan or package main, and
		// a _test.go file is never an import target.
		"orphan/orphan.go": nil,
		"app/main.go":      nil,
		"multi/a_test.go":  nil,
	}
	for file, want := range wantImporters {
		if got := fg.Importers[file]; !reflect.DeepEqual(got, want) {
			t.Errorf("Importers[%q] = %v, want %v", file, got, want)
		}
	}

	wantImports := []string{
		"multi/a.go", "multi/b.go", "named/named.go", "named/other.go",
		"single/single.go", "single2/impl.go",
	}
	gotImports := append([]string(nil), fg.Imports["app/main.go"]...)
	sort.Strings(gotImports)
	if !reflect.DeepEqual(gotImports, wantImports) {
		t.Errorf("Imports[app/main.go] = %v, want %v", gotImports, wantImports)
	}
	if fg.Coverage.Status != "" && fg.Coverage.Status != "complete" {
		t.Errorf("coverage = %q, want complete", fg.Coverage.Status)
	}

	// Hub status follows from package importers: one importer is below the
	// threshold either way, and the package attribution is what --importers
	// prints next to the count.
	pkg, files, ok := fg.GoPackage("multi/a.go")
	if !ok || pkg != "example.com/demo/multi" || len(files) != 2 {
		t.Errorf("GoPackage(multi/a.go) = %q, %v, %v; want example.com/demo/multi with 2 files", pkg, files, ok)
	}
	if pkg, files, ok := fg.GoPackage("multi/a_test.go"); !ok || pkg != "example.com/demo/multi" || len(files) != 2 {
		t.Errorf("GoPackage(multi/a_test.go) = %q, %v, %v; want the package it lives in", pkg, files, ok)
	}
	if _, _, ok := fg.GoPackage("go.mod"); ok {
		t.Errorf("GoPackage(go.mod) reported a package for a non-Go file")
	}
}

func TestCollapseGoPackageFiles(t *testing.T) {
	got := CollapseGoPackageFiles([]string{
		"analysis/contracts.go",
		"scanner/filegraph.go",
		"web/app.ts",
		"scanner/types.go",
		"scanner/walker.go",
		"config/config.go",
		"main.go",
		"root_options.go",
	})
	want := []string{
		"analysis/contracts.go",
		"scanner/ (3 files)",
		"web/app.ts",
		"config/config.go",
		"/ (2 files)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollapseGoPackageFiles = %v, want %v", got, want)
	}
	if got := CollapseGoPackageFiles(nil); len(got) != 0 {
		t.Fatalf("CollapseGoPackageFiles(nil) = %v, want empty", got)
	}
}

// goListPackage is the subset of `go list -json` this oracle reads.
type goListPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
	Module     *struct{ Path string }
}

func runGoList(t *testing.T, root string) []goListPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -json ./... in %s: %v\n%s", root, err, stderr.String())
	}
	var packages []goListPackage
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

// assertGraphCoversGoList checks that every cross-package import edge the Go
// toolchain reports between packages of this module has a file edge in fg:
// for each importing package P and imported package I, every non-test file of
// I is imported by at least one non-test file of P. Returns the number of
// package edges checked so a caller can refuse a vacuous pass.
func assertGraphCoversGoList(t *testing.T, root string, fg *FileGraph, packages []goListPackage) int {
	t.Helper()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	byImportPath := make(map[string]goListPackage, len(packages))
	for _, pkg := range packages {
		byImportPath[pkg.ImportPath] = pkg
	}
	relFiles := func(pkg goListPackage) []string {
		rel, err := filepath.Rel(absRoot, pkg.Dir)
		if err != nil {
			t.Fatalf("package %s dir %q is outside %q: %v", pkg.ImportPath, pkg.Dir, absRoot, err)
		}
		files := make([]string, 0, len(pkg.GoFiles))
		for _, f := range pkg.GoFiles {
			files = append(files, filepath.ToSlash(filepath.Join(rel, f)))
		}
		return files
	}
	edges := 0
	for _, pkg := range packages {
		if pkg.Module == nil {
			continue
		}
		module := pkg.Module.Path
		imported := make(map[string]bool)
		for _, f := range relFiles(pkg) {
			for _, target := range fg.Imports[f] {
				imported[filepath.ToSlash(target)] = true
			}
		}
		for _, imp := range pkg.Imports {
			if imp != module && !strings.HasPrefix(imp, module+"/") {
				continue
			}
			target, ok := byImportPath[imp]
			if !ok {
				continue
			}
			edges++
			for _, f := range relFiles(target) {
				if !imported[f] {
					t.Errorf("go list: package %s imports %s, but no file of %s imports %s in codemap's graph (its files import %d targets)",
						pkg.ImportPath, imp, pkg.ImportPath, f, len(imported))
				}
			}
		}
	}
	return edges
}

// TestGoGraphMatchesGoList is the truth oracle for Go resolution: the compiler
// knows every cross-package edge, and codemap's graph must contain each one.
// This is the check that would have caught #191 on day one.
func TestGoGraphMatchesGoList(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

	t.Run("fixture", func(t *testing.T) {
		fg, err := BuildFileGraph(context.Background(), goMultiFilePackageFixture, Filters{})
		if err != nil {
			t.Fatalf("BuildFileGraph: %v", err)
		}
		edges := assertGraphCoversGoList(t, goMultiFilePackageFixture, fg, runGoList(t, goMultiFilePackageFixture))
		if edges != 4 {
			t.Fatalf("checked %d package edges, want 4 (app/main.go imports four packages)", edges)
		}
	})

	t.Run("codemap", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping whole-repository oracle in -short mode")
		}
		root := ".."
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			t.Skipf("repository root not available: %v", err)
		}
		fg, err := BuildFileGraph(context.Background(), root, Filters{})
		if err != nil {
			t.Fatalf("BuildFileGraph: %v", err)
		}
		edges := assertGraphCoversGoList(t, root, fg, runGoList(t, root))
		if edges == 0 {
			t.Fatal("go list reported no in-module package edges for codemap; the oracle checked nothing")
		}
	})
}
