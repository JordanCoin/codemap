package scanner

import (
	"context"
	"strings"
	"testing"

	"codemap/analysis"
)

// A Swift project is the clearest case of a language whose files never import
// each other. Type-name reference resolution recovers the edges, but it is a
// name match rather than compiler resolution, so coverage must never read as
// complete: a consumer would conclude a change is isolated when nothing
// checked that.
func TestSwiftFixtureNeverReportsCompleteCoverage(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/symbol-imports-swift", Filters{})
	if err != nil {
		t.Fatalf("build swift fixture graph: %v", err)
	}
	if graph.Coverage.Status == analysis.CoverageComplete {
		t.Fatalf("swift fixture coverage = %q, want anything but complete", graph.Coverage.Status)
	}
	// Same-module files never import each other, so every edge here comes
	// from type-name reference resolution: B.swift mentions a type A.swift
	// declares, so B -> A. Coverage stays partial and the source says mixed.
	if got := graph.Imports["Sources/App/UserViewModel.swift"]; len(got) != 1 || got[0] != "Sources/App/Models.swift" {
		t.Fatalf("UserViewModel.swift imports = %v, want exactly [Sources/App/Models.swift] (it mentions User)", got)
	}
	if got := graph.Imports["Sources/App/ContentView.swift"]; len(got) != 1 || got[0] != "Sources/App/UserViewModel.swift" {
		t.Fatalf("ContentView.swift imports = %v, want exactly [Sources/App/UserViewModel.swift]", got)
	}
	if got := graph.Imports["Sources/App/Models.swift"]; len(got) != 0 {
		t.Fatalf("Models.swift imports = %v, want none (it references no project type)", got)
	}

	var named bool
	for _, source := range graph.Coverage.Sources {
		if source.Name == "symbol-imports/swift" {
			named = true
			if source.Status != analysis.SourceMixed {
				t.Fatalf("symbol-imports/swift status = %q, want mixed once type-name edges are resolved", source.Status)
			}
			if !strings.Contains(source.Detail, "Swift (3 files)") || !strings.Contains(source.Detail, "type-name reference") {
				t.Fatalf("source detail = %q, want it to name the language, file count and resolver", source.Detail)
			}
		}
	}
	if !named {
		t.Fatalf("coverage sources %+v omit a symbol-imports source naming Swift", graph.Coverage.Sources)
	}
	if len(graph.Coverage.Notes) == 0 {
		t.Fatal("coverage carries no note explaining why the graph cannot see intra-project edges")
	}
}

// The counterpart guard: a language whose imports do resolve to files keeps
// complete coverage, so this never becomes a blanket downgrade.
func TestGoFixtureKeepsCompleteCoverage(t *testing.T) {
	graph, err := BuildFileGraph(context.Background(), "../testdata/file-imports-go", Filters{})
	if err != nil {
		t.Fatalf("build go fixture graph: %v", err)
	}
	if graph.Coverage.Status != "" && graph.Coverage.Status != analysis.CoverageComplete {
		t.Fatalf("go fixture coverage = %q, want complete", graph.Coverage.Status)
	}
	for _, source := range graph.Coverage.Sources {
		if strings.HasPrefix(source.Name, "symbol-imports/") {
			t.Fatalf("go fixture gained %q; the file-level edge model applies to Go", source.Name)
		}
	}
}

func TestResolvesFileLevelImports(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript", "javascript", "rust", "c", "cpp", "dart", "ruby", "php", "lua", "solidity", "bash", "cue"} {
		if !ResolvesFileLevelImports(language) {
			t.Errorf("ResolvesFileLevelImports(%q) = false, want true", language)
		}
	}
	for _, language := range []string{"swift", "kotlin", "java", "csharp", "scala"} {
		if ResolvesFileLevelImports(language) {
			t.Errorf("ResolvesFileLevelImports(%q) = true, want false", language)
		}
	}
}

func TestInspectFileLanguagesFindsGraphRequirements(t *testing.T) {
	inventory := inspectFileLanguages([]FileInfo{
		{Path: "src/lib.rs", Ext: ".rs"},
		{Path: "cue/app.cue", Ext: ".cue"},
		{Path: "src/Main.java", Ext: ".java"},
		{Path: "src/Other.java", Ext: ".java"},
		{Path: "main.go", Ext: ".go"},
	})
	if !inventory.hasRust || !inventory.hasCUE {
		t.Fatalf("language requirements = %+v, want Rust and CUE", inventory)
	}
	if got := inventory.symbolLevel["Java"]; got != 2 {
		t.Fatalf("Java file count = %d, want 2", got)
	}
}

// Coverage that already knows less must never be talked back up.
func TestApplySymbolLevelImportCoverageOnlyRemovesConfidence(t *testing.T) {
	swift := []FileInfo{{Path: "App/Model.swift"}}
	for _, status := range []analysis.CoverageStatus{analysis.CoveragePartial, analysis.CoverageUnavailable} {
		got := ApplySymbolLevelImportCoverage(analysis.Coverage{Status: status}, swift)
		if got.Status != status {
			t.Errorf("status %q became %q, want it preserved", status, got.Status)
		}
	}

	got := ApplySymbolLevelImportCoverage(analysis.Coverage{Status: analysis.CoverageComplete}, swift)
	if got.Status != analysis.CoveragePartial {
		t.Errorf("complete coverage over Swift = %q, want partial", got.Status)
	}

	unchanged := ApplySymbolLevelImportCoverage(
		analysis.Coverage{Status: analysis.CoverageComplete},
		[]FileInfo{{Path: "main.go"}, {Path: "app.py"}},
	)
	if unchanged.Status != analysis.CoverageComplete {
		t.Errorf("complete coverage over Go and Python = %q, want it left complete", unchanged.Status)
	}
}

// Every symbol-level language present is named, so a mixed project says which
// slice it cannot see rather than emitting a bare partial.
func TestSymbolLevelSourcesNameEveryLanguagePresent(t *testing.T) {
	coverage := ApplySymbolLevelImportCoverage(
		analysis.Coverage{Status: analysis.CoverageComplete},
		[]FileInfo{
			{Path: "main.go"},
			{Path: "App/Model.swift"},
			{Path: "src/Main.kt"},
			{Path: "src/Other.kt"},
		},
	)
	var got []string
	for _, source := range coverage.Sources {
		if strings.HasPrefix(source.Name, "symbol-imports/") {
			got = append(got, source.Name+"|"+source.Detail)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d symbol-imports sources %v, want one per language present", len(got), got)
	}
	if !strings.Contains(got[0], "Kotlin (2 files)") || !strings.Contains(got[1], "Swift (1 files)") {
		t.Fatalf("sources %v omit per-language file counts in sorted order", got)
	}
}

// The milestone's exit test: the fixture's importer list must be exact, so a
// future resolver change that widens or narrows it is caught here rather than
// in someone's repository.
func TestFixtureImporterListsAreExact(t *testing.T) {
	swift, err := BuildFileGraph(context.Background(), "../testdata/symbol-imports-swift", Filters{})
	if err != nil {
		t.Fatalf("build swift fixture graph: %v", err)
	}
	if got := swift.Importers["Sources/App/Models.swift"]; len(got) != 1 || got[0] != "Sources/App/UserViewModel.swift" {
		t.Fatalf("swift Models.swift importers = %v, want exactly [Sources/App/UserViewModel.swift] (the one file that mentions User)", got)
	}

	golang, err := BuildFileGraph(context.Background(), "../testdata/file-imports-go", Filters{})
	if err != nil {
		t.Fatalf("build go fixture graph: %v", err)
	}
	got := golang.Importers["pkg/user.go"]
	if len(got) != 1 || got[0] != "main.go" {
		t.Fatalf("go pkg/user.go importers = %v, want exactly [main.go]", got)
	}
}

// A zero-importer answer has to say how much it checked, so the field is
// always present — the complete case is exactly where an omitted status let a
// silent zero read as a thorough one.
func TestEffectiveStatusSpellsOutComplete(t *testing.T) {
	if got := (GraphCoverage{}).EffectiveStatus(); got != analysis.CoverageComplete {
		t.Fatalf("zero-value coverage EffectiveStatus() = %q, want %q", got, analysis.CoverageComplete)
	}
	for _, status := range []analysis.CoverageStatus{analysis.CoveragePartial, analysis.CoverageUnavailable} {
		if got := (GraphCoverage{Status: status}).EffectiveStatus(); got != status {
			t.Fatalf("EffectiveStatus() = %q, want %q preserved", got, status)
		}
	}
}

// The declaration head decides the name: "class func" is a static method, not
// a type, and an extension declares nothing new.
func TestExtractTypeName(t *testing.T) {
	cases := map[string]string{
		"public final class Foo<T>: Bar { }":    "Foo",
		"struct Baz: Codable { }":               "Baz",
		"indirect enum Qux { case a }":          "Qux",
		"actor Act { }":                         "Act",
		"protocol Proto: AnyObject { }":         "Proto",
		"@objc class Obj: NSObject { }":         "Obj",
		"@available(iOS 15, *) struct Av { }":   "Av",
		"extension Foo { }":                     "",
		"class func shared() -> Foo { }":        "",
		"class var count: Int { 0 }":            "",
		"class Outer {\n  class func f() {}\n}": "Outer",
	}
	for text, want := range cases {
		if got := extractTypeName(text); got != want {
			t.Errorf("extractTypeName(%q) = %q, want %q", text, got, want)
		}
	}
}
