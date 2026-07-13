package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectLanguagesFromFiles_ManifestSignals(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "app.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "build.gradle.kts"), "plugins { kotlin(\"jvm\") }")
	mustWriteFile(t, filepath.Join(root, "Podfile"), "platform :ios, '13.0'")
	mustWriteFile(t, filepath.Join(root, "tsconfig.json"), "{}")
	mustWriteFile(t, filepath.Join(root, "Makefile"), "CC=gcc\nCXX=g++\n")
	mustWriteFile(t, filepath.Join(root, "packages", "ui", "package.json"), "{}")

	langs := detectLanguagesFromFiles(root)

	for _, want := range []string{"csharp", "kotlin", "java", "swift", "typescript", "javascript", "c", "cpp"} {
		if !langs[want] {
			t.Fatalf("expected %q to be detected, got %#v", want, langs)
		}
	}
}

func TestDetectLanguagesFromFiles_SubdirectorySources(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "src", "main.ts"), "export const n = 1")
	mustWriteFile(t, filepath.Join(root, "internal", "core", "worker.go"), "package core")

	langs := detectLanguagesFromFiles(root)

	if !langs["typescript"] {
		t.Fatalf("expected typescript from subdirectory source, got %#v", langs)
	}
	if !langs["go"] {
		t.Fatalf("expected go from subdirectory source, got %#v", langs)
	}
}

func TestBuildContextEnvelopeRespectsConfiguredFilters(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".codemap", "config.json"), `{"only":["go"],"exclude":["generated"]}`)
	mustWriteFile(t, filepath.Join(root, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, "schema.ts"), "export const schema = 1\n")
	mustWriteFile(t, filepath.Join(root, "generated", "hidden.go"), "package generated\n")

	cachedFileCount = -1
	t.Cleanup(func() { cachedFileCount = -1 })
	envelope := buildContextEnvelope(root, "", true)

	if envelope.Project.FileCount != 1 {
		t.Fatalf("file count = %d, want 1", envelope.Project.FileCount)
	}
	if !reflect.DeepEqual(envelope.Project.Languages, []string{"go"}) {
		t.Fatalf("languages = %#v, want [go]", envelope.Project.Languages)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
