package skills

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The builtins are the only authored copy of every skill. The host-native
// SKILL.md trees are generated from them (#113); regenerate with
// `go generate ./skills` and commit the result.
//
//go:generate go run .. skill export ../.claude/skills
//go:generate go run .. skill export ../plugins/codemap/skills

//go:embed builtin/*.md
var builtinFS embed.FS

// loadBuiltinSkills reads all embedded builtin skill files.
func loadBuiltinSkills() ([]Skill, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, err
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		data, err := builtinFS.ReadFile("builtin/" + entry.Name())
		if err != nil {
			continue
		}

		skill, err := ParseSkill(string(data), "builtin/"+entry.Name(), builtinSource)
		if err != nil {
			continue
		}
		skills = append(skills, *skill)
	}

	return skills, nil
}

// Builtins returns the embedded skills, the single source for every
// host-native SKILL.md.
func Builtins() ([]Skill, error) {
	return loadBuiltinSkills()
}

// HostSkillFile renders a skill as the SKILL.md hosts read: name and
// description in the frontmatter, body unchanged.
func HostSkillFile(s Skill) []byte {
	return []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", s.Meta.Name, s.Meta.Description, s.Body))
}

// ExportHostSkills writes <dir>/<name>/SKILL.md for every builtin and
// returns how many it wrote.
func ExportHostSkills(dir string) (int, error) {
	builtins, err := loadBuiltinSkills()
	if err != nil {
		return 0, err
	}
	for _, s := range builtins {
		target := filepath.Join(dir, s.Meta.Name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(target, HostSkillFile(s), 0o644); err != nil {
			return 0, err
		}
	}
	return len(builtins), nil
}
