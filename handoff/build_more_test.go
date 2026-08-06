package handoff

import (
	"context"
	"reflect"
	"testing"
	"time"

	"codemap/watch"
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

func TestSummarizeEventsFiltersSortsAndCaps(t *testing.T) {
	now := time.Now()
	old := now.Add(-3 * time.Hour)
	stale := now.Add(-2 * time.Hour)
	fresh := now.Add(-5 * time.Minute)

	tests := []struct {
		name      string
		state     *watch.State
		since     time.Duration
		maxEvents int
		wantPaths []string
	}{
		{
			name:  "nil state yields empty",
			state: nil,
			since: time.Hour, maxEvents: 10,
			wantPaths: nil,
		},
		{
			name: "drops stale and orders by time then path",
			state: &watch.State{RecentEvents: []watch.Event{
				{Time: old, Op: "WRITE", Path: "z.go"},
				{Time: fresh, Op: "WRITE", Path: "b.go"},
				{Time: fresh, Op: "WRITE", Path: "a.go"},
				{Time: stale, Op: "DELETE", Path: "skip.go"},
			}},
			since: time.Hour, maxEvents: 10,
			wantPaths: []string{"a.go", "b.go"},
		},
		{
			name: "caps to newest events",
			state: &watch.State{RecentEvents: []watch.Event{
				{Time: now.Add(-3 * time.Minute), Op: "WRITE", Path: "1.go"},
				{Time: now.Add(-2 * time.Minute), Op: "WRITE", Path: "2.go"},
				{Time: now.Add(-time.Minute), Op: "WRITE", Path: "3.go"},
			}},
			since: time.Hour, maxEvents: 2,
			wantPaths: []string{"2.go", "3.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeEvents(tt.state, tt.since, tt.maxEvents)
			var paths []string
			for _, e := range got {
				paths = append(paths, e.Path)
			}
			if !reflect.DeepEqual(paths, tt.wantPaths) {
				t.Fatalf("summarizeEvents paths = %v, want %v", paths, tt.wantPaths)
			}
		})
	}
}

func TestSummarizeHubsFiltersSortsAndCaps(t *testing.T) {
	importers := map[string][]string{
		"a.go": {"1.go", "2.go", "3.go", "4.go"},
		"b.go": {"1.go", "2.go", "3.go"},
		"c.go": {"1.go", "2.go"},
		"d.go": {"1.go"},
	}
	got := summarizeHubs(importers, 0)
	want := []HubSummary{{Path: "a.go", Importers: 4}, {Path: "b.go", Importers: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summarizeHubs = %v, want %v", got, want)
	}
	capped := summarizeHubs(importers, 1)
	if len(capped) != 1 || capped[0].Path != "a.go" {
		t.Fatalf("capped hubs = %v, want only a.go", capped)
	}
	if got := summarizeHubs(nil, 0); len(got) != 0 {
		t.Fatalf("nil importers = %v, want empty", got)
	}
}

func TestGitCurrentBranchContextErrors(t *testing.T) {
	if _, err := gitCurrentBranchContext(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error outside a git repository")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gitCurrentBranchContext(ctx, t.TempDir()); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
