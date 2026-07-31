package scanner

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	rustCoverageStatus = "partial"
	rustCoverageNote   = "Rust macro-generated, string-routed, renamed-dependency, and #[path] module edges may be unresolved"
)

// GraphCoverage describes known dependency-graph blind spots.
type GraphCoverage struct {
	Status string   `json:"status,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

type rustPackage struct {
	crateID   string
	root      string
	sourceDir string
	libFile   string
}

type rustWorkspaceIndex struct {
	byCrateID map[string][]rustPackage
	packages  []rustPackage
}

type cargoManifest struct {
	Package struct {
		Name string `toml:"name"`
	} `toml:"package"`
	Workspace struct {
		Members []string `toml:"members"`
		Exclude []string `toml:"exclude"`
	} `toml:"workspace"`
	Lib struct {
		Path string `toml:"path"`
	} `toml:"lib"`
}

func buildRustWorkspaceIndex(root string) *rustWorkspaceIndex {
	index := &rustWorkspaceIndex{byCrateID: make(map[string][]rustPackage)}
	rootManifest, ok := readCargoManifest(filepath.Join(root, "Cargo.toml"))
	if !ok {
		return index
	}

	memberDirs := []string{}
	if rootManifest.Package.Name != "" {
		memberDirs = append(memberDirs, ".")
	}
	for _, member := range rootManifest.Workspace.Members {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(member)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			rel, err := filepath.Rel(root, match)
			if err == nil {
				memberDirs = append(memberDirs, rel)
			}
		}
	}

	excluded := make(map[string]bool)
	for _, pattern := range rootManifest.Workspace.Exclude {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			if rel, err := filepath.Rel(root, match); err == nil {
				excluded[filepath.Clean(rel)] = true
			}
		}
	}

	for _, memberDir := range dedupe(memberDirs) {
		memberDir = filepath.Clean(memberDir)
		if excluded[memberDir] {
			continue
		}
		manifest, ok := readCargoManifest(filepath.Join(root, memberDir, "Cargo.toml"))
		if !ok || manifest.Package.Name == "" {
			continue
		}
		libFile := manifest.Lib.Path
		if libFile == "" {
			libFile = filepath.Join("src", "lib.rs")
		}
		libFile = filepath.Clean(filepath.Join(memberDir, libFile))
		pkg := rustPackage{
			crateID:   strings.ReplaceAll(manifest.Package.Name, "-", "_"),
			root:      memberDir,
			sourceDir: filepath.Dir(libFile),
			libFile:   libFile,
		}
		index.packages = append(index.packages, pkg)
		index.byCrateID[pkg.crateID] = append(index.byCrateID[pkg.crateID], pkg)
	}

	sort.Slice(index.packages, func(i, j int) bool {
		return len(index.packages[i].root) > len(index.packages[j].root)
	})
	return index
}

func readCargoManifest(path string) (cargoManifest, bool) {
	var manifest cargoManifest
	file, err := os.Open(path)
	if err != nil {
		return cargoManifest{}, false
	}
	defer file.Close()

	section := ""
	var pendingKey string
	var pendingValue strings.Builder
	apply := func(key, value string) {
		switch {
		case section == "package" && key == "name":
			manifest.Package.Name = parseCargoString(value)
		case section == "workspace" && key == "members":
			manifest.Workspace.Members = parseCargoStringArray(value)
		case section == "workspace" && key == "exclude":
			manifest.Workspace.Exclude = parseCargoStringArray(value)
		case section == "lib" && key == "path":
			manifest.Lib.Path = parseCargoString(value)
		}
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripCargoComment(scanner.Text()))
		if pendingKey != "" {
			pendingValue.WriteString(line)
			if strings.Contains(line, "]") {
				apply(pendingKey, pendingValue.String())
				pendingKey = ""
				pendingValue.Reset()
			}
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.Trim(line, "[]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") && !strings.Contains(value, "]") {
			pendingKey = key
			pendingValue.WriteString(value)
			continue
		}
		apply(key, value)
	}
	if scanner.Err() != nil {
		return cargoManifest{}, false
	}
	return manifest, true
}

func stripCargoComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func parseCargoString(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, ","))
	parsed, err := strconv.Unquote(value)
	if err != nil {
		return ""
	}
	return parsed
}

func parseCargoStringArray(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	var result []string
	for _, item := range strings.Split(value, ",") {
		if parsed := parseCargoString(item); parsed != "" {
			result = append(result, parsed)
		}
	}
	return result
}

func resolveRustReferences(root string, analysis FileAnalysis, idx *fileIndex, workspace *rustWorkspaceIndex) []string {
	var resolved []string
	for _, ref := range analysis.References {
		switch ref.Kind {
		case "rust-module":
			if rustModuleUsesPathAttribute(root, analysis.Path, ref.Line) {
				continue
			}
			if target := resolveRustModule(ref.Path, analysis.Path, idx); target != "" {
				resolved = append(resolved, target)
			}
		case "rust-path":
			if target := resolveRustPath(ref.Path, analysis.Path, idx, workspace); target != "" {
				resolved = append(resolved, target)
			}
		}
	}
	return dedupe(resolved)
}

func resolveRustModule(name, fromFile string, idx *fileIndex) string {
	dir := rustChildModuleDir(fromFile)
	for _, candidate := range []string{
		filepath.Join(dir, name+".rs"),
		filepath.Join(dir, name, "mod.rs"),
	} {
		if files := idx.byExact[candidate]; len(files) == 1 {
			return files[0]
		}
	}
	return ""
}

func rustChildModuleDir(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	switch base {
	case "lib.rs", "main.rs", "mod.rs":
		return dir
	default:
		return filepath.Join(dir, strings.TrimSuffix(base, filepath.Ext(base)))
	}
}

func resolveRustPath(path, fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "::")
	parts := strings.Split(path, "::")
	if len(parts) < 2 {
		return ""
	}

	var base string
	rootFile := ""
	switch parts[0] {
	case "crate":
		pkg, ok := workspace.packageForFile(fromFile)
		if !ok {
			return ""
		}
		base = pkg.sourceDir
		rootFile = pkg.libFile
		parts = parts[1:]
	case "self":
		base = rustChildModuleDir(fromFile)
		parts = parts[1:]
	case "super":
		base = rustChildModuleDir(fromFile)
		for len(parts) > 0 && parts[0] == "super" {
			base = filepath.Dir(base)
			parts = parts[1:]
		}
	default:
		packages := workspace.byCrateID[parts[0]]
		if len(packages) != 1 {
			return ""
		}
		base = packages[0].sourceDir
		rootFile = packages[0].libFile
		parts = parts[1:]
	}

	for i := len(parts); i > 0; i-- {
		modulePath := filepath.Join(append([]string{base}, parts[:i]...)...)
		for _, candidate := range []string{modulePath + ".rs", filepath.Join(modulePath, "mod.rs")} {
			if files := idx.byExact[candidate]; len(files) == 1 {
				return files[0]
			}
		}
	}
	if rootFile != "" {
		if files := idx.byExact[rootFile]; len(files) == 1 {
			return files[0]
		}
	}
	return ""
}

func (index *rustWorkspaceIndex) packageForFile(path string) (rustPackage, bool) {
	path = filepath.Clean(path)
	var rootPackage *rustPackage
	for _, pkg := range index.packages {
		if pkg.root == "." {
			candidate := pkg
			rootPackage = &candidate
			continue
		}
		if path == pkg.root || strings.HasPrefix(path, pkg.root+string(filepath.Separator)) {
			return pkg, true
		}
	}
	if rootPackage != nil {
		return *rootPackage, true
	}
	return rustPackage{}, false
}

func rustModuleUsesPathAttribute(root, file string, line int) bool {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	for i := line - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#[path") {
			return true
		}
		if !strings.HasPrefix(trimmed, "#[") {
			return false
		}
	}
	return false
}
