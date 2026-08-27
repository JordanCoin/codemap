package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAstGrepRustBuildScriptInputExtraction(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	source := `fn main() {
    println!("cargo:rerun-if-changed=schema.proto");
    println!("cargo::rerun-if-changed=assets/default.policy");
    // println!("cargo:rerun-if-changed=commented.proto");
    /*
    println!("cargo:rerun-if-changed=blocked.proto");
    */
    println!("cargo:rerun-if-changed= schema.proto");
    println!("cargo:rerun-if-changed=schema.proto ");
    println!("cargo:rerun-if-changed={path}");
    println!("cargo:rerun-if-changed={}", "schema.proto");
    println!("cargo:rerun-if-changed=schema.proto\nother.proto");
    println!("plain output without directive");
}
const DOC: &str = r##"
println!("cargo:rerun-if-changed=documented.proto");
"##;
`
	if err := os.WriteFile(filepath.Join(root, "build.rs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	astScanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(astScanner.Close)
	outcome, err := astScanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	var got []ImportReference
	for _, analysis := range outcome.Analyses {
		for _, ref := range analysis.References {
			if ref.Kind == "rust-build-input" {
				ref.Line = 0
				got = append(got, ref)
			}
		}
	}
	for _, analysis := range outcome.Analyses {
		for _, ref := range analysis.References {
			if ref.Kind == "rust-build-input" && len(analysis.Imports) != 0 {
				t.Fatalf("directive paths leaked into imports: %v", analysis.Imports)
			}
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []ImportReference{
		{Path: "assets/default.policy", Kind: "rust-build-input"},
		{Path: "schema.proto", Kind: "rust-build-input"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Cargo build input references = %#v, want %#v", got, want)
	}
}

func TestRustBuildScriptResolvesStaticCargoInputs(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "outside.proto")
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":                "[workspace]\nmembers = [\"app\"]\n",
		"outside.proto":             "syntax = \"proto3\";\n",
		"app/Cargo.toml":            "[package]\nname = \"app\"\nversion = \"0.1.0\"\nbuild = \"build.rs\"\n",
		"app/build.rs":              "fn main() {}\n",
		"app/schema.proto":          "syntax = \"proto3\";\n",
		"app/assets/default.policy": "allow if true\n",
	})

	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, "app", "app", []map[string]any{
			cargoTargetJSON(root, "app/build.rs", "build-script-build", rustTargetCustomBuild),
		}, nil),
	})
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(
		context.Background(),
		root,
		[]FileAnalysis{{Path: "app/build.rs", Language: "rust", References: []ImportReference{
			{Path: "schema.proto", Kind: "rust-build-input"},
			{Path: "assets/default.policy", Kind: "rust-build-input"},
			{Path: "assets", Kind: "rust-build-input"},
			{Path: "../outside.proto", Kind: "rust-build-input"},
			{Path: "missing.proto", Kind: "rust-build-input"},
			{Path: "*.proto", Kind: "rust-build-input"},
			{Path: absolute, Kind: "rust-build-input"},
			{Path: "build.rs", Kind: "rust-build-input"},
		}}},
		func(context.Context, string) ([]byte, error) { return metadata, nil },
	)
	if err != nil {
		t.Fatal(err)
	}

	got := append([]string(nil), graph.Imports["app/build.rs"]...)
	for i := range got {
		got[i] = filepath.ToSlash(got[i])
	}
	sort.Strings(got)
	want := []string{"app/assets/default.policy", "app/schema.proto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("build script inputs = %#v, want %#v", got, want)
	}
}

func TestRustBuildScriptResolvesTargetWithExtensionSibling(t *testing.T) {
	// idx.byExact also indexes files under their extension-stripped key, so
	// "app/data.json.gz" appears under the same "app/data.json" key as the
	// real target. The real directive must still resolve.
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":   "[package]\nname = \"app\"\nversion = \"0.1.0\"\nbuild = \"build.rs\"\n",
		"build.rs":     "fn main() {}\n",
		"data.json":    "{}\n",
		"data.json.gz": "not really gzip\n",
	})

	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, ".", "app", []map[string]any{
			cargoTargetJSON(root, "build.rs", "build-script-build", rustTargetCustomBuild),
		}, nil),
	})
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(
		context.Background(),
		root,
		[]FileAnalysis{{Path: "build.rs", Language: "rust", References: []ImportReference{
			{Path: "data.json", Kind: "rust-build-input"},
		}}},
		func(context.Context, string) ([]byte, error) { return metadata, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["build.rs"], []string{"data.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("build script inputs = %#v, want %#v", got, want)
	}
}

func TestRustBuildScriptInputsRequireCustomBuildTarget(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":   "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":   "pub fn emit() {}\n",
		"schema.proto": "syntax = \"proto3\";\n",
	})

	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, ".", "app", []map[string]any{
			cargoTargetJSON(root, "src/lib.rs", "app", rustTargetLib),
		}, nil),
	})
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(
		context.Background(),
		root,
		[]FileAnalysis{{Path: filepath.Join("src", "lib.rs"), Language: "rust", References: []ImportReference{{Path: "schema.proto", Kind: "rust-build-input"}}}},
		func(context.Context, string) ([]byte, error) { return metadata, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports[filepath.Join("src", "lib.rs")]; len(got) != 0 {
		t.Fatalf("non-build target inputs = %#v, want none", got)
	}
}
