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
	rustCoverageNote = "Rust macro-generated, string-routed, and #[path] module edges may be unresolved"

	rustTargetLib         = "lib"
	rustTargetBin         = "bin"
	rustTargetExample     = "example"
	rustTargetTest        = "test"
	rustTargetBench       = "bench"
	rustTargetCustomBuild = "custom-build"
)

type rustTarget struct {
	rootFile  string
	sourceDir string
	kind      string
}

type rustLocalDependency struct {
	packageRoot string
	alias       string
	kind        string
}

type rustPackage struct {
	crateID       string
	root          string
	lib           *rustTarget
	targets       []rustTarget
	dependencies  []rustLocalDependency
	authoritative bool
}

type rustWorkspaceIndex struct {
	byCrateID   map[string][]rustPackage
	byRoot      map[string]rustPackage
	packages    []rustPackage
	moduleDecls map[string]map[string]bool
	moduleNames map[string]map[string]bool
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

func buildRustFallbackWorkspaceIndex(root string, analyses []FileAnalysis) *rustWorkspaceIndex {
	index := &rustWorkspaceIndex{
		byCrateID:   make(map[string][]rustPackage),
		byRoot:      make(map[string]rustPackage),
		moduleDecls: make(map[string]map[string]bool),
		moduleNames: make(map[string]map[string]bool),
	}
	for _, analysis := range analyses {
		if analysis.Language != "rust" {
			continue
		}
		for _, ref := range analysis.References {
			if ref.Kind != "rust-module" {
				continue
			}
			if index.moduleNames[analysis.Path] == nil {
				index.moduleNames[analysis.Path] = make(map[string]bool)
			}
			index.moduleNames[analysis.Path][ref.Path] = true
			if rustModuleUsesPathAttribute(root, analysis.Path, ref.Line) {
				continue
			}
			if index.moduleDecls[analysis.Path] == nil {
				index.moduleDecls[analysis.Path] = make(map[string]bool)
			}
			index.moduleDecls[analysis.Path][ref.Path] = true
		}
	}
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
		lib := rustTarget{rootFile: libFile, sourceDir: filepath.Dir(libFile), kind: rustTargetLib}
		pkg := rustPackage{
			crateID: strings.ReplaceAll(manifest.Package.Name, "-", "_"),
			root:    memberDir,
			lib:     &lib,
			targets: []rustTarget{lib},
		}
		index.packages = append(index.packages, pkg)
		index.byCrateID[pkg.crateID] = append(index.byCrateID[pkg.crateID], pkg)
		index.byRoot[pkg.root] = pkg
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
			if target := resolveRustModule(ref.Path, analysis.Path, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-path":
			if target := resolveRustPath(ref.Path, analysis.Path, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		}
	}
	return dedupe(resolved)
}

func resolveRustModule(name, fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	dir := rustModuleDir(fromFile, idx, workspace)
	if dir == "" {
		return ""
	}
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

func rustModuleDir(fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	if pkg, ok := workspace.packageForFile(fromFile); ok && pkg.authoritative {
		target, ok := workspace.targetForFile(fromFile, idx)
		if !ok {
			return ""
		}
		if target.rootFile == filepath.Clean(fromFile) {
			return target.sourceDir
		}
	}
	return rustChildModuleDir(fromFile)
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
		target, ok := workspace.targetForFile(fromFile, idx)
		if !ok {
			return ""
		}
		base = target.sourceDir
		rootFile = target.rootFile
		parts = parts[1:]
	case "self":
		base = rustModuleDir(fromFile, idx, workspace)
		if base == "" {
			return ""
		}
		parts = parts[1:]
	case "super":
		base = rustModuleDir(fromFile, idx, workspace)
		if base == "" {
			return ""
		}
		for len(parts) > 0 && parts[0] == "super" {
			base = filepath.Dir(base)
			parts = parts[1:]
		}
	default:
		if workspace.moduleNames[fromFile][parts[0]] {
			if !workspace.moduleDecls[fromFile][parts[0]] {
				return ""
			}
			base = rustModuleDir(fromFile, idx, workspace)
			if base == "" {
				return ""
			}
			break
		}
		packages := workspace.externalPackagesForPath(parts[0], fromFile, idx)
		if len(packages) != 1 {
			return ""
		}
		if packages[0].lib == nil || !rustTargetIndexed(*packages[0].lib, idx) {
			return ""
		}
		base = packages[0].lib.sourceDir
		rootFile = packages[0].lib.rootFile
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

func (index *rustWorkspaceIndex) externalPackagesForPath(alias, fromFile string, idx *fileIndex) []rustPackage {
	pkg, ok := index.packageForFile(fromFile)
	if !ok || !pkg.authoritative {
		return index.byCrateID[alias]
	}
	target, ok := index.targetForFile(fromFile, idx)
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	var packages []rustPackage
	for _, dependency := range pkg.dependencies {
		if dependency.alias != alias || !rustDependencyEligible(dependency.kind, target.kind) || seen[dependency.packageRoot] {
			continue
		}
		dependencyPackage, ok := index.byRoot[dependency.packageRoot]
		if !ok {
			continue
		}
		seen[dependency.packageRoot] = true
		packages = append(packages, dependencyPackage)
	}
	return packages
}

func rustDependencyEligible(dependencyKind, targetKind string) bool {
	switch dependencyKind {
	case "build":
		return targetKind == rustTargetCustomBuild
	default:
		return targetKind != rustTargetCustomBuild
	}
}

func (index *rustWorkspaceIndex) targetForFile(path string, idx *fileIndex) (rustTarget, bool) {
	pkg, ok := index.packageForFile(path)
	if !ok {
		return rustTarget{}, false
	}
	path = filepath.Clean(path)
	for _, target := range pkg.targets {
		if rustTargetIndexed(target, idx) && path == target.rootFile {
			return target, true
		}
	}

	bestDepth := -1
	var best rustTarget
	ambiguous := false
	for _, target := range pkg.targets {
		if !rustTargetIndexed(target, idx) || !index.targetContainsFile(target, path, idx) {
			continue
		}
		depth := pathDepth(target.sourceDir)
		switch {
		case depth > bestDepth:
			bestDepth = depth
			best = target
			ambiguous = false
		case depth == bestDepth && target.rootFile != best.rootFile:
			ambiguous = true
		}
	}
	if bestDepth < 0 || ambiguous {
		return rustTarget{}, false
	}
	return best, true
}

func rustTargetIndexed(target rustTarget, idx *fileIndex) bool {
	files := idx.byExact[target.rootFile]
	return len(files) == 1 && files[0] == target.rootFile
}

func pathWithin(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if dir == "." {
		return true
	}
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func (index *rustWorkspaceIndex) targetContainsFile(target rustTarget, path string, idx *fileIndex) bool {
	if !pathWithin(path, target.sourceDir) {
		return false
	}
	rel, err := filepath.Rel(target.sourceDir, path)
	if err != nil || rel == "." {
		return err == nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	last := parts[len(parts)-1]
	if last == "mod.rs" {
		parts = parts[:len(parts)-1]
	} else if filepath.Ext(last) == ".rs" {
		parts[len(parts)-1] = strings.TrimSuffix(last, ".rs")
	} else {
		return false
	}
	if len(parts) == 0 {
		return false
	}

	parent := target.rootFile
	for i, name := range parts {
		if !index.moduleDecls[parent][name] {
			return false
		}
		modulePath := filepath.Join(append([]string{target.sourceDir}, parts[:i+1]...)...)
		var next string
		for _, candidate := range []string{modulePath + ".rs", filepath.Join(modulePath, "mod.rs")} {
			if files := idx.byExact[candidate]; len(files) == 1 {
				if next != "" {
					return false
				}
				next = files[0]
			}
		}
		if next == "" {
			return false
		}
		parent = next
	}
	return parent == path
}

func pathDepth(path string) int {
	path = filepath.Clean(path)
	if path == "." {
		return 0
	}
	return strings.Count(path, string(filepath.Separator)) + 1
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
