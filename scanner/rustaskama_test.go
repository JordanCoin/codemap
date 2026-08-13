package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAstGrepRustAskamaTemplateExtraction(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	source := `#[template(path = "page.html")]
struct Page;

#[template(path = r#"raw.html"#, escape = "none")]
enum Raw {}

#[other(path = "ignored.html")]
struct Other;

const TEMPLATE: &str = "dynamic.html";
#[template(path = TEMPLATE)]
struct Dynamic;

#[template(path = "configured.html", config = "askama-custom.toml")]
struct Configured;
`
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
			if ref.Kind == "rust-askama-template" {
				ref.Line = 0
				got = append(got, ref)
			}
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ExplicitTarget < got[j].ExplicitTarget })
	want := []ImportReference{
		{Path: `"page.html"`, Kind: "rust-askama-template", ExplicitTarget: `"page.html"`},
		{Path: `r#"raw.html"#`, Kind: "rust-askama-template", ExplicitTarget: `r#"raw.html"#`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Askama template references = %#v, want %#v", got, want)
	}
}

func TestRustAskamaTemplateResolvesWithinCargoPackage(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "app", "templates", "absolute.html")
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":                     "[workspace]\nmembers = [\"app\", \"configured\"]\n",
		"app/Cargo.toml":                 "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"app/src/lib.rs":                 "pub fn app() {}\n",
		"app/scratch/unowned.rs":         "pub fn scratch() {}\n",
		"app/templates/page.html":        "package page\n",
		"app/templates/raw.html":         "package raw\n",
		"app/templates/excluded.html":    "excluded\n",
		"app/templates/absolute.html":    "absolute\n",
		"templates/page.html":            "workspace lookalike\n",
		"outside.html":                   "outside\n",
		"configured/Cargo.toml":          "[package]\nname = \"configured\"\nversion = \"0.1.0\"\n",
		"configured/askama.toml":         "[general]\ndirs = [\"views\"]\n",
		"configured/src/lib.rs":          "pub fn configured() {}\n",
		"configured/templates/page.html": "wrong configured-directory target\n",
		".codemap/config.json":           `{"exclude":["app/templates/excluded.html"]}`,
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, "app", "app", "app", nil),
		cargoPackage(root, "configured", "configured", "configured", nil),
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: `"page.html"`, Kind: "rust-askama-template", ExplicitTarget: `"page.html"`},
			{Path: `r#"raw.html"#`, Kind: "rust-askama-template", ExplicitTarget: `r#"raw.html"#`},
			{Path: `"excluded.html"`, Kind: "rust-askama-template", ExplicitTarget: `"excluded.html"`},
			{Path: `"missing.html"`, Kind: "rust-askama-template", ExplicitTarget: `"missing.html"`},
			{Path: `"page"`, Kind: "rust-askama-template", ExplicitTarget: `"page"`},
			{Path: `"../outside.html"`, Kind: "rust-askama-template", ExplicitTarget: `"../outside.html"`},
			{Path: fmt.Sprintf("%q", absolute), Kind: "rust-askama-template", ExplicitTarget: fmt.Sprintf("%q", absolute)},
			{Path: `"unterminated`, Kind: "rust-askama-template", ExplicitTarget: `"unterminated`},
		}},
		{Path: "app/scratch/unowned.rs", Language: "rust", References: []ImportReference{
			{Path: `"page.html"`, Kind: "rust-askama-template", ExplicitTarget: `"page.html"`},
		}},
		{Path: "configured/src/lib.rs", Language: "rust", References: []ImportReference{
			{Path: `"page.html"`, Kind: "rust-askama-template", ExplicitTarget: `"page.html"`},
		}},
		{Path: "configured/templates/page.html", Language: "html"},
		{Path: "app/templates/page.html", Language: "html"},
		{Path: "app/templates/raw.html", Language: "html"},
		{Path: "app/templates/excluded.html", Language: "html"},
		{Path: "app/templates/absolute.html", Language: "html"},
		{Path: "templates/page.html", Language: "html"},
		{Path: "outside.html", Language: "html"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses,
		func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app/templates/page.html", "app/templates/raw.html"}
	if got := sortedImports(graph, "app/src/lib.rs"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Askama imports = %#v, want %#v", got, want)
	}
	if got := graph.Importers[filepath.FromSlash("templates/page.html")]; len(got) != 0 {
		t.Fatalf("workspace-root lookalike importers = %#v, want none", got)
	}
	if got := graph.Imports[filepath.FromSlash("app/scratch/unowned.rs")]; len(got) != 0 {
		t.Fatalf("unowned Rust source imports = %#v, want none", got)
	}
	if got := graph.Imports[filepath.FromSlash("configured/src/lib.rs")]; len(got) != 0 {
		t.Fatalf("configured template-directory imports = %#v, want none", got)
	}
}

func TestRustAskamaTemplateRequiresAuthoritativeUnambiguousTarget(t *testing.T) {
	idx := &fileIndex{byExact: map[string][]string{
		filepath.FromSlash("app/templates/page.html"): {"app/templates/page.html", "app/templates/page.html"},
	}}
	workspace := &rustWorkspaceIndex{packages: []rustPackage{{root: "app", authoritative: true}}}
	if got := resolveRustAskamaTemplate(t.TempDir(), "app/src/lib.rs", `"page.html"`, idx, workspace); got != "" {
		t.Fatalf("ambiguous target = %q, want unresolved", got)
	}
	idx.byExact[filepath.FromSlash("app/templates/page.html")] = []string{"app/templates/page.html"}
	workspace.packages[0].authoritative = false
	if got := resolveRustAskamaTemplate(t.TempDir(), "app/src/lib.rs", `"page.html"`, idx, workspace); got != "" {
		t.Fatalf("fallback-owned target = %q, want unresolved", got)
	}
}
