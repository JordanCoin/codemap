package scanner

import (
	"os"
	"path/filepath"

	ignore "github.com/sabhiram/go-gitignore"
)

// GitIgnoreCache manages nested .gitignore files throughout a project.
// It lazily loads gitignore files as directories are visited and efficiently
// checks paths against all applicable rules.
type GitIgnoreCache struct {
	root    string
	cache   map[string]*ignore.GitIgnore // abs dir path -> compiled gitignore (only dirs WITH gitignores)
	visited map[string]struct{}          // tracks visited dirs to avoid re-checking for .gitignore
}

// NewGitIgnoreCache creates a cache that supports nested .gitignore files.
// root should be the project root directory.
func NewGitIgnoreCache(root string) *GitIgnoreCache {
	absRoot, _ := filepath.Abs(root)
	c := &GitIgnoreCache{
		root:    absRoot,
		cache:   make(map[string]*ignore.GitIgnore),
		visited: make(map[string]struct{}),
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
	if gi, err := ignore.CompileIgnoreFile(gitignorePath); err == nil {
		c.cache[dir] = gi
	}
}

// ShouldIgnore checks if a path should be ignored based on all applicable .gitignore files.
// absPath must be an absolute path within the cache's root.
func (c *GitIgnoreCache) ShouldIgnore(absPath string) bool {
	// Fast path: no gitignores loaded
	if len(c.cache) == 0 {
		return false
	}

	// Walk up from file's parent to root, checking each cached gitignore
	dir := filepath.Dir(absPath)
	for {
		if gi, ok := c.cache[dir]; ok {
			relToGitignore, _ := filepath.Rel(dir, absPath)
			if gi.MatchesPath(relToGitignore) {
				return true
			}
		}

		// Stop at root
		if dir == c.root {
			break
		}

		// Move up one level
		parent := filepath.Dir(dir)
		if parent == dir {
			// Filesystem root, shouldn't happen but guard against infinite loop
			break
		}
		dir = parent
	}

	return false
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
func ScanFiles(root string, cache *GitIgnoreCache) ([]FileInfo, error) {
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
			return nil
		}

		// For files: check gitignore
		if cache != nil && cache.ShouldIgnore(absPath) {
			return nil
		}

		relPath, _ := filepath.Rel(absRoot, absPath)
		files = append(files, FileInfo{
			Path: relPath,
			Size: info.Size(),
			Ext:  filepath.Ext(path),
		})

		return nil
	})

	return files, err
}

// ScanForDeps walks the directory tree and analyzes files for dependencies.
// Supports nested .gitignore files via GitIgnoreCache.
func ScanForDeps(root string, cache *GitIgnoreCache, loader *GrammarLoader) ([]FileAnalysis, error) {
	var analyses []FileAnalysis
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
			return nil
		}

		// For files: check gitignore
		if cache != nil && cache.ShouldIgnore(absPath) {
			return nil
		}

		// Only analyze supported languages
		if DetectLanguage(path) == "" {
			return nil
		}

		// Analyze file
		analysis, err := loader.AnalyzeFile(path)
		if err != nil || analysis == nil {
			return nil
		}

		// Use relative path in output
		relPath, _ := filepath.Rel(absRoot, absPath)
		analysis.Path = relPath
		analyses = append(analyses, *analysis)

		return nil
	})

	return analyses, err
}
