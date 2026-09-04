package scanner

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/analysis"
	"codemap/config"
)

// FileGraph represents internal file-to-file dependencies within a project
type FileGraph struct {
	Root        string              // project root
	Module      string              // go module name (e.g., "codemap")
	Imports     map[string][]string // file -> files it imports
	Importers   map[string][]string // file -> files that import it
	Packages    map[string][]string // package path -> files in that package
	PathAliases map[string][]string // TS/JS path aliases from tsconfig.json (e.g., "@modules/*" -> ["src/modules/*"])
	BaseURL     string              // TS/JS baseUrl from tsconfig.json
	Coverage    GraphCoverage
}

// fileIndex provides fast lookup of files by various import-like keys
type fileIndex struct {
	byExact     map[string]uint32   // exact path -> inventory count
	bySuffix    []string            // paths ordered by suffix for nested lookup
	byDir       map[string][]string // directory -> files in it
	goPkgs      map[string][]string // Go package path -> files
	cueModules  []cueModuleInfo
	cuePackages map[string]string
}

// BuildFileGraph scans a project with explicit filters and builds its file
// graph while honoring caller cancellation. When the primary scan fails with
// an incomplete ast-grep outcome, Cargo metadata recovers what topology it can
// and the graph is marked partial.
func BuildFileGraph(ctx context.Context, root string, filters Filters) (*FileGraph, error) {
	return buildFileGraphWithFallbackWithFilters(ctx, root, filters, func(r string) (ScanOutcome, error) {
		return scanForDepsPrimaryOutcome(ctx, r)
	}, loadCargoFallbackMetadata)
}

// BuildFileGraphFromOutcome builds a file graph from a scan outcome, reusing
// the Cargo dedup guard so a fallback outcome doesn't re-run cargo metadata.
func BuildFileGraphFromOutcome(ctx context.Context, root string, outcome ScanOutcome, filters Filters) (*FileGraph, error) {
	return buildFileGraphFromOutcomeWithCargoMetadataAndFilters(ctx, root, outcome, filters, loadCargoMetadata)
}

// BuildFileGraphFromAnalyses builds a file graph from pre-computed analyses
// using the supplied filters.
func BuildFileGraphFromAnalyses(ctx context.Context, root string, analyses []FileAnalysis, filters Filters) (*FileGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	filtered, err := filterAnalysesContext(ctx, analyses, filters)
	if err != nil {
		return nil, err
	}
	return buildFileGraphFromAnalysesWithCargoMetadataAndFilters(ctx, root, filtered, filters, loadCargoMetadata, nil, nil, false)
}

func buildFileGraphFromAnalysesWithCargoMetadata(ctx context.Context, root string, analyses []FileAnalysis, loader cargoMetadataLoader) (*FileGraph, error) {
	cfg := config.Load(root)
	filters := Filters{Only: cfg.Only, Exclude: cfg.Exclude}
	filtered, err := filterAnalysesContext(ctx, analyses, filters)
	if err != nil {
		return nil, err
	}
	return buildFileGraphFromAnalysesWithCargoMetadataAndFilters(ctx, root, filtered, filters, loader, nil, nil, false)
}

func buildFileGraphFromAnalysesWithCargoMetadataAndFilters(ctx context.Context, root string, analyses []FileAnalysis, filters Filters, loader cargoMetadataLoader, sources []ScanSourceOutcome, inventory []FileInfo, hasInventory bool) (*FileGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	fg := &FileGraph{
		Root:        absRoot,
		Imports:     make(map[string][]string),
		Importers:   make(map[string][]string),
		Packages:    make(map[string][]string),
		PathAliases: make(map[string][]string),
	}
	hasCargoSource := false
	for _, source := range sources {
		fg.Coverage.AddSource(source)
		if source.Name == "cargo-metadata" {
			hasCargoSource = true
		}
	}

	// Detect module name from go.mod (for Go import resolution)
	fg.Module = detectModule(absRoot)

	// Detect path aliases from tsconfig.json (for TS/JS import resolution)
	fg.PathAliases, fg.BaseURL = detectPathAliases(absRoot)

	useJSWorkspace := needsJSWorkspaceResolver(analyses)
	useDartWorkspace := needsDartWorkspaceResolver(analyses)
	gitCache := NewGitIgnoreCache(root)
	scanOnly := filters.Only
	if useJSWorkspace || useDartWorkspace {
		scanOnly = nil
	}
	var allFiles []FileInfo
	if hasInventory && (len(filters.Only) == 0 || !useJSWorkspace && !useDartWorkspace) {
		allFiles = inventory
	} else {
		allFiles, err = ScanFiles(ctx, root, gitCache, scanOnly, filters.Exclude)
		if err != nil {
			return nil, err
		}
	}
	files := allFiles
	if len(filters.Only) > 0 && (useJSWorkspace || useDartWorkspace) {
		files = make([]FileInfo, 0, len(allFiles))
		for _, file := range allFiles {
			if MatchesFilters(file.Path, filepath.Ext(file.Path), filters.Only, nil) {
				files = append(files, file)
			}
		}
	}
	if !hasCUEAnalyses(analyses) {
		for _, file := range files {
			if !strings.EqualFold(filepath.Ext(file.Path), ".cue") {
				continue
			}
			cueOutcome, cueErr := scanCUEFilesFromFiles(ctx, absRoot, files)
			if cueErr != nil {
				return nil, cueErr
			}
			analyses = append(analyses, cueOutcome.Analyses...)
			for _, source := range cueOutcome.Sources {
				fg.Coverage.AddSource(source)
			}
			break
		}
	}
	rustWorkspace, cargoOutcome, err := buildRustWorkspaceIndex(ctx, absRoot, analyses, files, loader)
	if err != nil {
		return nil, err
	}
	// The outcome already carries cargo provenance; keep exactly one
	// cargo-metadata source per graph.
	if cargoOutcome != nil && !hasCargoSource {
		fg.Coverage.AddSource(*cargoOutcome)
	}

	// Build file index for fast fuzzy matching
	idx, err := buildFileIndexContext(ctx, files, fg.Module)
	if err != nil {
		return nil, err
	}
	idx.cueModules = detectCUEModulesWithFiles(absRoot, files)
	idx.cuePackages = make(map[string]string)
	for _, file := range files {
		if DetectLanguage(file.Path) != "cue" {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(file.Path))
		data, readErr := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(path)))
		if readErr == nil {
			idx.cuePackages[path], _ = cueHeader(data)
		}
	}
	for _, analysis := range analyses {
		if analysis.Language == "cue" && analysis.Package != "" {
			idx.cuePackages[filepath.ToSlash(filepath.Clean(analysis.Path))] = analysis.Package
		}
	}
	fg.Packages = idx.goPkgs
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.EqualFold(filepath.Ext(file.Path), ".rs") {
			if CoverageFromSources(fg.Coverage.Sources).Status != analysis.CoverageUnavailable {
				fg.Coverage.AddSource(ScanSourceOutcome{Name: "rust-cargo", Status: ScanSourceMixed, Detail: rustCoverageNote})
			}
			break
		}
	}

	var jsResolver *jsWorkspaceResolver
	if useJSWorkspace {
		jsResolver, err = buildJSWorkspaceResolver(ctx, absRoot, allFiles)
		if err != nil {
			return nil, err
		}
	}

	var dartResolver *dartWorkspaceResolver
	if useDartWorkspace {
		dartResolver, err = buildDartWorkspaceResolver(ctx, absRoot, allFiles)
		if err != nil {
			return nil, err
		}
	}

	// Resolve imports to files using universal fuzzy matching
	for _, a := range analyses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var resolvedImports []string

		if a.Language == "rust" {
			resolvedImports = resolveRustReferences(absRoot, a, idx, rustWorkspace)
		} else {
			sourceLanguage := DetectLanguage(a.Path)
			for _, imp := range a.Imports {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				resolved := fuzzyResolveWithWorkspace(
					imp, a.Path, idx, fg.Module, fg.PathAliases, fg.BaseURL, jsResolver, dartResolver,
				)
				// Exclude multi-file Go package imports to avoid inflating hub counts.
				// Go package imports start with the module prefix and resolve to all
				// files in that package. For all other imports (e.g., C# namespace
				// imports that resolve via directory matching), allow multi-file
				// resolution so inter-namespace dependencies are tracked.
				isGoPkg := sourceLanguage == "go" && isLocalGoImport(imp, fg.Module) && len(resolved) > 1
				if !isGoPkg && len(resolved) > 0 {
					resolvedImports = append(resolvedImports, resolved...)
				}
			}
		}

		if len(resolvedImports) > 0 {
			fg.Imports[a.Path] = dedupe(resolvedImports)

			// Build reverse map
			for _, imported := range fg.Imports[a.Path] {
				fg.Importers[imported] = append(fg.Importers[imported], a.Path)
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fg, nil
}

func applyPrecomputedFileEdges(fg *FileGraph, edges []fileEdge) {
	for _, edge := range edges {
		if edge.from == "" || edge.to == "" || edge.from == edge.to {
			continue
		}
		fg.Imports[edge.from] = dedupe(append(fg.Imports[edge.from], edge.to))
		fg.Importers[edge.to] = dedupe(append(fg.Importers[edge.to], edge.from))
	}
}

func needsJSWorkspaceResolver(analyses []FileAnalysis) bool {
	for _, analysis := range analyses {
		if isJavaScriptLanguage(DetectLanguage(analysis.Path)) {
			return true
		}
	}
	return false
}

func needsDartWorkspaceResolver(analyses []FileAnalysis) bool {
	for _, file := range analyses {
		if DetectLanguage(file.Path) == "dart" {
			return true
		}
	}
	return false
}

// buildFileIndex creates a multi-key index for fast import resolution
func buildFileIndex(files []FileInfo, goModule string) *fileIndex {
	idx, _ := buildFileIndexContext(context.Background(), files, goModule)
	return idx
}

func buildFileIndexContext(ctx context.Context, files []FileInfo, goModule string) (*fileIndex, error) {
	directoryHint := min(len(files), 1024)
	idx := &fileIndex{
		byExact:  make(map[string]uint32, len(files)),
		bySuffix: make([]string, 0, len(files)),
		byDir:    make(map[string][]string, directoryHint),
		goPkgs:   make(map[string][]string, directoryHint),
	}
	goPackagePaths := make(map[string]string, directoryHint)

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := f.Path
		dir := filepath.Dir(path)
		if dir == "." {
			dir = ""
		}

		// Index by directory
		idx.byDir[dir] = append(idx.byDir[dir], path)

		idx.byExact[path]++
		idx.bySuffix = append(idx.bySuffix, path)

		// Go package index. Import paths always use forward slashes, so the
		// key must be slash-normalized or lookups fail on Windows.
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && goModule != "" {
			pkgPath := goModule
			if dir != "" {
				var ok bool
				pkgPath, ok = goPackagePaths[dir]
				if !ok {
					pkgPath = goModule + "/" + filepath.ToSlash(dir)
					goPackagePaths[dir] = pkgPath
				}
			}
			idx.goPkgs[pkgPath] = append(idx.goPkgs[pkgPath], path)
		}
	}
	sort.Slice(idx.bySuffix, func(i, j int) bool {
		if order := comparePathsReversed(idx.bySuffix[i], idx.bySuffix[j]); order != 0 {
			return order < 0
		}
		return idx.bySuffix[i] < idx.bySuffix[j]
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return idx, nil
}

func comparePathsReversed(left, right string) int {
	limit := min(len(left), len(right))
	for i := 0; i < limit; i++ {
		leftByte, rightByte := left[len(left)-1-i], right[len(right)-1-i]
		if leftByte < rightByte {
			return -1
		}
		if leftByte > rightByte {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func comparePathSuffix(path, suffix string) int {
	prefixLen := len(suffix) + 1
	limit := min(len(path), prefixLen)
	for i := 0; i < limit; i++ {
		left := path[len(path)-1-i]
		right := byte(filepath.Separator)
		if i < len(suffix) {
			right = suffix[len(suffix)-1-i]
		}
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
	}
	if len(path) < prefixLen {
		return -1
	}
	if len(path) > prefixLen {
		return 1
	}
	return 0
}

func hasPathSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix)-1] == byte(filepath.Separator) && strings.HasSuffix(path, suffix)
}

func (idx *fileIndex) suffixMatches(suffix string) []string {
	if suffix == "" {
		return nil
	}
	start := sort.Search(len(idx.bySuffix), func(i int) bool {
		return comparePathSuffix(idx.bySuffix[i], suffix) >= 0
	})
	var matches []string
	for i := start; i < len(idx.bySuffix) && hasPathSuffix(idx.bySuffix[i], suffix); i++ {
		matches = append(matches, idx.bySuffix[i])
	}
	if len(matches) > 1 {
		sort.Strings(matches)
	}
	return matches
}

// fuzzyResolve converts an import path to compatible local file paths.
func fuzzyResolve(imp, fromFile string, idx *fileIndex, goModule string, pathAliases map[string][]string, baseURL string) []string {
	return fuzzyResolveWithWorkspace(imp, fromFile, idx, goModule, pathAliases, baseURL, nil, nil)
}

func fuzzyResolveWithWorkspace(
	imp, fromFile string,
	idx *fileIndex,
	goModule string,
	pathAliases map[string][]string,
	baseURL string,
	jsResolver *jsWorkspaceResolver,
	dartResolver *dartWorkspaceResolver,
) []string {
	sourceLanguage := DetectLanguage(fromFile)
	if sourceLanguage == "" {
		return nil
	}

	fromDir := filepath.Dir(fromFile)
	if fromDir == "." {
		fromDir = ""
	}

	// Strategy 1: Go package lookup, failing closed for non-local imports.
	if sourceLanguage == "go" {
		if strings.HasPrefix(imp, ".") {
			return resolveRelative(imp, fromDir, idx, sourceLanguage)
		}
		if !isLocalGoImport(imp, goModule) {
			return nil
		}
		return idx.goPkgs[strings.Trim(imp, "\"'`")]
	}
	if sourceLanguage == "dart" {
		return dartResolver.resolve(imp, fromFile, idx)
	}

	// CUE imports name packages, not individual files. Resolve only packages
	// under a declared CUE module to avoid linking external modules by
	// suffix coincidence.
	if sourceLanguage == "cue" {
		module, ok := nearestCUEModule(fromFile, idx.cueModules)
		if !ok {
			return nil
		}
		imp = strings.Trim(imp, "\"'`")
		imp, selector := splitCUEImport(imp)
		if imp != module.path && !strings.HasPrefix(imp, module.path+"/") {
			return nil
		}
		packagePath := strings.TrimPrefix(strings.TrimPrefix(imp, module.path), "/")
		packagePath = filepath.Join(module.root, filepath.FromSlash(packagePath))
		files := idx.byDir[packagePath]
		return resolveCUEPackage(files, idx.cuePackages, selector)
	}

	// Normalize the import path
	normalized := normalizeImport(imp)

	// Strategy 2: Relative path resolution (./foo, ../bar)
	if strings.HasPrefix(imp, ".") {
		// Python spells relative imports as a run of dots that counts package
		// levels, not as path segments: "from .mod import x" is a sibling
		// module, not a directory called ".mod". Routing it through the
		// JS-shaped resolver produced "pkg/.mod", which matches nothing, so
		// every intra-package edge in a Python project was lost.
		if sourceLanguage == "python" {
			return resolvePythonRelative(imp, fromDir, idx)
		}
		return resolveRelative(imp, fromDir, idx, sourceLanguage)
	}

	// Strategy 3: TypeScript/JavaScript path alias resolution (@modules/auth, @shared/utils)
	if isJavaScriptLanguage(sourceLanguage) && len(pathAliases) > 0 {
		if files := resolvePathAlias(imp, pathAliases, baseURL, idx, sourceLanguage); len(files) > 0 {
			return files
		}
	}
	if isJavaScriptLanguage(sourceLanguage) {
		if files := jsResolver.resolve(imp, fromFile, idx, sourceLanguage); len(files) > 0 {
			return files
		}
		return nil
	}

	// Strategy 4: Exact match (with common extensions)
	if files := tryExactMatch(normalized, idx, sourceLanguage); len(files) > 0 {
		return files
	}

	// Strategy 5: Suffix match (for nested packages like app.core.config -> */app/core/config.py)
	if files := trySuffixMatch(normalized, idx, sourceLanguage); len(files) > 0 {
		return files
	}

	// Strategy 6: Directory match (for namespace-level imports like C# "using Foo.Bar;"
	// where Foo.Bar normalizes to Foo/Bar and maps to the Foo/Bar/ directory).
	if files := tryDirMatch(normalized, idx, sourceLanguage); len(files) > 0 {
		return files
	}

	return nil
}

func nearestCUEModule(fromFile string, modules []cueModuleInfo) (cueModuleInfo, bool) {
	fromFile = filepath.ToSlash(filepath.Clean(fromFile))
	for _, module := range modules {
		if module.root == "" || fromFile == module.root || strings.HasPrefix(fromFile, module.root+"/") {
			return module, true
		}
	}
	return cueModuleInfo{}, false
}

func hasCUEAnalyses(analyses []FileAnalysis) bool {
	for _, analysis := range analyses {
		if analysis.Language == "cue" {
			return true
		}
	}
	return false
}

func splitCUEImport(imp string) (string, string) {
	separator := strings.LastIndex(imp, ":")
	if separator <= strings.LastIndex(imp, "/") {
		return imp, ""
	}
	return imp[:separator], imp[separator+1:]
}

func resolveCUEPackage(files []string, packages map[string]string, selector string) []string {
	files = compatibleFiles("cue", files)
	if len(files) == 0 {
		return nil
	}
	known := make(map[string]bool)
	for _, file := range files {
		if pkg := packages[file]; pkg != "" {
			known[pkg] = true
		}
	}
	if selector == "" && len(known) > 1 {
		base := filepath.Base(filepath.Dir(files[0]))
		if known[base] {
			selector = base
		} else {
			return nil
		}
	}
	if selector == "" || len(known) == 0 {
		return files
	}
	resolved := make([]string, 0, len(files))
	for _, file := range files {
		if packages[file] == selector {
			resolved = append(resolved, file)
		}
	}
	return resolved
}

func isLocalGoImport(imp, module string) bool {
	if module == "" {
		return false
	}
	imp = strings.Trim(imp, "\"'`")
	return imp == module || strings.HasPrefix(imp, module+"/")
}

// normalizeImport converts various import syntaxes to a path-like format
func normalizeImport(imp string) string {
	// Remove quotes
	imp = strings.Trim(imp, "\"'`")

	// Python dots to slashes: app.core.config -> app/core/config
	if strings.Contains(imp, ".") && !strings.Contains(imp, "/") && !strings.HasPrefix(imp, ".") {
		imp = strings.ReplaceAll(imp, ".", string(filepath.Separator))
	}

	// Rust :: to slashes: crate::foo::bar -> foo/bar
	if strings.HasPrefix(imp, "crate::") {
		imp = strings.TrimPrefix(imp, "crate::")
		imp = strings.ReplaceAll(imp, "::", string(filepath.Separator))
	}
	if strings.HasPrefix(imp, "super::") {
		imp = strings.ReplaceAll(imp, "::", string(filepath.Separator))
	}

	return imp
}

// resolvePythonRelative resolves a Python relative import. One leading dot
// means the current package, and each additional dot climbs one package: so
// from "pkg/sub/user.py", ".mod" is pkg/sub/mod and "..mod" is pkg/mod. What
// follows the dots is a dotted module path, where each dot is a directory
// separator.
//
// A bare run of dots ("from . import mod") names the package, not a module;
// the imported name lives in the import list rather than the path, so it is
// recovered during extraction. Anything that still arrives here as dots alone
// resolves to nothing rather than to a guess at which file was meant.
func resolvePythonRelative(imp, fromDir string, idx *fileIndex) []string {
	level := 0
	for level < len(imp) && imp[level] == '.' {
		level++
	}
	module := imp[level:]
	if module == "" {
		return nil
	}

	targetDir := fromDir
	for i := 1; i < level; i++ {
		targetDir = filepath.Dir(targetDir)
		if targetDir == "." {
			targetDir = ""
		}
	}

	candidate := filepath.FromSlash(strings.ReplaceAll(module, ".", "/"))
	if targetDir != "" {
		candidate = filepath.Join(targetDir, candidate)
	}
	if files := tryExactMatch(candidate, idx, "python"); len(files) > 0 {
		return files
	}
	// A package rather than a module: "from .sub import x" where sub/ is a
	// package directory resolves to its __init__.py.
	return tryExactMatch(filepath.Join(candidate, "__init__"), idx, "python")
}

// resolveRelative handles ./foo and ../bar style imports
func resolveRelative(imp, fromDir string, idx *fileIndex, sourceLanguage string) []string {
	// Count parent directory levels
	levels := 0
	rest := imp
	for strings.HasPrefix(rest, "../") {
		levels++
		rest = strings.TrimPrefix(rest, "../")
	}
	rest = strings.TrimPrefix(rest, "./")

	// Navigate up from fromDir
	targetDir := fromDir
	for i := 0; i < levels; i++ {
		targetDir = filepath.Dir(targetDir)
		if targetDir == "." {
			targetDir = ""
		}
	}

	// Build candidate path
	candidate := rest
	if targetDir != "" {
		candidate = filepath.Join(targetDir, rest)
	}

	return tryExactMatch(candidate, idx, sourceLanguage)
}

// tryExactMatch looks for exact path matches with common extensions.
// Extension list derived from the canonical scanner registry.
func tryExactMatch(path string, idx *fileIndex, sourceLanguage string) []string {
	if idx.byExact[path] == 1 && languagesCompatible(sourceLanguage, DetectLanguage(path)) {
		return []string{path}
	}
	for _, ext := range resolverExtensions[:len(resolverExtensions)-1] {
		candidate := path + ext
		if idx.byExact[candidate] == 1 && languagesCompatible(sourceLanguage, DetectLanguage(candidate)) {
			return []string{candidate}
		}
	}

	return nil
}

// trySuffixMatch finds files where the path ends with the normalized import
func trySuffixMatch(normalized string, idx *fileIndex, sourceLanguage string) []string {
	for _, ext := range resolverExtensions {
		candidate := normalized + ext
		if files := idx.suffixMatches(candidate); len(files) > 0 {
			files = compatibleFiles(sourceLanguage, files)
			if len(files) == 0 {
				continue
			}
			// Return the shortest match (most specific)
			if len(files) == 1 {
				return files
			}
			// Multiple matches - return all, let caller dedupe
			return files
		}
	}

	// Also try __init__.py for Python packages
	initCandidate := filepath.Join(normalized, "__init__.py")
	if files := idx.suffixMatches(initCandidate); len(files) > 0 {
		return compatibleFiles(sourceLanguage, files)
	}

	return nil
}

// tryDirMatch returns all files whose parent directory matches the given path.
// This resolves namespace-level imports (e.g. C# "using Foo.Bar;" -> "Foo/Bar/")
// where an import refers to a whole directory rather than a single file.
// It also tries progressively shorter suffixes to handle namespace prefixes
// (e.g. "MyApp/Models" tries "MyApp/Models" first, then "Models").
func tryDirMatch(path string, idx *fileIndex, sourceLanguage string) []string {
	// Try exact match first
	if files, ok := idx.byDir[path]; ok {
		if compatible := compatibleFiles(sourceLanguage, files); len(compatible) > 0 {
			return compatible
		}
	}
	// Normalize path separators for cross-platform compatibility
	nativePath := filepath.FromSlash(path)
	if nativePath != path {
		if files, ok := idx.byDir[nativePath]; ok {
			if compatible := compatibleFiles(sourceLanguage, files); len(compatible) > 0 {
				return compatible
			}
		}
	}
	// Try suffix match: strip leading segments progressively
	// This handles namespace prefixes like MyApp.Models -> Models
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 1; i < len(parts); i++ {
		suffix := filepath.Join(parts[i:]...)
		if files, ok := idx.byDir[suffix]; ok {
			if compatible := compatibleFiles(sourceLanguage, files); len(compatible) > 0 {
				return compatible
			}
		}
	}
	return nil
}

func compatibleFiles(sourceLanguage string, files []string) []string {
	var compatible []string
	for _, file := range files {
		if languagesCompatible(sourceLanguage, DetectLanguage(file)) {
			compatible = append(compatible, file)
		}
	}
	return compatible
}

func isJavaScriptLanguage(language string) bool {
	return language == "javascript" || language == "typescript"
}

// detectModule reads go.mod to find the module name
func detectModule(root string) string {
	modFile := filepath.Join(root, "go.mod")
	f, err := os.Open(modFile)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimPrefix(line, "module ")
		}
	}
	return ""
}

// HubThreshold is the number of non-test importers at which a file counts as
// a hub.
const HubThreshold = 3

// IsTestFile reports whether a path names a test file across the supported
// languages (Go _test files, JS/TS .test/.spec files, Python test_ modules).
func IsTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	noExt := strings.TrimSuffix(base, filepath.Ext(base))
	return strings.HasSuffix(noExt, ".test") || strings.HasSuffix(noExt, ".spec")
}

// CountHubImporters returns how many of the given importers count toward hub
// status (i.e. excluding test files).
func CountHubImporters(importers []string) int {
	count := 0
	for _, imp := range importers {
		if !IsTestFile(imp) {
			count++
		}
	}
	return count
}

// IsHub returns true if a file has HubThreshold+ non-test importers.
// Test files themselves never rank as hubs.
func (fg *FileGraph) IsHub(path string) bool {
	if IsTestFile(path) {
		return false
	}
	return CountHubImporters(fg.Importers[path]) >= HubThreshold
}

// HubFiles returns all files that qualify as hubs under IsHub.
func (fg *FileGraph) HubFiles() []string {
	var hubs []string
	for path := range fg.Importers {
		if fg.IsHub(path) {
			hubs = append(hubs, path)
		}
	}
	return hubs
}

// ConnectedFiles returns all files connected to the given file (imports + importers)
func (fg *FileGraph) ConnectedFiles(path string) []string {
	seen := make(map[string]bool)

	for _, f := range fg.Imports[path] {
		seen[f] = true
	}
	for _, f := range fg.Importers[path] {
		seen[f] = true
	}

	var result []string
	for f := range seen {
		if f != path {
			result = append(result, f)
		}
	}
	return result
}

// tsConfig represents the structure of tsconfig.json we care about
type tsConfig struct {
	CompilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
	Extends string `json:"extends"`
}

// detectPathAliases reads tsconfig.json (and jsconfig.json) to find path aliases
// Returns the paths map and baseUrl
func detectPathAliases(root string) (map[string][]string, string) {
	// Try tsconfig.json first, then jsconfig.json
	for _, configFile := range []string{"tsconfig.json", "jsconfig.json"} {
		configPath := filepath.Join(root, configFile)
		paths, baseURL := readTSConfig(configPath, root)
		if len(paths) > 0 {
			return paths, baseURL
		}
	}
	return nil, ""
}

// readTSConfig reads a tsconfig/jsconfig file and extracts paths, following extends
func readTSConfig(configPath, root string) (map[string][]string, string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, ""
	}

	var config tsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, ""
	}

	paths := config.CompilerOptions.Paths
	baseURL := config.CompilerOptions.BaseURL

	// If this config extends another, merge paths from parent
	if config.Extends != "" {
		parentPath := config.Extends
		// Resolve relative extends path
		if !filepath.IsAbs(parentPath) {
			parentPath = filepath.Join(filepath.Dir(configPath), parentPath)
		}
		// Add .json if not present
		if !strings.HasSuffix(parentPath, ".json") {
			parentPath += ".json"
		}

		parentPaths, parentBaseURL := readTSConfig(parentPath, root)
		if parentPaths != nil {
			// Child paths override parent paths
			if paths == nil {
				paths = parentPaths
			} else {
				for k, v := range parentPaths {
					if _, exists := paths[k]; !exists {
						paths[k] = v
					}
				}
			}
		}
		// Child baseUrl overrides parent
		if baseURL == "" {
			baseURL = parentBaseURL
		}
	}

	return paths, baseURL
}

// resolvePathAlias attempts to resolve an import using TypeScript path aliases
// e.g., "@modules/auth" with alias "@modules/*" -> ["src/modules/*"] becomes "src/modules/auth"
func resolvePathAlias(imp string, pathAliases map[string][]string, baseURL string, idx *fileIndex, sourceLanguage string) []string {
	// Try each alias pattern
	for pattern, targets := range pathAliases {
		var prefix, suffix string
		if starIdx := strings.Index(pattern, "*"); starIdx >= 0 {
			prefix = pattern[:starIdx]
			suffix = pattern[starIdx+1:]
		} else {
			// Exact match pattern (no wildcard)
			if imp == pattern {
				for _, target := range targets {
					resolved := target
					if baseURL != "" && !filepath.IsAbs(resolved) {
						resolved = filepath.Join(baseURL, resolved)
					}
					if files := tryExactMatch(resolved, idx, sourceLanguage); len(files) > 0 {
						return files
					}
				}
			}
			continue
		}

		// Check if import matches this pattern
		if !strings.HasPrefix(imp, prefix) {
			continue
		}
		if suffix != "" && !strings.HasSuffix(imp, suffix) {
			continue
		}

		// Extract the wildcard portion
		wildcardPart := imp[len(prefix):]
		if suffix != "" {
			wildcardPart = wildcardPart[:len(wildcardPart)-len(suffix)]
		}

		// Try each target mapping
		for _, target := range targets {
			resolved := target
			if starIdx := strings.Index(target, "*"); starIdx >= 0 {
				// Replace wildcard with captured portion
				resolved = target[:starIdx] + wildcardPart + target[starIdx+1:]
			}

			// Apply baseUrl if set
			if baseURL != "" && !filepath.IsAbs(resolved) {
				resolved = filepath.Join(baseURL, resolved)
			}

			// Try to find matching files
			if files := tryExactMatch(resolved, idx, sourceLanguage); len(files) > 0 {
				return files
			}
			if files := trySuffixMatch(resolved, idx, sourceLanguage); len(files) > 0 {
				return files
			}
		}
	}

	return nil
}
