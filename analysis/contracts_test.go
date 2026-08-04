package analysis

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeCoverageProducesDeterministicNonNilCollections(t *testing.T) {
	coverage := NormalizeCoverage(Coverage{
		Status: CoveragePartial,
		Sources: []Source{
			{Name: "zeta", Status: SourceFallback},
			{Name: "alpha", Status: SourceAuthoritative},
		},
		Issues: []Issue{{Code: "ambiguous", Message: "choose", Candidates: []string{"z", "a"}}},
	})
	if got, want := []string{coverage.Sources[0].Name, coverage.Sources[1].Name}, []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("source order = %v, want %v", got, want)
	}
	if got, want := coverage.Issues[0].Candidates, []string{"a", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	encoded, err := json.Marshal(NormalizeCoverage(Coverage{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || string(encoded) == `{"status":"","sources":null,"issues":null}` {
		t.Fatalf("coverage must encode non-null collections: %s", encoded)
	}
}

func TestNormalizeCoveragePreservesIssueOrderAndSortsRepeatedFields(t *testing.T) {
	input := Coverage{
		Sources: []Source{{Name: "same", Status: SourceFallback}, {Name: "same", Status: SourceAuthoritative}},
		Issues:  []Issue{{Code: "z", Candidates: []string{"b", "a"}}, {Code: "a"}},
	}
	got := NormalizeCoverage(input)
	if got.Sources[0].Status != SourceAuthoritative {
		t.Fatalf("source tie order = %#v", got.Sources)
	}
	if got.Issues[0].Code != "z" || !reflect.DeepEqual(got.Issues[0].Candidates, []string{"a", "b"}) || got.Issues[1].Code != "a" {
		t.Fatalf("issue order = %#v", got.Issues)
	}
}
