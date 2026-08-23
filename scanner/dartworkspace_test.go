package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDartWorkspaceResolvesPackageAndRelativeImports(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"pubspec.yaml":                    "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n",
		"lib/main.dart":                   "",
		"lib/src/shared.dart":             "",
		"lib/src/widget.dart":             "",
		"other/src/shared.dart":           "",
		"packages/design/pubspec.yaml":    "name: design_system\n",
		"packages/design/lib/button.dart": "",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	analyses := []FileAnalysis{{
		Path: filepath.FromSlash("lib/main.dart"),
		Imports: []string{
			"package:app/src/widget.dart",
			"src/shared.dart",
			"package:design_system/button.dart",
			"package:flutter/material.dart",
			"dart:async",
		},
	}}
	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{Only: []string{"dart"}})
	if err != nil {
		t.Fatal(err)
	}

	got := append([]string(nil), graph.Imports[filepath.FromSlash("lib/main.dart")]...)
	sort.Strings(got)
	want := []string{
		filepath.FromSlash("lib/src/shared.dart"),
		filepath.FromSlash("lib/src/widget.dart"),
		filepath.FromSlash("packages/design/lib/button.dart"),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dart imports = %#v, want local package and relative targets %#v", got, want)
	}
}

func TestDartWorkspaceRejectsAmbiguousPackageNames(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"lib/main.dart",
		"packages/one/lib/api.dart",
		"packages/two/lib/api.dart",
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"packages/one/pubspec.yaml", "packages/two/pubspec.yaml"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("name: duplicate\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path:     filepath.FromSlash("lib/main.dart"),
		Language: "dart",
		Imports:  []string{"package:duplicate/api.dart"},
	}}, Filters{Only: []string{"dart"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports[filepath.FromSlash("lib/main.dart")]; len(got) != 0 {
		t.Fatalf("ambiguous Dart package import resolved to %#v", got)
	}
}
