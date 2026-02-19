package handoff

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

	riskFiles := summarizeRiskFiles(changedFiles, state, opts.MaxRisk)
	changedFiles = capStrings(changedFiles, opts.MaxChanged)

	nextSteps, openQuestions := deriveGuidance(changedFiles, riskFiles, recentEvents, opts.BaseRef, state != nil)

	return &Artifact{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now(),
		Root:          absRoot,
		Branch:        branch,
		BaseRef:       opts.BaseRef,
		ChangedFiles:  changedFiles,
		RiskFiles:     riskFiles,
		RecentEvents:  recentEvents,
		NextSteps:     nextSteps,
		OpenQuestions: openQuestions,
	}, nil
}

func capStrings(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	return values[:max]
}

func collectChangedFiles(root, baseRef string) ([]string, error) {
	changed := make(map[string]struct{})

	branchLines, branchErr := runGitLines(root, "diff", "--name-only", baseRef+"...HEAD")
	for _, line := range branchLines {
		changed[line] = struct{}{}
	}

	workingLines, _ := runGitLines(root, "diff", "--name-only")
	for _, line := range workingLines {
		changed[line] = struct{}{}
	}

	stagedLines, _ := runGitLines(root, "diff", "--name-only", "--cached")
	for _, line := range stagedLines {
		changed[line] = struct{}{}
	}

	untrackedLines, _ := runGitLines(root, "ls-files", "--others", "--exclude-standard")
	for _, line := range untrackedLines {
		if !includeUntrackedPath(line) {
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

func includeUntrackedPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java", ".swift", ".kt", ".c", ".cpp", ".h",
		".md", ".json", ".yml", ".yaml", ".toml", ".sh", ".bash", ".zsh", ".css", ".html", ".sql", ".graphql":
		return true
	}
	return false
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

func summarizeRiskFiles(changed []string, state *watch.State, maxRisk int) []RiskFile {
	if state == nil || len(state.Importers) == 0 {
		return nil
	}

	risk := make([]RiskFile, 0, len(changed))
	for _, file := range changed {
		importers := len(state.Importers[file])
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
		return nil
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

func deriveGuidance(changed []string, risk []RiskFile, events []EventSummary, baseRef string, hasState bool) ([]string, []string) {
	nextSteps := make([]string, 0, 3)
	openQuestions := make([]string, 0, 2)

	if len(changed) == 0 {
		openQuestions = append(openQuestions, fmt.Sprintf("No changed files detected vs %s. Confirm the base ref and branch state.", baseRef))
	} else {
		nextSteps = append(nextSteps, "Run tests covering the changed files before handoff.")
	}

	if len(risk) > 0 {
		nextSteps = append(nextSteps, "Review downstream dependents for high-impact files before merge.")
	} else if len(changed) > 0 {
		nextSteps = append(nextSteps, "Quickly sanity-check imports and callers for the most edited files.")
	}

	if len(events) > 0 {
		nextSteps = append(nextSteps, "Start from the latest timeline entries to preserve work ordering and intent.")
	}

	if !hasState {
		openQuestions = append(openQuestions, "Live watch state was unavailable; dependency risk may be incomplete.")
	}

	return nextSteps, openQuestions
}
