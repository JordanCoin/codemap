package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type jsWorkspaceResolver struct {
	packageScopes []jsPackageScope
	denoScopes    []jsImportScope
	workspaces    []jsPackageWorkspace
}

type jsPackageWorkspace struct {
	root     string
	packages map[string][]*jsWorkspacePackage
}

type jsWorkspacePackage struct {
	root         string
	name         string
	exports      jsSpecifierMap
	hasExports   bool
	entryTargets []string
	sourceRoot   string
	outDir       string
}

type jsPackageScope struct {
	root    string
	pkg     *jsWorkspacePackage
	imports jsSpecifierMap
}

type jsImportScope struct {
	root    string
	imports jsSpecifierMap
}

type jsWorkspaceManifest struct {
	root       string
	pkg        *jsWorkspacePackage
	imports    jsSpecifierMap
	workspaces []string
}

type jsRushProject struct {
	name string
	root string
}

type jsSpecifierMap struct {
	exact   map[string]jsSpecifierTarget
	dynamic []jsSpecifierTarget
}

type jsSpecifierTarget struct {
	key    string
	target string
	valid  bool
	prefix bool
}

func buildJSWorkspaceResolver(ctx context.Context, root string, files []FileInfo) (*jsWorkspaceResolver, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	packages := make(map[string]*jsWorkspaceManifest)
	denoConfigs := make(map[string]*jsWorkspaceManifest)
	tsConfigs := make(map[string]map[string]any)
	pnpmWorkspaces := make(map[string][]string)
	rushWorkspaces := make(map[string][]jsRushProject)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.Base(file.Path)
		manifestRoot := cleanRepoPath(filepath.Dir(file.Path))
		if name == "pnpm-workspace.yaml" {
			pnpmWorkspaces[manifestRoot] = readPnpmWorkspace(filepath.Join(root, file.Path))
			continue
		}
		if name == "tsconfig.json" {
			if doc, ok := readJSWorkspaceManifest(filepath.Join(root, file.Path)); ok {
				tsConfigs[manifestRoot] = doc
			}
			continue
		}
		if name == "rush.json" {
			if doc, ok := readJSWorkspaceManifest(filepath.Join(root, file.Path)); ok {
				if projects, ok := parseRushProjects(manifestRoot, doc); ok {
					rushWorkspaces[manifestRoot] = projects
				}
			}
			continue
		}
		if name != "package.json" && name != "deno.json" && name != "deno.jsonc" {
			continue
		}

		doc, ok := readJSWorkspaceManifest(filepath.Join(root, file.Path))
		if !ok {
			continue
		}
		if name == "package.json" {
			packages[manifestRoot] = parsePackageManifest(manifestRoot, doc)
		} else {
			denoConfigs[manifestRoot] = parseDenoManifest(manifestRoot, doc)
		}
	}
	for manifestRoot := range tsConfigs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if manifest := packages[manifestRoot]; manifest != nil {
			// Parse the merged config: packages whose tsconfig.json inherits
			// rootDir/outDir via extends would otherwise leave both empty and
			// drop their workspace exports from the graph.
			configPath := filepath.Join(root, filepath.FromSlash(manifestRoot), "tsconfig.json")
			manifest.pkg.sourceRoot, manifest.pkg.outDir = mergedTSOutputDirs(configPath)
		}
	}

	resolver := &jsWorkspaceResolver{}
	for _, manifest := range packages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolver.packageScopes = append(resolver.packageScopes, jsPackageScope{
			root:    manifest.root,
			pkg:     manifest.pkg,
			imports: manifest.imports,
		})
	}
	for _, manifest := range denoConfigs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolver.denoScopes = append(resolver.denoScopes, jsImportScope{
			root:    manifest.root,
			imports: manifest.imports,
		})
	}
	sort.Slice(resolver.packageScopes, func(i, j int) bool {
		return len(resolver.packageScopes[i].root) > len(resolver.packageScopes[j].root)
	})
	sort.Slice(resolver.denoScopes, func(i, j int) bool {
		return len(resolver.denoScopes[i].root) > len(resolver.denoScopes[j].root)
	})

	workspaces := make(map[string]*jsPackageWorkspace)
	addWorkspace := func(owner *jsWorkspaceManifest, ownerRoot string, patterns []string, groups ...map[string]*jsWorkspaceManifest) error {
		if len(patterns) == 0 {
			return nil
		}
		workspace := workspaces[ownerRoot]
		if workspace == nil {
			workspace = newJSPackageWorkspace(ownerRoot)
			workspaces[ownerRoot] = workspace
		}
		if owner != nil {
			workspace.add(owner.pkg)
		}
		return addJSWorkspaceMembers(ctx, workspace, ownerRoot, patterns, groups...)
	}
	for _, owner := range packages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := addWorkspace(owner, owner.root, owner.workspaces, packages); err != nil {
			return nil, err
		}
	}
	for _, owner := range denoConfigs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := addWorkspace(owner, owner.root, owner.workspaces, packages, denoConfigs); err != nil {
			return nil, err
		}
	}
	for ownerRoot, patterns := range pnpmWorkspaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := addWorkspace(nil, ownerRoot, patterns, packages); err != nil {
			return nil, err
		}
	}
	for ownerRoot, projects := range rushWorkspaces {
		members, ok, err := validateRushProjects(ctx, projects, packages)
		if err != nil {
			return nil, err
		}
		if !ok || len(members) == 0 {
			continue
		}
		workspace := workspaces[ownerRoot]
		if workspace == nil {
			workspace = newJSPackageWorkspace(ownerRoot)
			workspaces[ownerRoot] = workspace
		}
		for _, member := range members {
			workspace.add(member.pkg)
		}
	}
	for _, workspace := range workspaces {
		resolver.workspaces = append(resolver.workspaces, *workspace)
	}
	sort.Slice(resolver.workspaces, func(i, j int) bool {
		return len(resolver.workspaces[i].root) > len(resolver.workspaces[j].root)
	})

	return resolver, nil
}

func addJSWorkspaceMembers(ctx context.Context, workspace *jsPackageWorkspace, ownerRoot string, patterns []string, groups ...map[string]*jsWorkspaceManifest) error {
	for _, manifests := range groups {
		for _, member := range manifests {
			if err := ctx.Err(); err != nil {
				return err
			}
			if matchesWorkspaceMember(ownerRoot, member.root, patterns) {
				workspace.add(member.pkg)
			}
		}
	}
	return nil
}

func parseRushProjects(ownerRoot string, doc map[string]any) ([]jsRushProject, bool) {
	items, ok := doc["projects"].([]any)
	if !ok {
		return nil, false
	}
	projects := make([]jsRushProject, 0, len(items))
	names := make(map[string]bool, len(items))
	roots := make(map[string]bool, len(items))
	for _, item := range items {
		project, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		name, nameOK := project["packageName"].(string)
		folder, folderOK := project["projectFolder"].(string)
		name = strings.TrimSpace(name)
		folder, folderOK = normalizeRushProjectFolder(folder, folderOK)
		if !nameOK || name == "" || !folderOK || names[name] || roots[folder] {
			return nil, false
		}
		names[name], roots[folder] = true, true
		projects = append(projects, jsRushProject{
			name: name,
			root: cleanRepoPath(filepath.Join(repoPath(ownerRoot), filepath.FromSlash(folder))),
		})
	}
	return projects, true
}

func normalizeRushProjectFolder(folder string, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	folder = strings.TrimSpace(strings.ReplaceAll(folder, `\`, "/"))
	folder = strings.TrimPrefix(folder, "./")
	if folder == "" || strings.HasPrefix(folder, "!") || path.IsAbs(folder) || hasDrivePrefix(folder) || strings.ContainsAny(folder, "*?[\x00") {
		return "", false
	}
	for _, part := range strings.Split(folder, "/") {
		if part == ".." {
			return "", false
		}
	}
	folder = path.Clean(folder)
	if folder == "." || path.IsAbs(folder) || hasDrivePrefix(folder) {
		return "", false
	}
	return folder, true
}

func validateRushProjects(ctx context.Context, projects []jsRushProject, packages map[string]*jsWorkspaceManifest) ([]*jsWorkspaceManifest, bool, error) {
	members := make([]*jsWorkspaceManifest, 0, len(projects))
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		member := packages[project.root]
		if member == nil || member.pkg == nil || member.pkg.name != project.name {
			return nil, false, nil
		}
		members = append(members, member)
	}
	return members, true, nil
}

func newJSPackageWorkspace(root string) *jsPackageWorkspace {
	return &jsPackageWorkspace{
		root:     root,
		packages: make(map[string][]*jsWorkspacePackage),
	}
}

func (w *jsPackageWorkspace) add(pkg *jsWorkspacePackage) {
	if pkg == nil || pkg.name == "" {
		return
	}
	for _, existing := range w.packages[pkg.name] {
		if existing.root == pkg.root {
			return
		}
	}
	w.packages[pkg.name] = append(w.packages[pkg.name], pkg)
}

func (r *jsWorkspaceResolver) resolve(imp, fromFile string, idx *fileIndex, sourceLanguage string) []string {
	if r == nil || isExternalJSSpecifier(imp) {
		return nil
	}

	if strings.HasPrefix(imp, "#") {
		scope := r.nearestPackageScope(fromFile)
		if scope == nil {
			return nil
		}
		target, matched := scope.imports.resolve(imp)
		if !matched || !target.valid {
			return nil
		}
		return scope.pkg.resolveTarget(target.target, idx, sourceLanguage)
	}

	if scope := r.nearestDenoScope(fromFile); scope != nil {
		if target, matched := scope.imports.resolve(imp); matched {
			if !target.valid {
				return nil
			}
			return resolveManifestTarget(scope.root, target.target, idx, sourceLanguage)
		}
	}

	name, subpath, ok := splitJSPackageSpecifier(imp)
	if !ok {
		return nil
	}
	var candidates []*jsWorkspacePackage
	for _, workspace := range r.workspaces {
		if pathContains(workspace.root, fromFile) {
			candidates = workspace.packages[name]
			break
		}
	}
	if len(candidates) != 1 {
		scope := r.nearestPackageScope(fromFile)
		if scope == nil || scope.pkg == nil || scope.pkg.name != name {
			return nil
		}
		candidates = []*jsWorkspacePackage{scope.pkg}
	}

	return candidates[0].resolve(subpath, idx, sourceLanguage)
}

func (r *jsWorkspaceResolver) nearestPackageScope(fromFile string) *jsPackageScope {
	for i := range r.packageScopes {
		if pathContains(r.packageScopes[i].root, fromFile) {
			return &r.packageScopes[i]
		}
	}
	return nil
}

func (r *jsWorkspaceResolver) nearestDenoScope(fromFile string) *jsImportScope {
	for i := range r.denoScopes {
		if pathContains(r.denoScopes[i].root, fromFile) {
			return &r.denoScopes[i]
		}
	}
	return nil
}

func (pkg *jsWorkspacePackage) resolve(subpath string, idx *fileIndex, sourceLanguage string) []string {
	key := "."
	if subpath != "" {
		key = "./" + subpath
	}
	if pkg.hasExports {
		target, matched := pkg.exports.resolve(key)
		if !matched || !target.valid {
			return nil
		}
		return pkg.resolveTarget(target.target, idx, sourceLanguage)
	}
	if subpath != "" {
		return pkg.resolveInferredTarget("./"+subpath, idx, sourceLanguage)
	}
	if len(pkg.entryTargets) != 1 {
		return nil
	}
	return pkg.resolveInferredTarget(pkg.entryTargets[0], idx, sourceLanguage)
}

func resolveManifestTarget(root, target string, idx *fileIndex, sourceLanguage string) []string {
	if isTypeOnlyTarget(target) {
		return nil
	}
	localPath, ok := manifestLocalPath(root, target)
	if !ok {
		return nil
	}
	if idx.byExact[localPath] == 1 && languagesCompatible(sourceLanguage, DetectLanguage(localPath)) {
		return []string{localPath}
	}
	return nil
}

func (pkg *jsWorkspacePackage) resolveTarget(target string, idx *fileIndex, sourceLanguage string) []string {
	if files := resolveManifestTarget(pkg.root, target, idx, sourceLanguage); len(files) > 0 {
		return files
	}
	if pkg.sourceRoot == "" || pkg.outDir == "" {
		return nil
	}

	localPath, ok := manifestLocalPath(pkg.root, target)
	if !ok {
		return nil
	}
	outRoot := cleanRepoPath(filepath.Join(repoPath(pkg.root), filepath.FromSlash(pkg.outDir)))
	if !pathContains(outRoot, localPath) || localPath == outRoot {
		return nil
	}
	relative, err := filepath.Rel(outRoot, localPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	sourcePath := cleanRepoPath(filepath.Join(repoPath(pkg.root), filepath.FromSlash(pkg.sourceRoot), relative))
	for _, extension := range []string{".mjs", ".cjs", ".jsx", ".js"} {
		sourcePath = strings.TrimSuffix(sourcePath, extension)
	}
	return tryExactMatch(sourcePath, idx, sourceLanguage)
}

func (pkg *jsWorkspacePackage) resolveInferredTarget(target string, idx *fileIndex, sourceLanguage string) []string {
	if localPath, ok := manifestLocalPath(pkg.root, target); ok {
		if files := tryExactMatch(localPath, idx, sourceLanguage); len(files) > 0 {
			return files
		}
	}
	return pkg.resolveTarget(target, idx, sourceLanguage)
}

func (m jsSpecifierMap) resolve(specifier string) (jsSpecifierTarget, bool) {
	if target, ok := m.exact[specifier]; ok {
		return target, true
	}

	var matches []jsSpecifierTarget
	bestSpecificity := -1
	for _, mapping := range m.dynamic {
		var remainder string
		switch {
		case mapping.prefix && strings.HasPrefix(specifier, mapping.key):
			remainder = strings.TrimPrefix(specifier, mapping.key)
		case !mapping.prefix && strings.Count(mapping.key, "*") == 1:
			prefix, suffix, _ := strings.Cut(mapping.key, "*")
			if !strings.HasPrefix(specifier, prefix) || !strings.HasSuffix(specifier, suffix) {
				continue
			}
			remainder = specifier[len(prefix) : len(specifier)-len(suffix)]
		default:
			continue
		}

		specificity := len(mapping.key)
		if specificity < bestSpecificity {
			continue
		}
		resolved := mapping
		if mapping.valid {
			if mapping.prefix {
				resolved.target += remainder
			} else {
				resolved.target = strings.Replace(mapping.target, "*", remainder, 1)
			}
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			matches = matches[:0]
		}
		matches = append(matches, resolved)
	}
	if len(matches) != 1 {
		return jsSpecifierTarget{}, len(matches) > 0
	}
	return matches[0], true
}

func parsePackageManifest(root string, doc map[string]any) *jsWorkspaceManifest {
	name, _ := doc["name"].(string)
	pkg := &jsWorkspacePackage{root: root, name: name}
	pkg.exports, pkg.hasExports = parseExports(doc)
	pkg.entryTargets = unambiguousEntryTargets(doc)
	return &jsWorkspaceManifest{
		root:       root,
		pkg:        pkg,
		imports:    parsePackageImports(doc["imports"]),
		workspaces: parseWorkspacePatterns(doc["workspaces"], "packages"),
	}
}

func parseDenoManifest(root string, doc map[string]any) *jsWorkspaceManifest {
	name, _ := doc["name"].(string)
	pkg := &jsWorkspacePackage{root: root, name: name}
	pkg.exports, pkg.hasExports = parseExports(doc)
	return &jsWorkspaceManifest{
		root:       root,
		pkg:        pkg,
		imports:    parseDenoImports(doc["imports"]),
		workspaces: parseWorkspacePatterns(doc["workspace"], "members"),
	}
}

func parseExports(doc map[string]any) (jsSpecifierMap, bool) {
	value, exists := doc["exports"]
	if !exists {
		return jsSpecifierMap{}, false
	}
	mappings := newJSSpecifierMap()
	switch exports := value.(type) {
	case string:
		mappings.addExact(".", exports)
	case map[string]any:
		subpaths := true
		for key := range exports {
			subpaths = subpaths && strings.HasPrefix(key, ".")
		}
		if !subpaths {
			target, ok := unambiguousRuntimeTarget(exports)
			if !ok {
				mappings.addInvalid(".")
			} else {
				mappings.addExact(".", target)
			}
			break
		}
		for key, value := range exports {
			target, ok := unambiguousRuntimeTarget(value)
			if !ok {
				mappings.addInvalid(key)
			} else {
				mappings.addPattern(key, target)
			}
		}
	default:
		mappings.addInvalid(".")
	}
	return mappings, true
}

func unambiguousRuntimeTarget(value any) (string, bool) {
	targets := make(map[string]bool)
	valid := true
	var collect func(any, bool)
	collect = func(current any, typeOnly bool) {
		if typeOnly {
			return
		}
		switch current := current.(type) {
		case string:
			if !isTypeOnlyTarget(current) {
				targets[current] = true
			}
		case map[string]any:
			for key, nested := range current {
				if key == "types" || strings.HasPrefix(key, "types@") {
					collect(nested, true)
				} else if isJSRuntimeCondition(key) {
					collect(nested, false)
				} else {
					valid = false
				}
			}
		default:
			valid = false
		}
	}
	collect(value, false)
	if !valid || len(targets) != 1 {
		return "", false
	}
	for target := range targets {
		return target, true
	}
	return "", false
}

func isJSRuntimeCondition(condition string) bool {
	switch condition {
	case "default", "import", "require", "module-sync", "node", "node-addons", "browser", "development", "production", "deno", "bun":
		return true
	default:
		return false
	}
}

func isTypeOnlyTarget(target string) bool {
	for _, suffix := range []string{".d.ts", ".d.mts", ".d.cts"} {
		if strings.HasSuffix(target, suffix) {
			return true
		}
	}
	return false
}

func parsePackageImports(value any) jsSpecifierMap {
	mappings := newJSSpecifierMap()
	imports, ok := value.(map[string]any)
	if !ok {
		return mappings
	}
	for key, target := range imports {
		if !strings.HasPrefix(key, "#") || key == "#" || strings.HasPrefix(key, "#/") {
			continue
		}
		targetString, ok := target.(string)
		if !ok {
			// Conditional shapes such as {"default": "./src/env.ts", "types":
			// "./src/env.d.ts"} are valid package-import targets and should use
			// the same runtime-target selection as exports instead of being
			// discarded as invalid.
			targetString, ok = unambiguousRuntimeTarget(target)
			if !ok {
				mappings.addInvalid(key)
				continue
			}
		}
		mappings.addPattern(key, targetString)
	}
	return mappings
}

func parseDenoImports(value any) jsSpecifierMap {
	mappings := newJSSpecifierMap()
	imports, ok := value.(map[string]any)
	if !ok {
		return mappings
	}
	for key, target := range imports {
		targetString, ok := target.(string)
		if !ok {
			mappings.addInvalid(key)
			continue
		}
		if strings.HasSuffix(key, "/") {
			mappings.addPrefix(key, targetString)
		} else {
			mappings.addExact(key, targetString)
		}
	}
	return mappings
}

func newJSSpecifierMap() jsSpecifierMap {
	return jsSpecifierMap{exact: make(map[string]jsSpecifierTarget)}
}

func (m *jsSpecifierMap) addExact(key, target string) {
	m.exact[key] = jsSpecifierTarget{key: key, target: target, valid: validLocalTarget(target)}
}

func (m *jsSpecifierMap) addInvalid(key string) {
	m.exact[key] = jsSpecifierTarget{key: key}
}

func (m *jsSpecifierMap) addPrefix(key, target string) {
	m.dynamic = append(m.dynamic, jsSpecifierTarget{
		key:    key,
		target: target,
		valid:  strings.HasSuffix(target, "/") && validLocalTarget(target),
		prefix: true,
	})
}

func (m *jsSpecifierMap) addPattern(key, target string) {
	keyStars := strings.Count(key, "*")
	targetStars := strings.Count(target, "*")
	if keyStars == 0 {
		m.addExact(key, target)
		return
	}
	m.dynamic = append(m.dynamic, jsSpecifierTarget{
		key:    key,
		target: target,
		valid:  keyStars == 1 && targetStars == 1 && validLocalTarget(target),
	})
}

func unambiguousEntryTargets(doc map[string]any) []string {
	seen := make(map[string]bool)
	for _, key := range []string{"module", "main"} {
		if target, ok := doc[key].(string); ok {
			target = strings.TrimSpace(filepath.ToSlash(target))
			if target != "" && !strings.Contains(target, ":") && !path.IsAbs(target) {
				if !strings.HasPrefix(target, "./") {
					target = "./" + target
				}
				if validLocalTarget(target) {
					seen[target] = true
				}
			}
		}
	}
	if len(seen) != 1 {
		return nil
	}
	for target := range seen {
		return []string{target}
	}
	return nil
}

func parseWorkspacePatterns(value any, objectKey string) []string {
	if object, ok := value.(map[string]any); ok {
		value = object[objectKey]
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var patterns []string
	for _, item := range items {
		pattern, ok := item.(string)
		if !ok {
			return nil
		}
		pattern, ok = normalizeWorkspacePattern(pattern)
		if !ok {
			return nil
		}
		patterns = append(patterns, pattern)
	}
	return patterns
}

func readPnpmWorkspace(manifestPath string) []string {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	var workspace struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &workspace); err != nil {
		return nil
	}
	patterns := make([]string, 0, len(workspace.Packages))
	for _, pattern := range workspace.Packages {
		pattern, ok := normalizeWorkspacePattern(pattern)
		if !ok {
			return nil
		}
		patterns = append(patterns, pattern)
	}
	return patterns
}

func parseTSOutputDirs(doc map[string]any) (string, string) {
	options, _ := doc["compilerOptions"].(map[string]any)
	rootDir, _ := options["rootDir"].(string)
	outDir, _ := options["outDir"].(string)
	rootDir = cleanManifestDir(rootDir)
	outDir = cleanManifestDir(outDir)
	return rootDir, outDir
}

// mergedTSOutputDirs reads a tsconfig.json, follows extends chains, and returns
// the effective compilerOptions.rootDir/outDir (child values override parents),
// matching the older path-alias loader which already follows extends.
func mergedTSOutputDirs(configPath string) (string, string) {
	options := mergedTSCompilerOptions(configPath)
	return parseTSOutputDirs(map[string]any{"compilerOptions": options})
}

func mergedTSCompilerOptions(configPath string) map[string]any {
	return mergedTSCompilerOptionsDepth(configPath, 0)
}

func mergedTSCompilerOptionsDepth(configPath string, depth int) map[string]any {
	// Guard against cyclic extends chains (a misconfiguration that would
	// otherwise recurse without bound).
	if depth > 32 {
		return nil
	}
	doc, ok := readJSWorkspaceManifest(configPath)
	if !ok {
		return nil
	}
	extends, _ := doc["extends"].(string)
	var merged map[string]any
	if extends != "" {
		parentPath := extends
		if !filepath.IsAbs(parentPath) {
			parentPath = filepath.Join(filepath.Dir(configPath), parentPath)
		}
		if !strings.HasSuffix(parentPath, ".json") {
			parentPath += ".json"
		}
		merged = mergedTSCompilerOptionsDepth(parentPath, depth+1)
	}
	child, _ := doc["compilerOptions"].(map[string]any)
	if merged == nil {
		return child
	}
	if child == nil {
		return merged
	}
	out := make(map[string]any, len(merged)+len(child))
	for key, value := range merged {
		out[key] = value
	}
	for key, value := range child {
		out[key] = value
	}
	return out
}

func cleanManifestDir(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(value)), "./")
	if value == "" || path.IsAbs(value) {
		return ""
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return ""
		}
	}
	return value
}

func matchesWorkspaceMember(ownerRoot, memberRoot string, patterns []string) bool {
	relative, err := filepath.Rel(repoPath(ownerRoot), repoPath(memberRoot))
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
		return false
	}
	included := false
	for _, pattern := range patterns {
		excluded := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if matchWorkspaceGlob(pattern, relative) {
			included = !excluded
		}
	}
	return included
}

func matchWorkspaceGlob(pattern, value string) bool {
	patternParts := strings.Split(pattern, "/")
	valueParts := strings.Split(value, "/")
	type position struct{ pattern, value int }
	memo := make(map[position]bool)
	seen := make(map[position]bool)

	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		key := position{patternIndex, valueIndex}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true

		switch {
		case patternIndex == len(patternParts):
			memo[key] = valueIndex == len(valueParts)
		case patternParts[patternIndex] == "**":
			memo[key] = match(patternIndex+1, valueIndex) ||
				(valueIndex < len(valueParts) && match(patternIndex, valueIndex+1))
		case valueIndex < len(valueParts):
			segmentMatch, err := path.Match(patternParts[patternIndex], valueParts[valueIndex])
			memo[key] = err == nil && segmentMatch && match(patternIndex+1, valueIndex+1)
		}
		return memo[key]
	}
	return match(0, 0)
}

func validWorkspacePattern(pattern string) bool {
	_, ok := normalizeWorkspacePattern(pattern)
	return ok
}

func normalizeWorkspacePattern(pattern string) (string, bool) {
	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	excluded := strings.HasPrefix(pattern, "!")
	if excluded {
		pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
	}
	pattern = strings.TrimPrefix(pattern, "./")
	if pattern == "" || strings.HasPrefix(pattern, "!") || path.IsAbs(pattern) || hasDrivePrefix(pattern) {
		return "", false
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == ".." {
			return "", false
		}
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, "candidate"); err != nil {
			return "", false
		}
	}
	if excluded {
		pattern = "!" + pattern
	}
	return pattern, true
}

func hasDrivePrefix(value string) bool {
	return len(value) >= 2 && value[1] == ':' &&
		(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z')
}

func validLocalTarget(target string) bool {
	if !strings.HasPrefix(target, "./") || strings.ContainsRune(target, 0) {
		return false
	}
	trimmed := strings.TrimPrefix(filepath.ToSlash(target), "./")
	if trimmed == "" {
		return false
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func manifestLocalPath(root, target string) (string, bool) {
	if !validLocalTarget(target) {
		return "", false
	}
	relative := strings.TrimPrefix(filepath.ToSlash(target), "./")
	resolved := cleanRepoPath(filepath.Join(repoPath(root), filepath.FromSlash(relative)))
	if !pathContains(root, resolved) {
		return "", false
	}
	return resolved, true
}

func splitJSPackageSpecifier(specifier string) (string, string, bool) {
	if specifier == "" || strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "#") {
		return "", "", false
	}
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") {
		// A scoped specifier needs both a non-empty scope and a package name:
		// "@/pkg" (empty scope) and "@scope/" (empty name) are malformed.
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" || parts[0] == "@" {
			return "", "", false
		}
		return strings.Join(parts[:2], "/"), strings.Join(parts[2:], "/"), true
	}
	if parts[0] == "" {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func isExternalJSSpecifier(specifier string) bool {
	lower := strings.ToLower(specifier)
	for _, prefix := range []string{
		"node:", "bun:", "npm:", "jsr:", "http:", "https:", "data:",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func pathContains(root, file string) bool {
	root = cleanRepoPath(root)
	file = cleanRepoPath(file)
	return root == "" || file == root || strings.HasPrefix(file, root+string(filepath.Separator))
}

func cleanRepoPath(value string) string {
	value = filepath.Clean(value)
	if value == "." {
		return ""
	}
	return value
}

func repoPath(value string) string {
	if value == "" {
		return "."
	}
	return value
}

func readJSWorkspaceManifest(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := stripJSONC(data)
	return doc, err == nil
}

func stripJSONC(data []byte) (map[string]any, error) {
	withoutComments, err := removeJSONComments(data)
	if err != nil {
		return nil, err
	}
	withoutTrailingCommas := removeJSONTrailingCommas(withoutComments)
	var value map[string]any
	if err := json.Unmarshal(withoutTrailingCommas, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func removeJSONComments(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		current := data[i]
		if inString {
			result = append(result, current)
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case current == '"':
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			result = append(result, current)
			continue
		}
		if current != '/' || i+1 >= len(data) {
			result = append(result, current)
			continue
		}
		switch data[i+1] {
		case '/':
			i += 2
			for ; i < len(data) && data[i] != '\n'; i++ {
				result = append(result, ' ')
			}
			if i < len(data) {
				result = append(result, data[i])
			}
		case '*':
			i += 2
			closed := false
			for ; i < len(data); i++ {
				if data[i] == '\n' {
					result = append(result, '\n')
				} else {
					result = append(result, ' ')
				}
				if i+1 < len(data) && data[i] == '*' && data[i+1] == '/' {
					result = append(result, ' ')
					i++
					closed = true
					break
				}
			}
			if !closed {
				return nil, errors.New("unterminated JSON block comment")
			}
		default:
			result = append(result, current)
		}
	}
	return result, nil
}

func removeJSONTrailingCommas(data []byte) []byte {
	result := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		current := data[i]
		if inString {
			result = append(result, current)
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case current == '"':
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			result = append(result, current)
			continue
		}
		if current == ',' {
			next := i + 1
			for next < len(data) && (data[next] == ' ' || data[next] == '\t' || data[next] == '\r' || data[next] == '\n') {
				next++
			}
			if next < len(data) && (data[next] == '}' || data[next] == ']') {
				continue
			}
		}
		result = append(result, current)
	}
	return result
}
