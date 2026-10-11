package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #109: Claude Code loads the first scope that DEFINES a codemap MCP
// server (project, then local, then user), valid or not. Doctor reported the
// first scope that VALIDATED, so a broken local entry was loaded by the agent
// while doctor said OK from the healthy user entry underneath it.
func TestRunDoctorReportsBrokenLocalMCPThatShadowsHealthyUserScope(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	originalVersionProbe := doctorVersionProbe
	originalMCPProbe := doctorMCPProbe
	t.Cleanup(func() {
		doctorLookPath = originalLook
		doctorVersionProbe = originalVersionProbe
		doctorMCPProbe = originalMCPProbe
	})
	doctorLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	doctorVersionProbe = func(doctorManagedLaunch) error { return nil }
	doctorMCPProbe = func(doctorManagedLaunch) error { return nil }

	binary := filepath.Join(home, "codemap")
	if runtimeIsWindows() {
		binary += ".exe"
	}
	if err := os.WriteFile(binary, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}
	healthy := map[string]any{
		"command": binary,
		"args":    managedMCPArgs(root, "1.0.0", "claude-setup"),
	}
	// The hand-wired local entry from the issue: a command with no args,
	// which the agent will load in preference to the user-scope entry.
	broken := map[string]any{"command": binary, "args": []string{}}
	payload := map[string]any{
		"mcpServers": map[string]any{"codemap": healthy},
		"projects": map[string]any{
			root: map[string]any{"mcpServers": map[string]any{"codemap": broken}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal claude.json: %v", err)
	}
	writeHookSettings(t, filepath.Join(home, ".claude.json"), string(data))

	out := captureOutput(func() { RunDoctor([]string{"--agent", "claude", root}, ".") })

	if strings.Contains(out, "OK   Claude MCP") {
		t.Fatalf("doctor reported Claude MCP OK while the effective local entry is broken:\n%s", out)
	}
	if !strings.Contains(out, "MISS Claude MCP") || !strings.Contains(out, "local scope") {
		t.Fatalf("doctor did not report the broken local scope as the effective registration:\n%s", out)
	}
	if !strings.Contains(out, "shadows healthy") || !strings.Contains(out, "user scope") {
		t.Fatalf("doctor did not name the healthy user-scope entry being shadowed:\n%s", out)
	}
}
