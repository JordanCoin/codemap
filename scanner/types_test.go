package scanner

import (
	"testing"

	"codemap/analysis"
)

func TestNewDepsProjectWithCoverageAndFiltersClonesEffectiveFilters(t *testing.T) {
	filters := Filters{Only: []string{"cue"}, Exclude: []string{"vendor"}}
	project := NewDepsProjectWithCoverageAndFilters("root", nil, nil, "", analysis.Coverage{}, filters)
	filters.Only[0], filters.Exclude[0] = "go", "target"

	if project.EffectiveFilters == nil || project.EffectiveFilters.Only[0] != "cue" || project.EffectiveFilters.Exclude[0] != "vendor" {
		t.Fatalf("effective filters were not cloned: %+v", project.EffectiveFilters)
	}
}

func TestResolverExtensionsReturnsIsolatedOrder(t *testing.T) {
	first := ResolverExtensions()
	if len(first) == 0 || first[len(first)-1] != "" {
		t.Fatalf("extensions = %#v, want bare-path fallback last", first)
	}
	first[0] = "changed"
	if second := ResolverExtensions(); second[0] == "changed" {
		t.Fatal("caller mutation changed resolver extension order")
	}
}
