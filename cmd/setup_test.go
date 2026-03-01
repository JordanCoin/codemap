package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureClaudeHooksCreatesSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.local.json")

	result, err := ensureClaudeHooks(settingsPath, false)
	if err != nil {
		t.Fatalf("ensureClaudeHooks returned error: %v", err)
	}
	if !result.CreatedFile {
		t.Fatal("expected CreatedFile to be true for first write")
	}
	if !result.WroteFile {
		t.Fatal("expected WroteFile to be true for first write")
	}
	if result.AddedHooks != len(recommendedClaudeHooks) {
		t.Fatalf("expected %d added hooks, got %d", len(recommendedClaudeHooks), result.AddedHooks)
	}

	settings := readSettingsFile(t, settingsPath)
	hooks := readHooksMap(t, settings)
	for _, spec := range recommendedClaudeHooks {
		if !hasHookSpec(hooks[spec.Event], spec) {
			t.Fatalf("expected %q hook command in event %q", spec.Command, spec.Event)
		}
	}
}

func TestEnsureClaudeHooksPreservesFieldsAndAvoidsDuplicates(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}

	existing := `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "codemap hook session-start"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ensureClaudeHooks(settingsPath, false)
	if err != nil {
		t.Fatalf("ensureClaudeHooks returned error: %v", err)
	}
	if result.CreatedFile {
		t.Fatal("expected CreatedFile to be false for existing file")
	}
	if !result.WroteFile {
		t.Fatal("expected WroteFile to be true when new hooks were added")
	}
	if result.ExistingHooks != 1 {
		t.Fatalf("expected ExistingHooks=1, got %d", result.ExistingHooks)
	}
	if result.AddedHooks != len(recommendedClaudeHooks)-1 {
		t.Fatalf("expected %d added hooks, got %d", len(recommendedClaudeHooks)-1, result.AddedHooks)
	}

	second, err := ensureClaudeHooks(settingsPath, false)
	if err != nil {
		t.Fatalf("second ensureClaudeHooks returned error: %v", err)
	}
	if second.AddedHooks != 0 {
		t.Fatalf("expected second run to add 0 hooks, got %d", second.AddedHooks)
	}
	if second.ExistingHooks != len(recommendedClaudeHooks) {
		t.Fatalf("expected second run ExistingHooks=%d, got %d", len(recommendedClaudeHooks), second.ExistingHooks)
	}
	if second.WroteFile {
		t.Fatal("expected second run to avoid rewriting settings file")
	}

	settings := readSettingsFile(t, settingsPath)
	if got, ok := settings["theme"].(string); !ok || got != "dark" {
		t.Fatalf("expected top-level theme field to be preserved, got %#v", settings["theme"])
	}

	hooks := readHooksMap(t, settings)
	if len(hooks["SessionStart"]) == 0 {
		t.Fatal("expected SessionStart hooks to exist")
	}
	count := 0
	for _, entry := range hooks["SessionStart"] {
		for _, hook := range entry.Hooks {
			if hook.Command == "codemap hook session-start" {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one session-start codemap hook, got %d", count)
	}
}

func TestEnsureClaudeHooksRejectsInvalidJSON(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureClaudeHooks(settingsPath, false); err == nil {
		t.Fatal("expected error for malformed settings JSON")
	}
}

func TestEnsureClaudeHooksAddsMatcherScopedHookWhenMatcherMissing(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}

	existing := `{
  "hooks": {
    "SessionStart": [{"hooks":[{"type":"command","command":"codemap hook session-start"}]}],
    "PreToolUse": [{"hooks":[{"type":"command","command":"codemap hook pre-edit"}]}],
    "PostToolUse": [{"matcher":"Edit|Write","hooks":[{"type":"command","command":"codemap hook post-edit"}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"command","command":"codemap hook prompt-submit"}]}],
    "PreCompact": [{"hooks":[{"type":"command","command":"codemap hook pre-compact"}]}],
    "SessionEnd": [{"hooks":[{"type":"command","command":"codemap hook session-stop"}]}]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ensureClaudeHooks(settingsPath, false)
	if err != nil {
		t.Fatalf("ensureClaudeHooks returned error: %v", err)
	}
	if result.AddedHooks != 1 {
		t.Fatalf("expected exactly 1 added hook for missing matcher, got %d", result.AddedHooks)
	}
	if result.ExistingHooks != len(recommendedClaudeHooks)-1 {
		t.Fatalf("expected ExistingHooks=%d, got %d", len(recommendedClaudeHooks)-1, result.ExistingHooks)
	}

	settings := readSettingsFile(t, settingsPath)
	hooks := readHooksMap(t, settings)
	preToolUseEntries := hooks["PreToolUse"]

	foundRecommended := false
	for _, entry := range preToolUseEntries {
		if strings.TrimSpace(entry.Matcher) != "Edit|Write" {
			continue
		}
		for _, hook := range entry.Hooks {
			if strings.TrimSpace(hook.Command) == "codemap hook pre-edit" && strings.EqualFold(strings.TrimSpace(hook.Type), "command") {
				foundRecommended = true
			}
		}
	}
	if !foundRecommended {
		t.Fatal("expected setup to add matcher-scoped PreToolUse codemap hook")
	}
}

func readSettingsFile(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readHooksMap(t *testing.T, settings map[string]interface{}) map[string][]claudeHookEntry {
	t.Helper()
	hooksValue, ok := settings["hooks"]
	if !ok {
		t.Fatal("expected hooks key in settings")
	}
	raw, err := json.Marshal(hooksValue)
	if err != nil {
		t.Fatal(err)
	}
	hooks := make(map[string][]claudeHookEntry)
	if err := json.Unmarshal(raw, &hooks); err != nil {
		t.Fatal(err)
	}
	return hooks
}
