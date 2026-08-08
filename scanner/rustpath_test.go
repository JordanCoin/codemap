package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func sortedImports(graph *FileGraph, path string) []string {
	imports := append([]string(nil), graph.Imports[path]...)
	sort.Strings(imports)
	return imports
}

func TestRustPathModuleResolvesLogicalAncestryAndDirectoryOwnership(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":               "[package]\nname = \"app\"\nversion = \"0.1.0\"\n[workspace]\nmembers = [\"consumer\"]\n",
		"src/lib.rs":               "#[path = \"../shared/redirect.rs\"]\npub mod logical;\nmod sibling;\n",
		"src/sibling.rs":           "pub fn run() {}\n",
		"shared/redirect.rs":       "mod child;\n",
		"shared/child.rs":          "pub fn run() {}\n",
		"shared/raw.rs":            "pub fn run() {}\n",
		"shared/redirect/child.rs": "pub fn wrong() {}\n",
		"consumer/Cargo.toml":      "[package]\nname = \"consumer\"\nversion = \"0.1.0\"\n",
		"consumer/src/lib.rs":      "pub fn call() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, ".", "app", "app", nil),
		cargoPackage(root, "consumer", "consumer", "consumer", []map[string]any{{
			"name": "app", "rename": nil, "path": root, "kind": nil,
		}}),
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "logical", Kind: "rust-path-module", ExplicitTarget: `"../shared/redirect.rs"`},
			{Path: "logical", Kind: "rust-module", Line: 1},
			{Path: "sibling", Kind: "rust-module", Line: 2},
			{Path: "crate::logical::child::run", Kind: "rust-path"},
		}},
		{Path: "src/sibling.rs", Language: "rust"},
		{Path: "shared/redirect.rs", Language: "rust", References: []ImportReference{
			{Path: "child", Kind: "rust-module"},
			{Path: "self::child::run", Kind: "rust-path"},
			{Path: "super::sibling::run", Kind: "rust-path"},
		}},
		{Path: "shared/child.rs", Language: "rust", References: []ImportReference{{Path: "super::super::sibling::run", Kind: "rust-path"}}},
		{Path: "shared/raw.rs", Language: "rust"},
		{Path: "shared/redirect/child.rs", Language: "rust"},
		{Path: "consumer/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "app::logical::child::run", Kind: "rust-path"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses,
		func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	for from, want := range map[string][]string{
		"src/lib.rs":          {"shared/child.rs", "shared/redirect.rs", "src/sibling.rs"},
		"shared/redirect.rs":  {"shared/child.rs", "src/sibling.rs"},
		"shared/child.rs":     {"src/sibling.rs"},
		"consumer/src/lib.rs": {"shared/child.rs"},
	} {
		if got := sortedImports(graph, from); !reflect.DeepEqual(got, want) {
			t.Errorf("%s imports = %#v, want %#v", from, got, want)
		}
	}
	if got := graph.Importers[filepath.FromSlash("shared/redirect/child.rs")]; len(got) != 0 {
		t.Fatalf("wrong physical child importers = %#v, want none", got)
	}
}

func TestRustPathModuleOwnershipIsConservative(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":  "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":  "#[path = \"../shared.rs\"] mod shared;\n",
		"src/main.rs": "#[path = \"../shared.rs\"] mod shared;\nfn main() {}\n",
		"shared.rs":   "pub fn run() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, ".", "app", []map[string]any{
			cargoTargetJSON(root, "src/lib.rs", "app", rustTargetLib),
			cargoTargetJSON(root, "src/main.rs", "app-bin", rustTargetBin),
		}, nil),
	})
	refs := []ImportReference{
		{Path: "shared", Kind: "rust-path-module", ExplicitTarget: `"../shared.rs"`},
		{Path: "shared", Kind: "rust-module"},
	}
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: refs},
		{Path: "src/main.rs", Language: "rust", References: refs},
		{Path: "shared.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses,
		func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Importers["shared.rs"]; len(got) != 0 {
		t.Fatalf("multiply owned explicit module importers = %#v, want none", got)
	}
}

func TestRustPathModuleUnsupportedFormsDoNotInventConventionalEdges(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":         "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":         "#[path = \"actual.rs\"]\nmod direct;\n\n#[cfg_attr(test, path = \"conditional_actual.rs\")]\n\n// The conditional attribute remains attached across trivia.\nmod conditional;\n\n#[cfg_attr(\n\n    // Trivia inside the multiline attribute must also make progress.\n    test,\n    path = \"multiline_actual.rs\"\n)]\nmod multiline;\n\n#[cfg_attr(test, doc = \"mentions path without assigning it\")]\nmod ordinary;\n",
		"src/direct.rs":      "pub fn wrong() {}\n",
		"src/conditional.rs": "pub fn wrong() {}\n",
		"src/multiline.rs":   "pub fn wrong() {}\n",
		"src/ordinary.rs":    "pub fn ordinary() {}\n",
		"src/actual.rs":      "pub fn run() {}\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "direct", Kind: "rust-module", Line: 1},
			{Path: "conditional", Kind: "rust-module", Line: 6},
			{Path: "multiline", Kind: "rust-module", Line: 14},
			{Path: "ordinary", Kind: "rust-module", Line: 17},
		}},
		{Path: "src/direct.rs", Language: "rust"},
		{Path: "src/conditional.rs", Language: "rust"},
		{Path: "src/multiline.rs", Language: "rust"},
		{Path: "src/ordinary.rs", Language: "rust"},
		{Path: "src/actual.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedImports(graph, "src/lib.rs"), []string{"src/ordinary.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unsupported path imports = %#v, want %#v", got, want)
	}
}

func TestRustPathModuleResolvesSafeLiteralForms(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "shared", "absolute.rs")
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":         "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":         "pub fn root() {}\n",
		"shared/raw.rs":      "pub fn raw() {}\n",
		"shared/absolute.rs": "pub fn absolute() {}\n",
		"shared/redirect.rs": "pub fn escaped() {}\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "raw", Kind: "rust-path-module", ExplicitTarget: `r#"../shared/raw.rs"#`},
			{Path: "absolute", Kind: "rust-path-module", ExplicitTarget: fmt.Sprintf("%q", absolute)},
			{Path: "escaped", Kind: "rust-path-module", ExplicitTarget: `"../shared/\u{72}edirect.rs"`},
		}},
		{Path: "shared/raw.rs", Language: "rust"},
		{Path: "shared/absolute.rs", Language: "rust"},
		{Path: "shared/redirect.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shared/absolute.rs", "shared/raw.rs", "shared/redirect.rs"}
	if got := sortedImports(graph, "src/lib.rs"); !reflect.DeepEqual(got, want) {
		t.Fatalf("safe literal imports = %#v, want %#v", got, want)
	}
}

func TestRustPathModuleRejectsUnsafeOrAmbiguousTargets(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":           "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":           "pub fn root() {}\n",
		"src/default.rs":       "pub fn wrong() {}\n",
		"src/non_rust.txt":     "not Rust\n",
		"src/ambiguous.rs":     "pub fn first() {}\n",
		"src/ambiguous/mod.rs": "pub fn second() {}\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "missing", Kind: "rust-path-module", ExplicitTarget: `"missing.rs"`},
			{Path: "malformed", Kind: "rust-path-module", ExplicitTarget: `"unterminated`},
			{Path: "outside", Kind: "rust-path-module", ExplicitTarget: `"../../outside.rs"`},
			{Path: "non_rust", Kind: "rust-path-module", ExplicitTarget: `"non_rust.txt"`},
			{Path: "ambiguous", Kind: "rust-module"},
		}},
		{Path: "src/default.rs", Language: "rust"},
		{Path: "src/ambiguous.rs", Language: "rust"},
		{Path: "src/ambiguous/mod.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["src/lib.rs"]; len(got) != 0 {
		t.Fatalf("unsafe or ambiguous targets invented imports = %#v, want none", got)
	}
}

func TestRustPathModuleMultipleLogicalParentsAndCyclesAreAmbiguous(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":    "pub fn root() {}\n",
		"src/parent.rs": "pub fn parent() {}\n",
		"shared.rs":     "pub fn shared() {}\n",
		"cycle.rs":      "pub fn cycle() {}\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: "parent", Kind: "rust-module"},
			{Path: "first", Kind: "rust-path-module", ExplicitTarget: `"../shared.rs"`},
			{Path: "cycle", Kind: "rust-path-module", ExplicitTarget: `"../cycle.rs"`},
		}},
		{Path: "src/parent.rs", Language: "rust", References: []ImportReference{
			{Path: "second", Kind: "rust-path-module", ExplicitTarget: `"../shared.rs"`},
		}},
		{Path: "shared.rs", Language: "rust"},
		{Path: "cycle.rs", Language: "rust", References: []ImportReference{
			{Path: "root_again", Kind: "rust-path-module", ExplicitTarget: `"src/lib.rs"`},
		}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Importers["shared.rs"]; len(got) != 0 {
		t.Fatalf("multiply owned module importers = %#v, want none", got)
	}
	if got := graph.Importers["cycle.rs"]; len(got) != 0 {
		t.Fatalf("cyclic module importers = %#v, want none", got)
	}
}

type cancelDuringModuleWalkContext struct {
	context.Context
	calls int
	limit int
}

func (ctx *cancelDuringModuleWalkContext) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.limit {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestRustLogicalModuleWalkHonorsCancellation(t *testing.T) {
	const moduleCount = 160
	root := t.TempDir()
	fixture := map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
	}
	var rootSource strings.Builder
	analyses := make([]FileAnalysis, 0, moduleCount+1)
	rootAnalysis := FileAnalysis{Path: "src/lib.rs", Language: "rust"}
	for i := 0; i < moduleCount; i++ {
		name := fmt.Sprintf("module_%03d", i)
		rootSource.WriteString("mod " + name + ";\n")
		fixture[filepath.Join("src", name+".rs")] = "pub fn run() {}\n"
		rootAnalysis.References = append(rootAnalysis.References, ImportReference{Path: name, Kind: "rust-module"})
		analyses = append(analyses, FileAnalysis{Path: filepath.Join("src", name+".rs"), Language: "rust"})
	}
	fixture["src/lib.rs"] = rootSource.String()
	analyses = append(analyses, rootAnalysis)
	writeRustCargoFixture(t, root, fixture)

	index := buildRustFallbackWorkspaceIndex(root, analyses)
	files := make([]FileInfo, 0, len(analyses))
	for _, analysis := range analyses {
		files = append(files, FileInfo{Path: analysis.Path})
	}
	ctx := &cancelDuringModuleWalkContext{Context: context.Background(), limit: moduleCount / 2}
	err := index.buildRustModuleLocations(ctx, root, files)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if ctx.calls != ctx.limit {
		t.Fatalf("context checks = %d, want %d", ctx.calls, ctx.limit)
	}
}

func TestResolveRustExplicitModuleRequiresOneIndexedRustFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join("src", "module.rs")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte("pub fn run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := rustModuleLocation{file: "src/lib.rs", childBase: "src"}
	declaration := rustModuleDeclaration{name: "module", explicitTarget: `"module.rs"`}
	if target, _ := resolveRustModuleDeclaration(root, parent, declaration, map[string]int{path: 2}); target != "" {
		t.Fatalf("multiply indexed target = %q, want unresolved", target)
	}
}
func TestCargoManifestParsingEdgeCases(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "Cargo.toml")
	content := `[workspace]
members = [
  "crates/a",
  "crates/b",
]  # closing bracket with comment
exclude = ["crates/ignored"]

[package]
name = "demo"  # trailing comment

[lib]
path = "src/lib.rs"
`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, ok := readCargoManifest(manifestPath)
	if !ok {
		t.Fatal("readCargoManifest failed on valid manifest")
	}
	if len(manifest.Workspace.Members) != 2 || manifest.Workspace.Members[0] != "crates/a" || manifest.Workspace.Members[1] != "crates/b" {
		t.Fatalf("workspace members = %v, want [crates/a crates/b]", manifest.Workspace.Members)
	}
	if len(manifest.Workspace.Exclude) != 1 || manifest.Workspace.Exclude[0] != "crates/ignored" {
		t.Fatalf("workspace exclude = %v, want [crates/ignored]", manifest.Workspace.Exclude)
	}
	if manifest.Package.Name != "demo" {
		t.Fatalf("package name = %q, want demo", manifest.Package.Name)
	}
	if manifest.Lib.Path != "src/lib.rs" {
		t.Fatalf("lib path = %q, want src/lib.rs", manifest.Lib.Path)
	}

	if _, ok := readCargoManifest(filepath.Join(root, "missing.toml")); ok {
		t.Fatal("readCargoManifest on a missing file should report not-ok")
	}
}

func TestStripCargoCommentKeepsHashesInsideStrings(t *testing.T) {
	cases := map[string]string{
		`members = ["a#b"]`:           `members = ["a#b"]`,
		`members = ["a"]  # trailing`: `members = ["a"]  `,
		`path = "src/#weird"`:         `path = "src/#weird"`,
		`path = "src/x\"#y"`:          `path = "src/x\"#y"`,
	}
	for input, want := range cases {
		if got := stripCargoComment(input); got != want {
			t.Fatalf("stripCargoComment(%q) = %q, want %q", input, got, want)
		}
	}
}

