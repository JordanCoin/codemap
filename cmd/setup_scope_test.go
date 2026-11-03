package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	return home
}

func TestRunSetupMovesProjectCodexEntriesToUserScope(t *testing.T) {
	home := isolateHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	captureOutput(func() { RunSetup([]string{"--agent", "codex", "--project", "--no-config", root}, ".") })
	configPath := filepath.Join(root, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	foreign := "\n[mcp_servers.other]\ncommand = \"other\"\n"
	if err := os.WriteFile(configPath, append(data, foreign...), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { RunSetup([]string{"--agent", "codex", "--no-config", root}, ".") })
	if !strings.Contains(out, "moved codemap's entries from") {
		t.Fatalf("expected move notice:\n%s", out)
	}
	hooks, _ := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if strings.Contains(string(hooks), " hook ") {
		t.Fatalf("project hooks still hold codemap commands:\n%s", hooks)
	}
	config, _ := os.ReadFile(configPath)
	if strings.Contains(string(config), "mcp_servers.codemap") || !strings.Contains(string(config), "[mcp_servers.other]") {
		t.Fatalf("want only the codemap table removed:\n%s", config)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); err != nil {
		t.Fatalf("user-scope hooks missing: %v", err)
	}
}

func TestRunDoctorFlagsCodexHooksInBothScopes(t *testing.T) {
	home := isolateHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	captureOutput(func() { RunSetup([]string{"--agent", "codex", "--project", "--no-config", "--no-mcp", root}, ".") })
	data, err := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	prevLook, prevCodex := doctorLookPath, runCodexPluginCommand
	doctorLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	runCodexPluginCommand = func(string, ...string) ([]byte, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { doctorLookPath, runCodexPluginCommand = prevLook, prevCodex })

	out := captureOutput(func() { RunDoctor([]string{"--agent", "codex", root}, ".") })
	if !strings.Contains(out, "run twice in a trusted project") {
		t.Fatalf("doctor did not flag duplicate Codex hooks:\n%s", out)
	}
}

func TestCodexUserScopeHonorsCODEX_HOME(t *testing.T) {
	isolateHome(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureOutput(func() { RunSetup([]string{"--agent", "codex", "--no-config", root}, ".") })
	if !strings.Contains(out, "configured for your user in "+codexHome) {
		t.Fatalf("setup did not print the CODEX_HOME path:\n%s", out)
	}
	for _, name := range []string{"hooks.json", "config.toml"} {
		if _, err := os.Stat(filepath.Join(codexHome, name)); err != nil {
			t.Fatalf("%s not written under CODEX_HOME: %v", name, err)
		}
	}
	if entries := hostCodemapMCPEntries(root, "codex"); len(entries) == 0 || entries[0].Path != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("hooks did not read the CODEX_HOME config: %+v", entries)
	}
}

func TestRunSetupExcludesWrittenSkillDirsFromGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	isolateHome(t)
	root := makeGitRepo(t)
	captureOutput(func() {
		RunSetup([]string{"--agent", "claude", "--no-config", "--no-hooks", "--no-mcp", root}, ".")
	})
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	status.Dir = root
	out, err := status.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), ".claude/skills") {
		t.Fatalf("skill dirs show in git status:\n%s", out)
	}
	exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if !strings.Contains(string(exclude), "/.claude/skills/hub-safety/") || strings.Contains(string(exclude), "\n/.claude/\n") {
		t.Fatalf("want per-skill rules only:\n%s", exclude)
	}
}
