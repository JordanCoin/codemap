package scanner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"codemap/config"
	"codemap/internal/projectpath"

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
	absRoot := projectpath.CanonicalPath(root)
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

// EnsureDir loads a .gitignore for dir if present.
// Safe to call repeatedly; directories are memoized.
func (c *GitIgnoreCache) EnsureDir(dir string) {
	if c == nil || dir == "" {
		return
	}
	c.tryLoadGitignore(projectpath.CanonicalPath(dir))
}

// ShouldIgnore checks if a path should be ignored based on all applicable .gitignore files.
// Git evaluates rules from root to leaf, with later rules overriding earlier ones.
func (c *GitIgnoreCache) ShouldIgnore(absPath string) bool {
	if len(c.cache) == 0 {
		return false
	}
	absPath = projectpath.CanonicalPath(absPath)

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
	".dart_tool":     true,
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

// MatchesFilters reports whether a file passes project only/exclude filters.
func MatchesFilters(relPath string, ext string, only []string, exclude []string) bool {
	return shouldIncludeFile(relPath, ext, only, exclude)
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

// ScanFiles walks the directory tree and returns all files while honoring
// caller cancellation. Supports nested .gitignore files via GitIgnoreCache.
// only: list of extensions to include (empty = all)
// exclude: list of patterns to exclude
func ScanFiles(ctx context.Context, root string, cache *GitIgnoreCache, only []string, exclude []string) ([]FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var files []FileInfo
	absRoot := projectpath.CanonicalPath(root)

	err := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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

		// For directories: load any .gitignore, then check if dir itself should be skipped
		if info.IsDir() {
			if cache != nil {
				cache.tryLoadGitignore(path)
				if cache.ShouldIgnore(path) {
					return filepath.SkipDir
				}
			}
			// Check if directory matches any exclude pattern
			relPath, _ := filepath.Rel(absRoot, path)
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
		if cache != nil && cache.ShouldIgnore(path) {
			return nil
		}

		relPath, _ := filepath.Rel(absRoot, path)
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
	if err == nil {
		err = ctx.Err()
	}

	return files, err
}

// Filters controls which files scanner operations include.
type Filters struct {
	Only    []string
	Exclude []string
}

// ConfiguredFilters returns the project's configured dependency filters.
func ConfiguredFilters(root string) Filters {
	cfg := config.Load(root)
	return Filters{Only: cfg.Only, Exclude: cfg.Exclude}
}

// ScanConfiguredFiles scans using the active setup root's project filters
// while honoring caller cancellation.
func ScanConfiguredFiles(ctx context.Context, root string, cache *GitIgnoreCache) ([]FileInfo, error) {
	cfg := config.Load(root)
	return ScanConfiguredFilesWithFilters(ctx, root, cache, Filters{Only: cfg.Only, Exclude: cfg.Exclude})
}

// ScanConfiguredFilesWithFilters scans files using already resolved filters.
func ScanConfiguredFilesWithFilters(ctx context.Context, root string, cache *GitIgnoreCache, filters Filters) ([]FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := ScanFiles(ctx, root, cache, filters.Only, filters.Exclude)
	if err != nil {
		return nil, err
	}
	configured := files[:0]
	for _, file := range files {
		if path := filepath.ToSlash(file.Path); path != ".codemap" && !strings.HasPrefix(path, ".codemap/") {
			configured = append(configured, file)
		}
	}
	return configured, nil
}

func filterAnalysesContext(ctx context.Context, analyses []FileAnalysis, filters Filters) ([]FileAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(filters.Only) == 0 && len(filters.Exclude) == 0 {
		return analyses, ctx.Err()
	}

	filtered := make([]FileAnalysis, 0, len(analyses))
	for _, analysis := range analyses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.ToSlash(analysis.Path)
		if MatchesFilters(path, filepath.Ext(path), filters.Only, filters.Exclude) {
			filtered = append(filtered, analysis)
		}
	}
	return filtered, ctx.Err()
}

func filterConfiguredAnalyses(root string, analyses []FileAnalysis) []FileAnalysis {
	cfg := config.Load(root)
	filtered, _ := filterAnalysesContext(context.Background(), analyses, Filters{Only: cfg.Only, Exclude: cfg.Exclude})
	return filtered
}

// ScanForDeps runs dependency analysis with explicit filters and cancellation,
// falling back to the Go parser when ast-grep fails.
func ScanForDeps(ctx context.Context, root string, filters Filters) (ScanOutcome, error) {
	outcome, _, err := scanForGraphOutcomeWithFilters(ctx, root, filters, func(r string) (ScanOutcome, error) {
		return scanForDepsPrimaryOutcome(ctx, r)
	}, loadCargoFallbackMetadata, false)
	if err != nil {
		return outcome, err
	}
	return appendCUEOutcome(ctx, root, filters, outcome)
}

func appendCUEOutcome(ctx context.Context, root string, filters Filters, outcome ScanOutcome) (ScanOutcome, error) {
	var cueOutcome ScanOutcome
	var err error
	if outcome.hasFileInventory {
		cueOutcome, err = scanCUEFilesFromFiles(ctx, root, outcome.files)
	} else {
		cueOutcome, err = scanCUEFiles(ctx, root, filters)
	}
	if err != nil {
		return ScanOutcome{}, err
	}
	outcome.Analyses = append(outcome.Analyses, cueOutcome.Analyses...)
	outcome.Sources = append(outcome.Sources, cueOutcome.Sources...)
	if cueOutcome.hasFileInventory {
		outcome.files = cueOutcome.files
		outcome.hasFileInventory = true
	}
	return outcome, nil
}

func scanForDepsPrimaryOutcome(ctx context.Context, root string) (ScanOutcome, error) {
	if err := ctx.Err(); err != nil {
		return ScanOutcome{}, err
	}
	astScanner, err := NewAstGrepScanner()
	if err != nil {
		return ScanOutcome{}, err
	}
	defer astScanner.Close()
	return astScanner.ScanDirectory(ctx, root)
}
