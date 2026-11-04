package scanner

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/limits"

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
	combined map[string]*ignore.GitIgnore // abs dir path -> root-to-leaf matcher, reset when a file loads

	// repoExcludes holds core.excludesFile and .git/info/exclude, matched
	// relative to repoTop like git does.
	repoExcludes *ignore.GitIgnore
	repoTop      string
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
		combined: make(map[string]*ignore.GitIgnore),
	}
	c.repoTop, c.repoExcludes = loadRepoExcludes(absRoot)
	c.tryLoadGitignore(absRoot)
	return c
}

// loadRepoExcludes compiles the repository-wide ignore sources git applies
// beneath every .gitignore: the user's core.excludesFile, then the repo's
// .git/info/exclude (a linked worktree reads its common dir's). It returns
// the directory those patterns are relative to.
//
// ponytail: matched apart from .gitignore, so a .gitignore "!pattern" cannot
// re-include a path these exclude; merge into one ordered list if that bites.
func loadRepoExcludes(root string) (string, *ignore.GitIgnore) {
	top, commonDir := findGitDirs(root)
	if top == "" {
		return "", nil
	}
	lines := readIgnoreLines(globalExcludesFile(top))
	lines = append(lines, readIgnoreLines(filepath.Join(commonDir, "info", "exclude"))...)
	if len(lines) == 0 {
		return "", nil
	}
	return top, ignore.CompileIgnoreLines(lines...)
}

// findGitDirs walks up from dir to the nearest .git and returns the worktree
// top and the git common dir.
func findGitDirs(dir string) (string, string) {
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			if !info.IsDir() {
				data, err := os.ReadFile(dotGit)
				pointer, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
				if err != nil || !ok {
					return "", ""
				}
				gitDir = strings.TrimSpace(pointer)
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
			}
			commonDir := gitDir
			if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				commonDir = strings.TrimSpace(string(data))
				if !filepath.IsAbs(commonDir) {
					commonDir = filepath.Join(gitDir, commonDir)
				}
			}
			return dir, commonDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

var globalExcludes sync.Map // repo top -> excludes file path

// globalExcludesFile is core.excludesFile, else $XDG_CONFIG_HOME/git/ignore
// (default ~/.config/git/ignore). git is asked once per repo per process.
func globalExcludesFile(top string) string {
	if cached, ok := globalExcludes.Load(top); ok {
		return cached.(string)
	}
	path := ""
	cmd := exec.Command("git", "config", "--path", "--get", "core.excludesFile")
	cmd.Dir = top
	if out, err := cmd.Output(); err == nil {
		path = strings.TrimSpace(string(out))
	}
	if path == "" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			path = filepath.Join(xdg, "git", "ignore")
		} else if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".config", "git", "ignore")
		}
	}
	globalExcludes.Store(top, path)
	return path
}

// readIgnoreLines returns a gitignore-format file's patterns.
func readIgnoreLines(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
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
	return lines
}

// tryLoadGitignore attempts to load dir's .gitignore and .ignore if not
// already visited. .ignore (ripgrep's and ast-grep's) comes second, so its
// rules win, as they do in those tools. Only adds to cache if a pattern exists.
func (c *GitIgnoreCache) tryLoadGitignore(dir string) {
	if _, seen := c.visited[dir]; seen {
		return
	}
	c.visited[dir] = struct{}{}

	lines := append(readIgnoreLines(filepath.Join(dir, ".gitignore")), readIgnoreLines(filepath.Join(dir, ".ignore"))...)
	if len(lines) > 0 {
		c.patterns[dir] = rootRelativePatterns(c.root, dir, lines)
		c.cache[dir] = ignore.CompileIgnoreLines(lines...)
		clear(c.combined)
	}
}

// rootRelativePatterns rewrites a nested ignore file's patterns to match
// from root, since ShouldIgnore combines every level into one matcher: a
// pattern with a slash is anchored at its own directory, one without
// matches at any depth below it. "/x" in sub/.gitignore means sub/x, not x.
func rootRelativePatterns(root, dir string, lines []string) []string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return lines
	}
	prefix := filepath.ToSlash(rel) + "/"
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		negate := ""
		if strings.HasPrefix(line, "!") {
			negate, line = "!", line[1:]
		}
		if strings.Contains(strings.TrimSuffix(line, "/"), "/") {
			line = prefix + strings.TrimPrefix(line, "/")
		} else {
			line = prefix + "**/" + line
		}
		out = append(out, negate+line)
	}
	return out
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
	absPath = projectpath.CanonicalPath(absPath)
	if c.repoExcludes != nil {
		if rel, err := filepath.Rel(c.repoTop, absPath); err == nil && c.repoExcludes.MatchesPath(filepath.ToSlash(rel)) {
			return true
		}
	}
	if len(c.cache) == 0 {
		return false
	}

	relPath, _ := filepath.Rel(c.root, absPath)
	// go-gitignore matches against forward-slash paths on every OS.
	relPath = filepath.ToSlash(relPath)
	parent := filepath.Dir(absPath)
	if combined, ok := c.combined[parent]; ok {
		return combined != nil && combined.MatchesPath(relPath)
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

	var combined *ignore.GitIgnore
	if len(allPatterns) > 0 {
		combined = ignore.CompileIgnoreLines(allPatterns...)
	}
	c.combined[parent] = combined
	return combined != nil && combined.MatchesPath(relPath)
}

// IgnoredDirs are directories to skip during scanning
var IgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	// codemap's and docmap's own state and caches are never project source
	// (#196, #184). Hiding them only through a gitignore rule left them in
	// the tree of any repo whose rule was never written.
	".codemap": true,
	".docmap":  true,
	// Go's own toolchain ignores testdata, and fixture repositories inside it
	// are not the project's source: scanning them inflates file counts and
	// lets a fixture's language change the project's reported coverage.
	"testdata":       true,
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

// IsNestedGitRepo reports whether dir is the root of a git repository other
// than the one being scanned: it holds a `.git` entry that is either a
// directory (a plain clone or an old-style submodule checkout) or a regular
// file (a linked worktree's or modern submodule's `gitdir:` pointer). Such a
// directory is a repository boundary, not project source, so every walker
// skips it. Callers never apply this to the scan root itself, whose `.git`
// is expected.
func IsNestedGitRepo(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

// NestedGitRepos returns the root-relative paths (OS separators, sorted) of
// every git repository nested anywhere under root: linked worktrees (for
// example `.claude/worktrees/agent-x`), plain clones, and submodules, at any
// depth and under dotted directories too. IgnoredDirs and gitignored
// directories are not entered, matching ScanFiles, and a nested repository's
// own subtree is not entered either. The file walker, the ast-grep scan and
// the Go fallback all exclude exactly this set, so `codemap .`, `context`,
// `handoff` and `--deps` agree on what is project source.
func NestedGitRepos(root string) []string {
	absRoot := projectpath.CanonicalPath(root)
	cache := NewGitIgnoreCache(absRoot)
	var repos []string
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || !d.IsDir() || path == absRoot {
			return nil
		}
		if IgnoredDirs[d.Name()] {
			return filepath.SkipDir
		}
		if IsNestedGitRepo(path) {
			if rel, relErr := filepath.Rel(absRoot, path); relErr == nil {
				repos = append(repos, filepath.ToSlash(rel))
			}
			return filepath.SkipDir
		}
		cache.tryLoadGitignore(path)
		if cache.ShouldIgnore(path) {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(repos)
	return repos
}

// matchesPattern does smart pattern matching:
//   - ".png" or "png" → extension match (case-insensitive)
//   - "Fonts" → directory/component match (contains /Fonts/ or ends with /Fonts)
//   - "*test*" → glob pattern (only if contains * or ?)
//   - "/tool" → anchored to the project root, like .gitignore: matches the
//     root entry "tool" (file or directory) and nothing nested
//   - "tool/" → directories only, at any depth: a root binary named "tool"
//     next to cmd/tool/ is kept while the directory is excluded
//   - "/tool/" → the root directory "tool" only
//
// Anchors are read before globs; a pattern with * or ? is matched as a glob
// unchanged, so "/build/*.log" is not anchored.
func matchesPattern(relPath string, pattern string) bool {
	return matchesPatternPath(relPath, false, pattern)
}

// matchesPatternPath is matchesPattern with the one fact the file walker has
// and a bare relative path does not carry: whether relPath is a directory.
// The anchored and directory-only forms need it; a file named "tool" and a
// directory named "tool" must answer differently to "tool/".
func matchesPatternPath(relPath string, isDir bool, pattern string) bool {
	relPath = filepath.ToSlash(relPath)
	if anchored, dirOnly := strings.HasPrefix(pattern, "/"), strings.HasSuffix(pattern, "/"); (anchored || dirOnly) && !strings.ContainsAny(pattern, "*?") {
		name := strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		if name == "" {
			return false
		}
		// "/a/b/c/" form: every directory ends in "/", files do not, so a
		// directory-only pattern can only match a "/name/" segment.
		full := "/" + relPath
		if isDir {
			full += "/"
		}
		switch {
		case anchored && dirOnly:
			return strings.HasPrefix(full, "/"+name+"/")
		case anchored:
			return full == "/"+name || strings.HasPrefix(full, "/"+name+"/")
		default:
			return strings.Contains(full, "/"+name+"/")
		}
	}

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
	files, _, err := ScanFilesBounded(ctx, root, cache, Filters{Only: only, Exclude: exclude, MaxFiles: -1})
	return files, err
}

// ScanFilesBounded walks like ScanFiles but keeps at most the configured
// number of files (limits.ScanFileBound) and reports how many matched in
// total, so a caller can say "scanned N of M" instead of presenting the
// prefix as the repository. The walk continues past the bound to count,
// which is a stat per file and no allocation; a repo that is too large to
// graph is still cheap to measure.
func ScanFilesBounded(ctx context.Context, root string, cache *GitIgnoreCache, filters Filters) ([]FileInfo, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	only, exclude := filters.Only, filters.Exclude
	bound := limits.ScanFileBound(filters.MaxFiles)
	var files []FileInfo
	total := 0
	absRoot := projectpath.CanonicalPath(root)

	err := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}

		name := info.Name()

		// Fast path: skip hardcoded ignored dirs/files. Never applied to the
		// walk root itself: a user who runs `codemap testdata/` or scans from
		// inside vendor/ asked for exactly that tree, and skipping it here
		// returns Files: 0 with no explanation.
		if IgnoredDirs[name] && path != absRoot {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// A symlink can point outside root or back into it (a loop lists the
		// repo twice); ast-grep and every reader follow an explicit path.
		if info.Mode()&os.ModeSymlink != 0 && path != absRoot {
			return nil
		}

		// For directories: load any .gitignore, then check if dir itself should be skipped
		if info.IsDir() {
			// A nested repository (linked worktree, plain clone, submodule)
			// is a boundary, not project source, whether or not the parent
			// repo's .gitignore happens to cover it (issue #131).
			if path != absRoot && IsNestedGitRepo(path) {
				return filepath.SkipDir
			}
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
					if pattern != "" && matchesPatternPath(relPath, true, pattern) {
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

		total++
		if bound > 0 && total > bound {
			return nil
		}
		files = append(files, FileInfo{
			Path: filepath.ToSlash(relPath),
			Size: info.Size(),
			Ext:  ext,
		})

		return nil
	})
	if err == nil {
		err = ctx.Err()
	}

	return files, total, err
}

// scanBoundSource names the coverage source recorded when a dependency scan
// was refused because the repository is above the file bound.
const scanBoundSource = "scan-bound"

// refusedByBound reports whether this outcome is the bound refusal rather
// than a scan, so callers skip the passes that would walk the tree again.
func (o ScanOutcome) refusedByBound() bool {
	for _, source := range o.Sources {
		if source.Name == scanBoundSource {
			return true
		}
	}
	return false
}

// ScanBoundNote is the coverage line printed when a scan stopped at the file
// bound. scanned is the number kept, total the number that matched.
func ScanBoundNote(scanned, total, bound int) string {
	return fmt.Sprintf("bounded: scanned %d of %d files (limit %d; raise with --max-files or config max_files)", scanned, total, bound)
}

// Filters controls which files scanner operations include.
type Filters struct {
	Only    []string
	Exclude []string
	// MaxFiles bounds the scan: 0 = limits.DefaultMaxScanFiles, negative =
	// unbounded (see limits.ScanFileBound).
	MaxFiles int
}

// ConfiguredFilters returns the project's configured dependency filters.
func ConfiguredFilters(root string) Filters {
	cfg := config.Load(root)
	return Filters{Only: cfg.Only, Exclude: cfg.Exclude, MaxFiles: cfg.MaxFiles}
}

// ScanConfiguredFiles scans using the active setup root's project filters
// while honoring caller cancellation.
func ScanConfiguredFiles(ctx context.Context, root string, cache *GitIgnoreCache) ([]FileInfo, error) {
	return ScanConfiguredFilesWithFilters(ctx, root, cache, ConfiguredFilters(root))
}

// ScanConfiguredFilesWithFilters scans files using already resolved filters.
// The result honours the file bound; hooks and the watch daemon therefore
// degrade to a prefix on a repo above it instead of holding every file.
func ScanConfiguredFilesWithFilters(ctx context.Context, root string, cache *GitIgnoreCache, filters Filters) ([]FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, _, err := ScanFilesBounded(ctx, root, cache, filters)
	if err != nil {
		return nil, err
	}
	return files, nil
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
	if outcome.refusedByBound() {
		return outcome, nil
	}
	outcome, err = appendCUEOutcome(ctx, root, filters, outcome)
	if err != nil {
		return outcome, err
	}
	// ast-grep emits matches in worker-completion order. blast-radius caps the
	// deps section to the first N analyses, so without a fixed order the same
	// diff counted a different set of files on every run (#153).
	sort.Slice(outcome.Analyses, func(i, j int) bool { return outcome.Analyses[i].Path < outcome.Analyses[j].Path })
	return outcome, nil
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
