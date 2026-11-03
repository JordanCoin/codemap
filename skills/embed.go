package skills

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
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

// InstallHostSkills writes every builtin as <base>/<rel>/<name>/SKILL.md,
// replacing only files codemap wrote and nobody edited. A repo controls every
// path below base, so all access goes through an os.Root and any symlinked
// component skips that skill instead of writing through it.
func InstallHostSkills(base, rel string) (InstallResult, error) {
	var result InstallResult
	builtins, err := loadBuiltinSkills()
	if err != nil {
		return result, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return result, err
	}
	defer root.Close()
	for _, s := range builtins {
		switch state, err := installSkill(root, filepath.Join(rel, s.Meta.Name), s); {
		case err != nil:
			result.Skipped = append(result.Skipped, s.Meta.Name)
		case state == "written":
			result.Written = append(result.Written, s.Meta.Name)
		case state == "unchanged":
			result.Unchanged = append(result.Unchanged, s.Meta.Name)
		default:
			result.Skipped = append(result.Skipped, s.Meta.Name)
		}
	}
	return result, nil
}

func installSkill(root *os.Root, dir string, s Skill) (string, error) {
	// ponytail: os.Root has no Rename/MkdirAll before Go 1.25, so each
	// component is Lstat-checked here; switch to root.MkdirAll/Rename then.
	prefix := ""
	for _, part := range strings.Split(filepath.Clean(dir), string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, err := root.Lstat(prefix)
		switch {
		case os.IsNotExist(err):
			if err := root.Mkdir(prefix, 0o755); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return "", fmt.Errorf("%s is not a plain directory", prefix)
		}
	}
	target := filepath.Join(dir, "SKILL.md")
	want := installedSkillFile(s)
	if info, err := root.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", target)
		}
		existing, err := readRootFile(root, target)
		if err != nil {
			return "", err
		}
		if bytes.Equal(existing, want) || bytes.Equal(existing, HostSkillFile(s)) {
			return "unchanged", nil
		}
		if !ownedSkillFile(existing) {
			return "skipped", nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp := filepath.Join(dir, fmt.Sprintf(".SKILL.md.tmp-%d", os.Getpid()))
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer root.Remove(tmp)
	if _, err := file.Write(want); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	base := root.Name()
	return "written", os.Rename(filepath.Join(base, tmp), filepath.Join(base, target))
}

func readRootFile(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
