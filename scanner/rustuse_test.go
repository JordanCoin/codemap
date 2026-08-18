package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestExpandRustUsePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "flat aliases",
			path: "crate::{alpha::Thing, beta as renamed}",
			want: []string{"crate::alpha::Thing", "crate::beta"},
		},
		{
			name: "nested self and glob",
			path: "self::{child, nested::{self, Thing}, exports::*}",
			want: []string{"self::child", "self::nested", "self::nested::Thing", "self::exports"},
		},
		{
			name: "repeated super",
			path: "super::{super::shared, sibling as alias}",
			want: []string{"super::super::shared", "super::sibling"},
		},
		{
			name: "simple alias",
			path: "crate::alpha as renamed",
			want: []string{"crate::alpha"},
		},
		{
			name: "external tree stays partial",
			path: "dependency::{alpha, beta}",
		},
		{
			name: "malformed tree stays partial",
			path: "crate::{alpha, beta",
		},
		{
			name: "commented leaf stays partial",
			path: "crate::{/* note */ alpha, beta}",
		},
		{
			name: "empty group stays partial",
			path: "crate::{}",
		},
		{
			name: "empty path segment stays partial",
			path: "crate::{alpha::::Thing, beta}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandRustUsePaths(tt.path); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expandRustUsePaths(%q) = %#v, want %#v", tt.path, got, tt.want)
			}
		})
	}
}

func TestAstGrepRustUseTreeExtraction(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	source := "use crate::{alpha::Thing, beta as renamed};\nuse crate::{/* note */ alpha::Thing, beta};\nuse crate::{};\nuse dependency::{External, Other};\nuse crate::exports;\npub use crate::{alpha::Thing, beta};\npub use crate::re_export;\n"
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	astScanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := astScanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var got []ImportReference
	for _, analysis := range outcome.Analyses {
		for _, ref := range analysis.References {
			if ref.Kind == "rust-use" {
				ref.Line = 0
				got = append(got, ref)
			}
		}
	}
	want := []ImportReference{
		{Path: "crate::{alpha::Thing, beta as renamed}", Kind: "rust-use"},
		{Path: "crate::{/* note */ alpha::Thing, beta}", Kind: "rust-use"},
		{Path: "crate::{}", Kind: "rust-use"},
		{Path: "dependency::{External, Other}", Kind: "rust-use"},
		{Path: "crate::exports", Kind: "rust-use"},
		{Path: "crate::{alpha::Thing, beta}", Kind: "rust-use"},
		{Path: "crate::re_export", Kind: "rust-use"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("whole Rust use references = %#v, want %#v", got, want)
	}
}

func TestRustUseMalformedTreesDoNotEmitCrateRootEdge(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"malformed\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":    "mod caller;\n",
		"src/caller.rs": "use crate::{/* note */ alpha::Thing, beta};\nuse crate::{};\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "caller", Kind: "rust-module"},
		}},
		{Path: "src/caller.rs", Language: "rust", References: []ImportReference{
			{Path: "crate::{/* note */ alpha::Thing, beta}", Kind: "rust-use"},
			{Path: "crate::{}", Kind: "rust-use"},
		}},
	}
	loader := func(context.Context, string) ([]byte, error) {
		return nil, errors.New("force manifest fallback")
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, loader)
	if err != nil {
		t.Fatal(err)
	}
	got := graph.Imports["src/caller.rs"]
	for _, target := range got {
		if target == "src/lib.rs" {
			t.Fatalf("malformed use trees emitted a false crate-root edge: %v", got)
		}
	}
	if len(got) != 0 {
		t.Fatalf("malformed use trees resolved to %v, want none", got)
	}
}

func TestRustUseReferencesResolveGroupedLocalModules(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"grouped\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":    "mod alpha;\nmod beta;\nmod nested;\nmod caller;\n",
		"src/alpha.rs":  "pub struct Thing;\n",
		"src/beta.rs":   "pub fn run() {}\n",
		"src/nested.rs": "pub struct Leaf;\n",
		"src/caller.rs": "use crate::{alpha::Thing, beta as renamed, nested::{self, Leaf}};\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "alpha", Kind: "rust-module"},
			{Path: "beta", Kind: "rust-module"},
			{Path: "nested", Kind: "rust-module"},
			{Path: "caller", Kind: "rust-module"},
		}},
		{Path: "src/alpha.rs", Language: "rust"},
		{Path: "src/beta.rs", Language: "rust"},
		{Path: "src/nested.rs", Language: "rust"},
		{Path: "src/caller.rs", Language: "rust", References: []ImportReference{{
			Path: "crate::{alpha::Thing, beta as renamed, nested::{self, Leaf}}",
			Kind: "rust-use",
		}}},
	}
	loader := func(context.Context, string) ([]byte, error) {
		return nil, errors.New("force manifest fallback")
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, loader)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), graph.Imports["src/caller.rs"]...)
	sort.Strings(got)
	want := []string{"src/alpha.rs", "src/beta.rs", "src/nested.rs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped local imports = %#v, want %#v", got, want)
	}
}

func TestExpandRustUsePathsBoundedAtMaxDepth(t *testing.T) {
	// Past maxRustUseTreeDepth the expansion must reject instead of recursing.
	if paths, ok := expandRustUseTree("a::{b}", "", maxRustUseTreeDepth+1); ok || paths != nil {
		t.Fatalf("expected depth bound to reject, got ok=%v paths=%v", ok, paths)
	}
	// A pathological deeply nested use tree must terminate without a hang.
	nested := "crate::a"
	for i := 0; i < 70; i++ {
		nested = "outer" + string(rune('a'+i%26)) + "::{" + nested + "}"
	}
	done := make(chan []string, 1)
	go func() {
		done <- expandRustUsePaths(nested)
	}()
	select {
	case <-done:
		// Requirement is termination without panic or hang.
	case <-time.After(5 * time.Second):
		t.Fatal("expandRustUsePaths did not terminate on deep nesting")
	}
}
