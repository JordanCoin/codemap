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
	want := recommendedClaudeHooks[0].Command
	count := 0
	for _, entry := range hooks["SessionStart"] {
		for _, hook := range entry.Hooks {
			if hook.Command == want {
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

	var recommended claudeHookSpec
	for _, spec := range recommendedClaudeHooks {
		if spec.Event == "PreToolUse" {
			recommended = spec
			break
		}
	}
	foundRecommended := false
	for _, entry := range preToolUseEntries {
		if hasHookSpec([]claudeHookEntry{entry}, recommended) {
			foundRecommended = true
		}
	}
	if !foundRecommended {
		t.Fatal("expected setup to add matcher-scoped PreToolUse codemap hook")
	}
}

func TestEnsureCodexHooksReconcilesMixedLegacyAndCanonicalConfig(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".codex", "hooks.json")
	executable := "/tmp/codemap-test"
	specs := generatedCodexHooks(executable)
	fixture := map[string]any{
		"unknown": json.Number("9007199254740993123"),
		"hooks": map[string]any{
			"SessionStart":     []any{hookTestEntry("", "codemap hook session-start"), hookTestEntry("", specs[0].Command)},
			"UserPromptSubmit": []any{hookTestEntry("", "codemap hook prompt-submit"), hookTestEntry("", specs[3].Command)},
			"PreCompact":       []any{hookTestEntry("", "codemap hook pre-compact"), hookTestEntry("", specs[4].Command)},
			"PreToolUse":       []any{hookTestEntry("Edit|Write", "codemap hook pre-edit"), hookTestEntry("apply_patch|Edit|Write", specs[1].Command)},
			"PostToolUse": []any{
				hookTestEntry("Edit|Write", "codemap hook post-edit"),
				map[string]any{"matcher": "apply_patch|Edit|Write", "hooks": []any{
					map[string]any{"type": "command", "command": specs[2].Command},
					map[string]any{"type": "command", "command": "screenshot --capture"},
				}},
			},
			"SessionEnd": []any{hookTestEntry("", "codemap hook session-stop")},
			"Stop":       []any{hookTestEntry("", specs[5].Command)},
		},
	}
	writeCodexHookFixture(t, settingsPath, fixture)

	result, err := ensureCodexHooksWithExecutable(settingsPath, false, executable)
	if err != nil {
		t.Fatalf("ensureCodexHooksWithExecutable: %v", err)
	}
	if result.AddedHooks != 0 || result.RemovedHooks != 6 || !result.WroteFile {
		t.Fatalf("result = %+v, want AddedHooks=0 RemovedHooks=6 WroteFile=true", result)
	}
	settings := readSettingsFile(t, settingsPath)
	precisionSettings, err := decodeJSONObject(mustReadFile(t, settingsPath))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := precisionSettings["unknown"].(json.Number); !ok || got.String() != "9007199254740993123" {
		t.Fatalf("unknown precision changed: %#v", precisionSettings["unknown"])
	}
	assertCodexCanonicalHooks(t, readHooksMap(t, settings), specs)
	if entries := readHooksMap(t, settings)["SessionEnd"]; len(entries) != 0 {
		for _, entry := range entries {
			for _, hook := range entry.Hooks {
				if strings.Contains(hook.Command, "codemap") {
					t.Fatalf("legacy SessionEnd Codemap hook remains: %q", hook.Command)
				}
			}
		}
	}
	if !hookCommandExists(readHooksMap(t, settings)["PostToolUse"], "screenshot --capture") {
		t.Fatal("unrelated screenshot hook was not preserved")
	}

	second, err := ensureCodexHooksWithExecutable(settingsPath, false, executable)
	if err != nil {
		t.Fatalf("second ensureCodexHooksWithExecutable: %v", err)
	}
	if second.AddedHooks != 0 || second.RemovedHooks != 0 || second.WroteFile {
		t.Fatalf("second result = %+v, want zero additions/removals and no write", second)
	}
}

func TestEnsureCodexHooksMigratesLegacyOnlyConfig(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".codex", "hooks.json")
	executable := "/tmp/codemap-test"
	specs := generatedCodexHooks(executable)
	fixture := map[string]any{"hooks": map[string]any{
		"SessionStart":     []any{hookTestEntry("", "codemap hook session-start")},
		"PreToolUse":       []any{hookTestEntry("Edit|Write", "codemap hook pre-edit")},
		"PostToolUse":      []any{hookTestEntry("Edit|Write", "codemap hook post-edit")},
		"UserPromptSubmit": []any{hookTestEntry("", "codemap hook prompt-submit")},
		"PreCompact":       []any{hookTestEntry("", "codemap hook pre-compact")},
		"SessionEnd":       []any{hookTestEntry("", "codemap hook session-stop")},
	}}
	writeCodexHookFixture(t, settingsPath, fixture)

	result, err := ensureCodexHooksWithExecutable(settingsPath, false, executable)
	if err != nil {
		t.Fatalf("ensureCodexHooksWithExecutable: %v", err)
	}
	if result.AddedHooks != 6 || result.RemovedHooks != 6 || !result.WroteFile {
		t.Fatalf("result = %+v, want AddedHooks=6 RemovedHooks=6 WroteFile=true", result)
	}
	settings := readSettingsFile(t, settingsPath)
	assertCodexCanonicalHooks(t, readHooksMap(t, settings), specs)
	if len(readHooksMap(t, settings)["SessionEnd"]) != 0 {
		t.Fatal("legacy SessionEnd event was not removed")
	}
	second, err := ensureCodexHooksWithExecutable(settingsPath, false, executable)
	if err != nil {
		t.Fatalf("second ensureCodexHooksWithExecutable: %v", err)
	}
	if second.AddedHooks != 0 || second.RemovedHooks != 0 || second.WroteFile {
		t.Fatalf("second result = %+v, want zero additions/removals and no write", second)
	}
}

func hookTestEntry(matcher, command string) map[string]any {
	entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	return entry
}

func writeCodexHookFixture(t *testing.T, path string, fixture map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCodexCanonicalHooks(t *testing.T, hooks map[string][]claudeHookEntry, specs []claudeHookSpec) {
	t.Helper()
	for _, spec := range specs {
		count := 0
		for _, entry := range hooks[spec.Event] {
			if strings.TrimSpace(entry.Matcher) != strings.TrimSpace(spec.Matcher) {
				continue
			}
			for _, hook := range entry.Hooks {
				if strings.TrimSpace(hook.Command) == spec.Command {
					count++
				}
			}
		}
		if count != 1 {
			t.Errorf("canonical %s hook count = %d, want 1", spec.Event, count)
		}
	}
	for event, entries := range hooks {
		for _, entry := range entries {
			for _, hook := range entry.Hooks {
				if strings.Contains(hook.Command, "codemap hook") && !strings.Contains(hook.Command, "--integration=codex-setup") {
					t.Errorf("legacy Codemap command remains in %s: %q", event, hook.Command)
				}
			}
		}
	}
}

func hookCommandExists(entries []claudeHookEntry, command string) bool {
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
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
