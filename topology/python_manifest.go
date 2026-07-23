package topology

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"
)

type pythonManifest struct {
	Path             string
	Root             string
	Name             string
	NormalizedName   string
	Dependencies     []pythonDependency
	WorkspaceMembers []string
	WorkspaceExclude []string
	UVSources        map[string][]pythonLocalSource
	PoetrySources    map[string][]pythonLocalSource
}

type pythonDependency struct {
	Name        string
	Conditional bool
}

type pythonLocalSource struct {
	Path        string
	Workspace   bool
	Conditional bool
}

type pythonProjectDocument struct {
	Project struct {
		Name                 string              `toml:"name"`
		Dependencies         []string            `toml:"dependencies"`
		OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		Dynamic              []string            `toml:"dynamic"`
	} `toml:"project"`
	Tool struct {
		UV struct {
			Workspace struct {
				Members []string `toml:"members"`
				Exclude []string `toml:"exclude"`
			} `toml:"workspace"`
			Sources map[string]any `toml:"sources"`
		} `toml:"uv"`
		Poetry struct {
			Name         string         `toml:"name"`
			Dependencies map[string]any `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

func parsePythonManifest(root, path string) (pythonManifest, []Issue, error) {
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return pythonManifest{}, nil, err
	}
	var document pythonProjectDocument
	if err := toml.Unmarshal(data, &document); err != nil {
		return pythonManifest{}, nil, err
	}

	manifest := pythonManifest{
		Path:             path,
		Root:             filepath.Dir(path),
		WorkspaceMembers: append([]string(nil), document.Tool.UV.Workspace.Members...),
		WorkspaceExclude: append([]string(nil), document.Tool.UV.Workspace.Exclude...),
		UVSources:        make(map[string][]pythonLocalSource),
		PoetrySources:    make(map[string][]pythonLocalSource),
	}
	var issues []Issue
	projectName := strings.TrimSpace(document.Project.Name)
	poetryName := strings.TrimSpace(document.Tool.Poetry.Name)
	switch {
	case projectName != "":
		manifest.Name = projectName
		if poetryName != "" && normalizePythonProjectName(projectName) != normalizePythonProjectName(poetryName) {
			issues = append(issues, pythonIssue("conflicting-project-name",
				fmt.Sprintf("%s declares project name %q and Poetry name %q", path, projectName, poetryName)))
		}
	case poetryName != "":
		manifest.Name = poetryName
	}
	manifest.NormalizedName = normalizePythonProjectName(manifest.Name)

	for _, dynamic := range document.Project.Dynamic {
		if strings.EqualFold(strings.TrimSpace(dynamic), "dependencies") {
			issues = append(issues, pythonIssue("dynamic-dependencies",
				fmt.Sprintf("%s computes project dependencies dynamically", path)))
			break
		}
	}
	for _, dependency := range document.Project.Dependencies {
		parsed, ok := parsePythonDependency(dependency)
		if !ok {
			issues = append(issues, pythonIssue("unsupported-dependency",
				fmt.Sprintf("%s has an unsupported dependency declaration %q", path, dependency)))
			continue
		}
		manifest.Dependencies = append(manifest.Dependencies, parsed)
	}
	groups := make([]string, 0, len(document.Project.OptionalDependencies))
	for group := range document.Project.OptionalDependencies {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		for _, dependency := range document.Project.OptionalDependencies[group] {
			parsed, ok := parsePythonDependency(dependency)
			if !ok {
				issues = append(issues, pythonIssue("unsupported-dependency",
					fmt.Sprintf("%s has an unsupported optional dependency declaration %q", path, dependency)))
				continue
			}
			parsed.Conditional = true
			manifest.Dependencies = append(manifest.Dependencies, parsed)
		}
	}
	for name, value := range document.Tool.UV.Sources {
		normalized := normalizePythonProjectName(name)
		sources, ok := decodePythonLocalSources(value)
		if !ok {
			issues = append(issues, pythonIssue("unsupported-local-source",
				fmt.Sprintf("%s has an unsupported uv source for %q", path, name)))
			continue
		}
		manifest.UVSources[normalized] = sources
	}
	for name, value := range document.Tool.Poetry.Dependencies {
		if strings.EqualFold(name, "python") {
			continue
		}
		sources, ok := decodePythonLocalSources(value)
		if !ok {
			continue
		}
		hasLocal := false
		for _, source := range sources {
			if source.Path != "" {
				hasLocal = true
				break
			}
		}
		if hasLocal {
			manifest.PoetrySources[normalizePythonProjectName(name)] = sources
		}
	}
	return manifest, issues, nil
}

func decodePythonLocalSources(value any) ([]pythonLocalSource, bool) {
	switch typed := value.(type) {
	case map[string]any:
		source, ok := decodePythonLocalSource(typed)
		if !ok {
			return nil, false
		}
		return []pythonLocalSource{source}, true
	case []any:
		sources := make([]pythonLocalSource, 0, len(typed))
		for _, item := range typed {
			table, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			source, ok := decodePythonLocalSource(table)
			if !ok {
				return nil, false
			}
			source.Conditional = true
			sources = append(sources, source)
		}
		return sources, len(sources) > 0
	default:
		return nil, false
	}
}

func decodePythonLocalSource(table map[string]any) (pythonLocalSource, bool) {
	source := pythonLocalSource{}
	if path, ok := table["path"].(string); ok {
		source.Path = strings.TrimSpace(path)
	}
	if workspace, ok := table["workspace"].(bool); ok {
		source.Workspace = workspace
	}
	for _, key := range []string{"marker", "markers"} {
		if marker, ok := table[key].(string); ok && strings.TrimSpace(marker) != "" {
			source.Conditional = true
		}
	}
	return source, source.Path != "" || source.Workspace
}

func parsePythonDependency(dependency string) (pythonDependency, bool) {
	dependency = strings.TrimSpace(dependency)
	conditional := false
	if marker := strings.IndexByte(dependency, ';'); marker >= 0 {
		if strings.TrimSpace(dependency[marker+1:]) == "" {
			return pythonDependency{}, false
		}
		dependency = strings.TrimSpace(dependency[:marker])
		conditional = true
	}
	end := 0
	for end < len(dependency) {
		r := rune(dependency[end])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			break
		}
		end++
	}
	if end == 0 {
		return pythonDependency{}, false
	}
	name := normalizePythonProjectName(dependency[:end])
	return pythonDependency{Name: name, Conditional: conditional}, name != ""
}

func normalizePythonProjectName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var result strings.Builder
	separator := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			separator = false
			result.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func pythonIssue(code, message string) Issue {
	return Issue{Provider: "python", Code: code, Message: message}
}
