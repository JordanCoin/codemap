package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSubagentFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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
	return root
}

// runSubagentStart runs the hook for root and returns its additionalContext.
func runSubagentStart(t *testing.T, root string) string {
	t.Helper()
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
	if got := envelope.HookSpecificOutput.HookEventName; got != "SubagentStart" {
		t.Fatalf("hookEventName = %q", got)
	}
	ctx := envelope.HookSpecificOutput.AdditionalContext
	if ctx == "" || len(ctx) > 2500 {
		t.Fatalf("additionalContext length %d, want 1..2500:\n%s", len(ctx), ctx)
	}
	return ctx
}

func repoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestHookSubagentStartOrientsWithinCap(t *testing.T) {
	files := map[string]string{
		"go.mod":       "module example.com/demo\n\ngo 1.22\n",
		"pkg/types.go": "package pkg\n\nconst X = 1\n",
		"u1/u.go":      "package u1\n",
	}
	for _, name := range []string{"a", "b", "c"} {
		files["cmd/"+name+"/main.go"] = "package main\n\nimport \"example.com/demo/pkg\"\n\nvar _ = pkg.X\n"
	}
	root := writeSubagentFixture(t, files)
	before := repoFiles(t, root)

	ctx := runSubagentStart(t, root)
	for _, want := range []string{"Hubs (", "pkg/types.go", "cmd/", "u1/"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("additionalContext missing %q:\n%s", want, ctx)
		}
	}
	// Directories only: no file names (a single-file dir included), no header panel.
	for _, gone := range []string{"main.go", "u.go", "╭"} {
		if strings.Contains(ctx, gone) {
			t.Fatalf("additionalContext should not contain %q:\n%s", gone, ctx)
		}
	}
	if after := repoFiles(t, root); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("hook wrote into a repo that never ran setup:\nbefore %v\nafter  %v", before, after)
	}

	outside, _ := json.Marshal(map[string]string{"cwd": t.TempDir()})
	out, stderr, err := runCodemapWithInput(string(outside), "hook", "subagent-start")
	if err != nil || out != "" {
		t.Fatalf("outside a repo: err=%v stdout=%q stderr=%q, want silent exit 0", err, out, stderr)
	}
}

func TestHookSubagentStartKeepsHubsWhenDirectoriesOverflow(t *testing.T) {
	files := map[string]string{
		"go.mod":     "module example.com/wide\n\ngo 1.22\n",
		"lib/lib.go": "package lib\n\nconst X = 1\n",
	}
	for i := 0; i < 65; i++ {
		files[fmt.Sprintf("directory_with_a_long_name_%02d/main.go", i)] = "package main\n\nimport \"example.com/wide/lib\"\n\nvar _ = lib.X\n"
	}
	ctx := runSubagentStart(t, writeSubagentFixture(t, files))
	for _, want := range []string{"more directories", "Hubs (", "lib/", "Next:"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("over-cap block missing %q:\n%s", want, ctx)
		}
	}
}
