package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseHookFilePathsAcceptsHostPayloadShapes pins the payload shapes the
// hooks actually receive. Claude Code nests the path under tool_input for
// Edit/Write/NotebookEdit; only a legacy shape puts it at the top level. Missing
// the nested form silently disables both agent-edit provenance and the
// post-edit blast-radius report, because the parser returns no paths and the
// hook returns early.
func TestParseHookFilePathsAcceptsHostPayloadShapes(t *testing.T) {
	target := filepath.Join("cmd", "doctor.go")
	quoted := strings.ReplaceAll(target, `\`, `\\`)

	for _, tt := range []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "legacy top-level file_path",
			input: `{"session_id":"s1","file_path":"` + quoted + `"}`,
			want:  []string{target},
		},
		{
			name:  "Claude Edit tool_input.file_path",
			input: `{"session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"` + quoted + `"}}`,
			want:  []string{target},
		},
		{
			name:  "Claude Write tool_input.file_path",
			input: `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"` + quoted + `","content":"x"}}`,
			want:  []string{target},
		},
		{
			name:  "Claude NotebookEdit tool_input.notebook_path",
			input: `{"session_id":"s1","tool_name":"NotebookEdit","tool_input":{"notebook_path":"` + quoted + `"}}`,
			want:  []string{target},
		},
		{
			name:  "Codex apply_patch tool_input.command",
			input: `{"session_id":"s1","tool_input":{"command":"*** Update File: ` + quoted + `\n@@\n-a\n+b\n"}}`,
			want:  []string{target},
		},
		{
			name:  "no path present",
			input: `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			want:  nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHookFilePaths([]byte(tt.input))
			if len(got) != len(tt.want) {
				t.Fatalf("parseHookFilePaths() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseHookFilePaths()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestHookPostEditRecordsAgentEditForNestedPayload covers the whole path rather
// than just the parser: the parser was arguably fine, it simply never saw the
// shape it needed, so the regression has to be pinned end to end.
func TestHookPostEditRecordsAgentEditForNestedPayload(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	quoted := strings.ReplaceAll(target, `\`, `\\`)
	payload := `{"session_id":"nested-session","tool_name":"Edit","tool_input":{"file_path":"` + quoted + `"}}`

	withStdinInput(t, payload, func() {
		if err := hookPostEdit(root); err != nil {
			t.Fatalf("hookPostEdit() = %v", err)
		}
	})

	edits := loadAgentEdits(root, time.Time{})
	if !edits.paths[normalizeAgentEditPath(root, target)] {
		t.Fatalf("expected agent edit recorded for %s, got %v", target, edits.paths)
	}
}
