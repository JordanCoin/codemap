package scanner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
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
	// KnownFiles is every file this graph actually scanned (post filters),
	// keyed by slash-separated path relative to Root. A path absent here was
	// never indexed at all — e.g. it lives under an excluded or hidden
	// directory — which is a distinct, worse state than "indexed and found
	// to have zero importers": see Indexed.
	KnownFiles map[string]bool
}

// Indexed reports whether path (slash-separated, relative to fg.Root) was
// part of this graph's scanned inventory. A false result means codemap never
// looked at the file at all, so an empty Importers/Imports entry for it is a
// coverage gap, not a confirmed zero.
func (fg *FileGraph) Indexed(path string) bool {
	if fg == nil {
		return false
	}
	return fg.KnownFiles[path]
}

// UnindexedExplanation says why a file has no dependency answer in a graph
// and what to do about it. Reason is one clause naming the single cause that
// applies; Next is the action that would get the file an answer.
type UnindexedExplanation struct {
	Reason string
	Next   string
}

// ExplainUnindexed reports whether path (relative to fg.Root, either
// separator) is a file this graph cannot answer for, and names the one reason
// why. ok is false when the graph vouches for the file (it was scanned and its
// extension has import analysis), so callers keep their normal output; it is
// also false when the graph carries no inventory at all (KnownFiles == nil,
// e.g. a graph rebuilt from a daemon cache): with nothing scanned, the honest
// answer is "unknown", never "not indexed". filters are the project filters
// the graph was built with, so a file dropped by only/exclude is named as such
// instead of being lumped in with ignored paths.
func (fg *FileGraph) ExplainUnindexed(path string, filters Filters) (UnindexedExplanation, bool) {
	if fg == nil || fg.KnownFiles == nil {
		return UnindexedExplanation{}, false
	}
	rel := filepath.ToSlash(filepath.Clean(path))
	abs := filepath.Join(fg.Root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	switch {
	case err != nil && os.IsNotExist(err):
		return UnindexedExplanation{Reason: "path does not exist", Next: "check the path with find_file, then retry with the path it returns"}, true
	case err != nil:
		return UnindexedExplanation{Reason: "path is not readable: " + err.Error(), Next: "fix the path's permissions, then retry"}, true
	case info.IsDir():
		return UnindexedExplanation{Reason: "path is a directory, not a file", Next: "pass one file path; use get_structure for a directory"}, true
	}
	ext := filepath.Ext(rel)
	if !fg.KnownFiles[rel] {
		if !MatchesFilters(rel, ext, filters.Only, filters.Exclude) {
			return UnindexedExplanation{Reason: "excluded by config: only/exclude in .codemap/config.json", Next: "change only/exclude in .codemap/config.json to include it, then retry"}, true
		}
		for _, part := range strings.Split(pathpkg.Dir(rel), "/") {
			if IgnoredDirs[part] {
				return UnindexedExplanation{Reason: "under ignored directory " + part + "/", Next: "codemap always skips " + part + "/; nothing to configure"}, true
			}
		}
		cache := NewGitIgnoreCache(fg.Root)
		for dir := filepath.Dir(abs); dir != fg.Root && strings.HasPrefix(dir, fg.Root); dir = filepath.Dir(dir) {
			cache.EnsureDir(dir)
		}
		if cache.ShouldIgnore(abs) {
			return UnindexedExplanation{Reason: "ignored by .gitignore", Next: "remove the .gitignore rule covering it to include it in the scan"}, true
		}
		return UnindexedExplanation{Reason: "absent from the scan inventory (newer than the scan, or the walk skipped it)", Next: "rerun the scan, then retry"}, true
	}
	if !IsSourceExt(ext) {
		kind := ext + " has no import analysis"
		if ext == "" {
			kind = "no extension, so no import analysis"
		}
		return UnindexedExplanation{Reason: "unsupported file type: " + kind, Next: "dependency analysis covers source files only; use find_file to locate it"}, true
	}
	return UnindexedExplanation{}, false
}

// fileIndex provides fast lookup of files by various import-like keys
type fileIndex struct {
	byExact     map[string]uint32   // exact path -> inventory count
	bySuffix    []string            // paths ordered by suffix for nested lookup
	byDir       map[string][]string // directory -> files in it
	goPkgs      map[string][]string // Go package path -> files
	cueModules  []cueModuleInfo
	cuePackages map[string]string
	// bashAssigned is filled on first use by bashAssignedVariables.
	bashAssigned map[string]bool
	// pythonDirs and pythonRoots are filled on first use by pythonDir and
	// pythonFromRoot.
	pythonDirs  map[string]bool
	pythonRoots map[string]pythonRoot
	// unfilteredFiles, set when an only list narrowed this index, lists what the
	// ignore rules and excludes still allow.
	unfilteredFiles func() ([]FileInfo, error)
	// absRoot and filters back a narrow fallback in tryExactMatch: a file
	// excluded from the scanned inventory purely by .gitignore can still be
	// the exact, unambiguous target of a plain relative import from a file
	// that *was* scanned. absRoot empty disables the fallback entirely
	// (e.g. tests that build a fileIndex from bare FileInfo values with no
	// real directory behind them). filters is still enforced in that
	// fallback so it recovers gitignore's blind spot without also
	// resurrecting a file the project or caller explicitly excluded via
	// --only/--exclude or config.json, which must stay absolutely excluded.
	absRoot string
	filters Filters
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
	filters := Filters{Only: cfg.Only, Exclude: cfg.Exclude, MaxFiles: cfg.MaxFiles}
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
		Root:      absRoot,
		Imports:   make(map[string][]string),
		Importers: make(map[string][]string),
		Packages:  make(map[string][]string),
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

	analysisLanguages := inspectAnalysisLanguages(analyses)
	useJSWorkspace := analysisLanguages.hasJS
	useDartWorkspace := analysisLanguages.hasDart
	if useJSWorkspace {
		fg.PathAliases, fg.BaseURL = detectPathAliases(absRoot)
	}
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
	fg.KnownFiles = make(map[string]bool, len(files))
	for _, file := range files {
		fg.KnownFiles[filepath.ToSlash(file.Path)] = true
	}
	languages := inspectFileLanguages(files)
	hasCUEAnalysis := analysisLanguages.hasCUE
	if languages.hasCUE && !hasCUEAnalysis {
		cueOutcome, cueErr := scanCUEFilesFromFiles(ctx, absRoot, files)
		if cueErr != nil {
			return nil, cueErr
		}
		analyses = append(analyses, cueOutcome.Analyses...)
		for _, source := range cueOutcome.Sources {
			fg.Coverage.AddSource(source)
		}
	}
	var rustWorkspace *rustWorkspaceIndex
	if languages.hasRust || analysisLanguages.hasRust {
		var cargoOutcome *ScanSourceOutcome
		rustWorkspace, cargoOutcome, err = buildRustWorkspaceIndex(ctx, absRoot, analyses, files, loader)
		if err != nil {
			return nil, err
		}
		// The outcome already carries cargo provenance; keep exactly one
		// cargo-metadata source per graph.
		if cargoOutcome != nil && !hasCargoSource {
			fg.Coverage.AddSource(*cargoOutcome)
		}
	}

	// Build file index for fast fuzzy matching
	idx, err := buildFileIndexContext(ctx, files, fg.Module, absRoot, filters)
	if err != nil {
		return nil, err
	}
	if len(filters.Only) > 0 {
		idx.unfilteredFiles = func() ([]FileInfo, error) {
			return ScanFiles(ctx, root, gitCache, nil, filters.Exclude)
		}
	}
	if languages.hasCUE || hasCUEAnalysis {
		idx.cueModules = detectCUEModulesWithFiles(absRoot, files)
		idx.cuePackages = make(map[string]string)
		for _, file := range files {
			if fileInfoLanguage(file) != "cue" {
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
	}
	fg.Packages = idx.goPkgs
	if languages.hasRust {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if CoverageFromSources(fg.Coverage.Sources).Status != analysis.CoverageUnavailable {
			fg.Coverage.AddSource(ScanSourceOutcome{Name: "rust-cargo", Status: ScanSourceMixed, Detail: rustCoverageNote, Languages: []string{"rust"}})
		}
	}
	var jsResolver *jsWorkspaceResolver
	if useJSWorkspace {
		jsResolver, err = buildJSWorkspaceResolver(ctx, absRoot, allFiles)
		if err != nil {
			return nil, err
		}
		for _, note := range jsResolver.notes {
			if !slices.Contains(fg.Coverage.Notes, note) {
				fg.Coverage.Notes = append(fg.Coverage.Notes, note)
			}
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
	unresolved := newUnresolvedImportTally(idx)
	for _, a := range analyses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var resolvedImports []string

		if a.Language == "rust" {
			resolvedImports = resolveRustReferences(absRoot, a, idx, rustWorkspace)
		} else {
			sourceLanguage := DetectLanguage(a.Path)
			angleIncludes := angleOnlyIncludes(a.References)
			pythonMembers := pythonFromMembers(a.References)
			imports := a.Imports
			for _, ref := range a.References {
				if ref.Kind == bashDynamicImport {
					imports = append(slices.Clip(imports), ref.Path)
				}
			}
			for _, imp := range imports {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				spelled := imp
				if angleIncludes[imp] {
					spelled = "<" + imp + ">"
				}
				var resolved []string
				var found bool
				if pythonMembers[imp] {
					resolved, found = resolvePythonMember(imp, a.Path, idx)
				} else {
					resolved = fuzzyResolveWithWorkspace(
						spelled, a.Path, idx, fg.Module, fg.PathAliases, fg.BaseURL, jsResolver, dartResolver,
					)
					found = len(resolved) > 0 || sourceLanguage == "python" && idx.pythonNamespacePackage(imp, pythonFromDir(a.Path))
				}
				// A package the nearest package.json declares is external even
				// when a tsconfig alias would also match it.
				if !jsResolver.declaresDependency(a.Path, strings.Trim(imp, "\"'`")) {
					var aliases map[string][]string
					if scopes := jsResolver.pathAliasScopes(a.Path, fg.PathAliases, fg.BaseURL); len(scopes) > 0 {
						aliases = scopes[0].aliases
					}
					unresolved.record(sourceLanguage, a.Path, imp, aliases, found)
				}
				// A Go import names a package, and a package is every non-test
				// .go file in its directory, so one import fans out to one edge
				// per file. Dropping the multi-file case (as this once did, to
				// keep hub counts small) made every importer of a package with
				// two or more files vanish from --deps and --importers: see #191.
				// Text renderers that need package granularity collapse these
				// edges themselves; the graph keeps the file edges.
				resolvedImports = append(resolvedImports, resolved...)
			}
		}

		key := keyPath(a.Path)
		resolvedImports = slices.DeleteFunc(slashPaths(resolvedImports), func(target string) bool { return target == key })
		if len(resolvedImports) > 0 {
			fg.Imports[key] = dedupe(resolvedImports)
		}
	}
	batsReached := resolveBatsHelperLoads(analyses, fg, func(imp, from string) []string {
		return fuzzyResolveWithWorkspace(imp, from, idx, fg.Module, fg.PathAliases, fg.BaseURL, jsResolver, dartResolver)
	})
	for key, imports := range fg.Imports {
		for _, imported := range imports {
			fg.Importers[imported] = append(fg.Importers[imported], key)
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Languages whose imports name modules rather than files cannot produce
	// intra-project edges from imports, so an empty graph over them is a blind
	// spot rather than a finding. Swift gets type-name reference edges instead
	// and is reported mixed; the rest stay unavailable. Recording it here is
	// what keeps --importers and blast-radius honest too: both read this
	// graph's provenance.
	for _, source := range unresolved.sources() {
		fg.Coverage.AddSource(source)
	}
	fg.Coverage.Issues = append(fg.Coverage.Issues, unresolved.issues...)
	if CoverageFromSources(fg.Coverage.Sources).Status != analysis.CoverageUnavailable {
		for _, source := range missingEdgeSources(absRoot, files, unresolved.phpUnresolvedUse, idx, batsReached) {
			fg.Coverage.AddSource(source)
		}
	}
	resolved := map[string]bool{}
	if addSwiftTypeReferenceEdges(absRoot, analyses, fg) {
		resolved[symbolLevelImportLanguages["swift"]] = true
	}
	fg.Coverage.addSymbolLevelImportCoverage(languages.symbolLevel, resolved)
	// A file resolved via tryExactMatch's on-disk fallback (above) is, by
	// construction, real and present — it just wasn't part of the scanned
	// inventory KnownFiles was built from (e.g. gitignored). Now that it has
	// a proven importer, it should read as indexed rather than "not
	// indexed": the whole point of the fallback is to turn a dropped edge
	// into a correct one, and leaving KnownFiles stale would have the
	// importers report claim the edge while simultaneously claiming the
	// target was never looked at.
	for imported := range fg.Importers {
		fg.KnownFiles[filepath.ToSlash(imported)] = true
	}
	fg.sortEdges()
	return fg, nil
}

// swiftIdentifier matches one identifier token; type references in Swift are
// bare names, so a word-boundary scan over the source is the resolver.
var swiftIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// swiftImportLine strips import statements before the scan: "import Firebase"
// names a module, and a project enum that happens to share the name would
// otherwise gain every file in the app as an importer.
var swiftImportLine = regexp.MustCompile(`(?m)^\s*(?:@testable\s+)?import\s+[^\n]*`)

// addSwiftTypeReferenceEdges adds file -> file edges for every Swift file that
// mentions a type declared in another Swift file. Reports whether any Swift
// type was declared at all, which is what decides the coverage status.
//
// ponytail: name match, not symbol resolution. Two files declaring the same
// type name both become targets. Upgrade path is a SourceKit or swift-syntax
// pass if precision matters.
func addSwiftTypeReferenceEdges(absRoot string, analyses []FileAnalysis, fg *FileGraph) bool {
	declaredBy := make(map[string][]string)
	for _, a := range analyses {
		if a.Language != "swift" {
			continue
		}
		for _, name := range a.Types {
			if len(name) < 3 {
				continue
			}
			declaredBy[name] = append(declaredBy[name], keyPath(a.Path))
		}
	}
	if len(declaredBy) == 0 {
		return false
	}
	for _, a := range analyses {
		if a.Language != "swift" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(a.Path)))
		if err != nil {
			continue
		}
		seen := make(map[string]bool)
		var targets []string
		from := keyPath(a.Path)
		for _, ident := range swiftIdentifier.FindAll(swiftImportLine.ReplaceAll(blankSwiftLiterals(data), nil), -1) {
			for _, target := range declaredBy[string(ident)] {
				if target == from || seen[target] || slices.Contains(fg.Imports[from], target) {
					continue
				}
				seen[target] = true
				targets = append(targets, target)
			}
		}
		if len(targets) == 0 {
			continue
		}
		sort.Strings(targets)
		fg.Imports[from] = append(fg.Imports[from], targets...)
		for _, target := range targets {
			fg.Importers[target] = append(fg.Importers[target], from)
		}
	}
	return true
}

// blankSwiftLiterals returns src with comments and string literals spaced out,
// keeping the code inside \(...) interpolations, which does reference types.
func blankSwiftLiterals(src []byte) []byte {
	out := slices.Clone(src)
	blankSwiftCode(out, 0, false)
	return out
}

func blankSwiftRange(b []byte, from, to int) {
	for i := from; i < to; i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
}

// blankSwiftCode walks code from i. Inside an interpolation it stops after the
// closing parenthesis and returns the index it reached.
func blankSwiftCode(b []byte, i int, interpolation bool) int {
	depth := 0
	for i < len(b) {
		rest := b[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("//")):
			end := bytes.IndexByte(rest, '\n')
			if end < 0 {
				end = len(rest)
			}
			blankSwiftRange(b, i, i+end)
			i += end
		case bytes.HasPrefix(rest, []byte("/*")):
			end, nested := i+2, 1
			for end < len(b) && nested > 0 {
				if bytes.HasPrefix(b[end:], []byte("/*")) {
					nested++
					end += 2
				} else if bytes.HasPrefix(b[end:], []byte("*/")) {
					nested--
					end += 2
				} else {
					end++
				}
			}
			blankSwiftRange(b, i, end)
			i = end
		case len(bytes.TrimLeft(rest, "#")) > 0 && bytes.TrimLeft(rest, "#")[0] == '"':
			i = blankSwiftString(b, i)
		case interpolation && b[i] == '(':
			depth++
			i++
		case interpolation && b[i] == ')':
			if depth == 0 {
				return i + 1
			}
			depth--
			i++
		default:
			i++
		}
	}
	return i
}

// blankSwiftString blanks the literal opening at i and returns the index after
// it. An unterminated single-line literal ends at its newline, so a stray quote
// cannot swallow the rest of the file.
func blankSwiftString(b []byte, i int) int {
	hashes := len(b[i:]) - len(bytes.TrimLeft(b[i:], "#"))
	pounds := strings.Repeat("#", hashes)
	multiline := bytes.HasPrefix(b[i+hashes:], []byte(`"""`))
	closer, escape := []byte(`"`+pounds), []byte(`\`+pounds)
	start := i + hashes + 1
	if multiline {
		closer = []byte(`"""` + pounds)
		start = i + hashes + 3
	}
	segment, j := i, start
	for j < len(b) {
		switch {
		case bytes.HasPrefix(b[j:], closer):
			blankSwiftRange(b, segment, j+len(closer))
			return j + len(closer)
		case !multiline && b[j] == '\n':
			blankSwiftRange(b, segment, j)
			return j
		case bytes.HasPrefix(b[j:], escape):
			after := j + len(escape)
			if after < len(b) && b[after] == '(' {
				blankSwiftRange(b, segment, after+1)
				j = blankSwiftCode(b, after+1, true)
				segment = j
			} else {
				j = after + 1
			}
		default:
			j++
		}
	}
	blankSwiftRange(b, segment, len(b))
	return len(b)
}

// sortEdges orders the reverse edge lists. Importers are appended while
// iterating analyses, whose order the scanner does not fix, so the same
// repository scanned twice produced the same importers in a different
// sequence: --importers output shifted between identical runs, diffs of
// codemap output showed changes that were not changes, and no caller could
// assert an exact list.
//
// Imports are deliberately left alone. They are appended per file in
// resolution order, which is already stable and which callers rely on: the
// CUE resolver returns a selected package before the package it falls back
// to, and DepsProject sorts its own copy for JSON output anyway.
func (fg *FileGraph) sortEdges() {
	if fg == nil {
		return
	}
	for file := range fg.Importers {
		sort.Strings(fg.Importers[file])
	}
}

func applyPrecomputedFileEdges(fg *FileGraph, edges []fileEdge) {
	for _, edge := range edges {
		from, to := keyPath(edge.from), keyPath(edge.to)
		if from == "" || to == "" || from == to {
			continue
		}
		fg.Imports[from] = dedupe(append(fg.Imports[from], to))
		fg.Importers[to] = dedupe(append(fg.Importers[to], from))
	}
}

type analysisLanguageInventory struct {
	hasJS   bool
	hasDart bool
	hasRust bool
	hasCUE  bool
}

func inspectAnalysisLanguages(analyses []FileAnalysis) analysisLanguageInventory {
	var inventory analysisLanguageInventory
	for _, analysis := range analyses {
		language := DetectLanguage(analysis.Path)
		if isJavaScriptLanguage(language) {
			inventory.hasJS = true
		} else if language == "dart" {
			inventory.hasDart = true
		}
		switch analysis.Language {
		case "rust":
			inventory.hasRust = true
		case "cue":
			inventory.hasCUE = true
		}
	}
	return inventory
}

// buildFileIndex creates a multi-key index for fast import resolution. It has
// no project root behind it (callers pass bare FileInfo values, often in
// tests), so the on-disk fallback in tryExactMatch stays disabled; use
// buildFileIndexContext directly to enable it.
func buildFileIndex(files []FileInfo, goModule string) *fileIndex {
	idx, _ := buildFileIndexContext(context.Background(), files, goModule, "", Filters{})
	return idx
}

func buildFileIndexContext(ctx context.Context, files []FileInfo, goModule string, absRoot string, filters Filters) (*fileIndex, error) {
	directoryHint := min(len(files), 1024)
	idx := &fileIndex{
		byExact:  make(map[string]uint32, len(files)),
		bySuffix: make([]string, 0, len(files)),
		byDir:    make(map[string][]string, directoryHint),
		goPkgs:   make(map[string][]string, directoryHint),
		absRoot:  absRoot,
		filters:  filters,
	}
	goPackagePaths := make(map[string]string, directoryHint)

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := keyPath(f.Path)
		dir := filepath.ToSlash(filepath.Dir(path))
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

// exact reports how many indexed files have this path, whatever separator
// the probe was built with.
func (idx *fileIndex) exact(p string) uint32 {
	return idx.byExact[keyPath(p)]
}

// dir returns the files directly inside the directory, whatever separator
// the probe was built with.
func (idx *fileIndex) dir(p string) ([]string, bool) {
	files, ok := idx.byDir[keyPath(p)]
	return files, ok
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
		right := byte('/')
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
	return len(path) > len(suffix) && path[len(path)-len(suffix)-1] == '/' && strings.HasSuffix(path, suffix)
}

func (idx *fileIndex) suffixMatches(suffix string) []string {
	suffix = keyPath(suffix)
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
		files, _ := idx.dir(packagePath)
		return resolveCUEPackage(files, idx.cuePackages, selector)
	}

	if sourceLanguage == "c" || sourceLanguage == "cpp" {
		return resolveCInclude(imp, fromDir, idx, sourceLanguage)
	}

	if sourceLanguage == "bash" && strings.ContainsAny(imp, "$`") {
		return resolveBashDynamic(imp, idx)
	}

	if sourceLanguage == "python" && !strings.HasPrefix(imp, ".") {
		return resolvePythonModule(imp, fromDir, idx)
	}

	// Normalize the import path
	normalized := normalizeImport(imp)

	if sourceLanguage == "lua" {
		return resolveLuaModule(normalized, fromDir, idx)
	}

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
		files := resolveRelative(imp, fromDir, idx, sourceLanguage)
		// A file match (dir.js beside dir/) wins over the directory, as in Node;
		// a dir/index.* match yields to the directory's package.json main.
		if isJavaScriptLanguage(sourceLanguage) {
			dir := cleanRepoPath(filepath.Join(fromDir, imp))
			if len(files) == 0 || dir == "" || strings.HasPrefix(files[0], dir+"/") {
				if main := jsResolver.directoryPackageMain(dir, idx, sourceLanguage); len(main) > 0 {
					return main
				}
			}
		}
		return files
	}

	// Strategy 3: TypeScript/JavaScript path alias resolution (@modules/auth, @shared/utils)
	if isJavaScriptLanguage(sourceLanguage) {
		scopes := jsResolver.pathAliasScopes(fromFile, pathAliases, baseURL)
		if len(scopes) > 0 {
			if files := resolvePathAlias(imp, scopes[0].aliases, scopes[0].baseURL, idx, sourceLanguage); len(files) > 0 {
				return files
			}
		}
		if files := jsResolver.resolve(imp, fromFile, idx, sourceLanguage); len(files) > 0 {
			return files
		}
		// A workspace: dependency links the local package, whose exports often
		// name unbuilt dist files; an outer tsconfig's paths map it to source.
		if len(scopes) > 1 && jsResolver.declaresWorkspaceDependency(fromFile, strings.Trim(imp, "\"'`")) {
			for _, scope := range scopes[1:] {
				if files := resolvePathAlias(imp, scope.aliases, scope.baseURL, idx, sourceLanguage); len(files) > 0 {
					return files
				}
			}
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

// resolveLuaModule searches the conventional package.path roots first, so
// lua/app/init.lua wins over a same-named plugin/app.lua. A bare name found only
// deeper ("cjson" -> kong/tools/cjson.lua) is an external library, and a module
// never names a directory, so the suffix and directory fan-outs stay off for it.
// A dotted spelling is a dofile-style path from the file, and "." or ".." name
// no file at all.
func resolveLuaModule(module, fromDir string, idx *fileIndex) []string {
	if strings.HasPrefix(module, ".") {
		if base := pathpkg.Base(module); base == "." || base == ".." {
			return nil
		}
		return resolveRelative(module, fromDir, idx, "lua")
	}
	for _, root := range []string{"lua/", "src/", "lib/", ""} {
		if files := tryExactMatch(root+module, idx, "lua"); len(files) > 0 {
			return files
		}
	}
	if !strings.Contains(module, "/") {
		return nil
	}
	return trySuffixMatch(module, idx, "lua")
}

// resolveBashDynamic links "${DIR}/lib/a.sh" only when its literal remainder
// names exactly one indexed shell file; the prefix's value is unknown, so a
// second candidate makes the target a guess.
func resolveBashDynamic(imp string, idx *fileIndex) []string {
	remainder := bashDynamicRemainder(imp)
	if remainder == "" || !idx.bashPrefixInRepo(imp) {
		return nil
	}
	matches := idx.suffixMatches(remainder)
	if idx.exact(remainder) == 1 {
		matches = append(matches, keyPath(remainder))
	}
	matches = compatibleFiles("bash", matches)
	if len(matches) != 1 {
		return nil
	}
	return matches
}

var (
	bashShebang           = regexp.MustCompile(`^#!.*\bk?(?:ba|z|da)?sh\b`)
	bashVariableReference = regexp.MustCompile(`\$\{?[#!]?([A-Za-z_]\w*|0)`)
	bashDefaultAssignment = regexp.MustCompile(`\$\{([A-Za-z_]\w*):?=`)
	bashAssignmentWord    = regexp.MustCompile(`^([A-Za-z_]\w*)\+?=`)
	// The shell or bats sets these to a place inside the project.
	bashSelfLocatingVariables = map[string]bool{"0": true, "BASH_SOURCE": true, "PWD": true, "BATS_TEST_DIRNAME": true, "BATS_TEST_FILENAME": true}
	// Words that may precede an assignment in a command.
	bashAssignmentPrefixes = map[string]bool{"then": true, "do": true, "else": true, "if": true, "elif": true, "while": true, "until": true, "!": true, "export": true, "local": true, "readonly": true, "declare": true, "typeset": true, "exec": true, "env": true, "command": true, "builtin": true}
)

// bashPrefixInRepo reports whether the dynamic prefix of imp plausibly points
// into the project: a self-locating form, or variables the project assigns.
// "$NVM_DIR/nvm.sh" with NVM_DIR set only by the user's shell points elsewhere.
//
// ponytail: an assignment anywhere counts, whatever its value or file, so a
// variable the repo sets to an outside path still links. Evaluating the value
// is the upgrade if that shows up.
func (idx *fileIndex) bashPrefixInRepo(imp string) bool {
	for _, segment := range bashWordSegments(imp) {
		if !strings.ContainsAny(segment, "$`") {
			break
		}
		if body, ok := bashSubstitutionBody(segment); ok {
			if !idx.bashSubstitutionInRepo(body) {
				return false
			}
			continue
		}
		for _, match := range bashVariableReference.FindAllStringSubmatch(segment, -1) {
			if !idx.bashVariableInRepo(match[1]) {
				return false
			}
		}
	}
	return true
}

func (idx *fileIndex) bashVariableInRepo(name string) bool {
	return bashSelfLocatingVariables[name] || name != "HOME" && idx.bashAssignedVariables()[name]
}

func bashSubstitutionBody(segment string) (string, bool) {
	if open := strings.Index(segment, "$("); open >= 0 {
		end := strings.LastIndexByte(segment, ')')
		return segment[open+2 : max(end, open+2)], true
	}
	if open := strings.IndexByte(segment, '`'); open >= 0 {
		end := strings.LastIndexByte(segment, '`')
		return segment[open+1 : max(end, open+1)], true
	}
	return "", false
}

// bashSubstitutionInRepo reports whether a command substitution prints a path in
// the project: its last command locates itself, and any cd before that goes
// somewhere the project can vouch for.
func (idx *fileIndex) bashSubstitutionInRepo(body string) bool {
	commands, _ := bashCommands(body)
	located := false
	for _, command := range commands {
		words := strings.Fields(command)
		for len(words) > 0 && (bashAssignmentWord.MatchString(words[0]) || words[0] == "command" || words[0] == "builtin") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		args := strings.Join(words[1:], " ")
		switch pathpkg.Base(strings.TrimPrefix(words[0], `\`)) {
		case "pwd":
			located = true
		case "git":
			located = len(words) > 1 && words[1] == "rev-parse"
		case "dirname", "realpath", "readlink":
			located = idx.bashArgumentInRepo(args)
		case "cd", "pushd":
			if !idx.bashArgumentInRepo(args) {
				return false
			}
			located = false
		default:
			located = false
		}
	}
	return located
}

// bashArgumentInRepo reports whether args reference variables, all of which point
// into the project. A literal path could be anywhere.
func (idx *fileIndex) bashArgumentInRepo(args string) bool {
	references := bashVariableReference.FindAllStringSubmatch(args, -1)
	return len(references) > 0 && !slices.ContainsFunc(references, func(match []string) bool {
		return !idx.bashVariableInRepo(match[1])
	})
}

func (idx *fileIndex) bashAssignedVariables() map[string]bool {
	if idx.bashAssigned != nil {
		return idx.bashAssigned
	}
	idx.bashAssigned = make(map[string]bool)
	// An only list drops an extensionless launcher (bats' bin/bats) from the
	// index, yet it is often what sets the root the libraries source from. It
	// stays out of the graph; only its assignments are read.
	shellCandidates := slices.Collect(maps.Keys(idx.byExact))
	if idx.unfilteredFiles != nil {
		if unfiltered, err := idx.unfilteredFiles(); err == nil {
			for _, file := range unfiltered {
				shellCandidates = append(shellCandidates, keyPath(file.Path))
			}
		}
	}
	for _, file := range shellCandidates {
		if DetectLanguage(file) != "bash" && pathpkg.Ext(file) != "" {
			continue
		}
		data, _ := readProjectFile(idx.absRoot, file)
		if DetectLanguage(file) != "bash" && !bashShebang.Match(data) {
			continue
		}
		forEachBashCommand(data, func(_ int, command string) {
			addBashAssignments(command, idx.bashAssigned)
		})
	}
	return idx.bashAssigned
}

// addBashAssignments records the variables a command assigns: words in assignment
// position, a for variable, and ${X:=y}. Quoted text assigns nothing, except that
// a double-quoted ${X:=y} still expands.
func addBashAssignments(command string, assigned map[string]bool) {
	for _, match := range bashDefaultAssignment.FindAllStringSubmatch(blankBashQuotes(command, false), -1) {
		assigned[match[1]] = true
	}
	words := strings.Fields(blankBashQuotes(command, true))
	for i := 0; i < len(words); i++ {
		word := strings.TrimLeft(words[i], "({")
		if match := bashAssignmentWord.FindStringSubmatch(word); match != nil {
			assigned[match[1]] = true
			continue
		}
		switch {
		case word == "for" && i+2 < len(words) && words[i+2] == "in":
			assigned[words[i+1]] = true
			return
		case word == "function":
			i++
		case word != "" && !bashAssignmentPrefixes[word] && word[0] != '-' && !strings.HasSuffix(word, ")"):
			return
		}
	}
}

// blankBashQuotes empties single-quoted strings, and double-quoted ones if asked.
func blankBashQuotes(command string, double bool) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == '\'':
			if c == quote {
				quote = 0
				out.WriteByte(c)
			}
		case c == '\\' && i+1 < len(command):
			i++
			if quote == 0 || !double {
				out.WriteByte(c)
				out.WriteByte(command[i])
			}
		case c == '\'' && quote == 0:
			quote = c
			out.WriteByte(c)
		case c == '"':
			quote ^= '"'
			out.WriteByte(c)
		case quote == 0 || !double:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// resolveBatsHelperLoads links load calls in the files bats reaches: every .bats
// file, the setup_suite.bash bats runs itself, and whatever those import. load
// is a bats function, so anywhere else it is the project's own command. It returns
// the files reached.
func resolveBatsHelperLoads(analyses []FileAnalysis, fg *FileGraph, resolve func(imp, from string) []string) map[string]bool {
	loads := make(map[string][]string)
	var pending []string
	for _, a := range analyses {
		key := keyPath(a.Path)
		for _, ref := range a.References {
			if ref.Kind == bashHelperLoad {
				loads[key] = append(loads[key], ref.Path)
			}
		}
		if strings.HasSuffix(key, ".bats") || pathpkg.Base(key) == "setup_suite.bash" {
			pending = append(pending, key)
		}
	}
	seen := make(map[string]bool)
	for len(pending) > 0 {
		file := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[file] {
			continue
		}
		seen[file] = true
		imports := fg.Imports[file]
		for _, load := range loads[file] {
			imports = append(imports, resolve(load, file)...)
		}
		imports = slices.DeleteFunc(dedupe(slashPaths(imports)), func(target string) bool { return target == file })
		if len(imports) > 0 {
			fg.Imports[file] = imports
		}
		pending = append(pending, imports...)
	}
	return seen
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

	// Python dots to slashes: app.core.config -> app/core/config. A dot that
	// introduces a source extension is a file name, not a module path:
	// #include "b.h" and require_once 'b.php' became b/h and b/php, so every
	// same-directory C, C++ and PHP include resolved to nothing.
	if strings.Contains(imp, ".") && !strings.Contains(imp, "/") && !strings.HasPrefix(imp, ".") && extToLang[strings.ToLower(filepath.Ext(imp))] == "" {
		imp = strings.ReplaceAll(imp, ".", "/")
	}

	// Rust :: to slashes: crate::foo::bar -> foo/bar
	if strings.HasPrefix(imp, "crate::") {
		imp = strings.TrimPrefix(imp, "crate::")
		imp = strings.ReplaceAll(imp, "::", "/")
	}
	if strings.HasPrefix(imp, "super::") {
		imp = strings.ReplaceAll(imp, "::", "/")
	}

	return imp
}

// pythonModulePath turns a dotted module path into its root-relative path
// (no extension). One leading dot means the current package, and each
// additional dot climbs one package: from "pkg/sub/user.py", ".mod" is
// pkg/sub/mod and "..mod" is pkg/mod. ok is false for a climb past the root.
func pythonModulePath(module, fromDir string) (string, bool) {
	level := 0
	for level < len(module) && module[level] == '.' {
		level++
	}
	path := filepath.FromSlash(strings.ReplaceAll(module[level:], ".", "/"))
	if level == 0 {
		return path, true
	}
	targetDir := fromDir
	for i := 1; i < level; i++ {
		// filepath.Dir("") is ".", which normalizes back to "", so an
		// unchecked loop clamps at the scan root and a dot count deeper than
		// the file resolves to a root-level module it never named. Climbing
		// past the root has to resolve to nothing: the package above the root
		// is not visible, and guessing produces a fabricated edge.
		if targetDir == "" {
			return "", false
		}
		targetDir = filepath.Dir(targetDir)
		if targetDir == "." {
			targetDir = ""
		}
	}
	return filepath.Join(targetDir, path), true
}

// resolvePythonRelative resolves a Python relative import (see
// pythonModulePath). A bare run of dots names the package itself, which is
// its __init__.py.
func resolvePythonRelative(imp, fromDir string, idx *fileIndex) []string {
	candidate, ok := pythonModulePath(imp, fromDir)
	if !ok {
		return nil
	}
	if strings.Trim(imp, ".") == "" {
		return tryExactMatch(filepath.Join(candidate, "__init__"), idx, "python")
	}
	return pythonPackageMember(candidate, idx)
}

// pythonPackageMember resolves a module path to its module file, or to its
// package __init__.py.
func pythonPackageMember(path string, idx *fileIndex) []string {
	if files := tryExactMatch(path, idx, "python"); len(files) > 0 {
		return files
	}
	return tryExactMatch(filepath.Join(path, "__init__"), idx, "python")
}

// resolvePythonModule resolves a dotted module path to its module file or
// package __init__.py. A namespace package has no file, so it resolves to
// nothing rather than to every file in its directory (#265).
func resolvePythonModule(module, fromDir string, idx *fileIndex) []string {
	if strings.HasPrefix(module, ".") {
		return resolvePythonRelative(module, fromDir, idx)
	}
	path := strings.ReplaceAll(module, ".", "/")
	if files := tryExactMatch(path, idx, "python"); len(files) > 0 {
		return files
	}
	return trySuffixMatch(path, idx, "python")
}

// pythonNamespacePackage reports whether module names a project directory
// that holds Python code but no __init__.py: it resolves, with no file edge.
func (idx *fileIndex) pythonNamespacePackage(module, fromDir string) bool {
	return idx.pythonNamespaceDir(module, fromDir) != ""
}

// pythonNamespaceDir returns the project directory module names, or "". An
// absolute module is looked for under each import root in turn (#273).
func (idx *fileIndex) pythonNamespaceDir(module, fromDir string) string {
	path, ok := pythonModulePath(module, fromDir)
	if !ok {
		return ""
	}
	if strings.HasPrefix(module, ".") {
		if idx.pythonDir(path) {
			return path
		}
		return ""
	}
	for _, root := range idx.pythonImportRoots(fromDir) {
		if candidate := filepath.Join(root, path); idx.pythonDir(candidate) {
			return candidate
		}
	}
	return ""
}

// pythonImportRoots lists where an absolute import from fromDir can start:
// the repo root, src/, then the importing script's directory and each one
// above it. A directory holding __init__.py is a package, not on sys.path
// (see importableFiles), so it is skipped.
func (idx *fileIndex) pythonImportRoots(fromDir string) []string {
	roots := []string{"", "src"}
	for dir := fromDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		if idx.exact(filepath.Join(dir, "__init__.py")) == 0 {
			roots = append(roots, dir)
		}
	}
	return roots
}

// pythonDir reports whether path is a project directory with Python code at
// or below it.
func (idx *fileIndex) pythonDir(path string) bool {
	if path == "" {
		return false
	}
	if idx.pythonDirs == nil {
		idx.pythonDirs = map[string]bool{}
		for dir, files := range idx.byDir {
			if !slices.ContainsFunc(files, func(f string) bool { return DetectLanguage(f) == "python" }) {
				continue
			}
			for ; dir != "" && dir != "." && !idx.pythonDirs[dir]; dir = pathpkg.Dir(dir) {
				idx.pythonDirs[dir] = true
			}
		}
	}
	return idx.pythonDirs[filepath.ToSlash(path)]
}

// pythonRoot is what the P of "from P import name" resolves to: its files
// (module or __init__.py), or its directory when it is a namespace package.
type pythonRoot struct {
	files     []string
	namespace string
}

func (idx *fileIndex) pythonFromRoot(parent, fromDir string) pythonRoot {
	// A namespace package resolves against the importer's directory, so
	// the key carries it for absolute imports too.
	key := fromDir + "\x00" + parent
	if root, ok := idx.pythonRoots[key]; ok {
		return root
	}
	var root pythonRoot
	if strings.Trim(parent, ".") == "" {
		root.files = resolvePythonRelative(parent, fromDir, idx)
	} else {
		root.files = resolvePythonModule(parent, fromDir, idx)
	}
	if len(root.files) == 0 {
		root.namespace = idx.pythonNamespaceDir(parent, fromDir)
	}
	if idx.pythonRoots == nil {
		idx.pythonRoots = map[string]pythonRoot{}
	}
	idx.pythonRoots[key] = root
	return root
}

// resolvePythonMember resolves "from P import name", spelled "P.name" (or
// ".name" when P is bare dots). P resolves first; name is then the submodule
// beside P's __init__.py, or a name defined in P, so P's own file (#266). A
// submodule is only looked for where P resolved, so a stdlib P cannot pick up
// a same-named project directory through the suffix match. resolved is true
// when the import names something in the project, even with no file edge.
func resolvePythonMember(spec, fromFile string, idx *fileIndex) (files []string, resolved bool) {
	fromDir := pythonFromDir(fromFile)
	i := strings.LastIndex(spec, ".")
	parent, name := spec[:i], spec[i+1:]
	if strings.Trim(spec[:i+1], ".") == "" {
		parent = spec[:i+1]
	}
	root := idx.pythonFromRoot(parent, fromDir)
	for _, file := range root.files {
		if filepath.Base(file) != "__init__.py" {
			files = append(files, file)
			continue
		}
		dir := filepath.Dir(file)
		if sub := pythonPackageMember(filepath.Join(dir, name), idx); len(sub) > 0 {
			files = append(files, sub...)
		} else if !idx.pythonDir(filepath.Join(dir, name)) {
			files = append(files, file)
		}
	}
	if root.namespace != "" {
		files = append(files, pythonPackageMember(filepath.Join(root.namespace, name), idx)...)
		resolved = idx.pythonDir(filepath.Join(root.namespace, name))
	}
	return files, resolved || len(root.files) > 0 || len(files) > 0
}

func pythonFromDir(fromFile string) string {
	if dir := filepath.Dir(fromFile); dir != "." {
		return dir
	}
	return ""
}

func pythonFromMembers(refs []ImportReference) map[string]bool {
	var members map[string]bool
	for _, ref := range refs {
		if ref.Kind == pythonFromMember {
			if members == nil {
				members = make(map[string]bool)
			}
			members[ref.Path] = true
		}
	}
	return members
}

// resolveRelative handles ./foo and ../bar style imports
func resolveRelative(imp, fromDir string, idx *fileIndex, sourceLanguage string) []string {
	return tryExactMatch(filepath.Join(fromDir, imp), idx, sourceLanguage)
}

func isAboveRoot(path string) bool {
	return path == ".." || strings.HasPrefix(path, "../")
}

// tryExactMatch looks for exact path matches with common extensions.
// Extension list derived from the canonical scanner registry.
func tryExactMatch(path string, idx *fileIndex, sourceLanguage string) []string {
	path = keyPath(path)
	// A path above the scan root names nothing we indexed. Probing it would
	// either miss or, through the on-disk recovery below, leave the project.
	if isAboveRoot(path) {
		return nil
	}
	// The project root as a directory import. Appending "/index.js" to an
	// empty path would build the absolute "/index.js", a key no file has.
	if path == "" {
		path = "."
	}
	if idx.exact(path) == 1 && languagesCompatible(sourceLanguage, DetectLanguage(path)) {
		return []string{path}
	}
	// Under ESM and NodeNext, TypeScript requires the specifier to name the
	// emitted JavaScript file, so "./helper.js" is written in a file whose
	// only on-disk counterpart is helper.ts. The specifier already carries an
	// extension, so appending one below never matches and the edge was lost.
	for _, candidate := range typescriptSourceCandidates(path, sourceLanguage) {
		if idx.exact(candidate) == 1 && languagesCompatible(sourceLanguage, DetectLanguage(candidate)) {
			return []string{candidate}
		}
	}
	for _, ext := range resolverExtensions[:len(resolverExtensions)-1] {
		candidate := path + ext
		if idx.exact(candidate) == 1 && languagesCompatible(sourceLanguage, DetectLanguage(candidate)) {
			return []string{candidate}
		}
	}

	// A candidate absent from the scanned inventory (count 0, as opposed to
	// the ambiguous count >= 2 case above, which is deliberately left
	// unresolved) may still be a real, readable file that the scan excluded
	// for an orthogonal reason — most commonly .gitignore, since ScanFiles
	// applies the project's gitignore rules to build the inventory this
	// index is drawn from. A plain relative require('./x') naming an exact,
	// unambiguous path is confident evidence the file is genuinely part of
	// the dependency graph even though it isn't part of the scanned file
	// set, so this on-disk check recovers exactly that edge without
	// widening the fuzzy/suffix resolution strategies to gitignored files.
	if idx.absRoot != "" {
		if idx.exact(path) == 0 && languagesCompatible(sourceLanguage, DetectLanguage(path)) && idx.gitignoreOnlyRecoverable(path) {
			return []string{path}
		}
		for _, candidate := range typescriptSourceCandidates(path, sourceLanguage) {
			if idx.exact(candidate) == 0 && languagesCompatible(sourceLanguage, DetectLanguage(candidate)) && idx.gitignoreOnlyRecoverable(candidate) {
				return []string{candidate}
			}
		}
		for _, ext := range resolverExtensions[:len(resolverExtensions)-1] {
			candidate := path + ext
			if idx.exact(candidate) == 0 && languagesCompatible(sourceLanguage, DetectLanguage(candidate)) && idx.gitignoreOnlyRecoverable(candidate) {
				return []string{candidate}
			}
		}
	}

	return nil
}

// gitignoreOnlyRecoverable reports whether relPath is missing from the
// scanned inventory *solely* because of .gitignore, as opposed to an
// explicit --only/--exclude filter, a config.json exclude, or one of the
// scanner's own never-source directory names (node_modules, vendor,
// testdata, ...) — all of which must stay absolutely excluded, unlike
// .gitignore's blind spot. ScanFiles' only exclusion mechanisms are those
// three plus .gitignore, so a real, regular file that clears the other two
// and is still absent can only be missing because of .gitignore.
func (idx *fileIndex) gitignoreOnlyRecoverable(relPath string) bool {
	if !MatchesFilters(filepath.ToSlash(relPath), filepath.Ext(relPath), idx.filters.Only, idx.filters.Exclude) {
		return false
	}
	for dir := filepath.ToSlash(filepath.Dir(relPath)); dir != "." && dir != "/" && dir != ""; dir = pathpkg.Dir(dir) {
		if IgnoredDirs[pathpkg.Base(dir)] {
			return false
		}
	}
	return isRegularFile(idx.absRoot, relPath)
}

// isRegularFile reports whether root-relative path exists on disk under
// absRoot as a regular (not directory, not symlink-to-directory) file.
//
// Stat alone is not enough: on a case-insensitive filesystem "FreeRTOS.h" stats
// as the freertos.h beside it, so the listing has to hold the exact name.
func isRegularFile(absRoot, relPath string) bool {
	path := filepath.Join(absRoot, filepath.FromSlash(relPath))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	return err == nil && slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return entry.Name() == filepath.Base(path) })
}

// typescriptEmitExtensions maps an emitted JavaScript extension to the
// TypeScript sources that produce it, in the order the compiler prefers.
var typescriptEmitExtensions = map[string][]string{
	".js":  {".ts", ".tsx", ".d.ts"},
	".jsx": {".tsx", ".d.ts"},
	".mjs": {".mts", ".d.mts"},
	".cjs": {".cts", ".d.cts"},
}

// typescriptSourceCandidates returns the TypeScript files a JavaScript
// specifier may name. It applies only to JS-family importers, and only when
// the specifier already carries an emitted extension: for anything else the
// ordinary extension-appending search is correct and this returns nothing.
func typescriptSourceCandidates(path, sourceLanguage string) []string {
	if !isJavaScriptLanguage(sourceLanguage) {
		return nil
	}
	sources, emitted := typescriptEmitExtensions[strings.ToLower(pathpkg.Ext(path))]
	if !emitted {
		return nil
	}
	stem := strings.TrimSuffix(path, pathpkg.Ext(path))
	candidates := make([]string, 0, len(sources))
	for _, ext := range sources {
		candidates = append(candidates, stem+ext)
	}
	return candidates
}

// trySuffixMatch finds files where the path ends with the normalized import
func trySuffixMatch(normalized string, idx *fileIndex, sourceLanguage string) []string {
	for _, ext := range resolverExtensions {
		candidate := normalized + ext
		if files := idx.suffixMatches(candidate); len(files) > 0 {
			files = importableFiles(compatibleFiles(sourceLanguage, files), candidate, idx, sourceLanguage)
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
		return importableFiles(compatibleFiles(sourceLanguage, files), initCandidate, idx, sourceLanguage)
	}

	return nil
}

// importableFiles drops Python suffix matches whose implied import root is
// itself a package. A directory holding __init__.py is not on sys.path, so
// "import pathlib" cannot mean src/_pytest/pathlib.py: that module is
// _pytest.pathlib, and the import is the standard library's.
func importableFiles(files []string, suffix string, idx *fileIndex, sourceLanguage string) []string {
	if sourceLanguage != "python" {
		return files
	}
	suffix = keyPath(suffix)
	return slices.DeleteFunc(files, func(file string) bool {
		return idx.exact(strings.TrimSuffix(file, suffix)+"__init__.py") > 0
	})
}

// tryDirMatch returns all files whose parent directory matches the given path.
// This resolves namespace-level imports (e.g. C# "using Foo.Bar;" -> "Foo/Bar/")
// where an import refers to a whole directory rather than a single file.
// It also tries progressively shorter suffixes to handle namespace prefixes
// (e.g. "MyApp/Models" tries "MyApp/Models" first, then "Models").
func tryDirMatch(path string, idx *fileIndex, sourceLanguage string) []string {
	// Try exact match first
	if files, ok := idx.dir(path); ok {
		if compatible := compatibleFiles(sourceLanguage, files); len(compatible) > 0 {
			return compatible
		}
	}
	// Normalize path separators for cross-platform compatibility
	nativePath := filepath.FromSlash(path)
	if nativePath != path {
		if files, ok := idx.dir(nativePath); ok {
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
		if files, ok := idx.dir(suffix); ok {
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

// IsFileHub is IsHub for a claim about path itself. A file of a multi-file
// Go package shares the package's importers, so the count says nothing about
// the file and it never qualifies; CollapseGoPackageHubs lists the package.
func (fg *FileGraph) IsFileHub(path string) bool {
	if _, shared := fg.SharedGoPackage(path); shared {
		return false
	}
	return fg.IsHub(path)
}

// SharedGoPackage returns the import path of path's Go package when that
// package has two or more files, so path's importer count is the package's
// rather than its own.
func (fg *FileGraph) SharedGoPackage(path string) (string, bool) {
	pkg, files, ok := fg.GoPackage(path)
	return pkg, ok && len(files) > 1
}

// GoPackage reports the Go package that path belongs to when this graph
// resolved Go imports at package granularity: the package's import path and
// its non-test files, which include path itself unless path is a _test.go
// file (one lives in the package but is never an import target, so it is
// absent from Packages). ok is false for a non-Go file, for a graph with no
// go.mod, and for a directory the index never saw.
func (fg *FileGraph) GoPackage(path string) (importPath string, files []string, ok bool) {
	if fg == nil || fg.Module == "" || len(fg.Packages) == 0 {
		return "", nil, false
	}
	if !strings.EqualFold(filepath.Ext(path), ".go") {
		return "", nil, false
	}
	importPath = fg.Module
	if dir := pathpkg.Dir(filepath.ToSlash(path)); dir != "" && dir != "." {
		importPath += "/" + dir
	}
	files, ok = fg.Packages[importPath]
	if !ok {
		return "", nil, false
	}
	return importPath, files, true
}

// CollapseGoPackageFiles rewrites a list of file paths for text output so Go
// files that were linked as one package read as one entry. Go imports name
// packages, so every non-test .go file of a directory arrives in an edge list
// together, and printing each one inflates the output without adding an edge
// the reader can act on. Two or more .go paths in one directory become
// "dir/ (N files)" with N the number of paths collapsed into it, placed at the
// first of their positions; every other path is returned unchanged.
func CollapseGoPackageFiles(paths []string) []string {
	perDir := make(map[string]int, len(paths))
	for _, path := range paths {
		if strings.EqualFold(filepath.Ext(path), ".go") {
			perDir[pathpkg.Dir(filepath.ToSlash(path))]++
		}
	}
	out := make([]string, 0, len(paths))
	emitted := make(map[string]bool, len(perDir))
	for _, path := range paths {
		if !strings.EqualFold(filepath.Ext(path), ".go") {
			out = append(out, path)
			continue
		}
		dir := pathpkg.Dir(filepath.ToSlash(path))
		if perDir[dir] < 2 {
			out = append(out, path)
			continue
		}
		if emitted[dir] {
			continue
		}
		emitted[dir] = true
		if dir == "." {
			dir = ""
		}
		out = append(out, fmt.Sprintf("%s/ (%d files)", dir, perDir[pathpkg.Dir(filepath.ToSlash(path))]))
	}
	return out
}

// HubEntry is one row of a hub list: a file, or a multi-file Go package
// whose files share one importer set because Go imports name packages.
type HubEntry struct {
	Path      string   `json:"path"`  // the file, or "dir/" for a package
	Files     []string `json:"files"` // the hub files behind this row; more than one means a package
	Importers int      `json:"importer_count"`
}

// IsPackage reports whether the entry stands for a multi-file Go package.
func (e HubEntry) IsPackage() bool { return len(e.Files) > 1 }

// String is "path (N importers)", or "dir/ (package, F files, N importers)".
func (e HubEntry) String() string {
	if e.IsPackage() {
		return fmt.Sprintf("%s (package, %d files, %d importers)", e.Path, len(e.Files), e.Importers)
	}
	return fmt.Sprintf("%s (%d importers)", e.Path, e.Importers)
}

// CollapseGoPackageHubs turns a ranked hub list into one entry per file,
// except that two or more .go files of one directory with the same importer
// set become a single package entry at the first one's position. The same set
// is the signature of a package-level edge, and needs no go.mod lookup, so
// this also works on importer maps read back from the daemon state. Every
// other path passes through unchanged.
func CollapseGoPackageHubs(hubs []string, importers map[string][]string) []HubEntry {
	groupKey := func(path string) string {
		if !strings.EqualFold(filepath.Ext(path), ".go") {
			return ""
		}
		set := append([]string(nil), importers[path]...)
		sort.Strings(set)
		return pathpkg.Dir(filepath.ToSlash(path)) + "\x00" + strings.Join(set, "\x00")
	}
	groups := make(map[string][]string, len(hubs))
	for _, path := range hubs {
		if key := groupKey(path); key != "" {
			groups[key] = append(groups[key], path)
		}
	}
	out := make([]HubEntry, 0, len(hubs))
	emitted := make(map[string]bool, len(groups))
	for _, path := range hubs {
		key := groupKey(path)
		files := groups[key]
		if len(files) < 2 {
			out = append(out, HubEntry{Path: path, Files: []string{path}, Importers: len(importers[path])})
			continue
		}
		if emitted[key] {
			continue
		}
		emitted[key] = true
		dir := pathpkg.Dir(filepath.ToSlash(path))
		entry := HubEntry{Path: dir + "/", Files: files, Importers: len(importers[path])}
		if dir == "." {
			entry.Path = "/"
		}
		out = append(out, entry)
	}
	return out
}

// HubFiles returns all files that qualify as hubs under IsHub, ordered by
// non-test importer count descending and then by path. Map iteration order is
// random, so callers that truncate or display the head of this list would
// otherwise show a different set of hubs on every run over the same graph.
func (fg *FileGraph) HubFiles() []string {
	var hubs []string
	for path := range fg.Importers {
		if fg.IsHub(path) {
			hubs = append(hubs, path)
		}
	}
	counts := make(map[string]int, len(hubs))
	for _, path := range hubs {
		counts[path] = CountHubImporters(fg.Importers[path])
	}
	sort.Slice(hubs, func(i, j int) bool {
		if counts[hubs[i]] != counts[hubs[j]] {
			return counts[hubs[i]] > counts[hubs[j]]
		}
		return hubs[i] < hubs[j]
	})
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

// readTSConfig reads one tsconfig/jsconfig file; see tsConfigReader.pathAliases.
func readTSConfig(configPath, root string) (map[string][]string, string) {
	return newTSConfigReader(root).pathAliases(configPath)
}

// Bounds on tsconfig reading, which runs on untrusted repositories: extends may
// be an array, so a crafted repo can fan out reads without a cycle.
const (
	maxTSConfigReads   = 4096    // configs opened per scan
	maxTSConfigExtends = 16      // extends entries followed per config, first ones win
	maxTSConfigBytes   = 1 << 20 // larger configs are skipped
)

type tsConfigChain struct {
	paths      map[string][]string
	declaredIn map[string]string
	baseURL    string
	options    map[string]any // merged compilerOptions, child keys winning
}

// tsConfigReader reads every config of one scan at most once (shared parents
// and diamonds reuse the cached result, a cycle is skipped) and only inside
// root. reads counts configs opened, each costing one EvalSymlinks and stat.
type tsConfigReader struct {
	root         string // symlinks resolved; "" when root cannot be resolved
	rootSpelling string // root as given, the prefix configPath arguments share
	done         map[string]tsConfigChain
	active       map[string]bool
	reads        int
	maxReads     int
	notes        []string
}

func newTSConfigReader(root string) *tsConfigReader {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = ""
	}
	return &tsConfigReader{root: resolved, rootSpelling: root, done: map[string]tsConfigChain{}, active: map[string]bool{}, maxReads: maxTSConfigReads}
}

func (r *tsConfigReader) note(message string) {
	if !slices.Contains(r.notes, message) {
		r.notes = append(r.notes, message)
	}
}

// pathAliases returns a config's paths, following extends, with the directory
// the targets resolve against relative to root. TypeScript resolves a target
// against the effective baseUrl, or without one against the config that
// declared the paths, so an inherited key whose declaring config sits elsewhere
// is rebased onto the returned directory.
func (r *tsConfigReader) pathAliases(configPath string) (map[string][]string, string) {
	merged := r.read(configPath, 0)
	chain, declaredIn, baseURL := merged.paths, merged.declaredIn, merged.baseURL
	if len(chain) == 0 {
		return nil, ""
	}
	base := baseURL
	if base == "" {
		base = filepath.Dir(configPath)
	}
	// A fresh map: chain is cached and shared with configs extending this one.
	paths := make(map[string][]string, len(chain))
	for key, targets := range chain {
		keyBase := baseURL
		if keyBase == "" {
			keyBase = declaredIn[key]
		}
		if keyBase == base {
			paths[key] = targets
			continue
		}
		rebased := make([]string, 0, len(targets))
		for _, target := range targets {
			if !filepath.IsAbs(target) {
				if rel, err := filepath.Rel(base, filepath.Join(keyBase, target)); err == nil {
					target = rel
				}
			}
			rebased = append(rebased, target)
		}
		paths[key] = rebased
	}
	rel, err := filepath.Rel(r.rootSpelling, base)
	if err != nil {
		return nil, ""
	}
	return paths, filepath.ToSlash(rel)
}

// outputDirs returns the effective compilerOptions.rootDir/outDir of a config,
// following extends (child values override parents).
func (r *tsConfigReader) outputDirs(configPath string) (string, string) {
	return parseTSOutputDirs(map[string]any{"compilerOptions": r.read(configPath, 0).options})
}

// read returns a config merged with its extends chain: paths (child keys
// override parent keys), the directory of the config that declared each key,
// the effective baseUrl as a directory ("" when no config in the chain sets
// one), and compilerOptions.
func (r *tsConfigReader) read(configPath string, depth int) tsConfigChain {
	key := filepath.Clean(configPath)
	if chain, ok := r.done[key]; ok {
		return chain
	}
	if depth > 32 || r.root == "" || r.active[key] {
		return tsConfigChain{}
	}
	if r.reads >= r.maxReads {
		r.note(fmt.Sprintf("tsconfig: stopped after %d configs; later extends were not followed", r.maxReads))
		return tsConfigChain{}
	}
	r.reads++
	// Resolving symlinks first keeps a linked config from reading outside the root.
	resolved, err := filepath.EvalSymlinks(key)
	if err != nil || !withinDir(r.root, resolved) {
		r.done[key] = tsConfigChain{}
		return tsConfigChain{}
	}
	if info, err := os.Stat(resolved); err != nil || info.Size() > maxTSConfigBytes {
		if err == nil {
			r.note(fmt.Sprintf("tsconfig: skipped %s, larger than %d bytes", filepath.Base(key), maxTSConfigBytes))
		}
		r.done[key] = tsConfigChain{}
		return tsConfigChain{}
	}
	r.active[key] = true
	chain := r.parse(key, depth)
	delete(r.active, key)
	r.done[key] = chain
	return chain
}

func withinDir(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (r *tsConfigReader) parse(configPath string, depth int) tsConfigChain {
	doc, ok := readJSWorkspaceManifest(configPath)
	if !ok {
		return tsConfigChain{}
	}
	paths := map[string][]string{}
	declaredIn := map[string]string{}
	merged := map[string]any{}
	var baseURL string
	extends := doc["extends"]
	if single, ok := extends.(string); ok {
		extends = []any{single}
	}
	parents, _ := extends.([]any)
	if len(parents) > maxTSConfigExtends {
		r.note(fmt.Sprintf("tsconfig: %s lists %d extends; followed the first %d", filepath.Base(configPath), len(parents), maxTSConfigExtends))
		parents = parents[:maxTSConfigExtends]
	}
	for _, parent := range parents {
		parentPath, _ := parent.(string)
		if parentPath == "" {
			continue
		}
		if !filepath.IsAbs(parentPath) {
			parentPath = filepath.Join(filepath.Dir(configPath), parentPath)
		}
		if !strings.HasSuffix(parentPath, ".json") {
			parentPath += ".json"
		}
		parentChain := r.read(parentPath, depth+1)
		for key, targets := range parentChain.paths {
			paths[key], declaredIn[key] = targets, parentChain.declaredIn[key]
		}
		if parentChain.baseURL != "" {
			baseURL = parentChain.baseURL
		}
		for key, value := range parentChain.options {
			merged[key] = value
		}
	}
	options, _ := doc["compilerOptions"].(map[string]any)
	for key, value := range options {
		merged[key] = value
	}
	if value, _ := options["baseUrl"].(string); value != "" {
		baseURL = filepath.Join(filepath.Dir(configPath), value)
	}
	declared, _ := options["paths"].(map[string]any)
	for key, value := range declared {
		items, _ := value.([]any)
		var targets []string
		for _, item := range items {
			if target, ok := item.(string); ok {
				targets = append(targets, target)
			}
		}
		paths[key], declaredIn[key] = targets, filepath.Dir(configPath)
	}
	return tsConfigChain{paths: paths, declaredIn: declaredIn, baseURL: baseURL, options: merged}
}

// normalizeAliasTarget puts a substituted tsconfig alias target into the same
// shape as the file index, which stores repository-relative paths with no "./"
// prefix. create-next-app has shipped "@/*": ["./*"] for years, so the
// substituted target is "./lib/a1" while the index holds "lib/a1" and nothing
// matches. filepath.Join already cleaned the target when a baseUrl was set,
// which is why this only ever bit projects without one.
func normalizeAliasTarget(target string) string {
	if target == "" {
		return target
	}
	cleaned := filepath.ToSlash(filepath.Clean(target))
	if cleaned == "." {
		return ""
	}
	return cleaned
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
					resolved = normalizeAliasTarget(resolved)
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

			resolved = normalizeAliasTarget(resolved)

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
