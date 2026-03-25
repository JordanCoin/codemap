package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	builtinSource = "builtin"
	projectSource = "project"
	globalSource  = "global"
)

// LoadSkills discovers SKILL.md and *.md files from multiple sources in priority order:
// 1. Builtin (embedded in binary)
// 2. Project-local (.codemap/skills/)
// 3. Global (~/.codemap/skills/)
// Later sources override earlier ones if they share the same name.
func LoadSkills(root string) (*SkillIndex, error) {
	var all []Skill

	// 1. Builtin skills (embedded)
	builtins, err := loadBuiltinSkills()
	if err != nil {
		return nil, err
	}
	all = append(all, builtins...)

	// 2. Project-local skills
	projectDir := filepath.Join(root, ".codemap", "skills")
	projectSkills, _ := loadSkillsFromDir(projectDir, projectSource)
	all = append(all, projectSkills...)

	// 3. Global skills
	home, _ := os.UserHomeDir()
	if home != "" {
		globalDir := filepath.Join(home, ".codemap", "skills")
		globalSkills, _ := loadSkillsFromDir(globalDir, globalSource)
		all = append(all, globalSkills...)
	}

	// Deduplicate: later sources win (project overrides builtin, global overrides project)
	seen := make(map[string]int) // name -> index in deduped
	var deduped []Skill
	for _, s := range all {
		if idx, exists := seen[s.Meta.Name]; exists {
			deduped[idx] = s // replace
		} else {
			seen[s.Meta.Name] = len(deduped)
			deduped = append(deduped, s)
		}
	}

	return newIndex(deduped), nil
}

// loadSkillsFromDir reads all .md files in a directory as skills.
func loadSkillsFromDir(dir, source string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		skill, err := ParseSkill(string(data), path, source)
		if err != nil {
			continue
		}
		skills = append(skills, *skill)
	}

	return skills, nil
}

// ParseSkill parses a SKILL.md file with YAML frontmatter.
func ParseSkill(content, filePath, source string) (*Skill, error) {
	meta, body, err := parseFrontmatter(content)
	if err != nil {
		return nil, err
	}

	// Derive name from filename if frontmatter didn't provide one
	if meta.Name == "" && filePath != "" {
		base := filepath.Base(filePath)
		meta.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	return &Skill{
		Meta:     meta,
		Body:     body,
		FilePath: filePath,
		Source:   source,
	}, nil
}

// parseFrontmatter splits YAML frontmatter from markdown body.
func parseFrontmatter(content string) (SkillMeta, string, error) {
	var meta SkillMeta

	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		// No frontmatter — treat entire content as body, use filename as name
		return meta, content, nil
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return meta, content, nil
	}

	yamlBlock := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:])

	if err := yaml.Unmarshal([]byte(yamlBlock), &meta); err != nil {
		return meta, content, err
	}

	return meta, body, nil
}

// MatchResult pairs a skill with its match score.
type MatchResult struct {
	Skill  *Skill `json:"skill"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// MatchSkills returns skills relevant to a given intent category, file list, and languages.
// Results are sorted by score descending, capped at topK.
func (idx *SkillIndex) MatchSkills(category string, files []string, languages []string, topK int) []MatchResult {
	if idx == nil || len(idx.Skills) == 0 {
		return nil
	}

	type scored struct {
		skill  *Skill
		score  int
		reason string
	}

	var candidates []scored

	for i := range idx.Skills {
		s := &idx.Skills[i]
		score := 0
		var reasons []string

		// Keyword match with intent category
		for _, kw := range s.Meta.Keywords {
			if strings.EqualFold(kw, category) {
				score += 3
				reasons = append(reasons, "category:"+category)
			}
		}

		// Language match
		for _, lang := range languages {
			for _, sl := range s.Meta.Languages {
				if strings.EqualFold(sl, lang) {
					score += 2
					reasons = append(reasons, "language:"+lang)
				}
			}
		}

		// Path pattern match
		for _, pattern := range s.Meta.PathPatterns {
			for _, file := range files {
				if matchPath(pattern, file) {
					score += 1
					reasons = append(reasons, "path:"+file)
				}
			}
		}

		// Priority boost — only applied when there's a real signal
		if score > 0 {
			score += s.Meta.Priority / 5
		}

		if score > 0 {
			reason := strings.Join(reasons, ", ")
			candidates = append(candidates, scored{skill: s, score: score, reason: reason})
		}
	}

	// Sort by score descending, then by name for stability
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].skill.Meta.Name < candidates[j].skill.Meta.Name
	})

	if topK > 0 && len(candidates) > topK {
		candidates = candidates[:topK]
	}

	results := make([]MatchResult, len(candidates))
	for i, c := range candidates {
		results[i] = MatchResult{Skill: c.skill, Score: c.score, Reason: c.reason}
	}
	return results
}

// matchPath does a simple glob-like match: supports * and ** patterns,
// and falls back to substring matching.
func matchPath(pattern, path string) bool {
	// Simple substring match for non-glob patterns
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(path, pattern)
	}
	// Use filepath.Match for simple globs
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Also try matching against just the filename
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}
