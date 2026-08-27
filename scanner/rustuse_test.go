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

func TestExpandRustUseReferencePathsAllowsBareGroupsOnly(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{
			path: "app_core::{alpha::Thing, beta as renamed}",
			want: []string{"app_core::alpha::Thing", "app_core::beta"},
		},
		{path: "app_core::alpha::Thing"},
		{path: "app_core::{alpha, beta"},
	}

	for _, tt := range tests {
		if got := expandRustUseReferencePaths(tt.path); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("expandRustUseReferencePaths(%q) = %#v, want %#v", tt.path, got, tt.want)
		}
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
	source := "use crate::{alpha::Thing, beta as renamed};\nuse dependency::{External, Other};\n"
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
		{Path: "dependency::{External, Other}", Kind: "rust-use"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("whole Rust use references = %#v, want %#v", got, want)
	}
}

func TestRustUseReferencesResolveCurrentPackageLibraryFromBinary(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":   "[package]\nname = \"app-core\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":   "pub mod alpha;\npub mod beta;\npub mod gamma;\n",
		"src/alpha.rs": "pub struct Thing;\n",
		"src/beta.rs":  "pub fn run() {}\n",
		"src/gamma.rs": "pub struct Thing;\n",
		"src/main.rs":  "use app_core::{alpha::Thing, beta as renamed};\nuse app_core::gamma::Thing as Gamma;\n",
		"build.rs":     "use app_core::{alpha::Thing, beta as renamed};\nuse app_core::gamma::Thing as Gamma;\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, ".", "app-core", []map[string]any{
			cargoTargetJSON(root, "src/lib.rs", "app_core", rustTargetLib),
			cargoTargetJSON(root, "src/main.rs", "app", rustTargetBin),
			cargoTargetJSON(root, "build.rs", "build-script-build", rustTargetCustomBuild),
		}, nil),
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "alpha", Kind: "rust-module"},
			{Path: "beta", Kind: "rust-module"},
			{Path: "gamma", Kind: "rust-module"},
		}},
		{Path: "src/alpha.rs", Language: "rust"},
		{Path: "src/beta.rs", Language: "rust"},
		{Path: "src/gamma.rs", Language: "rust"},
		{Path: "src/main.rs", Language: "rust", References: []ImportReference{
			{Path: "app_core::{alpha::Thing, beta as renamed}", Kind: "rust-use"},
			{Path: "app_core::gamma::Thing", Kind: "rust-path"},
		}},
		{Path: "build.rs", Language: "rust", References: []ImportReference{
			{Path: "app_core::{alpha::Thing, beta as renamed}", Kind: "rust-use"},
			{Path: "app_core::gamma::Thing", Kind: "rust-path"},
		}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return metadata, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), graph.Imports["src/main.rs"]...)
	sort.Strings(got)
	want := []string{"src/alpha.rs", "src/beta.rs", "src/gamma.rs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("current-package imports = %#v, want %#v", got, want)
	}
	if got := graph.Imports["build.rs"]; len(got) != 0 {
		t.Fatalf("build-script current-package imports = %#v, want none", got)
	}
	graph, err = buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("cargo unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["src/main.rs"]; len(got) != 0 {
		t.Fatalf("fallback current-package imports = %#v, want none", got)
	}
}

func TestRustPathMetadataFailurePreservesExternalFallback(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"app\", \"core\"]\n",
		"app/Cargo.toml":  "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"app/src/lib.rs":  "use core_lib::api::Thing;\n",
		"core/Cargo.toml": "[package]\nname = \"core-lib\"\nversion = \"0.1.0\"\n",
		"core/src/lib.rs": "pub mod api;\n",
		"core/src/api.rs": "pub struct Thing;\n",
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "core_lib::api::Thing", Kind: "rust-path"},
			{Path: "single", Kind: "rust-path"},
		}},
		{Path: "core/src/lib.rs", Language: "rust", References: []ImportReference{{
			Path: "api", Kind: "rust-module",
		}}},
		{Path: "core/src/api.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("cargo unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["app/src/lib.rs"], []string{"core/src/api.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback external imports = %#v, want %#v", got, want)
	}
}

func TestRustUseReferencesResolveOnlyOneLocalDependencyAlias(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":        "[workspace]\n",
		"app/Cargo.toml":    "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"app/src/lib.rs":    "use facade::{api::Thing, util as renamed};\nuse serde::{Serialize, Deserialize};\nuse shared::{api::Other, api as duplicate};\n",
		"core/Cargo.toml":   "[package]\nname = \"core-lib\"\nversion = \"0.1.0\"\n",
		"core/src/lib.rs":   "pub mod api;\npub mod util;\n",
		"core/src/api.rs":   "pub struct Thing;\n",
		"core/src/util.rs":  "pub fn run() {}\n",
		"first/Cargo.toml":  "[package]\nname = \"first\"\nversion = \"0.1.0\"\n",
		"first/src/lib.rs":  "pub mod api;\n",
		"first/src/api.rs":  "pub struct Other;\n",
		"second/Cargo.toml": "[package]\nname = \"second\"\nversion = \"0.1.0\"\n",
		"second/src/lib.rs": "pub mod api;\n",
		"second/src/api.rs": "pub struct Other;\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, "app", "app", "app", []map[string]any{
			{"name": "core-lib", "rename": "facade", "path": filepath.Join(root, "core"), "kind": nil},
			{"name": "first", "rename": "shared", "path": filepath.Join(root, "first"), "kind": nil},
			{"name": "second", "rename": "shared", "path": filepath.Join(root, "second"), "kind": nil},
		}),
		cargoPackage(root, "core", "core-lib", "core_lib", nil),
		cargoPackage(root, "first", "first", "first", nil),
		cargoPackage(root, "second", "second", "second", nil),
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "facade::{api::Thing, util as renamed}", Kind: "rust-use"},
			{Path: "serde::{Serialize, Deserialize}", Kind: "rust-use"},
			{Path: "shared::{api::Other, api as duplicate}", Kind: "rust-use"},
		}},
		{Path: "core/src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "api", Kind: "rust-module"},
			{Path: "util", Kind: "rust-module"},
		}},
		{Path: "core/src/api.rs", Language: "rust"},
		{Path: "core/src/util.rs", Language: "rust"},
		{Path: "first/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
		{Path: "second/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return metadata, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), graph.Imports["app/src/lib.rs"]...)
	sort.Strings(got)
	want := []string{"core/src/api.rs", "core/src/util.rs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local dependency grouped imports = %#v, want %#v", got, want)
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

func TestRustUseMalformedTreesDoNotEmitCrateRootEdge(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"malformed\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":    "mod caller;\n",
		"src/caller.rs": "use crate::{/* note */ alpha::Thing, beta};\nuse crate::{};\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{{Path: "caller", Kind: "rust-module"}}},
		{Path: "src/caller.rs", Language: "rust", References: []ImportReference{
			{Path: "crate::{/* note */ alpha::Thing, beta}", Kind: "rust-use"},
			{Path: "crate::{}", Kind: "rust-use"},
		}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("force manifest fallback")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["src/caller.rs"]; len(got) != 0 {
		t.Fatalf("malformed use trees resolved to %v, want none", got)
	}
}

func TestRustUseSuperInsideInlineModDoesNotCreateFalseEdge(t *testing.T) {
	astScanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(astScanner.Close)
	if !astScanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"foocrate\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":    "pub mod config;\npub mod foo;\npub mod control;\n",
		"src/config.rs": "pub fn value() -> i32 { 1 }\n",
		// foo's own `config` function shadows the sibling `config` module
		// from `tests`' point of view: `super::config` there means foo's fn,
		// not src/config.rs.
		"src/foo.rs": "pub fn config() -> i32 { 42 }\n\npub fn other() -> i32 { 7 }\n\n" +
			"#[cfg(test)]\nmod tests {\n    use super::{config, other};\n\n" +
			"    fn use_both() -> i32 { config() + other() }\n}\n",
		// Control: a plain top-level (non-nested) crate-rooted use still
		// resolves to the sibling module.
		"src/control.rs": "use crate::config;\n\npub fn call() -> i32 { config::value() }\n",
	})

	outcome, err := astScanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, ".", "foocrate", "foocrate", nil),
	})
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, outcome.Analyses, func(context.Context, string) ([]byte, error) {
		return metadata, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range graph.Imports["src/foo.rs"] {
		if target == "src/config.rs" {
			t.Fatalf("src/foo.rs imports = %#v, want no edge to src/config.rs (super::config inside mod tests is foo's own fn)", graph.Imports["src/foo.rs"])
		}
	}
	if got, want := graph.Imports["src/control.rs"], []string{"src/config.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("control top-level crate-rooted use = %#v, want %#v", got, want)
	}
}

func TestExpandRustUsePathsBoundedAtMaxDepth(t *testing.T) {
	if paths, ok := expandRustUseTree("a::{b}", "", maxRustUseTreeDepth+1); ok || paths != nil {
		t.Fatalf("expected depth bound to reject, got ok=%v paths=%v", ok, paths)
	}
	nested := "crate::a"
	for i := 0; i < 70; i++ {
		nested = "outer" + string(rune('a'+i%26)) + "::{" + nested + "}"
	}
	done := make(chan []string, 1)
	go func() { done <- expandRustUsePaths(nested) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expandRustUsePaths did not terminate on deep nesting")
	}
}
