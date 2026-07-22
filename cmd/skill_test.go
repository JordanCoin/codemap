package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSkill_PrintsUsageForUnknownOrMissingSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "unknown subcommand", args: []string{"bogus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureOutput(func() {
				RunSkill(tt.args, t.TempDir())
			})

			checks := []string{
				"Usage: codemap skill <list|show|init>",
				"Commands:",
				"list",
				"show <name>",
				"init",
			}
			for _, check := range checks {
				if !strings.Contains(out, check) {
					t.Fatalf("expected output to contain %q, got:\n%s", check, out)
				}
			}
		})
	}
}

func TestRunSkillList_PrintsBuiltinSkills(t *testing.T) {
	root := t.TempDir()

	out := captureOutput(func() {
		runSkillList(root)
	})

	checks := []string{
		"Available skills",
		"[builtin]",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRunSkillShow_PrintsBuiltinSkillDetails(t *testing.T) {
	root := t.TempDir()

	out := captureOutput(func() {
		runSkillShow(root, "explore")
	})

	checks := []string{
		"# explore",
		"Source: builtin",
		"Description:",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRunSkillInit_CreatesTemplateAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".codemap", "skills", "my-skill.md")

	tests := []struct {
		name         string
		run          func() string
		wantContains []string
	}{
		{
			name: "first run creates template",
			run: func() string {
				return captureOutput(func() {
					runSkillInit(root)
				})
			},
			wantContains: []string{
				"Created skill template",
				"Run 'codemap skill list'",
			},
		},
		{
			name: "second run reports already exists",
			run: func() string {
				return captureOutput(func() {
					runSkillInit(root)
				})
			},
			wantContains: []string{
				"Skill template already exists",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.run()
			for _, check := range tt.wantContains {
				if !strings.Contains(out, check) {
					t.Fatalf("expected output to contain %q, got:\n%s", check, out)
				}
			}
		})
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected template file at %s: %v", path, err)
	}
	if !strings.Contains(string(data), "name: my-skill") {
		t.Fatalf("expected template content in %s, got:\n%s", path, string(data))
	}
}
