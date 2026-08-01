package scanner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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
