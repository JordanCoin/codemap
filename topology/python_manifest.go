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
	PackageRoots     []string
	PackageLayout    bool
	Dependencies     []pythonDependency
	WorkspaceMembers []string
	WorkspaceExclude []string
	UVSources        map[string][]pythonLocalSource
	PoetrySources    map[string][]pythonLocalSource
}

type pythonSetuptoolsConfig struct {
	PackageDir map[string]string `toml:"package-dir"`
	PyModules  []string          `toml:"py-modules"`
	Packages   *struct {
		Find *struct {
			Where []string `toml:"where"`
		} `toml:"find"`
	} `toml:"packages"`
}

type pythonHatchConfig struct {
	Build *struct {
		Targets *struct {
			Wheel *struct {
				Packages []string `toml:"packages"`
			} `toml:"wheel"`
		} `toml:"targets"`
	} `toml:"build"`
}

type pythonPDMConfig struct {
	Build *struct {
		Includes []string `toml:"includes"`
	} `toml:"build"`
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
		Setuptools *pythonSetuptoolsConfig `toml:"setuptools"`
		Hatch      *pythonHatchConfig      `toml:"hatch"`
		PDM        *pythonPDMConfig        `toml:"pdm"`
		UV         struct {
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
	manifestPath, err := safePythonManifestPath(root, path)
	if err != nil {
		return pythonManifest{}, []Issue{pythonIssue("invalid-manifest-path",
			fmt.Sprintf("%s could not be read safely: %v", path, err))}, nil
	}
	data, err := os.ReadFile(manifestPath)
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
	packageRoots, packageLayout := pythonPackageRoots(document)
	manifest.PackageLayout = packageLayout
	for _, packageRoot := range packageRoots {
		manifest.PackageRoots = append(manifest.PackageRoots,
			filepath.Clean(filepath.Join(manifest.Root, filepath.FromSlash(packageRoot))))
	}
	if manifest.PackageLayout {
		issues = append(issues, pythonIssue("unsupported-package-layout",
			fmt.Sprintf("%s declares a package layout that is not fully mapped", path)))
	}
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
		// Only dynamic dependencies affect the graph; other fields are not parsed here.
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

func safePythonManifestPath(root, manifest string) (string, error) {
	if manifest == "" || filepath.IsAbs(manifest) {
		return "", fmt.Errorf("manifest path must be repository-relative")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absRoot, filepath.Clean(manifest))
	realManifest, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(realRoot, realManifest)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("manifest escapes repository")
	}
	return realManifest, nil
}

func pythonPackageRoots(document pythonProjectDocument) ([]string, bool) {
	var roots []string
	declared := document.Tool.Setuptools != nil || document.Tool.Hatch != nil || document.Tool.PDM != nil
	if setuptools := document.Tool.Setuptools; setuptools != nil {
		for _, root := range setuptools.PackageDir {
			roots = append(roots, root)
		}
		if setuptools.Packages != nil && setuptools.Packages.Find != nil {
			roots = append(roots, setuptools.Packages.Find.Where...)
		}
	}
	if hatch := document.Tool.Hatch; hatch != nil && hatch.Build != nil && hatch.Build.Targets != nil && hatch.Build.Targets.Wheel != nil {
		roots = append(roots, hatch.Build.Targets.Wheel.Packages...)
	}
	if pdm := document.Tool.PDM; pdm != nil && pdm.Build != nil {
		roots = append(roots, pdm.Build.Includes...)
	}
	return uniqueSortedStrings(roots), declared
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
