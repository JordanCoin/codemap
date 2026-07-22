package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAstGrepRustLiteralIncludeExtraction(t *testing.T) {
	astScanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(astScanner.Close)
	if !astScanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	source := `include!("generated.rs");
include!(r#"nested/raw.rs"#);
const VALUE: i32 = include!("nested/value.rs");
include!(concat!("generated", ".rs"));
include_str!("data.txt");
include_bytes!("data.bin");
`
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := astScanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	var got []ImportReference
	var imports []string
	for _, analysis := range outcome.Analyses {
		imports = append(imports, analysis.Imports...)
		for _, ref := range analysis.References {
			if ref.Kind == "rust-include" {
				ref.Line = 0
				got = append(got, ref)
			}
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []ImportReference{
		{Path: "generated.rs", Kind: "rust-include", ExplicitTarget: `"generated.rs"`},
		{Path: "nested/raw.rs", Kind: "rust-include", ExplicitTarget: `r#"nested/raw.rs"#`},
		{Path: "nested/value.rs", Kind: "rust-include", ExplicitTarget: `"nested/value.rs"`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("literal Rust include references = %#v, want %#v", got, want)
	}
	sort.Strings(imports)
	if want := []string{"generated.rs", "nested/raw.rs", "nested/value.rs"}; !reflect.DeepEqual(imports, want) {
		t.Fatalf("literal Rust include imports = %#v, want %#v", imports, want)
	}
}

func TestRustLiteralIncludesResolveConservatively(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":       "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":       "pub fn root() {}\n",
		"src/generated.rs": "pub fn generated() {}\n",
		"shared/raw.rs":    "pub fn raw() {}\n",
		"src/non_rust.txt": "not Rust\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "generated.rs", Kind: "rust-include", ExplicitTarget: `"generated.rs"`},
			{Path: "../shared/raw.rs", Kind: "rust-include", ExplicitTarget: `r#"../shared/raw.rs"#`},
			{Path: "lib.rs", Kind: "rust-include", ExplicitTarget: `"lib.rs"`},
			{Path: "missing.rs", Kind: "rust-include", ExplicitTarget: `"missing.rs"`},
			{Path: "../../outside.rs", Kind: "rust-include", ExplicitTarget: `"../../outside.rs"`},
			{Path: "non_rust.txt", Kind: "rust-include", ExplicitTarget: `"non_rust.txt"`},
			{Path: "dynamic", Kind: "rust-include", ExplicitTarget: `concat!("generated", ".rs")`},
		}},
		{Path: "src/generated.rs", Language: "rust"},
		{Path: "shared/raw.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shared/raw.rs", "src/generated.rs"}
	if got := sortedImports(graph, "src/lib.rs"); !reflect.DeepEqual(got, want) {
		t.Fatalf("literal include imports = %#v, want %#v", got, want)
	}
}

func TestResolveRustIncludeRequiresRealIndexedFile(t *testing.T) {
	root := t.TempDir()
	// bindings.rs.in is indexed under the bindings.rs key though no such
	// file exists; the include must stay unresolved.
	idx := buildFileIndex([]FileInfo{{Path: "src/bindings.rs.in"}, {Path: "src/main.rs"}}, "")
	if target := resolveRustInclude(root, "src/main.rs", `"bindings.rs"`, idx); target != "" {
		t.Fatalf("phantom include target = %q, want unresolved", target)
	}
	// With both files present, the edge to the real file survives.
	idx = buildFileIndex([]FileInfo{{Path: "src/bindings.rs"}, {Path: "src/bindings.rs.in"}, {Path: "src/main.rs"}}, "")
	if target := resolveRustInclude(root, "src/main.rs", `"bindings.rs"`, idx); target != "src/bindings.rs" {
		t.Fatalf("include target = %q, want src/bindings.rs", target)
	}
}
