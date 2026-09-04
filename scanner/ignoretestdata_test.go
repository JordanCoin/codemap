package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Fixture repositories under testdata are not the project's source. Scanning
// them inflates file counts and lets a fixture's language change the project's
// reported coverage — a Swift fixture made the whole project report partial.
// Go's own toolchain ignores the directory for the same reason.
func TestScanSkipsTestdataDirectories(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n\nfunc main() {}\n")
	write("testdata/fixture/main.go", "package main\n\nfunc main() {}\n")
	write("internal/testdata/nested/app.go", "package nested\n")

	files, err := ScanFiles(context.Background(), root, NewGitIgnoreCache(root), nil, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, file := range files {
		if file.Path != "main.go" {
			t.Errorf("scan returned %q, want only main.go — testdata must be skipped at any depth", file.Path)
		}
	}
	if len(files) != 1 {
		t.Fatalf("scan returned %d files, want 1", len(files))
	}
}

// A fixture is still scannable when it is itself the root, which is how the
// fixture tests in this package use them.
func TestTestdataFixtureScansWhenItIsTheRoot(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "testdata", "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ScanFiles(context.Background(), fixture, NewGitIgnoreCache(fixture), nil, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 || files[0].Path != "app.go" {
		t.Fatalf("scan of the fixture root returned %v, want [app.go]", files)
	}
}
