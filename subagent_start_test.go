package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookSubagentStartOrientsWithinCap(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/demo\n\ngo 1.22\n",
		"pkg/types.go": "package pkg\n\nconst X = 1\n",
	}
	for _, name := range []string{"a", "b", "c"} {
		files["cmd/"+name+"/main.go"] = "package main\n\nimport \"example.com/demo/pkg\"\n\nvar _ = pkg.X\n"
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitMainTestCmd(t, root, "init", "-q")

	payload, _ := json.Marshal(map[string]string{"cwd": root, "agent_type": "scout", "hook_event_name": "SubagentStart"})
	out, stderr, err := runCodemapWithInput(string(payload), "hook", "subagent-start", t.TempDir())
	if err != nil {
		t.Fatalf("subagent-start failed: %v\n%s", err, stderr)
	}
	var envelope struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, out)
	}
	got := envelope.HookSpecificOutput
	if got.HookEventName != "SubagentStart" {
		t.Fatalf("hookEventName = %q", got.HookEventName)
	}
	ctx := got.AdditionalContext
	if ctx == "" || len(ctx) > 2500 {
		t.Fatalf("additionalContext length %d, want 1..2500:\n%s", len(ctx), ctx)
	}
	for _, want := range []string{"Hubs (", "pkg/types.go", "cmd/"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("additionalContext missing %q:\n%s", want, ctx)
		}
	}
	// Directories only: no file names, no header panel.
	for _, gone := range []string{"main.go", "╭"} {
		if strings.Contains(ctx, gone) {
			t.Fatalf("additionalContext should not contain %q:\n%s", gone, ctx)
		}
	}

	outside, _ := json.Marshal(map[string]string{"cwd": t.TempDir()})
	out, stderr, err = runCodemapWithInput(string(outside), "hook", "subagent-start")
	if err != nil || out != "" {
		t.Fatalf("outside a repo: err=%v stdout=%q stderr=%q, want silent exit 0", err, out, stderr)
	}
}
