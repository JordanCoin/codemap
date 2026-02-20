package handoff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

type changedEntry struct {
	Path   string
	Status string
}

var changedStatusRank = map[string]int{
	"branch":    1,
	"modified":  2,
	"staged":    3,
	"untracked": 4,
	"event":     5,
}

func normalizeOptions(opts BuildOptions, fileCount int) BuildOptions {
	if opts.BaseRef == "" {
		opts.BaseRef = DefaultBaseRef
	}
	if opts.Since <= 0 {
		opts.Since = DefaultSince
	}

	budget := limits.HandoffBudgetForRepo(fileCount)
	if opts.MaxChanged <= 0 {
		opts.MaxChanged = budget.MaxChanged
	}
	if opts.MaxRisk <= 0 {
		opts.MaxRisk = budget.MaxRisk
	}
	if opts.MaxEvents <= 0 {
		opts.MaxEvents = budget.MaxEvents
	}
	if opts.MaxHubs <= 0 {
		opts.MaxHubs = max(budget.MaxRisk, 8)
	}
	return opts
}

// Build creates a multi-agent handoff artifact from git + daemon state.
func Build(root string, opts BuildOptions) (*Artifact, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	state := opts.State
	if state == nil {
		state = watch.ReadState(absRoot)
	}

	fileCount := resolveRepoFileCount(absRoot, state)
	opts = normalizeOptions(opts, fileCount)

	branch, err := gitCurrentBranch(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read git branch: %w", err)
	}

	entries, diffErr := collectChangedEntries(absRoot, opts.BaseRef)
	if diffErr != nil {
		return nil, diffErr
	}
	changedAll := entryPaths(entries)

	recentEvents := summarizeEvents(state, opts.Since, opts.MaxEvents)
	if len(changedAll) == 0 && len(recentEvents) > 0 {
		changedAll = changedFromEvents(recentEvents)
		sort.Strings(changedAll)
		entries = make([]changedEntry, 0, len(changedAll))
		for _, path := range changedAll {
			entries = append(entries, changedEntry{Path: path, Status: "event"})
		}
	}

	importers := dependencyImportersForHandoff(absRoot, state, fileCount)
	riskFiles := summarizeRiskFiles(changedAll, importers, opts.MaxRisk)
	selectedPaths := prioritizeChangedPaths(changedAll, riskFiles, opts.MaxChanged)
	entries = selectEntries(entries, selectedPaths)

	changedStubs := buildFileStubs(absRoot, entries)
	hubs := summarizeHubs(importers, opts.MaxHubs)

	nextSteps, openQuestions := deriveGuidance(selectedPaths, riskFiles, recentEvents, opts.BaseRef, state != nil, len(importers) > 0)

	prefix := PrefixSnapshot{
		FileCount: fileCount,
		Hubs:      nonNilHubs(hubs),
	}
	delta := DeltaSnapshot{
		Changed:       nonNilStubs(changedStubs),
		RiskFiles:     nonNilRiskFiles(riskFiles),
		RecentEvents:  nonNilEvents(recentEvents),
		NextSteps:     nonNilStrings(nextSteps),
		OpenQuestions: nonNilStrings(openQuestions),
	}

	prefixHash, prefixBytes, err := hashCanonical(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to hash prefix snapshot: %w", err)
	}
	deltaHash, deltaBytes, err := hashCanonical(delta)
	if err != nil {
		return nil, fmt.Errorf("failed to hash delta snapshot: %w", err)
	}
	combinedHash := hashFromStrings(prefixHash, deltaHash)

	previous := opts.Previous
	if previous == nil {
		previous, _ = ReadLatest(absRoot)
	}
	metrics := buildCacheMetrics(previous, prefixHash, deltaHash, prefixBytes, deltaBytes)
	generatedAt := time.Now()
	if previous != nil && previous.PrefixHash == prefixHash && previous.DeltaHash == deltaHash && !previous.GeneratedAt.IsZero() {
		// Preserve timestamp across identical artifacts to keep output deterministic.
		generatedAt = previous.GeneratedAt
	}

	return &Artifact{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   generatedAt,
		Root:          absRoot,
		Branch:        branch,
		BaseRef:       opts.BaseRef,
		Prefix:        prefix,
		Delta:         delta,
		PrefixHash:    prefixHash,
		DeltaHash:     deltaHash,
		CombinedHash:  combinedHash,
		Metrics:       metrics,

		// Legacy top-level mirrors.
		ChangedFiles:  stubPaths(changedStubs),
		RiskFiles:     nonNilRiskFiles(riskFiles),
		RecentEvents:  nonNilEvents(recentEvents),
		NextSteps:     nonNilStrings(nextSteps),
		OpenQuestions: nonNilStrings(openQuestions),
	}, nil
}

func collectChangedEntries(root, baseRef string) ([]changedEntry, error) {
	changed := make(map[string]changedEntry)

	branchLines, branchErr := runGitLines(root, "diff", "--name-only", baseRef+"...HEAD")
	for _, line := range branchLines {
		addChangedEntry(changed, root, line, "branch")
	}

	workingLines, _ := runGitLines(root, "diff", "--name-only")
	for _, line := range workingLines {
		addChangedEntry(changed, root, line, "modified")
	}

	stagedLines, _ := runGitLines(root, "diff", "--name-only", "--cached")
	for _, line := range stagedLines {
		addChangedEntry(changed, root, line, "staged")
	}

	untrackedLines, _ := runGitLines(root, "ls-files", "--others", "--exclude-standard")
	for _, line := range untrackedLines {
		addChangedEntry(changed, root, line, "untracked")
	}

	if len(changed) == 0 && branchErr != nil {
		return nil, fmt.Errorf("failed to compute changed files: %w", branchErr)
	}

	result := make([]changedEntry, 0, len(changed))
	for _, entry := range changed {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func addChangedEntry(changed map[string]changedEntry, root, path, status string) {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" || !includeChangedPath(root, normalized) {
		return
	}

	previous, ok := changed[normalized]
	if !ok || changedStatusRank[status] > changedStatusRank[previous.Status] {
		changed[normalized] = changedEntry{Path: normalized, Status: status}
	}
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

func buildFileStubs(root string, changed []changedEntry) []FileStub {
	if len(changed) == 0 {
		return []FileStub{}
	}

	stubs := make([]FileStub, 0, len(changed))
	for _, entry := range changed {
		stub := FileStub{
			Path:   entry.Path,
			Status: entry.Status,
		}

		absPath := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Stat(absPath)
		if err == nil && !info.IsDir() {
			stub.Size = info.Size()
			stub.Hash = fileSHA256(absPath)
		}
		stubs = append(stubs, stub)
	}
	return stubs
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func summarizeHubs(importersByFile map[string][]string, maxHubs int) []HubSummary {
	if len(importersByFile) == 0 {
		return []HubSummary{}
	}

	hubs := make([]HubSummary, 0, len(importersByFile))
	for path, importers := range importersByFile {
		if len(importers) < 3 {
			continue
		}
		hubs = append(hubs, HubSummary{
			Path:      path,
			Importers: len(importers),
		})
	}

	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].Importers != hubs[j].Importers {
			return hubs[i].Importers > hubs[j].Importers
		}
		return hubs[i].Path < hubs[j].Path
	})

	if maxHubs > 0 && len(hubs) > maxHubs {
		hubs = hubs[:maxHubs]
	}
	return hubs
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

	sort.Slice(result, func(i, j int) bool {
		if !result[i].Time.Equal(result[j].Time) {
			return result[i].Time.Before(result[j].Time)
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Op < result[j].Op
	})

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
	sort.Strings(changed)
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
	if len(changed) > 0 {
		nextSteps = append(nextSteps, "Run tests covering changed files before handoff.")
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

func prioritizeChangedPaths(changed []string, risk []RiskFile, maxChanged int) []string {
	if len(changed) <= maxChanged {
		return nonNilStrings(changed)
	}

	available := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		available[path] = struct{}{}
	}

	out := make([]string, 0, maxChanged)
	seen := make(map[string]struct{}, maxChanged)
	for _, r := range risk {
		if _, ok := available[r.Path]; !ok {
			continue
		}
		if _, ok := seen[r.Path]; ok {
			continue
		}
		out = append(out, r.Path)
		seen[r.Path] = struct{}{}
		if len(out) >= maxChanged {
			return out
		}
	}

	for _, path := range changed {
		if _, ok := seen[path]; ok {
			continue
		}
		out = append(out, path)
		if len(out) >= maxChanged {
			break
		}
	}
	return out
}

func selectEntries(entries []changedEntry, selectedPaths []string) []changedEntry {
	if len(selectedPaths) == 0 {
		return []changedEntry{}
	}
	byPath := make(map[string]changedEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}

	selected := make([]changedEntry, 0, len(selectedPaths))
	for _, path := range selectedPaths {
		if entry, ok := byPath[path]; ok {
			selected = append(selected, entry)
		} else {
			selected = append(selected, changedEntry{Path: path, Status: "event"})
		}
	}
	return selected
}

func entryPaths(entries []changedEntry) []string {
	if len(entries) == 0 {
		return []string{}
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func stubPaths(stubs []FileStub) []string {
	if len(stubs) == 0 {
		return []string{}
	}
	paths := make([]string, 0, len(stubs))
	for _, stub := range stubs {
		paths = append(paths, stub.Path)
	}
	return paths
}

func hashCanonical(v any) (string, int, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), len(data), nil
}

func hashFromStrings(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{':'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func buildCacheMetrics(previous *Artifact, prefixHash, deltaHash string, prefixBytes, deltaBytes int) CacheMetrics {
	totalBytes := prefixBytes + deltaBytes
	metrics := CacheMetrics{
		PrefixBytes: prefixBytes,
		DeltaBytes:  deltaBytes,
		TotalBytes:  totalBytes,
	}
	if previous == nil {
		return metrics
	}

	metrics.PreviousCombinedHash = previous.CombinedHash
	if previous.PrefixHash == prefixHash && prefixHash != "" {
		metrics.PrefixReused = true
		metrics.UnchangedBytes += prefixBytes
	}
	if previous.DeltaHash == deltaHash && deltaHash != "" {
		metrics.DeltaReused = true
		metrics.UnchangedBytes += deltaBytes
	}
	if totalBytes > 0 {
		metrics.ReuseRatio = float64(metrics.UnchangedBytes) / float64(totalBytes)
	}
	return metrics
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

func resolveRepoFileCount(root string, state *watch.State) int {
	if state != nil && state.FileCount > 0 {
		return state.FileCount
	}

	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(root, gitCache, nil, nil)
	if err != nil {
		return 0
	}
	return len(files)
}

func dependencyImportersForHandoff(root string, state *watch.State, fileCount int) map[string][]string {
	if state != nil && len(state.Importers) > 0 {
		return state.Importers
	}

	// Reuse daemon file count when available to avoid an extra scan.
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

func nonNilStubs(items []FileStub) []FileStub {
	if items == nil {
		return []FileStub{}
	}
	return items
}

func nonNilHubs(items []HubSummary) []HubSummary {
	if items == nil {
		return []HubSummary{}
	}
	return items
}
