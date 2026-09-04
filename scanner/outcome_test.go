package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codemap/analysis"
)

func requireSourceOutcome(t *testing.T, coverage GraphCoverage, source string) ScanSourceOutcome {
	t.Helper()
	for _, outcome := range coverage.Sources {
		if outcome.Name == source {
			return outcome
		}
	}
	t.Fatalf("source %q missing from %#v", source, coverage.Sources)
	return ScanSourceOutcome{}
}

func TestGraphCoverageSourceOutcomesPreserveSemanticCoverage(t *testing.T) {
	coverage := GraphCoverage{Status: analysis.CoveragePartial, Notes: []string{"dynamic routes unresolved"}}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceAuthoritative})
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceFallback, Detail: "1 of 1 Cargo manifests used fallback topology"})

	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got := requireSourceOutcome(t, coverage, "ast-grep").Status; got != ScanSourceAuthoritative {
		t.Fatalf("ast-grep status = %q", got)
	}
	if got := requireSourceOutcome(t, coverage, "cargo-metadata").Status; got != ScanSourceFallback {
		t.Fatalf("cargo status = %q", got)
	}
	if got, want := coverage.Notes[len(coverage.Notes)-1], "1 of 1 Cargo manifests used fallback topology"; got != want {
		t.Fatalf("last note = %q, want %q", got, want)
	}
}

func TestScannerOutcomeGuardPaths(t *testing.T) {
	var nilCoverage *GraphCoverage
	nilCoverage.AddSource(ScanSourceOutcome{Name: "ignored", Status: ScanSourceFailed})

	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{})
	if len(coverage.Sources) != 0 {
		t.Fatalf("empty source was recorded: %#v", coverage.Sources)
	}

	cause := errors.New("scanner failed")
	withDetail := &IncompleteScanError{
		Outcome: ScanSourceOutcome{Name: "scanner", Status: ScanSourceFailed, Detail: "stable detail"},
		Err:     cause,
	}
	if got, want := withDetail.Error(), "stable detail"; got != want {
		t.Fatalf("detail error = %q, want %q", got, want)
	}
	if !errors.Is(withDetail, cause) {
		t.Fatal("wrapped scanner error is not discoverable")
	}

	withoutDetail := &IncompleteScanError{Outcome: ScanSourceOutcome{Name: "scanner", Status: ScanSourceFailed}}
	if got, want := withoutDetail.Error(), "scanner dependency scan is failed"; got != want {
		t.Fatalf("fallback error = %q, want %q", got, want)
	}

	var nilError *IncompleteScanError
	if got, want := nilError.Error(), "incomplete dependency scan"; got != want {
		t.Fatalf("nil error = %q, want %q", got, want)
	}
	if nilError.Unwrap() != nil {
		t.Fatal("nil error unwrap must be nil")
	}
}

func TestCargoMetadataOutcomesAndRetry(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs": "pub fn call() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{cargoPackage(root, ".", "app", "app", nil)})
	analyses := []FileAnalysis{{Path: "src/lib.rs", Language: "rust"}}
	attempts := 0
	loader := func(context.Context, string) ([]byte, error) {
		attempts++
		if attempts == 1 {
			return []byte("{\"packages\":[]}"), nil
		}
		return metadata, nil
	}

	first, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSourceOutcome(t, first.Coverage, "cargo-metadata").Status; got != ScanSourceFallback {
		t.Fatalf("first status = %q, want %q", got, ScanSourceFallback)
	}

	second, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSourceOutcome(t, second.Coverage, "cargo-metadata").Status; got != ScanSourceAuthoritative {
		t.Fatalf("second status = %q, want %q", got, ScanSourceAuthoritative)
	}
	if attempts != 2 {
		t.Fatalf("metadata attempts = %d, want 2", attempts)
	}
}

func TestCargoMetadataMixedOutcome(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":        "[package]\nname = \"fallback\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":        "pub fn fallback() {}\n",
		"nested/Cargo.toml": "[package]\nname = \"nested\"\nversion = \"0.1.0\"\n",
		"nested/src/lib.rs": "pub fn nested() {}\n",
	})
	nestedRoot := filepath.Join(root, "nested")
	metadata := cargoMetadataJSON(t, nestedRoot, []map[string]any{cargoPackage(root, "nested", "nested", "nested", nil)})
	loader := func(_ context.Context, manifest string) ([]byte, error) {
		if filepath.Clean(manifest) == filepath.Join(nestedRoot, "Cargo.toml") {
			return metadata, nil
		}
		return nil, errors.New("cargo unavailable")
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, []FileAnalysis{
		{Path: "src/lib.rs", Language: "rust"},
		{Path: "nested/src/lib.rs", Language: "rust"},
	}, loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSourceOutcome(t, graph.Coverage, "cargo-metadata").Status; got != ScanSourceMixed {
		t.Fatalf("status = %q, want %q", got, ScanSourceMixed)
	}
}

func TestNonCargoGraphHasNoCargoOutcome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	graph, err := BuildFileGraphFromOutcome(context.Background(), root, ScanOutcome{
		Analyses: []FileAnalysis{{Path: "main.go", Language: "go"}},
		Sources:  []ScanSourceOutcome{{Name: "ast-grep", Status: ScanSourceAuthoritative}},
	}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSourceOutcome(t, graph.Coverage, "ast-grep").Status; got != ScanSourceAuthoritative {
		t.Fatalf("ast-grep status = %q", got)
	}
	for _, outcome := range graph.Coverage.Sources {
		if outcome.Name == "cargo-metadata" {
			t.Fatalf("unexpected Cargo outcome: %#v", outcome)
		}
	}
}

func TestBuildFileGraphFromOutcomeReusesFileInventory(t *testing.T) {
	root := t.TempDir()
	outcome := ScanOutcome{
		Analyses: []FileAnalysis{{Path: "app/main.py", Language: "python", Imports: []string{"pkg.util"}}},
		Sources:  []ScanSourceOutcome{{Name: "ast-grep", Status: ScanSourceAuthoritative}},
		files: []FileInfo{
			{Path: "app/main.py", Ext: ".py"},
			{Path: filepath.FromSlash("src/pkg/util.py"), Ext: ".py"},
		},
		hasFileInventory: true,
	}

	graph, err := BuildFileGraphFromOutcome(context.Background(), root, outcome, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.FromSlash("src/pkg/util.py")
	if got := graph.Imports["app/main.py"]; !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("imports = %v, want [%s]", got, want)
	}
}

func TestBuildFileGraphFromOutcomeReusesKnownEmptyInventory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	graph, err := BuildFileGraphFromOutcome(context.Background(), root, ScanOutcome{
		Sources:          []ScanSourceOutcome{{Name: "ast-grep", Status: ScanSourceAuthoritative}},
		hasFileInventory: true,
	}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Imports) != 0 || len(graph.Importers) != 0 {
		t.Fatalf("known empty inventory produced edges: %+v", graph)
	}
}

func TestAppendCUEOutcomeReusesFallbackInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.cue"), []byte("package schema\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := appendCUEOutcome(context.Background(), root, Filters{}, ScanOutcome{
		files:            []FileInfo{{Path: "schema.cue", Ext: ".cue"}},
		hasFileInventory: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 1 || outcome.Analyses[0].Path != "schema.cue" || !outcome.hasFileInventory {
		t.Fatalf("CUE analyses = %#v", outcome.Analyses)
	}
}

func TestAppendCUEOutcomeReusesKnownEmptyInventory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	outcome, err := appendCUEOutcome(context.Background(), root, Filters{}, ScanOutcome{hasFileInventory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 0 || !outcome.hasFileInventory {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestCoverageFromSourcesMatrix(t *testing.T) {
	tests := []struct {
		name    string
		sources []analysis.Source
		want    analysis.CoverageStatus
	}{
		{"empty", nil, analysis.CoverageUnavailable},
		{"authoritative", []analysis.Source{{Name: "ast-grep", Status: analysis.SourceAuthoritative}}, analysis.CoverageComplete},
		{"fallback", []analysis.Source{{Name: "ast-grep", Status: analysis.SourceFallback}}, analysis.CoveragePartial},
		{"timeout", []analysis.Source{{Name: "ast-grep", Status: analysis.SourceTimeout}}, analysis.CoverageUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CoverageFromSources(tt.sources).Status; got != tt.want {
				t.Fatalf("CoverageFromSources(%v) = %q, want %q", tt.sources, got, tt.want)
			}
		})
	}
}

// Regression for PR #105: a timed-out or failed primary scan that extracted no
// dependency references must keep graph coverage unavailable, and only a usable
// fallback/mixed source may promote it to partial.
func TestGraphCoverageTimeoutStaysUnavailable(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceTimeout, Detail: "30s limit hit"})
	if got, want := coverage.Status, analysis.CoverageUnavailable; got != want {
		t.Fatalf("timeout status = %q, want %q", got, want)
	}

	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceFallback})
	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status after fallback = %q, want %q", got, want)
	}
	if len(coverage.Notes) != 1 || coverage.Notes[0] != "30s limit hit" {
		t.Fatalf("notes = %#v, want timeout detail only", coverage.Notes)
	}
}

func TestGraphCoverageFailedOnlyStaysUnavailable(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceFailed, Detail: "malformed output"})
	if got, want := coverage.Status, analysis.CoverageUnavailable; got != want {
		t.Fatalf("failed status = %q, want %q", got, want)
	}
	if len(coverage.Notes) != 1 || coverage.Notes[0] != "malformed output" {
		t.Fatalf("notes = %#v, want failed detail", coverage.Notes)
	}
}

func TestGraphCoveragePartialNotDemotedByTimeout(t *testing.T) {
	coverage := GraphCoverage{Status: analysis.CoveragePartial}
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceMixed})
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceTimeout})
	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status = %q, want %q (usable sources must not be demoted)", got, want)
	}
}

func TestGraphCoverageAuthoritativeLeavesStatusUnset(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceAuthoritative})
	if coverage.Status != "" {
		t.Fatalf("authoritative-only status = %q, want unset", coverage.Status)
	}
}

func TestGraphCoverageUnavailableSourceStaysUnavailable(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceUnavailable, Detail: "scanner not installed"})
	if got, want := coverage.Status, analysis.CoverageUnavailable; got != want {
		t.Fatalf("unavailable source status = %q, want %q", got, want)
	}
	if len(coverage.Notes) != 1 || coverage.Notes[0] != "scanner not installed" {
		t.Fatalf("notes = %#v, want unavailable detail", coverage.Notes)
	}
}

// A usable source recorded after a degraded-only source must repair the
// reading: coverage can never stay "unavailable" once dependency references
// were extracted by any source.
func TestGraphCoverageAuthoritativeRepairsUnavailable(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceTimeout, Detail: "30s limit hit"})
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceAuthoritative})
	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status after authoritative repair = %q, want %q", got, want)
	}
	if len(coverage.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 recorded", len(coverage.Sources))
	}
	if len(coverage.Notes) != 1 || coverage.Notes[0] != "30s limit hit" {
		t.Fatalf("notes = %#v, want timeout detail only", coverage.Notes)
	}
}

// A degraded source appended after a usable one must not mark the graph
// unavailable: the usable source already extracted dependency references.
func TestGraphCoverageDegradedAfterUsableKeepsPartial(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceAuthoritative})
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceTimeout, Detail: "cargo hung"})
	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if len(coverage.Notes) != 1 || coverage.Notes[0] != "cargo hung" {
		t.Fatalf("notes = %#v, want cargo detail", coverage.Notes)
	}
}

// Regression for PR #105 follow-up: distinct sources may legitimately carry the
// same caveat (the shared Rust resolution note), but every recorded note is
// surfaced to users, so AddSource must record each detail only once. Without
// dedup, ast-grep + rust-cargo both carrying the Rust note doubled the warning
// in rendered output and in CoverageNotes.
func TestGraphCoverageAddSourceDedupesSharedDetail(t *testing.T) {
	coverage := GraphCoverage{}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceAuthoritative, Detail: rustCoverageNote})
	coverage.AddSource(ScanSourceOutcome{Name: "rust-cargo", Status: ScanSourceMixed, Detail: rustCoverageNote})
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceFallback, Detail: "1 of 1 Cargo manifests used fallback topology"})

	if got, want := coverage.Status, analysis.CoveragePartial; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if len(coverage.Sources) != 3 {
		t.Fatalf("sources = %d, want 3 recorded", len(coverage.Sources))
	}
	wantNotes := []string{rustCoverageNote, "1 of 1 Cargo manifests used fallback topology"}
	if !reflect.DeepEqual(coverage.Notes, wantNotes) {
		t.Fatalf("notes = %#v, want deduped %#v", coverage.Notes, wantNotes)
	}
}
