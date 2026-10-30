package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user-scope file that exists but lacks the codemap entry is the problem to
// report; the absent project-scope file in front of it must not hide it.
func TestCheckScopedFileNamesExistingUndefinedScopeOverAbsentOne(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.json")
	if err := os.WriteFile(userPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	scopes := []doctorScope{
		{name: "project", path: filepath.Join(dir, "project.json")},
		{name: "user", path: userPath},
	}
	validate := func(path string) error {
		if _, err := os.Stat(path); err != nil {
			return err
		}
		return fmt.Errorf("missing SessionEnd hook: %w", errScopeUndefined)
	}

	failures := 0
	out := captureOutput(func() { checkScopedFile("Codex hooks", scopes, validate, &failures) })
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}
	line := doctorLineFor(t, out, "MISS Codex hooks")
	for _, want := range []string{userPath, "user scope", "missing SessionEnd hook"} {
		if !strings.Contains(line, want) {
			t.Fatalf("MISS line lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "project.json") {
		t.Fatalf("MISS line named the absent project scope instead:\n%s", line)
	}
}

// The plugin manifest belongs to `codemap plugin install`; an older bundle's
// bash-wrapper entry must point there, never at `codemap setup`.
func TestValidateCodexPluginMCPRepairHintIsPluginInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	manifest := `{"mcpServers":{"codemap":{"command":"/bin/bash","args":["./scripts/run-codemap-mcp.sh"]}}}`
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validateCodexPluginMCP(path)
	if err == nil {
		t.Fatal("bash wrapper entry must be reported as unrecognized")
	}
	if !strings.Contains(err.Error(), "repair with `codemap plugin install`") || strings.Contains(err.Error(), "setup --agent") {
		t.Fatalf("wrong repair hint: %v", err)
	}
}
