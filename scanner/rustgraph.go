package scanner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	rustCoverageNote = "Rust macro-generated, dynamically string-routed, inline #[path], and conditional path edges may be unresolved"

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

type rustModuleDeclaration struct {
	name           string
	explicitTarget string
	unsupported    bool
}

type rustModuleLocation struct {
	file        string
	parent      string
	name        string
	childBase   string
	packageRoot string
	target      rustTarget
}

type rustWorkspaceIndex struct {
	byCrateID        map[string][]rustPackage
	byRoot           map[string]rustPackage
	packages         []rustPackage
	moduleDecls      map[string]map[string][]rustModuleDeclaration
	moduleChildren   map[string]map[string]string
	moduleLocations  map[string]rustModuleLocation
	ambiguousModules map[string]bool
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
		byCrateID:        make(map[string][]rustPackage),
		byRoot:           make(map[string]rustPackage),
		moduleDecls:      make(map[string]map[string][]rustModuleDeclaration),
		moduleChildren:   make(map[string]map[string]string),
		moduleLocations:  make(map[string]rustModuleLocation),
		ambiguousModules: make(map[string]bool),
	}
	for _, analysis := range analyses {
		if analysis.Language != "rust" {
			continue
		}
		index.addRustModuleDeclarations(root, analysis)
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

func (index *rustWorkspaceIndex) addRustModuleDeclarations(root string, analysis FileAnalysis) {
	if index.moduleDecls[analysis.Path] == nil {
		index.moduleDecls[analysis.Path] = make(map[string][]rustModuleDeclaration)
	}
	for _, ref := range analysis.References {
		if ref.Kind != "rust-module" && ref.Kind != "rust-path-module" {
			continue
		}
		declaration := rustModuleDeclaration{name: ref.Path}
		switch ref.Kind {
		case "rust-path-module":
			declaration.explicitTarget = ref.ExplicitTarget
		case "rust-module":
			declaration.unsupported = rustModuleAttributeKind(root, analysis.Path, ref.Line) != ""
		}
		index.moduleDecls[analysis.Path][ref.Path] = append(index.moduleDecls[analysis.Path][ref.Path], declaration)
	}
	for name, declarations := range index.moduleDecls[analysis.Path] {
		var explicit []rustModuleDeclaration
		unsupported := false
		for _, declaration := range declarations {
			if declaration.explicitTarget != "" {
				explicit = appendUniqueRustDeclaration(explicit, declaration)
			} else if declaration.unsupported {
				unsupported = true
			}
		}
		switch {
		case len(explicit) > 0:
			index.moduleDecls[analysis.Path][name] = explicit
		case unsupported:
			index.moduleDecls[analysis.Path][name] = []rustModuleDeclaration{{name: name, unsupported: true}}
		default:
			index.moduleDecls[analysis.Path][name] = []rustModuleDeclaration{{name: name}}
		}
	}
}

func appendUniqueRustDeclaration(declarations []rustModuleDeclaration, candidate rustModuleDeclaration) []rustModuleDeclaration {
	for _, declaration := range declarations {
		if declaration == candidate {
			return declarations
		}
	}
	return append(declarations, candidate)
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

type rustModuleEdgeCandidate struct {
	parent      string
	name        string
	child       string
	locationKey string
}

func (index *rustWorkspaceIndex) buildRustModuleLocations(ctx context.Context, root string, files []FileInfo) error {
	fileCounts := make(map[string]int)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(file.Path), ".rs") {
			fileCounts[filepath.Clean(file.Path)]++
		}
	}

	candidates := make(map[string]map[string]rustModuleLocation)
	var edges []rustModuleEdgeCandidate
	visited := make(map[string]bool)
	var walk func(rustModuleLocation, map[string]bool) error
	walk = func(location rustModuleLocation, stack map[string]bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		location.file = filepath.Clean(location.file)
		key := rustModuleLocationKey(location)
		if candidates[location.file] == nil {
			candidates[location.file] = make(map[string]rustModuleLocation)
		}
		candidates[location.file][key] = location
		if stack[location.file] || visited[key] {
			return nil
		}
		visited[key] = true
		nextStack := make(map[string]bool, len(stack)+1)
		for path := range stack {
			nextStack[path] = true
		}
		nextStack[location.file] = true

		for name, declarations := range index.moduleDecls[location.file] {
			if err := ctx.Err(); err != nil {
				return err
			}
			resolved := make(map[string]rustModuleLocation)
			for _, declaration := range declarations {
				if declaration.unsupported {
					continue
				}
				child, childBase := resolveRustModuleDeclaration(root, location, declaration, fileCounts)
				if child == "" {
					continue
				}
				resolved[child] = rustModuleLocation{
					file: child, parent: location.file, name: name, childBase: childBase,
					packageRoot: location.packageRoot, target: location.target,
				}
			}
			if len(resolved) != 1 {
				continue
			}
			for child, childLocation := range resolved {
				childKey := rustModuleLocationKey(childLocation)
				edges = append(edges, rustModuleEdgeCandidate{parent: location.file, name: name, child: child, locationKey: childKey})
				if err := walk(childLocation, nextStack); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, pkg := range index.packages {
		for _, target := range pkg.targets {
			if err := ctx.Err(); err != nil {
				return err
			}
			rootFile := filepath.Clean(target.rootFile)
			if fileCounts[rootFile] != 1 {
				continue
			}
			location := rustModuleLocation{
				file: rootFile, childBase: filepath.Dir(rootFile), packageRoot: pkg.root, target: target,
			}
			if err := walk(location, nil); err != nil {
				return err
			}
		}
	}

	index.moduleLocations = make(map[string]rustModuleLocation)
	index.ambiguousModules = make(map[string]bool)
	for file, fileCandidates := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(fileCandidates) != 1 {
			index.ambiguousModules[file] = true
			continue
		}
		for _, location := range fileCandidates {
			index.moduleLocations[file] = location
		}
	}

	index.moduleChildren = make(map[string]map[string]string)
	ambiguousEdges := make(map[string]bool)
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent, parentOK := index.moduleLocations[edge.parent]
		child, childOK := index.moduleLocations[edge.child]
		if !parentOK || !childOK || rustModuleLocationKey(child) != edge.locationKey || child.parent != parent.file {
			continue
		}
		edgeKey := edge.parent + "\x00" + edge.name
		if existing := index.moduleChildren[edge.parent][edge.name]; existing != "" && existing != edge.child {
			ambiguousEdges[edgeKey] = true
			delete(index.moduleChildren[edge.parent], edge.name)
			continue
		}
		if ambiguousEdges[edgeKey] {
			continue
		}
		if index.moduleChildren[edge.parent] == nil {
			index.moduleChildren[edge.parent] = make(map[string]string)
		}
		index.moduleChildren[edge.parent][edge.name] = edge.child
	}
	return ctx.Err()
}

func rustModuleLocationKey(location rustModuleLocation) string {
	return strings.Join([]string{
		location.file, location.parent, location.name, location.childBase,
		location.packageRoot, location.target.rootFile, location.target.kind,
	}, "\x00")
}

func resolveRustModuleDeclaration(root string, parent rustModuleLocation, declaration rustModuleDeclaration, fileCounts map[string]int) (string, string) {
	if declaration.explicitTarget != "" {
		target := resolveRustExplicitModule(root, parent.file, declaration.explicitTarget)
		if target == "" || fileCounts[target] != 1 {
			return "", ""
		}
		return target, filepath.Dir(target)
	}
	var resolved string
	for _, candidate := range []string{
		filepath.Join(parent.childBase, declaration.name+".rs"),
		filepath.Join(parent.childBase, declaration.name, "mod.rs"),
	} {
		candidate = filepath.Clean(candidate)
		if fileCounts[candidate] == 1 {
			if resolved != "" {
				return "", ""
			}
			resolved = candidate
		}
	}
	if resolved == "" {
		return "", ""
	}
	return resolved, filepath.Clean(filepath.Join(parent.childBase, declaration.name))
}

func resolveRustExplicitModule(root, declaringFile, literal string) string {
	value, ok := parseRustStringLiteral(literal)
	if !ok || value == "" {
		return ""
	}
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.Dir(declaringFile), path)
	}
	rel, ok := projectRelativePath(root, path)
	if !ok || !strings.EqualFold(filepath.Ext(rel), ".rs") {
		return ""
	}
	return filepath.Clean(rel)
}

// resolveRustInclude resolves include!(...) relative to the declaring file.
// byExact also indexes files under their extension-stripped key, so accept
// only when the target itself is indexed exactly once.
func resolveRustInclude(root, declaringFile, literal string, idx *fileIndex) string {
	target := resolveRustExplicitModule(root, declaringFile, literal)
	if target == "" {
		return ""
	}
	exact := 0
	for _, file := range idx.byExact[target] {
		if file == target {
			exact++
		}
	}
	if exact != 1 {
		return ""
	}
	return target
}

func parseRustStringLiteral(literal string) (string, bool) {
	literal = strings.TrimSpace(literal)
	if strings.HasPrefix(literal, "r") {
		quote := strings.IndexByte(literal, '"')
		if quote < 1 {
			return "", false
		}
		hashes := literal[1:quote]
		if strings.Trim(hashes, "#") != "" {
			return "", false
		}
		suffix := "\"" + hashes
		if !strings.HasSuffix(literal, suffix) || len(literal) < quote+1+len(suffix) {
			return "", false
		}
		return literal[quote+1 : len(literal)-len(suffix)], true
	}
	return parseRustCookedString(literal)
}

func parseRustCookedString(literal string) (string, bool) {
	if len(literal) < 2 || literal[0] != '"' || literal[len(literal)-1] != '"' {
		return "", false
	}
	var value strings.Builder
	for i := 1; i < len(literal)-1; i++ {
		if literal[i] != '\\' {
			if literal[i] == '"' {
				return "", false
			}
			value.WriteByte(literal[i])
			continue
		}
		i++
		if i >= len(literal)-1 {
			return "", false
		}
		switch literal[i] {
		case '0':
			value.WriteByte(0)
		case 't':
			value.WriteByte('\t')
		case 'n':
			value.WriteByte('\n')
		case 'r':
			value.WriteByte('\r')
		case '\'', '"', '\\':
			value.WriteByte(literal[i])
		case 'x':
			if i+2 >= len(literal)-1 {
				return "", false
			}
			escaped, err := strconv.ParseUint(literal[i+1:i+3], 16, 8)
			if err != nil || escaped > 0x7f {
				return "", false
			}
			value.WriteByte(byte(escaped))
			i += 2
		case 'u':
			if i+1 >= len(literal)-1 || literal[i+1] != '{' {
				return "", false
			}
			end := strings.IndexByte(literal[i+2:len(literal)-1], '}')
			if end < 0 {
				return "", false
			}
			end += i + 2
			digits := strings.ReplaceAll(literal[i+2:end], "_", "")
			if len(digits) == 0 || len(digits) > 6 {
				return "", false
			}
			escaped, err := strconv.ParseUint(digits, 16, 32)
			if err != nil || !utf8.ValidRune(rune(escaped)) {
				return "", false
			}
			value.WriteRune(rune(escaped))
			i = end
		case '\n':
			for i+1 < len(literal)-1 && (literal[i+1] == ' ' || literal[i+1] == '\t' || literal[i+1] == '\n' || literal[i+1] == '\r') {
				i++
			}
		case '\r':
			if i+1 >= len(literal)-1 || literal[i+1] != '\n' {
				return "", false
			}
			i++
			for i+1 < len(literal)-1 && (literal[i+1] == ' ' || literal[i+1] == '\t' || literal[i+1] == '\n' || literal[i+1] == '\r') {
				i++
			}
		default:
			return "", false
		}
	}
	return value.String(), true
}

func resolveRustAskamaTemplate(root, fromFile, literal string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	value, ok := parseRustStringLiteral(literal)
	if !ok || value == "" {
		return ""
	}
	path := filepath.FromSlash(value)
	if filepath.IsAbs(path) {
		return ""
	}
	pkg, ok := workspace.packageForFile(fromFile)
	if !ok || !pkg.authoritative {
		return ""
	}
	if _, ok := workspace.targetForFile(fromFile, idx); !ok {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, pkg.root, "askama.toml")); !os.IsNotExist(err) {
		return ""
	}
	templateRoot := filepath.Clean(filepath.Join(pkg.root, "templates"))
	target := filepath.Clean(filepath.Join(templateRoot, path))
	if !pathWithin(target, templateRoot) {
		return ""
	}
	files := idx.byExact[target]
	if len(files) != 1 || files[0] != target {
		return ""
	}
	return files[0]
}

func resolveRustReferences(root string, analysis FileAnalysis, idx *fileIndex, workspace *rustWorkspaceIndex) []string {
	var resolved []string
	for _, ref := range analysis.References {
		switch ref.Kind {
		case "rust-path-module":
			if target := workspace.moduleChildren[analysis.Path][ref.Path]; target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-module":
			if rustModuleAttributeKind(root, analysis.Path, ref.Line) != "" {
				continue
			}
			if target := resolveRustModule(ref.Path, analysis.Path, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-use":
			for _, path := range expandRustUseReferencePaths(ref.Path) {
				if target := resolveRustUsePath(path, analysis.Path, idx, workspace); target != "" && target != analysis.Path {
					resolved = append(resolved, target)
				}
			}
		case "rust-path":
			if target := resolveRustReferencePath(ref.Path, analysis.Path, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-askama-template":
			if target := resolveRustAskamaTemplate(root, analysis.Path, ref.ExplicitTarget, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-include":
			if target := resolveRustInclude(root, analysis.Path, ref.ExplicitTarget, idx); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		case "rust-build-input":
			if target := resolveRustBuildScriptInput(analysis.Path, ref.Path, idx, workspace); target != "" && target != analysis.Path {
				resolved = append(resolved, target)
			}
		}
	}
	return dedupe(resolved)
}

func resolveRustModule(name, fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	fromFile = filepath.Clean(fromFile)
	if target := workspace.moduleChildren[fromFile][name]; target != "" {
		return target
	}
	_, owned := workspace.moduleLocations[fromFile]
	if len(workspace.moduleDecls[fromFile][name]) > 0 && (owned || workspace.ambiguousModules[fromFile]) {
		return ""
	}
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
	if location, ok := workspace.moduleLocations[filepath.Clean(fromFile)]; ok {
		return location.childBase
	}
	if workspace.ambiguousModules[filepath.Clean(fromFile)] {
		return ""
	}
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
	if (parts[0] == "self" || parts[0] == "super") && !workspace.hasLogicalLocation(fromFile) {
		base := rustModuleDir(fromFile, idx, workspace)
		if base == "" {
			return ""
		}
		if parts[0] == "self" {
			parts = parts[1:]
		} else {
			for len(parts) > 0 && parts[0] == "super" {
				base = filepath.Dir(base)
				parts = parts[1:]
			}
		}
		return resolveRustPathFromDirectory(base, parts, idx)
	}

	var current string
	rootFallback := false
	switch parts[0] {
	case "crate":
		target, ok := workspace.targetForFile(fromFile, idx)
		if !ok {
			return ""
		}
		current = target.rootFile
		rootFallback = true
		parts = parts[1:]
	case "self":
		current = filepath.Clean(fromFile)
		if _, ok := workspace.moduleLocations[current]; !ok {
			return ""
		}
		parts = parts[1:]
	case "super":
		current = filepath.Clean(fromFile)
		for len(parts) > 0 && parts[0] == "super" {
			location, ok := workspace.moduleLocations[current]
			if !ok || location.parent == "" {
				return ""
			}
			current = location.parent
			parts = parts[1:]
		}
	default:
		if child := resolveRustModule(parts[0], fromFile, idx, workspace); child != "" {
			current = child
			parts = parts[1:]
			break
		}
		packages := workspace.externalPackagesForPath(parts[0], fromFile, idx)
		if len(packages) != 1 {
			return ""
		}
		if packages[0].lib == nil || !rustTargetIndexed(*packages[0].lib, idx) {
			return ""
		}
		current = packages[0].lib.rootFile
		rootFallback = true
		parts = parts[1:]
	}

	return resolveRustPathParts(current, parts, rootFallback, idx, workspace)
}

func resolveRustReferencePath(path, fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "::")
	root, _, ok := strings.Cut(path, "::")
	if !ok {
		return ""
	}
	pkg, found := workspace.packageForFile(fromFile)
	if !found || pkg.crateID != strings.TrimPrefix(root, "r#") {
		return resolveRustPath(path, fromFile, idx, workspace)
	}
	if !pkg.authoritative {
		return ""
	}
	return resolveRustUsePath(path, fromFile, idx, workspace)
}

func resolveRustUsePath(path, fromFile string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "::")
	parts := strings.Split(path, "::")
	if len(parts) < 2 || parts[0] == "crate" || parts[0] == "self" || parts[0] == "super" {
		return resolveRustPath(path, fromFile, idx, workspace)
	}
	pkg, ok := workspace.packageForFile(fromFile)
	if !ok || !pkg.authoritative {
		return ""
	}
	if pkg.lib == nil || pkg.crateID != strings.TrimPrefix(parts[0], "r#") || !rustTargetIndexed(*pkg.lib, idx) {
		return resolveRustPath(path, fromFile, idx, workspace)
	}
	callerTarget, ok := workspace.targetForFile(fromFile, idx)
	if !ok || callerTarget.rootFile == pkg.lib.rootFile || callerTarget.kind == rustTargetCustomBuild {
		return resolveRustPath(path, fromFile, idx, workspace)
	}
	return resolveRustPathParts(pkg.lib.rootFile, parts[1:], true, idx, workspace)
}

func resolveRustPathParts(current string, parts []string, rootFallback bool, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	resolvedChild := false
	for _, part := range parts {
		child := resolveRustModule(part, current, idx, workspace)
		if child == "" {
			break
		}
		current = child
		resolvedChild = true
	}
	if resolvedChild || rootFallback {
		return current
	}
	return ""
}

func (index *rustWorkspaceIndex) hasLogicalLocation(path string) bool {
	_, ok := index.moduleLocations[filepath.Clean(path)]
	return ok
}

func resolveRustPathFromDirectory(base string, parts []string, idx *fileIndex) string {
	for i := len(parts); i > 0; i-- {
		modulePath := filepath.Join(append([]string{base}, parts[:i]...)...)
		for _, candidate := range []string{modulePath + ".rs", filepath.Join(modulePath, "mod.rs")} {
			if files := idx.byExact[candidate]; len(files) == 1 {
				return files[0]
			}
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
	path = filepath.Clean(path)
	if location, ok := index.moduleLocations[path]; ok {
		return location.target, true
	}
	if index.ambiguousModules[path] {
		return rustTarget{}, false
	}
	pkg, ok := index.packageForFile(path)
	if !ok {
		return rustTarget{}, false
	}
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
	path = filepath.Clean(path)
	if location, ok := index.moduleLocations[path]; ok {
		return location.target.rootFile == target.rootFile && location.target.kind == target.kind
	}
	if index.ambiguousModules[path] {
		return false
	}
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
		if len(index.moduleDecls[parent][name]) == 0 {
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
	if location, ok := index.moduleLocations[path]; ok {
		pkg, ok := index.byRoot[location.packageRoot]
		return pkg, ok
	}
	if index.ambiguousModules[path] {
		return rustPackage{}, false
	}
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

func rustModuleAttributeKind(root, file string, line int) string {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		line = len(lines)
	}
	for i := line - 1; i >= 0; {
		if next, skipped := skipRustTriviaBackward(lines, i); skipped {
			i = next
			continue
		}
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasSuffix(trimmed, "]") {
			break
		}
		end := i
		depth := 0
		start := -1
		for ; i >= 0; i-- {
			if next, skipped := skipRustTriviaBackward(lines, i); skipped {
				i = next + 1
				continue
			}
			part := strings.TrimSpace(lines[i])
			depth += strings.Count(part, "]") - strings.Count(part, "[")
			if strings.HasPrefix(part, "#[") && depth == 0 {
				start = i
				break
			}
		}
		if start < 0 {
			break
		}
		attribute := strings.Join(lines[start:end+1], " ")
		switch {
		case strings.HasPrefix(strings.TrimSpace(attribute), "#[path") && rustAttributeAssignsPath(attribute):
			return "direct"
		case strings.HasPrefix(strings.TrimSpace(attribute), "#[cfg_attr") && rustAttributeAssignsPath(attribute):
			return "conditional"
		}
		i = start - 1
	}
	return ""
}

func skipRustTriviaBackward(lines []string, line int) (int, bool) {
	trimmed := strings.TrimSpace(lines[line])
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return line - 1, true
	}
	if !strings.HasSuffix(trimmed, "*/") {
		return line, false
	}
	depth := 0
	for i := line; i >= 0; i-- {
		part := lines[i]
		depth += strings.Count(part, "*/") - strings.Count(part, "/*")
		if depth <= 0 {
			return i - 1, true
		}
	}
	return -1, true
}

func rustAttributeAssignsPath(attribute string) bool {
	return rustAttributeAssigns(attribute, "path")
}

func rustAttributeAssigns(attribute, name string) bool {
	for offset := 0; offset < len(attribute); {
		relative := strings.Index(attribute[offset:], name)
		if relative < 0 {
			return false
		}
		i := offset + relative
		beforeOK := i == 0 || !isRustIdentifierByte(attribute[i-1])
		j := i + len(name)
		afterOK := j == len(attribute) || !isRustIdentifierByte(attribute[j])
		if beforeOK && afterOK {
			for j < len(attribute) && (attribute[j] == ' ' || attribute[j] == '\t' || attribute[j] == '\n' || attribute[j] == '\r') {
				j++
			}
			if j < len(attribute) && attribute[j] == '=' {
				return true
			}
		}
		offset = i + len(name)
	}
	return false
}

func isRustIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
