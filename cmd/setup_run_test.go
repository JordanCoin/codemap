package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSettingsPath(t *testing.T) {
	root := t.TempDir()
	local, err := claudeSettingsPath(root, false)
	if err != nil {
		t.Fatalf("local claudeSettingsPath error: %v", err)
	}
	if local != filepath.Join(root, ".claude", "settings.local.json") {
		t.Fatalf("unexpected local settings path: %s", local)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	global, err := claudeSettingsPath(root, true)
	if err != nil {
		t.Fatalf("global claudeSettingsPath error: %v", err)
	}
	if global != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("unexpected global settings path: %s", global)
	}
}

func TestRunSetupNoConfigNoHooks(t *testing.T) {
	root := t.TempDir()
	out := captureOutput(func() { RunSetup([]string{"--no-config", "--no-hooks", root}, ".") })

	checks := []string{
		"codemap setup",
		"Config: skipped (--no-config)",
		"Hooks: skipped (--no-hooks)",
		"Next:",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRunSetupCreatesConfigAndHooks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := captureOutput(func() { RunSetup([]string{root}, ".") })
	if !strings.Contains(first, "Config: created") || !strings.Contains(first, "Hooks: created") {
		t.Fatalf("unexpected first setup output:\n%s", first)
	}

	cfgPath := filepath.Join(root, ".codemap", "config.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
	settingsPath := filepath.Join(root, ".claude", "settings.local.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("expected settings file to exist: %v", err)
	}

	second := captureOutput(func() { RunSetup([]string{root}, ".") })
	if !strings.Contains(second, "Config: already exists") || !strings.Contains(second, "Hooks: already configured") {
		t.Fatalf("unexpected second setup output:\n%s", second)
	}

	settings := readSettingsFile(t, settingsPath)
	hooks := readHooksMap(t, settings)
	if len(hooks) == 0 {
		t.Fatal("expected hooks to be configured")
	}
}
