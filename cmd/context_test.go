package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codemap/watch"
)

func TestRunContextPreservesExplicitSubtree(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "pkg")
	mustWriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWriteFile(t, filepath.Join(nested, "main.go"), "package pkg\n")

	var envelope ContextEnvelope
	out := captureOutput(func() { RunContext([]string{nested}, root) })
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Project.Root != nested {
		t.Fatalf("context root = %q, want explicit subtree %q", envelope.Project.Root, nested)
	}
}

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

	envelope := buildContextEnvelope(root, "", true)

	if envelope.Project.FileCount != 1 {
		t.Fatalf("file count = %d, want 1", envelope.Project.FileCount)
	}
	if !reflect.DeepEqual(envelope.Project.Languages, []string{"go"}) {
		t.Fatalf("languages = %#v, want [go]", envelope.Project.Languages)
	}
}

func TestBuildContextEnvelopeFallsBackToConfiguredScanForLegacyState(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".codemap", "config.json"), `{"only":["go"]}`)
	mustWriteFile(t, filepath.Join(root, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, "notes.txt"), "not source\n")

	legacyState, err := json.Marshal(watch.State{
		UpdatedAt: time.Now(),
		FileCount: 999,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, ".codemap", "state.json"), string(legacyState))

	envelope := buildContextEnvelope(root, "", true)

	if envelope.Project.FileCount != 1 {
		t.Fatalf("file count = %d, want configured source count 1", envelope.Project.FileCount)
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
