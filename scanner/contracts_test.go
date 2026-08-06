package scanner

import (
	"encoding/json"
	"reflect"
	"testing"

	"codemap/analysis"
)

func TestNewDepsProjectReturnsVersionedCompleteCoverage(t *testing.T) {
	project := newDepsProject("/repo", []FileAnalysis{{Path: "empty.go"}}, map[string][]string{"go": nil}, "main")
	if project.SchemaVersion != analysis.SchemaVersion || project.Coverage.Status != analysis.CoverageComplete {
		t.Fatalf("versioned coverage = %#v", project)
	}
	if got, want := project.Coverage.Sources, []analysis.Source{{Name: "ast-grep", Status: analysis.SourceAuthoritative}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("coverage sources = %#v, want %#v", got, want)
	}
	if project.Coverage.Issues == nil || project.Files[0].Functions == nil || project.Files[0].Imports == nil || project.ExternalDeps["go"] == nil {
		t.Fatalf("dependency collections must be non-nil: %#v", project)
	}
}

func TestNewDepsProjectProducesDeterministicCollections(t *testing.T) {
	first := newDepsProject("/repo", []FileAnalysis{
		{Path: "z.go", Functions: []string{"Z", "A"}, Imports: []string{"z", "a"}},
		{Path: "a.go"},
	}, map[string][]string{"go": {"z", "a"}}, "")
	second := newDepsProject("/repo", []FileAnalysis{
		{Path: "a.go"},
		{Path: "z.go", Functions: []string{"A", "Z"}, Imports: []string{"a", "z"}},
	}, map[string][]string{"go": {"a", "z"}}, "")
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("dependency JSON is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestNewDepsProjectOrdersDuplicateFileKeysAndReportsRustCoverage(t *testing.T) {
	first := newDepsProject("/repo", []FileAnalysis{
		{Path: "same.rs", Language: "rust", Functions: []string{"z"}},
		{Path: "same.rs", Language: "rust", Functions: []string{"a"}},
	}, nil, "")
	second := newDepsProject("/repo", []FileAnalysis{
		{Path: "same.rs", Language: "rust", Functions: []string{"a"}},
		{Path: "same.rs", Language: "rust", Functions: []string{"z"}},
	}, nil, "")
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("duplicate file keys are not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.Coverage.Status != analysis.CoveragePartial {
		t.Fatalf("Rust coverage = %q, want partial", first.Coverage.Status)
	}
}

func TestNewDepsProjectRustCoverageFromPathAndInventory(t *testing.T) {
	// A Rust file whose Language field is empty still flips coverage to partial
	// via DetectLanguage on the path.
	fromPath := newDepsProject("/repo", []FileAnalysis{{Path: "crate/src/lib.rs"}}, nil, "")
	if fromPath.Coverage.Status != analysis.CoveragePartial {
		t.Fatalf("path-detected Rust coverage = %q, want partial", fromPath.Coverage.Status)
	}
	// Rust files in the FileInfo inventory also flip a complete scan to partial.
	fromInventory := newDepsProject("/repo", []FileAnalysis{{Path: "main.go"}}, nil, "",
		[]FileInfo{{Path: "crate/src/lib.rs", Ext: ".rs"}})
	if fromInventory.Coverage.Status != analysis.CoveragePartial {
		t.Fatalf("inventory Rust coverage = %q, want partial", fromInventory.Coverage.Status)
	}
	// No Rust anywhere keeps coverage complete.
	complete := newDepsProject("/repo", []FileAnalysis{{Path: "main.go"}}, nil, "",
		[]FileInfo{{Path: "main.go", Ext: ".go"}})
	if complete.Coverage.Status != analysis.CoverageComplete {
		t.Fatalf("non-Rust coverage = %q, want complete", complete.Coverage.Status)
	}
}
