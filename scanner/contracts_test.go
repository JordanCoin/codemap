package scanner

import (
	"encoding/json"
	"reflect"
	"testing"

	"codemap/analysis"
)

func TestNewDepsProjectReturnsVersionedCompleteCoverage(t *testing.T) {
	project := NewDepsProject("/repo", []FileAnalysis{{Path: "empty.go"}}, map[string][]string{"go": nil}, "main")
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
	first := NewDepsProject("/repo", []FileAnalysis{
		{Path: "z.go", Functions: []string{"Z", "A"}, Imports: []string{"z", "a"}},
		{Path: "a.go"},
	}, map[string][]string{"go": {"z", "a"}}, "")
	second := NewDepsProject("/repo", []FileAnalysis{
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
	first := NewDepsProject("/repo", []FileAnalysis{
		{Path: "same.rs", Language: "rust", Functions: []string{"z"}},
		{Path: "same.rs", Language: "rust", Functions: []string{"a"}},
	}, nil, "")
	second := NewDepsProject("/repo", []FileAnalysis{
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
