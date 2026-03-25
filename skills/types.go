package skills

// SkillMeta contains the YAML frontmatter metadata for a skill.
type SkillMeta struct {
	Name         string   `yaml:"name" json:"name"`
	Description  string   `yaml:"description" json:"description"`
	Version      string   `yaml:"version,omitempty" json:"version,omitempty"`
	Author       string   `yaml:"author,omitempty" json:"author,omitempty"`
	Priority     int      `yaml:"priority,omitempty" json:"priority,omitempty"`
	PathPatterns []string `yaml:"pathPatterns,omitempty" json:"path_patterns,omitempty"`
	Keywords     []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Languages    []string `yaml:"languages,omitempty" json:"languages,omitempty"`
}

// Skill represents a loaded skill with metadata and instructions.
type Skill struct {
	Meta     SkillMeta `json:"meta"`
	Body     string    `json:"body"`      // markdown instructions (without frontmatter)
	FilePath string    `json:"file_path"` // where this skill was loaded from
	Source   string    `json:"source"`    // "builtin", "project", "global"
}

// SkillIndex provides fast lookup across all loaded skills.
type SkillIndex struct {
	Skills     []Skill            `json:"skills"`
	ByName     map[string]*Skill  `json:"-"`
	ByKeyword  map[string][]*Skill `json:"-"`
	ByLanguage map[string][]*Skill `json:"-"`
}

// newIndex builds lookup maps from a slice of skills.
func newIndex(skills []Skill) *SkillIndex {
	idx := &SkillIndex{
		Skills:     skills,
		ByName:     make(map[string]*Skill),
		ByKeyword:  make(map[string][]*Skill),
		ByLanguage: make(map[string][]*Skill),
	}
	for i := range idx.Skills {
		s := &idx.Skills[i]
		if s.Meta.Name != "" {
			idx.ByName[s.Meta.Name] = s
		}
		for _, kw := range s.Meta.Keywords {
			idx.ByKeyword[kw] = append(idx.ByKeyword[kw], s)
		}
		for _, lang := range s.Meta.Languages {
			idx.ByLanguage[lang] = append(idx.ByLanguage[lang], s)
		}
	}
	return idx
}
