package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func cargoFallbackFixture(t *testing.T, dependency map[string]any) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"app\", \"core\"]\n",
		"app/Cargo.toml":  "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"app/src/lib.rs":  "pub fn call() {}\n",
		"core/Cargo.toml": "[package]\nname = \"core\"\nversion = \"0.1.0\"\n",
		"core/src/lib.rs": "pub fn api() {}\n",
	})
	return root, cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, "app", "app", "app", []map[string]any{dependency}),
		cargoPackage(root, "core", "core", "core", nil),
	})
}

func TestBuildFileGraphUsesCargoFallbackOnce(t *testing.T) {
	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": nil,
	})
	loads := 0
	graph, err := buildFileGraphWithFallback(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceUnavailable, "ast-grep unavailable", ErrAstGrepNotFound)
		},
		func(context.Context, string) ([]byte, error) {
			loads++
			return metadata, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["app/src/lib.rs"], []string{"core/src/lib.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback imports = %#v, want %#v", got, want)
	}
	if loads != 1 {
		t.Fatalf("Cargo metadata loads = %d, want 1", loads)
	}
	if graph.Coverage.Status != "partial" {
		t.Fatalf("coverage status = %q, want partial", graph.Coverage.Status)
	}
	if got := requireSourceOutcome(t, graph.Coverage, "ast-grep").Status; got != ScanSourceUnavailable {
		t.Fatalf("ast-grep status = %q, want %q", got, ScanSourceUnavailable)
	}
	if got := requireSourceOutcome(t, graph.Coverage, "cargo-metadata").Status; got != ScanSourceFallback {
		t.Fatalf("Cargo status = %q, want %q", got, ScanSourceFallback)
	}
	if len(graph.Coverage.Sources) < 2 {
		t.Fatalf("sources = %#v, want primary and fallback outcomes", graph.Coverage.Sources)
	}
}

func TestScanForGraphOutcomeUsesGoFallbackWithoutCargo(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {}\n",
	})
	loads := 0
	outcome, usedFallback, err := scanForGraphOutcome(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceUnavailable, "ast-grep unavailable", ErrAstGrepNotFound)
		},
		func(context.Context, string) ([]byte, error) {
			loads++
			return nil, errors.New("unexpected Cargo fallback")
		},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !usedFallback {
		t.Fatal("Go-only recovery did not report fallback use")
	}
	if loads != 0 {
		t.Fatalf("Cargo metadata loads = %d, want 0", loads)
	}
	want := []FileAnalysis{{
		Path:      "main.go",
		Language:  "go",
		Functions: []string{"main"},
		Imports:   []string{"fmt"},
	}}
	if !reflect.DeepEqual(outcome.Analyses, want) {
		t.Fatalf("analyses = %#v, want %#v", outcome.Analyses, want)
	}
	if len(outcome.Sources) != 2 {
		t.Fatalf("sources = %#v, want ast-grep and Go parser", outcome.Sources)
	}
	if outcome.Sources[0].Name != "ast-grep" || outcome.Sources[1].Name != "go-parser" {
		t.Fatalf("sources = %#v, want ast-grep then Go parser", outcome.Sources)
	}
}

func TestScanForGraphOutcomeCombinesGoAndCargoFallbacks(t *testing.T) {
	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": nil,
	})
	writeRustCargoFixture(t, root, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	outcome, usedFallback, err := scanForGraphOutcome(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep failed", errors.New("scan failure"))
		},
		func(context.Context, string) ([]byte, error) {
			return metadata, nil
		},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !usedFallback {
		t.Fatal("mixed recovery did not report fallback use")
	}
	if got, want := outcome.Analyses, []FileAnalysis{{
		Path:      "main.go",
		Language:  "go",
		Functions: []string{"main"},
		Imports:   []string{},
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("analyses = %#v, want %#v", got, want)
	}
	if got, want := outcome.precomputedEdges, []fileEdge{{from: "app/src/lib.rs", to: "core/src/lib.rs"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %#v, want %#v", got, want)
	}
	if len(outcome.Sources) != 3 ||
		outcome.Sources[0].Name != "ast-grep" ||
		outcome.Sources[1].Name != "go-parser" ||
		outcome.Sources[2].Name != "cargo-metadata" {
		t.Fatalf("sources = %#v, want ast-grep, Go parser, and Cargo metadata", outcome.Sources)
	}
}

func TestBuildFileGraphFromFallbackOutcomePreservesCargoEdgesWithoutReload(t *testing.T) {
	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": nil,
	})
	writeRustCargoFixture(t, root, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	outcome, _, err := scanForGraphOutcome(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceUnavailable, "ast-grep unavailable", ErrAstGrepNotFound)
		},
		func(context.Context, string) ([]byte, error) {
			return metadata, nil
		},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	loads := 0
	graph, err := buildFileGraphFromOutcomeWithCargoMetadataAndFilters(
		context.Background(),
		root,
		outcome,
		Filters{},
		func(context.Context, string) ([]byte, error) {
			loads++
			return metadata, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 0 {
		t.Fatalf("Cargo metadata reloads = %d, want 0", loads)
	}
	if got, want := graph.Imports["app/src/lib.rs"], []string{"core/src/lib.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback imports = %#v, want %#v", got, want)
	}
}

func TestScanForDepsOutcomeRejectsCargoOnlyEmptyRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": nil,
	})
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "sg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "cargo"), []byte("#!/bin/sh\n/bin/cat \"$CODEMAP_TEST_CARGO_METADATA\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(metadataPath, metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CODEMAP_TEST_CARGO_METADATA", metadataPath)

	outcome, err := ScanForDeps(context.Background(), root, Filters{})
	if !errors.Is(err, ErrAstGrepNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrAstGrepNotFound)
	}
	if len(outcome.Analyses) != 0 {
		t.Fatalf("analyses = %#v, want no false successful analyses", outcome.Analyses)
	}
}

func TestScanForGraphOutcomeHonorsCancellationDuringFallback(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml": "package = \"broken\"\n",
		"main.go":    "package main\n\nfunc main() {}\n",
		"src/lib.rs": "pub fn value() {}\n",
	})
	ctx, cancel := context.WithCancel(context.Background())

	_, _, err := scanForGraphOutcome(
		ctx,
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep failed", errors.New("scan failure"))
		},
		func(context.Context, string) ([]byte, error) {
			cancel()
			return nil, errors.New("metadata canceled")
		},
		false,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}

func TestScanForGraphOutcomeHonorsPreCanceledContext(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := scanForGraphOutcome(
		ctx,
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep failed", errors.New("scan failure"))
		},
		nil,
		false,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}

func TestScanForGraphOutcomePreservesPrimaryErrorWhenFileScanFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	primaryErr := newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep failed", errors.New("scan failure"))

	_, _, err := scanForGraphOutcome(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{}, primaryErr
		},
		nil,
		false,
	)
	if err != primaryErr {
		t.Fatalf("error = %v, want original %v", err, primaryErr)
	}
}

func TestBuildFileGraphDoesNotFallbackAfterAuthoritativeScan(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{"main.go": "package main\n"})
	loads := 0
	graph, err := buildFileGraphWithFallback(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) {
			return ScanOutcome{Sources: []ScanSourceOutcome{{Name: "ast-grep", Status: ScanSourceAuthoritative}}}, nil
		},
		func(context.Context, string) ([]byte, error) {
			loads++
			return nil, errors.New("unexpected Cargo fallback")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 0 {
		t.Fatalf("Cargo metadata loads = %d, want 0", loads)
	}
	if got := requireSourceOutcome(t, graph.Coverage, "ast-grep").Status; got != ScanSourceAuthoritative {
		t.Fatalf("ast-grep status = %q, want %q", got, ScanSourceAuthoritative)
	}
}

func TestBuildFileGraphDoesNotFallbackForOtherScanner(t *testing.T) {
	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": nil,
	})
	primaryErr := &IncompleteScanError{
		Outcome: ScanSourceOutcome{Name: "tree-sitter", Status: ScanSourceFailed},
		Err:     errors.New("primary scanner failure"),
	}
	loads := 0
	_, err := buildFileGraphWithFallback(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) { return ScanOutcome{}, primaryErr },
		func(context.Context, string) ([]byte, error) {
			loads++
			return metadata, nil
		},
	)
	if err != primaryErr {
		t.Fatalf("error = %v, want original %v", err, primaryErr)
	}
	if loads != 0 {
		t.Fatalf("Cargo metadata loads = %d, want 0", loads)
	}
}

func TestCargoFallbackRejectsUnprovenEdges(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dependency map[string]any
		files      []FileInfo
	}{
		{
			name: "optional dependency",
			dependency: map[string]any{
				"name": "core", "path": "core", "kind": nil, "optional": true,
			},
			files: []FileInfo{{Path: "app/src/lib.rs"}, {Path: "core/src/lib.rs"}},
		},
		{
			name: "target conditioned dependency",
			dependency: map[string]any{
				"name": "core", "path": "core", "kind": nil, "target": "cfg(unix)",
			},
			files: []FileInfo{{Path: "app/src/lib.rs"}, {Path: "core/src/lib.rs"}},
		},
		{
			name: "filtered dependency target",
			dependency: map[string]any{
				"name": "core", "path": "core", "kind": nil,
			},
			files: []FileInfo{{Path: "app/src/lib.rs"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, metadata := cargoFallbackFixture(t, tt.dependency)
			_, err := buildCargoFallbackOutcome(context.Background(), root, tt.files, func(context.Context, string) ([]byte, error) {
				return metadata, nil
			})
			if err == nil {
				t.Fatal("Cargo fallback accepted an unproven edge")
			}
		})
	}
}

func TestCargoFallbackFailurePreservesPrimaryError(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs": "pub fn call() {}\n",
	})
	primaryErr := &IncompleteScanError{
		Outcome: ScanSourceOutcome{Name: "ast-grep", Status: ScanSourceFailed, Detail: "ast-grep failed"},
		Err:     errors.New("primary scanner failure"),
	}
	_, err := buildFileGraphWithFallback(
		context.Background(),
		root,
		func(string) (ScanOutcome, error) { return ScanOutcome{}, primaryErr },
		func(context.Context, string) ([]byte, error) { return []byte("not JSON"), nil },
	)
	if err != primaryErr {
		t.Fatalf("error = %v, want original %v", err, primaryErr)
	}
}

func TestCargoFallbackAttemptsMetadataOnlyOnce(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"a/Cargo.toml": "[package]\nname = \"a\"\nversion = \"0.1.0\"\n",
		"a/src/lib.rs": "pub fn a() {}\n",
		"b/Cargo.toml": "[package]\nname = \"b\"\nversion = \"0.1.0\"\n",
		"b/src/lib.rs": "pub fn b() {}\n",
	})
	loads := 0
	_, err := buildCargoFallbackOutcome(context.Background(), root, []FileInfo{
		{Path: "a/src/lib.rs"}, {Path: "b/src/lib.rs"},
	}, func(context.Context, string) ([]byte, error) {
		loads++
		return []byte("not JSON"), nil
	})
	if err == nil {
		t.Fatal("Cargo fallback unexpectedly succeeded")
	}
	if loads != 1 {
		t.Fatalf("Cargo metadata loads = %d, want 1", loads)
	}
}

func TestCargoFallbackMetadataArgsAreLocked(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "Cargo.toml")
	got := cargoFallbackMetadataArgs(manifest)
	if !reflect.DeepEqual(got, append(cargoMetadataArgs(manifest), "--locked")) {
		t.Fatalf("fallback args = %#v", got)
	}
}

func TestCargoFallbackPropagatesCanceledDiscovery(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := buildCargoFallbackOutcome(ctx, root, []FileInfo{
		{Path: "src/lib.rs"},
	}, func(context.Context, string) ([]byte, error) {
		t.Fatal("Cargo metadata loader called after context cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Cargo fallback error = %v, want context.Canceled", err)
	}
}

func TestCargoFallbackAcceptsDevDependencyFromLibraryTarget(t *testing.T) {
	// #98 merged all-non-build-target dev-dependency resolution; the fallback
	// must treat dev dependencies on source targets as proven edges.
	root, metadata := cargoFallbackFixture(t, map[string]any{
		"name": "core", "path": "core", "kind": "dev",
	})
	outcome, err := buildCargoFallbackOutcome(context.Background(), root, []FileInfo{
		{Path: "app/src/lib.rs"}, {Path: "core/src/lib.rs"},
	}, func(context.Context, string) ([]byte, error) {
		return metadata, nil
	})
	if err != nil {
		t.Fatalf("Cargo fallback rejected a dev dependency from a library target: %v", err)
	}
	if len(outcome.precomputedEdges) != 1 {
		t.Fatalf("precomputed edges = %#v, want 1", outcome.precomputedEdges)
	}
}
