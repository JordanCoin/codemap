package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	coverage := GraphCoverage{Status: "partial", Notes: []string{"dynamic routes unresolved"}}
	coverage.AddSource(ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceAuthoritative})
	coverage.AddSource(ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceFallback, Detail: "1 of 1 Cargo manifests used fallback topology"})

	if got, want := coverage.Status, "partial"; got != want {
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
