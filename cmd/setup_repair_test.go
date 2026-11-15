package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairSubagentHook(t *testing.T) {
	t.Cleanup(func() { SessionStartNotice = "" })
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

	RepairSubagentHook(root, "claude")
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "hook subagent-start --integration=claude-setup") {
		t.Fatalf("SubagentStart not added:\n%s", got)
	}
	RepairSubagentHook(root, "claude")
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Fatal("second repair rewrote the file")
	}
	if strings.Count(SessionStartNotice, "\n") != 1 || !strings.Contains(SessionStartNotice, path) {
		t.Fatalf("notice = %q", SessionStartNotice)
	}
	if g, _ := os.ReadFile(globalPath); string(g) != foreign {
		t.Fatalf("unmanaged settings were touched:\n%s", g)
	}

	// Codex: same repair on its own hooks file, with the trust step named.
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	codexPath, _ := codexHooksPath(root, true)
	repoPath, _ := codexHooksPath(root, false)
	oldCodex := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"codemap hook session-start --agent=codex --integration=codex-setup"}]}]}}`
	// The project file may come from a cloned repo: it must stay untouched.
	for _, p := range []string{codexPath, repoPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(oldCodex), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	SessionStartNotice = ""
	RepairSubagentHook(root, "codex")
	if c, _ := os.ReadFile(codexPath); !strings.Contains(string(c), "hook subagent-start --agent=codex --integration=codex-setup") {
		t.Fatalf("Codex SubagentStart not added:\n%s", c)
	}
	if r, _ := os.ReadFile(repoPath); string(r) != oldCodex || strings.Contains(SessionStartNotice, repoPath) {
		t.Fatalf("project Codex hooks were repaired or named: %q\n%s", SessionStartNotice, r)
	}
	if !strings.Contains(SessionStartNotice, "/hooks in Codex") {
		t.Fatalf("notice = %q", SessionStartNotice)
	}
}
