package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBuildGoFallbackOutcome(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"cmd/main.go": `package main

import (
	alias "example.com/lib"
	_ "example.com/side-effect"
)

func main() {}

type worker struct{}

func (worker) Run() {}
`,
		"cmd/main_test.go": `package main

import "testing"

func TestFeature(t *testing.T) {}
`,
		"cmd/broken.go": "package main\nfunc broken(",
		"notes.txt":     "not Go",
	})
	files := []FileInfo{
		{Path: filepath.FromSlash("cmd/main_test.go")},
		{Path: filepath.FromSlash("notes.txt")},
		{Path: filepath.FromSlash("cmd/broken.go")},
		{Path: filepath.FromSlash("cmd/main.go")},
	}

	outcome, err := buildGoFallbackOutcome(context.Background(), root, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileAnalysis{
		{
			Path:      filepath.FromSlash("cmd/main.go"),
			Language:  "go",
			Functions: []string{"main", "Run"},
			Imports:   []string{"example.com/lib", "example.com/side-effect"},
		},
		{
			Path:      filepath.FromSlash("cmd/main_test.go"),
			Language:  "go",
			Functions: []string{"TestFeature"},
			Imports:   []string{"testing"},
		},
	}
	if !reflect.DeepEqual(outcome.Analyses, want) {
		t.Fatalf("analyses = %#v, want %#v", outcome.Analyses, want)
	}
	if len(outcome.Sources) != 1 || outcome.Sources[0].Name != "go-parser" || outcome.Sources[0].Status != ScanSourceFallback {
		t.Fatalf("sources = %#v, want Go parser fallback", outcome.Sources)
	}
	if !strings.Contains(outcome.Sources[0].Detail, "2 of 3 Go files") || !strings.Contains(outcome.Sources[0].Detail, "skipped 1") {
		t.Fatalf("detail = %q, want recovered and skipped counts", outcome.Sources[0].Detail)
	}
}

func TestScanForDepsOutcomeUsesGoFallbackWithoutAstGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "sg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"cmd/main.go": "package main\n\nimport \"example.com/lib\"\n\nfunc main() {}\n",
	})

	outcome, err := ScanForDeps(context.Background(), root, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []FileAnalysis{{
		Path:      filepath.FromSlash("cmd/main.go"),
		Language:  "go",
		Functions: []string{"main"},
		Imports:   []string{"example.com/lib"},
	}}
	if !reflect.DeepEqual(outcome.Analyses, want) {
		t.Fatalf("analyses = %#v, want %#v", outcome.Analyses, want)
	}
	if len(outcome.Sources) != 2 ||
		outcome.Sources[0].Name != "ast-grep" ||
		outcome.Sources[0].Status != ScanSourceUnavailable ||
		outcome.Sources[1].Name != "go-parser" ||
		outcome.Sources[1].Status != ScanSourceFallback {
		t.Fatalf("sources = %#v, want unavailable ast-grep followed by Go parser fallback", outcome.Sources)
	}
}

func TestBuildGoFallbackOutcomeUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files map[string]string
		list  []FileInfo
	}{
		{
			name:  "no Go files",
			files: map[string]string{"main.rs": "fn main() {}"},
			list:  []FileInfo{{Path: "main.rs"}},
		},
		{
			name:  "no parseable Go files",
			files: map[string]string{"broken.go": "package main\nfunc broken("},
			list:  []FileInfo{{Path: "broken.go"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeRustCargoFixture(t, root, tt.files)
			_, err := buildGoFallbackOutcome(context.Background(), root, tt.list)
			if !errors.Is(err, errGoFallbackUnavailable) {
				t.Fatalf("error = %v, want %v", err, errGoFallbackUnavailable)
			}
		})
	}
}

func TestBuildGoFallbackOutcomeHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{"main.go": "package main\nfunc main() {}\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := buildGoFallbackOutcome(ctx, root, []FileInfo{{Path: "main.go"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}
