package handoff

import (
	"reflect"
	"testing"
)

func TestChangedFromEventsDedupesAndSorts(t *testing.T) {
	events := []EventSummary{
		{Path: "pkg/types.go"},
		{Path: "main.go"},
		{Path: "pkg/types.go"},
		{Path: "a.go"},
	}

	got := changedFromEvents(events)
	want := []string{"a.go", "main.go", "pkg/types.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changedFromEvents() = %v, want %v", got, want)
	}
}

func TestPrioritizeChangedPathsPrefersRiskBeforeRemainingOrder(t *testing.T) {
	changed := []string{"a.go", "b.go", "c.go", "d.go"}
	risk := []RiskFile{
		{Path: "c.go", Importers: 5},
		{Path: "missing.go", Importers: 4},
		{Path: "a.go", Importers: 3},
	}

	got := prioritizeChangedPaths(changed, risk, 3)
	want := []string{"c.go", "a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prioritizeChangedPaths() = %v, want %v", got, want)
	}
}

func TestBuildCacheMetricsTracksReuseAccounting(t *testing.T) {
	previous := &Artifact{
		CombinedHash: "combined",
		PrefixHash:   "prefix",
		DeltaHash:    "delta",
	}

	got := buildCacheMetrics(previous, "prefix", "delta", 20, 30)
	if !got.PrefixReused || !got.DeltaReused {
		t.Fatalf("expected both layers to be reused, got %+v", got)
	}
	if got.UnchangedBytes != 50 || got.TotalBytes != 50 {
		t.Fatalf("unexpected byte accounting: %+v", got)
	}
	if got.ReuseRatio != 1 {
		t.Fatalf("expected reuse ratio 1, got %v", got.ReuseRatio)
	}
	if got.PreviousCombinedHash != "combined" {
		t.Fatalf("previous combined hash = %q, want combined", got.PreviousCombinedHash)
	}
}
