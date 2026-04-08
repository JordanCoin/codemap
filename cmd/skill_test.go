package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSkillUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "unknown subcommand", args: []string{"unknown"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureOutput(func() {
				RunSkill(tt.args, t.TempDir())
			})
			if !strings.Contains(out, "Usage: codemap skill <list|show|init>") {
				t.Fatalf("expected usage output, got:\n%s", out)
			}
		})
	}
}

func TestRunSkillListIncludesProjectSkill(t *testing.T) {
	root := t.TempDir()
	writeProjectSkill(t, root, "test-list-skill", "list description", []string{"automation"})

	out := captureOutput(func() {
		runSkillList(root)
	})

	checks := []string{
		"Available skills",
		"test-list-skill",
		"[project]",
		"list description",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRunSkillShowPrintsSkillBody(t *testing.T) {
	root := t.TempDir()
	writeProjectSkill(t, root, "show-skill", "show description", []string{"go"})

	out := captureOutput(func() {
		runSkillShow(root, "show-skill")
	})

	checks := []string{
		"# show-skill",
		"Source: project",
		"Description: show description",
		"Keywords: go",
		"# Body for show-skill",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRunSkillInit(t *testing.T) {
	root := t.TempDir()
	wantPath := filepath.Join(root, ".codemap", "skills", "my-skill.md")

	tests := []struct {
		name          string
		setup         func(t *testing.T)
		wantContains  string
		wantFileExist bool
	}{
		{
			name:          "creates template when missing",
			setup:         func(t *testing.T) {},
			wantContains:  "Created skill template",
			wantFileExist: true,
		},
		{
			name: "reports existing template",
			setup: func(t *testing.T) {
				if err := os.MkdirAll(filepath.Dir(wantPath), 0o755); err != nil {
					t.Fatalf("mkdir skills dir: %v", err)
				}
				if err := os.WriteFile(wantPath, []byte("existing"), 0o644); err != nil {
					t.Fatalf("write existing template: %v", err)
				}
			},
			wantContains:  "Skill template already exists",
			wantFileExist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatalf("reset root: %v", err)
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatalf("mkdir root: %v", err)
			}

			tt.setup(t)
			out := captureOutput(func() {
				runSkillInit(root)
			})

			if !strings.Contains(out, tt.wantContains) {
				t.Fatalf("expected output to contain %q, got:\n%s", tt.wantContains, out)
			}

			_, err := os.Stat(wantPath)
			if tt.wantFileExist && err != nil {
				t.Fatalf("expected template file to exist at %s: %v", wantPath, err)
			}
		})
	}
}

func writeProjectSkill(t *testing.T, root, name, description string, keywords []string) {
	t.Helper()

	dir := filepath.Join(root, ".codemap", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}

	content := "---\n"
	content += "name: " + name + "\n"
	content += "description: " + description + "\n"
	content += "keywords:\n"
	for _, kw := range keywords {
		content += "  - " + kw + "\n"
	}
	content += "---\n\n"
	content += "# Body for " + name + "\n"

	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
}
