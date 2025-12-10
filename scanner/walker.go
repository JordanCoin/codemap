package scanner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// GitIgnoreCache manages nested .gitignore files throughout a project.
// It lazily loads gitignore files as directories are visited and checks
// paths against all applicable rules from root to leaf.
type GitIgnoreCache struct {
	root     string
	cache    map[string]*ignore.GitIgnore // abs dir path -> compiled gitignore (only dirs WITH gitignores)
	patterns map[string][]string          // abs dir path -> raw pattern lines
	visited  map[string]struct{}          // tracks visited dirs to avoid re-checking for .gitignore
}

// NewGitIgnoreCache creates a cache that supports nested .gitignore files.
// root should be the project root directory.
func NewGitIgnoreCache(root string) *GitIgnoreCache {
	absRoot, _ := filepath.Abs(root)
	c := &GitIgnoreCache{
		root:     absRoot,
		cache:    make(map[string]*ignore.GitIgnore),
		patterns: make(map[string][]string),
		visited:  make(map[string]struct{}),
	}
	c.tryLoadGitignore(absRoot)
	return c
}

// tryLoadGitignore attempts to load a .gitignore from dir if not already visited.
// Only adds to cache if a valid .gitignore exists.
func (c *GitIgnoreCache) tryLoadGitignore(dir string) {
	if _, seen := c.visited[dir]; seen {
		return
	}
	c.visited[dir] = struct{}{}

	gitignorePath := filepath.Join(dir, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		return
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}

	if len(lines) > 0 {
		c.patterns[dir] = lines
		c.cache[dir] = ignore.CompileIgnoreLines(lines...)
	}
}

// ShouldIgnore checks if a path should be ignored based on all applicable .gitignore files.
// Git evaluates rules from root to leaf, with later rules overriding earlier ones.
func (c *GitIgnoreCache) ShouldIgnore(absPath string) bool {
	if len(c.cache) == 0 {
		return false
	}

	// Collect directories from leaf to root
	var dirs []string
	for dir := filepath.Dir(absPath); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == c.root || dir == filepath.Dir(dir) {
			break
		}
	}

	// Combine all patterns from root to leaf into one gitignore.
	// This allows negation patterns in child .gitignore to override parent rules.
	var allPatterns []string
	for i := len(dirs) - 1; i >= 0; i-- {
		if patterns, ok := c.patterns[dirs[i]]; ok {
			allPatterns = append(allPatterns, patterns...)
		}
	}

	if len(allPatterns) == 0 {
		return false
	}

	combined := ignore.CompileIgnoreLines(allPatterns...)
	relPath, _ := filepath.Rel(c.root, absPath)
	return combined.MatchesPath(relPath)
}

// IgnoredDirs are directories to skip during scanning
var IgnoredDirs = map[string]bool{
	".git":           true,
	"node_modules":   true,
	"vendor":         true,
	"Pods":           true,
	"build":          true,
	"DerivedData":    true,
	".idea":          true,
	".vscode":        true,
	"__pycache__":    true,
	".DS_Store":      true,
	"venv":           true,
	".venv":          true,
	".env":           true,
	".pytest_cache":  true,
	".mypy_cache":    true,
	".ruff_cache":    true,
	".coverage":      true,
	"htmlcov":        true,
	".tox":           true,
	"dist":           true,
	".next":          true,
	".nuxt":          true,
	"target":         true,
	".gradle":        true,
	".cargo":         true,
	".grammar-build": true,
	"grammars":       true,
}

// matchesPattern does smart pattern matching:
// - ".png" or "png" → extension match (case-insensitive)
// - "Fonts" → directory/component match (contains /Fonts/ or ends with /Fonts)
// - "*test*" → glob pattern (only if contains * or ?)
func matchesPattern(relPath string, pattern string) bool {
	// If pattern contains glob characters, use glob matching
	if strings.ContainsAny(pattern, "*?") {
		// Match against filename
		if matched, _ := filepath.Match(pattern, filepath.Base(relPath)); matched {
			return true
		}
		// Match against full relative path
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
		return false
	}

	// Extension match: .png, .xcassets, png, xcassets
	ext := strings.TrimPrefix(pattern, ".")
	if strings.HasSuffix(strings.ToLower(relPath), "."+strings.ToLower(ext)) {
		return true
	}

	// Directory component match: Fonts → matches path/Fonts/file or path/Fonts
	if strings.Contains(relPath, "/"+pattern+"/") ||
		strings.HasSuffix(relPath, "/"+pattern) ||
		strings.HasPrefix(relPath, pattern+"/") ||
		relPath == pattern {
		return true
	}

	return false
}

// shouldIncludeFile checks if a file passes the only/exclude filters
func shouldIncludeFile(relPath string, ext string, only []string, exclude []string) bool {
	// If --only specified, file extension must be in the list
	if len(only) > 0 {
		extNoDot := strings.TrimPrefix(ext, ".")
		found := false
		for _, o := range only {
			o = strings.TrimPrefix(strings.TrimSpace(o), ".")
			if strings.EqualFold(extNoDot, o) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// If --exclude specified, check against each pattern
	for _, pattern := range exclude {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" && matchesPattern(relPath, pattern) {
			return false
		}
	}

	return true
}

// LoadGitignore loads .gitignore from root if it exists
// Deprecated: Use NewGitIgnoreCache for nested gitignore support
func LoadGitignore(root string) *ignore.GitIgnore {
	gitignorePath := filepath.Join(root, ".gitignore")

	if _, err := os.Stat(gitignorePath); err == nil {
		if gitignore, err := ignore.CompileIgnoreFile(gitignorePath); err == nil {
			return gitignore
		}
	}

	return nil
}

// ScanFiles walks the directory tree and returns all files.
// Supports nested .gitignore files via GitIgnoreCache.
// only: list of extensions to include (empty = all)
// exclude: list of patterns to exclude
func ScanFiles(root string, cache *GitIgnoreCache, only []string, exclude []string) ([]FileInfo, error) {
	var files []FileInfo
	absRoot, _ := filepath.Abs(root)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		name := info.Name()

		// Fast path: skip hardcoded ignored dirs/files
		if IgnoredDirs[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Compute absolute path once for gitignore checks and relative path calculation
		absPath, _ := filepath.Abs(path)

		// For directories: load any .gitignore, then check if dir itself should be skipped
		if info.IsDir() {
			if cache != nil {
				cache.tryLoadGitignore(absPath)
				if cache.ShouldIgnore(absPath) {
					return filepath.SkipDir
				}
			}
			// Check if directory matches any exclude pattern
			relPath, _ := filepath.Rel(absRoot, absPath)
			if relPath != "." {
				for _, pattern := range exclude {
					pattern = strings.TrimSpace(pattern)
					if pattern != "" && matchesPattern(relPath, pattern) {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}

		// For files: check gitignore
		if cache != nil && cache.ShouldIgnore(absPath) {
			return nil
		}

		relPath, _ := filepath.Rel(absRoot, absPath)
		ext := filepath.Ext(path)

		// Apply user filters (--only and --exclude)
		if !shouldIncludeFile(relPath, ext, only, exclude) {
			return nil
		}

		files = append(files, FileInfo{
			Path: relPath,
			Size: info.Size(),
			Ext:  ext,
		})

		return nil
	})

	return files, err
}

// ScanForDeps uses ast-grep for batched dependency analysis.
func ScanForDeps(root string) ([]FileAnalysis, error) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		return nil, err
	}
	defer scanner.Close()

	if !scanner.Available() {
		return nil, fmt.Errorf("ast-grep not found in PATH (tried 'sg' and 'ast-grep')")
	}

	return scanner.ScanDirectory(root)
}
