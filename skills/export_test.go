package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestHostSkillDirsMatchBuiltins is #113: skills/builtin/*.md is the only
// authored copy; the Claude Code and Codex plugin trees are generated from it
// and must not drift in either direction.
func TestHostSkillDirsMatchBuiltins(t *testing.T) {
	builtins, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(builtins) < 7 {
		t.Fatalf("expected at least 7 builtin skills, got %d", len(builtins))
	}
	for _, s := range builtins {
		rendered := HostSkillFile(s)
		back, err := ParseSkill(string(rendered), "SKILL.md", "host")
		if err != nil {
			t.Fatalf("host SKILL.md for %s does not parse: %v", s.Meta.Name, err)
		}
		if back.Meta.Name != s.Meta.Name || back.Meta.Description != s.Meta.Description || back.Body != s.Body {
			t.Fatalf("host SKILL.md for %s does not round-trip:\n%s", s.Meta.Name, rendered)
		}
		if bytes.Contains(rendered, []byte("—")) {
			t.Errorf("skills/builtin/%s.md contains an em-dash; user-facing copy must not", s.Meta.Name)
		}
	}

	for _, dir := range []string{"../.claude/skills", "../plugins/codemap/skills"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		extra := map[string]bool{}
		for _, entry := range entries {
			if entry.IsDir() {
				extra[entry.Name()] = true
			}
		}
		for _, s := range builtins {
			path := filepath.Join(dir, s.Meta.Name, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, HostSkillFile(s)) {
				t.Errorf("%s is out of date with skills/builtin/%s.md; run `go generate ./skills` and commit", path, s.Meta.Name)
			}
			delete(extra, s.Meta.Name)
		}
		for name := range extra {
			t.Errorf("%s/%s has no source in skills/builtin; delete it or add skills/builtin/%s.md", dir, name, name)
		}
	}
}
