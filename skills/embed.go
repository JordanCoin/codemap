package skills

import (
	"bytes"
	"crypto/sha256"
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

// InstallResult names the skills InstallHostSkills wrote, left as they were,
// or skipped because someone else's file is in the way.
type InstallResult struct {
	Written   []string
	Unchanged []string
	Skipped   []string
}

func generatedSkillMarker(body []byte) string {
	return fmt.Sprintf("<!-- codemap:generated sha256:%x -->\n", sha256.Sum256(body))
}

// installedSkillFile is HostSkillFile plus a hash of it, so a later install
// can tell its own unedited output from a user's edit.
func installedSkillFile(s Skill) []byte {
	body := HostSkillFile(s)
	return append(body, generatedSkillMarker(body)...)
}

// ownedSkillFile reports whether data is an installed file whose hash still
// matches its content, whatever codemap version wrote it.
func ownedSkillFile(data []byte) bool {
	cut := bytes.LastIndexByte(bytes.TrimSuffix(data, []byte("\n")), '\n')
	return cut >= 0 && string(data[cut+1:]) == generatedSkillMarker(data[:cut+1])
}

// InstallHostSkills writes every builtin as <dir>/<name>/SKILL.md, replacing
// only files codemap wrote and nobody edited.
func InstallHostSkills(dir string) (InstallResult, error) {
	var result InstallResult
	builtins, err := loadBuiltinSkills()
	if err != nil {
		return result, err
	}
	// A repo can ship any of these paths as a symlink to a file outside it.
	rootLinked := isSymlink(dir)
	for _, s := range builtins {
		target := filepath.Join(dir, s.Meta.Name, "SKILL.md")
		if rootLinked || isSymlink(filepath.Dir(target)) || isSymlink(target) {
			result.Skipped = append(result.Skipped, s.Meta.Name)
			continue
		}
		want := installedSkillFile(s)
		existing, err := os.ReadFile(target)
		switch {
		case err == nil && (bytes.Equal(existing, want) || bytes.Equal(existing, HostSkillFile(s))):
			result.Unchanged = append(result.Unchanged, s.Meta.Name)
			continue
		case err == nil && !ownedSkillFile(existing):
			result.Skipped = append(result.Skipped, s.Meta.Name)
			continue
		case err != nil && !os.IsNotExist(err):
			return result, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		if err := writeFileViaRename(target, want); err != nil {
			return result, err
		}
		result.Written = append(result.Written, s.Meta.Name)
	}
	return result, nil
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// writeFileViaRename replaces path itself, never a file a symlink points at.
func writeFileViaRename(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".SKILL.md.tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
