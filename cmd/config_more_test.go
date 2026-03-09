package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/config"
)

func TestInitProjectConfigCreatesAutoDetectedConfig(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "main.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "worker.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "extra.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "web", "app.ts"), "export const app = 1\n")
	mustWriteConfigFixture(t, filepath.Join(root, "web", "view.ts"), "export const view = 1\n")
	mustWriteConfigFixture(t, filepath.Join(root, "scripts", "seed.py"), "print('ok')\n")
	mustWriteConfigFixture(t, filepath.Join(root, "README.md"), "docs\n")
	mustWriteConfigFixture(t, filepath.Join(root, "assets", "logo.png"), "png\n")

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig returned error: %v", err)
	}

	if got, want := strings.Join(result.TopExts, ","), "go,ts,py"; got != want {
		t.Fatalf("TopExts = %q, want %q", got, want)
	}
	if result.TotalFiles != 8 {
		t.Fatalf("TotalFiles = %d, want 8", result.TotalFiles)
	}
	if result.MatchedFiles != 6 {
		t.Fatalf("MatchedFiles = %d, want 6", result.MatchedFiles)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got, want := strings.Join(cfg.Only, ","), "go,ts,py"; got != want {
		t.Fatalf("config only = %q, want %q", got, want)
	}
}

func TestInitProjectConfigReturnsErrConfigExists(t *testing.T) {
	root := t.TempDir()
	cfgPath := config.ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := initProjectConfig(root)
	if !errors.Is(err, errConfigExists) {
		t.Fatalf("expected errConfigExists, got %v", err)
	}
}

func TestRunConfigInitAndShow(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, "cmd", "main.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "cmd", "worker.go"), "package main\n")

	initOut := captureOutput(func() { RunConfig("init", root) })
	if !strings.Contains(initOut, "Created ") || !strings.Contains(initOut, "only: go") {
		t.Fatalf("unexpected init output:\n%s", initOut)
	}

	showOut := captureOutput(func() { RunConfig("show", root) })
	checks := []string{"Config:", "only:", "go"}
	for _, check := range checks {
		if !strings.Contains(showOut, check) {
			t.Fatalf("expected show output to contain %q, got:\n%s", check, showOut)
		}
	}
}

func TestConfigShowNoConfigFile(t *testing.T) {
	out := captureOutput(func() { configShow(t.TempDir()) })
	if !strings.Contains(out, "No config file found.") {
		t.Fatalf("expected missing-config guidance, got:\n%s", out)
	}
}

func mustWriteConfigFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
