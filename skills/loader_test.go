package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
priority: 5
keywords: ["refactor", "test"]
languages: ["go", "python"]
---

# Test Skill

This is the body.`

	meta, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if meta.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", meta.Name)
	}
	if meta.Description != "A test skill" {
		t.Errorf("expected description 'A test skill', got %q", meta.Description)
	}
	if meta.Priority != 5 {
		t.Errorf("expected priority 5, got %d", meta.Priority)
	}
	if len(meta.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(meta.Keywords))
	}
	if len(meta.Languages) != 2 {
		t.Errorf("expected 2 languages, got %d", len(meta.Languages))
	}
	if body != "# Test Skill\n\nThis is the body." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	content := "# Just a markdown file\n\nNo frontmatter here."
	meta, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "" {
		t.Errorf("expected empty name, got %q", meta.Name)
	}
	if body != content {
		t.Errorf("expected body to equal content")
	}
}

func TestParseSkill(t *testing.T) {
	content := `---
name: hub-safety
description: Safety checks for hub files
priority: 10
keywords: ["hub", "refactor"]
---

# Hub Safety

Check importers before editing.`

	skill, err := ParseSkill(content, "test/hub-safety.md", "test")
	if err != nil {
		t.Fatalf("ParseSkill error: %v", err)
	}
	if skill.Meta.Name != "hub-safety" {
		t.Errorf("expected name 'hub-safety', got %q", skill.Meta.Name)
	}
	if skill.Source != "test" {
		t.Errorf("expected source 'test', got %q", skill.Source)
	}
	if skill.FilePath != "test/hub-safety.md" {
		t.Errorf("unexpected file path: %q", skill.FilePath)
	}
}

func TestLoadBuiltinSkills(t *testing.T) {
	skills, err := loadBuiltinSkills()
	if err != nil {
		t.Fatalf("loadBuiltinSkills error: %v", err)
	}
	if len(skills) < 5 {
		t.Errorf("expected at least 5 builtin skills, got %d", len(skills))
	}

	// Verify each has a name
	for _, s := range skills {
		if s.Meta.Name == "" {
			t.Errorf("builtin skill from %s has empty name", s.FilePath)
		}
		if s.Source != "builtin" {
			t.Errorf("expected source 'builtin', got %q", s.Source)
		}
	}
}

func TestLoadSkillsFromDir(t *testing.T) {
	// Create temp dir with a skill
	dir := t.TempDir()
	content := `---
name: custom-skill
description: A custom skill
keywords: ["custom"]
---

# Custom

Custom instructions.`

	if err := os.WriteFile(filepath.Join(dir, "custom.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	skills, err := loadSkillsFromDir(dir, "project")
	if err != nil {
		t.Fatalf("loadSkillsFromDir error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Meta.Name != "custom-skill" {
		t.Errorf("expected name 'custom-skill', got %q", skills[0].Meta.Name)
	}
	if skills[0].Source != "project" {
		t.Errorf("expected source 'project', got %q", skills[0].Source)
	}
}

func TestNewIndex(t *testing.T) {
	skills := []Skill{
		{Meta: SkillMeta{Name: "a", Keywords: []string{"refactor"}, Languages: []string{"go"}}},
		{Meta: SkillMeta{Name: "b", Keywords: []string{"refactor", "test"}, Languages: []string{"go", "python"}}},
	}

	idx := newIndex(skills)
	if len(idx.ByName) != 2 {
		t.Errorf("expected 2 names, got %d", len(idx.ByName))
	}
	if len(idx.ByKeyword["refactor"]) != 2 {
		t.Errorf("expected 2 skills for 'refactor', got %d", len(idx.ByKeyword["refactor"]))
	}
	if len(idx.ByLanguage["go"]) != 2 {
		t.Errorf("expected 2 skills for 'go', got %d", len(idx.ByLanguage["go"]))
	}
	if len(idx.ByLanguage["python"]) != 1 {
		t.Errorf("expected 1 skill for 'python', got %d", len(idx.ByLanguage["python"]))
	}
}

func TestMatchSkills_ByCategory(t *testing.T) {
	skills := []Skill{
		{Meta: SkillMeta{Name: "hub-safety", Keywords: []string{"hub", "refactor"}}},
		{Meta: SkillMeta{Name: "test-first", Keywords: []string{"test", "tdd"}}},
		{Meta: SkillMeta{Name: "explore", Keywords: []string{"explore", "understand"}}},
	}
	idx := newIndex(skills)

	results := idx.MatchSkills("refactor", nil, nil, 3)
	if len(results) != 1 {
		t.Fatalf("expected 1 match for 'refactor', got %d", len(results))
	}
	if results[0].Skill.Meta.Name != "hub-safety" {
		t.Errorf("expected hub-safety, got %q", results[0].Skill.Meta.Name)
	}
}

func TestMatchSkills_ByLanguage(t *testing.T) {
	skills := []Skill{
		{Meta: SkillMeta{Name: "a", Keywords: []string{"refactor"}, Languages: []string{"go"}}},
		{Meta: SkillMeta{Name: "b", Keywords: []string{"refactor"}, Languages: []string{"python"}}},
	}
	idx := newIndex(skills)

	results := idx.MatchSkills("refactor", nil, []string{"go"}, 3)
	if len(results) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(results))
	}
	// "a" should rank higher (keyword + language match)
	if results[0].Skill.Meta.Name != "a" {
		t.Errorf("expected 'a' first (language match), got %q", results[0].Skill.Meta.Name)
	}
}

func TestMatchSkills_TopK(t *testing.T) {
	var skills []Skill
	for i := 0; i < 10; i++ {
		skills = append(skills, Skill{
			Meta: SkillMeta{
				Name:     "skill-" + string(rune('a'+i)),
				Keywords: []string{"common"},
			},
		})
	}
	idx := newIndex(skills)

	results := idx.MatchSkills("common", nil, nil, 3)
	if len(results) != 3 {
		t.Errorf("expected 3 results (topK=3), got %d", len(results))
	}
}

func TestMatchSkills_Empty(t *testing.T) {
	idx := newIndex(nil)
	results := idx.MatchSkills("refactor", nil, nil, 3)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty index, got %d", len(results))
	}
}

func TestMatchSkills_NilIndex(t *testing.T) {
	var idx *SkillIndex
	results := idx.MatchSkills("refactor", nil, nil, 3)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil index, got %d", len(results))
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"cmd/", "cmd/hooks.go", true},
		{"*.go", "main.go", true},
		{"scanner", "scanner/types.go", true},
		{"nope", "cmd/hooks.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.path, func(t *testing.T) {
			if got := matchPath(tt.pattern, tt.path); got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestLoadSkills_Integration(t *testing.T) {
	// Use a temp dir as root (no project/global skills, just builtins)
	root := t.TempDir()
	idx, err := LoadSkills(root)
	if err != nil {
		t.Fatalf("LoadSkills error: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil index")
	}
	if len(idx.Skills) < 5 {
		t.Errorf("expected at least 5 builtin skills, got %d", len(idx.Skills))
	}
}

func TestLoadSkills_ProjectOverridesBuiltin(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".codemap", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Override the hub-safety builtin
	override := `---
name: hub-safety
description: Custom hub safety
priority: 20
keywords: ["hub"]
---

# Custom Hub Safety

My custom instructions.`
	if err := os.WriteFile(filepath.Join(skillsDir, "hub-safety.md"), []byte(override), 0644); err != nil {
		t.Fatal(err)
	}

	idx, err := LoadSkills(root)
	if err != nil {
		t.Fatalf("LoadSkills error: %v", err)
	}

	s := idx.ByName["hub-safety"]
	if s == nil {
		t.Fatal("expected hub-safety skill")
	}
	if s.Source != "project" {
		t.Errorf("expected project override, got source %q", s.Source)
	}
	if s.Meta.Priority != 20 {
		t.Errorf("expected priority 20 from override, got %d", s.Meta.Priority)
	}
}
