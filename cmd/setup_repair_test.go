package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairClaudeSubagentHook(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, _ := claudeSettingsPath(root, false)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Settings as an older codemap setup wrote them: no SubagentStart.
	old := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"codemap hook session-start --integration=claude-setup"}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	// A settings file codemap does not manage must stay byte-identical.
	globalPath, _ := claudeSettingsPath(root, true)
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := `{"hooks":{}}`
	if err := os.WriteFile(globalPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	RepairClaudeSubagentHook(root)
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "hook subagent-start --integration=claude-setup") {
		t.Fatalf("SubagentStart not added:\n%s", got)
	}
	RepairClaudeSubagentHook(root)
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Fatal("second repair rewrote the file")
	}
	if g, _ := os.ReadFile(globalPath); string(g) != foreign {
		t.Fatalf("unmanaged settings were touched:\n%s", g)
	}
}
