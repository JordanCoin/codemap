package handoff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/limits"
	"codemap/scanner"
	"codemap/watch"
)

func normalizeOptions(opts BuildOptions) BuildOptions {
	if opts.BaseRef == "" {
		opts.BaseRef = DefaultBaseRef
	}
	if opts.Since <= 0 {
		opts.Since = DefaultSince
	}
	if opts.MaxChanged <= 0 {
		opts.MaxChanged = 50
	}
	if opts.MaxRisk <= 0 {
		opts.MaxRisk = 10
	}
	if opts.MaxEvents <= 0 {
		opts.MaxEvents = 20
	}
	return opts
}

// Build creates a multi-agent handoff artifact from git + daemon state.
func Build(root string, opts BuildOptions) (*Artifact, error) {
	opts = normalizeOptions(opts)

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	state := opts.State
	if state == nil {
		state = watch.ReadState(absRoot)
	}

	branch, err := gitCurrentBranch(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read git branch: %w", err)
	}

	changedFiles, diffErr := collectChangedFiles(absRoot, opts.BaseRef)
	if diffErr != nil {
		return nil, diffErr
	}
	sort.Strings(changedFiles)

	recentEvents := summarizeEvents(state, opts.Since, opts.MaxEvents)
	if len(changedFiles) == 0 && len(recentEvents) > 0 {
		changedFiles = changedFromEvents(recentEvents)
		sort.Strings(changedFiles)
	}

	importers := dependencyImportersForHandoff(absRoot, state)
	riskFiles := summarizeRiskFiles(changedFiles, importers, opts.MaxRisk)
	changedFiles = capStrings(changedFiles, opts.MaxChanged)

	nextSteps, openQuestions := deriveGuidance(changedFiles, riskFiles, recentEvents, opts.BaseRef, state != nil, len(importers) > 0)

	return &Artifact{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now(),
		Root:          absRoot,
		Branch:        branch,
		BaseRef:       opts.BaseRef,
		ChangedFiles:  nonNilStrings(changedFiles),
		RiskFiles:     nonNilRiskFiles(riskFiles),
		RecentEvents:  nonNilEvents(recentEvents),
		NextSteps:     nonNilStrings(nextSteps),
		OpenQuestions: nonNilStrings(openQuestions),
	}, nil
}

func capStrings(values []string, max int) []string {
	if len(values) == 0 {
		return []string{}
	}
	if len(values) <= max {
		return values
	}
	return values[:max]
}

func collectChangedFiles(root, baseRef string) ([]string, error) {
	changed := make(map[string]struct{})

	branchLines, branchErr := runGitLines(root, "diff", "--name-only", baseRef+"...HEAD")
	for _, line := range branchLines {
		if !includeChangedPath(root, line) {
			continue
		}
		changed[line] = struct{}{}
	}

	workingLines, _ := runGitLines(root, "diff", "--name-only")
	for _, line := range workingLines {
		if !includeChangedPath(root, line) {
			continue
		}
		changed[line] = struct{}{}
	}

	stagedLines, _ := runGitLines(root, "diff", "--name-only", "--cached")
	for _, line := range stagedLines {
		if !includeChangedPath(root, line) {
			continue
		}
		changed[line] = struct{}{}
	}

	untrackedLines, _ := runGitLines(root, "ls-files", "--others", "--exclude-standard")
	for _, line := range untrackedLines {
		if !includeChangedPath(root, line) {
			continue
		}
		changed[line] = struct{}{}
	}

	if len(changed) == 0 && branchErr != nil {
		return nil, fmt.Errorf("failed to compute changed files: %w", branchErr)
	}

	result := make([]string, 0, len(changed))
	for path := range changed {
		result = append(result, path)
	}
	return result, nil
}

func includeChangedPath(root, path string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return false
	}

	// Ignore tool/build/vendor directories.
	parts := strings.Split(normalized, "/")
	for _, p := range parts {
		switch p {
		case ".git", ".codemap", "node_modules", "vendor", "dist", "build", "target", "__pycache__", ".next", ".nuxt":
			return false
		}
	}

	ext := strings.ToLower(filepath.Ext(normalized))
	switch ext {
	case ".exe", ".dll", ".bin", ".o", ".a", ".so", ".dylib", ".wasm", ".class", ".jar", ".zip", ".tar", ".gz", ".7z",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".tiff", ".mp3", ".wav", ".ogg", ".mp4", ".mov", ".avi",
		".log", ".out", ".pdf", ".ttf", ".otf", ".woff", ".woff2":
		return false
	}

	// Keep extensionless or uncommon files unless they appear binary.
	return !isLikelyBinary(root, normalized)
}

func isLikelyBinary(root, relPath string) bool {
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return false
	}

	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 2048)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func runGitLines(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	raw := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil, nil
	}

	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func gitCurrentBranch(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func summarizeRiskFiles(changed []string, importersByFile map[string][]string, maxRisk int) []RiskFile {
	if len(importersByFile) == 0 {
		return []RiskFile{}
	}

	risk := make([]RiskFile, 0, len(changed))
	for _, file := range changed {
		importers := len(importersByFile[file])
		if importers < 2 {
			continue
		}

		isHub := importers >= 3
		reason := fmt.Sprintf("imported by %d files", importers)
		if isHub {
			reason = fmt.Sprintf("hub file imported by %d files", importers)
		}
		risk = append(risk, RiskFile{
			Path:      file,
			Importers: importers,
			IsHub:     isHub,
			Reason:    reason,
		})
	}

	sort.Slice(risk, func(i, j int) bool {
		if risk[i].Importers != risk[j].Importers {
			return risk[i].Importers > risk[j].Importers
		}
		return risk[i].Path < risk[j].Path
	})

	if len(risk) > maxRisk {
		risk = risk[:maxRisk]
	}
	return risk
}

func summarizeEvents(state *watch.State, since time.Duration, maxEvents int) []EventSummary {
	if state == nil || len(state.RecentEvents) == 0 {
		return []EventSummary{}
	}

	cutoff := time.Now().Add(-since)
	result := make([]EventSummary, 0, len(state.RecentEvents))
	for _, e := range state.RecentEvents {
		if e.Time.Before(cutoff) {
			continue
		}
		result = append(result, EventSummary{
			Time:  e.Time,
			Op:    e.Op,
			Path:  e.Path,
			Delta: e.Delta,
			IsHub: e.IsHub,
		})
	}

	if len(result) > maxEvents {
		result = result[len(result)-maxEvents:]
	}
	return result
}

func changedFromEvents(events []EventSummary) []string {
	if len(events) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, e := range events {
		seen[e.Path] = struct{}{}
	}
	changed := make([]string, 0, len(seen))
	for path := range seen {
		changed = append(changed, path)
	}
	return changed
}

func deriveGuidance(changed []string, risk []RiskFile, events []EventSummary, baseRef string, hasState bool, hasDependencyContext bool) ([]string, []string) {
	nextSteps := make([]string, 0, 2)
	openQuestions := make([]string, 0, 3)

	if len(changed) == 0 {
		openQuestions = append(openQuestions, fmt.Sprintf("No changed files detected vs %s. Confirm the base ref and branch state.", baseRef))
	}

	if len(risk) > 0 {
		nextSteps = append(nextSteps, "Review downstream dependents for high-impact files before merge.")
	}

	if !hasState {
		openQuestions = append(openQuestions, "Live watch state was unavailable; timeline may be incomplete.")
	}
	if !hasDependencyContext {
		openQuestions = append(openQuestions, "Dependency graph context was unavailable; risk files may be incomplete.")
	}
	if len(events) == 0 && hasState {
		openQuestions = append(openQuestions, "No recent timeline events matched the lookback window.")
	}

	return nextSteps, openQuestions
}

func dependencyImportersForHandoff(root string, state *watch.State) map[string][]string {
	if state != nil && len(state.Importers) > 0 {
		return state.Importers
	}

	// Reuse daemon file count when available to avoid an extra scan.
	if state != nil && state.FileCount > limits.LargeRepoFileCount {
		return nil
	}

	fileCount := 0
	if state != nil {
		fileCount = state.FileCount
	}
	if fileCount == 0 {
		gitCache := scanner.NewGitIgnoreCache(root)
		files, err := scanner.ScanFiles(root, gitCache, nil, nil)
		if err != nil {
			return nil
		}
		fileCount = len(files)
	}
	if fileCount > limits.LargeRepoFileCount {
		return nil
	}

	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil
	}
	return fg.Importers
}

func nonNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func nonNilRiskFiles(items []RiskFile) []RiskFile {
	if items == nil {
		return []RiskFile{}
	}
	return items
}

func nonNilEvents(items []EventSummary) []EventSummary {
	if items == nil {
		return []EventSummary{}
	}
	return items
}
