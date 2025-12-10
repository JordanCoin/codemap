package scanner

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// FileGraph represents internal file-to-file dependencies within a project
type FileGraph struct {
	Root      string              // project root
	Module    string              // go module name (e.g., "codemap")
	Imports   map[string][]string // file -> files it imports
	Importers map[string][]string // file -> files that import it
	Packages  map[string][]string // package path -> files in that package
}

// fileIndex provides fast lookup of files by various import-like keys
type fileIndex struct {
	byExact  map[string][]string // exact path -> files
	bySuffix map[string][]string // path suffix -> files (for nested packages)
	byDir    map[string][]string // directory -> files in it
	goPkgs   map[string][]string // Go package path -> files
}

// BuildFileGraph analyzes a project and returns file-level dependencies
// Uses ast-grep for multi-language support with universal fuzzy resolution
func BuildFileGraph(root string) (*FileGraph, error) {
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

	// Detect module name from go.mod (for Go import resolution)
	fg.Module = detectModule(absRoot)

	// Scan all files
	gitCache := NewGitIgnoreCache(root)
	files, err := ScanFiles(root, gitCache, nil, nil)
	if err != nil {
		return nil, err
	}

	// Build file index for fast fuzzy matching
	idx := buildFileIndex(files, fg.Module)
	fg.Packages = idx.goPkgs

	// Use ast-grep to extract imports for all languages
	analyses, err := ScanForDeps(root)
	if err != nil {
		return nil, err
	}

	// Resolve imports to files using universal fuzzy matching
	for _, a := range analyses {
		var resolvedImports []string

		for _, imp := range a.Imports {
			resolved := fuzzyResolve(imp, a.Path, idx, fg.Module)
			resolvedImports = append(resolvedImports, resolved...)
		}

		if len(resolvedImports) > 0 {
			fg.Imports[a.Path] = dedupe(resolvedImports)

			// Build reverse map
			for _, imported := range fg.Imports[a.Path] {
				fg.Importers[imported] = append(fg.Importers[imported], a.Path)
			}
		}
	}

	return fg, nil
}

// buildFileIndex creates a multi-key index for fast import resolution
func buildFileIndex(files []FileInfo, goModule string) *fileIndex {
	idx := &fileIndex{
		byExact:  make(map[string][]string),
		bySuffix: make(map[string][]string),
		byDir:    make(map[string][]string),
		goPkgs:   make(map[string][]string),
	}

	for _, f := range files {
		path := f.Path
		dir := filepath.Dir(path)
		if dir == "." {
			dir = ""
		}

		// Index by directory
		idx.byDir[dir] = append(idx.byDir[dir], path)

		// Index by exact path (without extension for fuzzy matching)
		idx.byExact[path] = append(idx.byExact[path], path)
		noExt := strings.TrimSuffix(path, filepath.Ext(path))
		idx.byExact[noExt] = append(idx.byExact[noExt], path)

		// Index by all path suffixes (for nested package resolution)
		// e.g., "llm-server/app/core/config.py" indexed as:
		//   - "app/core/config.py"
		//   - "core/config.py"
		//   - "config.py"
		parts := strings.Split(path, string(filepath.Separator))
		for i := 1; i < len(parts); i++ {
			suffix := strings.Join(parts[i:], string(filepath.Separator))
			idx.bySuffix[suffix] = append(idx.bySuffix[suffix], path)
			// Also without extension
			noExt := strings.TrimSuffix(suffix, filepath.Ext(suffix))
			idx.bySuffix[noExt] = append(idx.bySuffix[noExt], path)
		}

		// Go package index
		if strings.HasSuffix(path, ".go") && goModule != "" {
			pkgPath := goModule
			if dir != "" {
				pkgPath = goModule + "/" + dir
			}
			idx.goPkgs[pkgPath] = append(idx.goPkgs[pkgPath], path)
		}
	}

	return idx
}

// fuzzyResolve converts an import path to actual file paths using universal matching
// No language-specific switch - relies on pattern matching against file index
func fuzzyResolve(imp, fromFile string, idx *fileIndex, goModule string) []string {
	fromDir := filepath.Dir(fromFile)
	if fromDir == "." {
		fromDir = ""
	}

	// Normalize the import path
	normalized := normalizeImport(imp)

	// Strategy 1: Go package lookup (if it looks like a Go module import)
	if goModule != "" && strings.HasPrefix(imp, goModule) {
		if files, ok := idx.goPkgs[imp]; ok {
			return files
		}
	}

	// Strategy 2: Relative path resolution (./foo, ../bar)
	if strings.HasPrefix(imp, ".") {
		return resolveRelative(imp, fromDir, idx)
	}

	// Strategy 3: Exact match (with common extensions)
	if files := tryExactMatch(normalized, idx); len(files) > 0 {
		return files
	}

	// Strategy 4: Suffix match (for nested packages like app.core.config -> */app/core/config.py)
	if files := trySuffixMatch(normalized, idx); len(files) > 0 {
		return files
	}

	return nil
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

// resolveRelative handles ./foo and ../bar style imports
func resolveRelative(imp, fromDir string, idx *fileIndex) []string {
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

	return tryExactMatch(candidate, idx)
}

// tryExactMatch looks for exact path matches with common extensions
func tryExactMatch(path string, idx *fileIndex) []string {
	// Common extensions to try (in order of preference)
	extensions := []string{
		"", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java",
		"/index.js", "/index.ts", "/index.tsx", "/__init__.py", "/mod.rs",
	}

	for _, ext := range extensions {
		candidate := path + ext
		if files, ok := idx.byExact[candidate]; ok {
			return files
		}
	}

	return nil
}

// trySuffixMatch finds files where the path ends with the normalized import
func trySuffixMatch(normalized string, idx *fileIndex) []string {
	// Try with common extensions
	extensions := []string{"", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java", ".go"}

	for _, ext := range extensions {
		candidate := normalized + ext
		if files, ok := idx.bySuffix[candidate]; ok {
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
	if files, ok := idx.bySuffix[initCandidate]; ok {
		return files
	}

	return nil
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

// IsHub returns true if a file has 3+ importers
func (fg *FileGraph) IsHub(path string) bool {
	return len(fg.Importers[path]) >= 3
}

// HubFiles returns all files that are imported by 3+ other files
func (fg *FileGraph) HubFiles() []string {
	var hubs []string
	for path, importers := range fg.Importers {
		if len(importers) >= 3 {
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
