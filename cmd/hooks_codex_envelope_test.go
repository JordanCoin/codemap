package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/watch"
)

// decodeSingleHookEnvelope asserts stdout holds exactly one JSON value and
// nothing else: no prefix, no trailing record, no ANSI.
func decodeSingleHookEnvelope(t *testing.T, stdout string) map[string]any {
	t.Helper()
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("Codex-mode stdout contains ANSI:\n%q", stdout)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(stdout)))
	var envelope map[string]any
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("Codex-mode stdout is not one JSON object: %v\n%q", err, stdout)
	}
	if decoder.More() {
		t.Fatalf("Codex-mode stdout holds more than one JSON record:\n%q", stdout)
	}
	return envelope
}

// TestCodexPostEditAlwaysEmitsOneJSONEnvelope is #156: Codex requires exactly
// one JSON response from a PostToolUse hook, and codemap answered a pathless
// apply_patch payload with nothing, which Codex reports as "hook returned
// invalid post-tool-use JSON output".
func TestCodexPostEditAlwaysEmitsOneJSONEnvelope(t *testing.T) {
	t.Setenv("CODEX", "1")
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "types.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 4,
		Importers: map[string][]string{"pkg/types.go": {"cmd/a.go", "cmd/b.go", "cmd/c.go"}},
		Imports: map[string][]string{
			"cmd/a.go": {"pkg/types.go"},
			"cmd/b.go": {"pkg/types.go"},
			"cmd/c.go": {"pkg/types.go"},
		},
	})

	t.Run("pathless apply_patch gets a no-op envelope", func(t *testing.T) {
		var hookErr error
		var stdout string
		withStdinInput(t, `{"tool_name":"apply_patch","tool_input":{},"tool_response":{}}`, func() {
			stdout = captureOutput(func() { hookErr = RunHook("post-edit", root) })
		})
		if hookErr != nil {
			t.Fatalf("RunHook(post-edit) = %v", hookErr)
		}
		envelope := decodeSingleHookEnvelope(t, stdout)
		if _, hasContext := envelope["hookSpecificOutput"]; hasContext {
			t.Fatalf("pathless edit injected context: %v", envelope)
		}
		if envelope["continue"] != true {
			t.Fatalf("no-op envelope = %v, want {\"continue\": true}", envelope)
		}
	})

	t.Run("file path keeps context inside the envelope and records provenance", func(t *testing.T) {
		quoted := strings.ReplaceAll(target, `\`, `\\`)
		payload := `{"session_id":"codex-envelope","tool_name":"apply_patch","tool_input":{"command":"*** Update File: ` + quoted + `\n@@\n-a\n+b\n"},"tool_response":{}}`
		var hookErr error
		var stdout string
		withStdinInput(t, payload, func() {
			stdout = captureOutput(func() { hookErr = RunHook("post-edit", root) })
		})
		if hookErr != nil {
			t.Fatalf("RunHook(post-edit) = %v", hookErr)
		}
		envelope := decodeSingleHookEnvelope(t, stdout)
		specific, _ := envelope["hookSpecificOutput"].(map[string]any)
		context, _ := specific["additionalContext"].(string)
		if specific["hookEventName"] != "PostToolUse" || !strings.Contains(context, "After editing: pkg/types.go still fans out to 3 importers.") {
			t.Fatalf("envelope lacks the post-edit context: %v", envelope)
		}
		edits := loadAgentEdits(root, time.Time{})
		if !edits.bySession["codex-envelope"]["pkg/types.go"] {
			t.Fatalf("Codex post-edit did not record provenance: %+v", edits.bySession)
		}
	})
}

// Claude mode is unchanged: a pathless payload prints nothing, a file path
// prints the plain context with no envelope.
func TestClaudePostEditKeepsPlainOutput(t *testing.T) {
	t.Setenv("CODEX", "")
	root := t.TempDir()
	var hookErr error
	var stdout string
	withStdinInput(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`, func() {
		stdout = captureOutput(func() { hookErr = RunHook("post-edit", root) })
	})
	if hookErr != nil {
		t.Fatalf("RunHook(post-edit) = %v", hookErr)
	}
	if stdout != "" {
		t.Fatalf("Claude-mode pathless post-edit printed %q, want nothing", stdout)
	}
}
