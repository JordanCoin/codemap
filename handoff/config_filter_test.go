package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codemap/limits"
	"codemap/watch"
)

func TestConfiguredFileCountControlsHandoffBudgetsAndDependencies(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runCmd(t, root, "git", "init")
	write("go.mod", "module example\n\ngo 1.25\n")
	write("notes.md", "not configured source\n")
	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	write(".codemap/config.json", `{"only":["go"]}`)
	write("lib/lib.go", "package lib\n")
	write("cmd/main.go", "package main\n\nimport _ \"example/lib\"\n")
	for i := 0; i < 28; i++ {
		write(fmt.Sprintf("file%02d.go", i), "package example\n")
	}

	state := &watch.State{FileCount: limits.LargeRepoFileCount + 1}
	artifact, err := Build(root, BuildOptions{BaseRef: "HEAD", State: state})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if artifact.Prefix.FileCount != 30 {
		t.Fatalf("configured file count = %d, want 30", artifact.Prefix.FileCount)
	}
	if len(artifact.Delta.Changed) != 30 {
		t.Fatalf("configured small-repo budget kept %d changed files, want 30", len(artifact.Delta.Changed))
	}

	importers, _ := dependencyContextForFile(root, state, "lib/lib.go")
	if len(importers) != 1 || importers[0] != "cmd/main.go" {
		t.Fatalf("configured dependency importers = %v, want [cmd/main.go]", importers)
	}
}
