package scanner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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
	if len(outcome.Sources) != 1 || outcome.Sources[0].Source != "go-parser" || outcome.Sources[0].Status != ScanSourceFallback {
		t.Fatalf("sources = %#v, want Go parser fallback", outcome.Sources)
	}
	if !strings.Contains(outcome.Sources[0].Detail, "2 of 3 Go files") || !strings.Contains(outcome.Sources[0].Detail, "skipped 1") {
		t.Fatalf("detail = %q, want recovered and skipped counts", outcome.Sources[0].Detail)
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
