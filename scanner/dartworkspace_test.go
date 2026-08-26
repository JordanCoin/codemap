package scanner

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDartWorkspaceResolvesPackageAndRelativeImports(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"pubspec.yaml":                    "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n  design_system: any\n",
		"lib/main.dart":                   "",
		"lib/src/shared.dart":             "",
		"lib/src/widget.dart":             "",
		"other/src/shared.dart":           "",
		"packages/design/pubspec.yaml":    "name: design_system\n",
		"packages/design/lib/button.dart": "",
		"tool/x.dart":                     "",
	})

	analyses := []FileAnalysis{{
		Path:     filepath.FromSlash("lib/main.dart"),
		Language: "dart",
		Imports: []string{
			"package:app/src/widget.dart",
			"src/shared.dart",
			"package:design_system/button.dart",
			"package:flutter/material.dart",
			"dart:async",
			"package:app/../tool/x.dart",
			`package:app/..\tool\x.dart`,
		},
	}}
	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{Only: []string{"dart"}})
	if err != nil {
		t.Fatal(err)
	}

	assertWorkspaceImports(t, graph, "lib/main.dart", []string{
		filepath.FromSlash("lib/src/shared.dart"),
		filepath.FromSlash("lib/src/widget.dart"),
		filepath.FromSlash("packages/design/lib/button.dart"),
	})
}

func TestDartWorkspaceRequiresDeclaredPackageDependency(t *testing.T) {
	for _, field := range []string{"dependencies", "dev_dependencies", "dependency_overrides"} {
		t.Run(field, func(t *testing.T) {
			root := t.TempDir()
			writeRustCargoFixture(t, root, map[string]string{
				"pubspec.yaml":                       "name: app\n" + field + ":\n  shared: any\n",
				"lib/main.dart":                      "",
				"packages/shared/pubspec.yaml":       "name: shared\n",
				"packages/shared/lib/api.dart":       "",
				"third_party/unrelated/pubspec.yaml": "name: unrelated\n",
				"third_party/unrelated/lib/api.dart": "",
			})

			graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
				Path:     filepath.FromSlash("lib/main.dart"),
				Language: "dart",
				Imports: []string{
					"package:shared/api.dart",
					"package:unrelated/api.dart",
				},
			}}, Filters{Only: []string{"dart"}})
			if err != nil {
				t.Fatal(err)
			}

			assertWorkspaceImports(t, graph, "lib/main.dart", []string{
				filepath.FromSlash("packages/shared/lib/api.dart"),
			})
		})
	}
}

func TestDartWorkspaceRejectsAmbiguousPackageNames(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"pubspec.yaml":                "name: app\ndependencies:\n  duplicate: any\n  unique: any\n",
		"lib/main.dart":               "",
		"packages/one/pubspec.yaml":   "name: duplicate\n",
		"packages/one/lib/api.dart":   "",
		"packages/two/pubspec.yaml":   "name: duplicate\n",
		"packages/two/lib/api.dart":   "",
		"packages/three/pubspec.yaml": "name: unique\n",
		"packages/three/lib/api.dart": "",
	})

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path:     filepath.FromSlash("lib/main.dart"),
		Language: "dart",
		Imports: []string{
			"package:duplicate/api.dart",
			"package:unique/api.dart",
		},
	}}, Filters{Only: []string{"dart"}})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceImports(t, graph, "lib/main.dart", []string{
		filepath.FromSlash("packages/three/lib/api.dart"),
	})
}
