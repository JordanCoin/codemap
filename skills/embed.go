package skills

import (
	"embed"
	"strings"
)

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
