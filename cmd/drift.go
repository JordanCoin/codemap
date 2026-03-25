package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"codemap/config"
)

// DriftWarning represents a documentation that may be out of sync with code.
type DriftWarning struct {
	Subsystem     string `json:"subsystem"`
	CodePath      string `json:"code_path"`      // pattern or file that changed
	DocPath       string `json:"doc_path"`       // doc file that may need updating
	CommitsBehind int    `json:"commits_behind"` // how many code commits since doc was last updated
	Reason        string `json:"reason"`
}

// CheckDrift compares recent code changes against documentation freshness.
// It resolves doc paths from the routing.subsystems config first, then falls
// back to convention-based doc path guessing.
func CheckDrift(root string, cfg config.DriftConfig, routing config.RoutingConfig) []DriftWarning {
	if !cfg.Enabled || len(cfg.RequireDocsFor) == 0 {
		return nil
	}

	recentCommits := cfg.RecentCommits
	if recentCommits <= 0 {
		recentCommits = 10
	}

	// Build a map of subsystem ID -> configured doc paths
	subsystemDocs := make(map[string][]string)
	subsystemPaths := make(map[string][]string)
	for _, sub := range routing.Subsystems {
		if sub.ID != "" {
			if len(sub.Docs) > 0 {
				subsystemDocs[sub.ID] = sub.Docs
			}
			if len(sub.Paths) > 0 {
				subsystemPaths[sub.ID] = sub.Paths
			}
		}
	}

	var warnings []DriftWarning

	for _, subsystemID := range cfg.RequireDocsFor {
		docPaths := resolveDocPaths(subsystemID, subsystemDocs)
		codePaths := resolveCodePaths(subsystemID, subsystemPaths)
		w := checkSubsystemDrift(root, subsystemID, docPaths, codePaths, recentCommits)
		warnings = append(warnings, w...)
	}

	return warnings
}

// resolveDocPaths returns doc file paths for a subsystem. It checks the routing
// config first (explicit docs), then falls back to convention-based guessing.
func resolveDocPaths(subsystemID string, subsystemDocs map[string][]string) []string {
	// Use configured docs if available
	if docs, ok := subsystemDocs[subsystemID]; ok && len(docs) > 0 {
		return docs
	}

	// Fallback to convention: docs/<id>.md or docs/<ID>.md
	return []string{
		filepath.Join("docs", subsystemID+".md"),
		filepath.Join("docs", strings.ToUpper(subsystemID)+".md"),
	}
}

// checkSubsystemDrift checks if docs are stale for a subsystem by examining
// git log for code vs doc changes in the recent commit window.
func checkSubsystemDrift(root, subsystemID string, docPaths, codePaths []string, recentCommits int) []DriftWarning {
	var warnings []DriftWarning

	// Cache git log results to avoid redundant calls within this subsystem check
	cache := make(map[string]int)
	cachedCommitsAgo := func(path string) int {
		if v, ok := cache[path]; ok {
			return v
		}
		v := lastModifiedCommitsAgo(root, path, recentCommits)
		cache[path] = v
		return v
	}

	for _, docPath := range docPaths {
		// Check how many commits ago the doc was last modified.
		// -1 means the doc has no commits in the window — treat as very stale.
		docCommits := cachedCommitsAgo(docPath)

		for _, codePath := range codePaths {
			codeCommits := cachedCommitsAgo(codePath)
			if codeCommits < 0 {
				continue // code hasn't changed in window — no drift
			}

			if docCommits < 0 {
				// Doc has no commits in the window but code does — stale doc
				warnings = append(warnings, DriftWarning{
					Subsystem:     subsystemID,
					CodePath:      codePath,
					DocPath:       docPath,
					CommitsBehind: recentCommits,
					Reason:        fmt.Sprintf("%s changed recently but %s has not been updated in the last %d commits", codePath, docPath, recentCommits),
				})
			} else if codeCommits < docCommits {
				// Code changed more recently than docs
				warnings = append(warnings, DriftWarning{
					Subsystem:     subsystemID,
					CodePath:      codePath,
					DocPath:       docPath,
					CommitsBehind: docCommits - codeCommits,
					Reason:        fmt.Sprintf("%s changed %d commits after %s was last updated", codePath, docCommits-codeCommits, docPath),
				})
			}
		}
	}

	return warnings
}

// lastModifiedCommitsAgo returns how many commits ago a path was last modified.
// Returns -1 if the path has no commits in the window or doesn't exist.
func lastModifiedCommitsAgo(root, path string, window int) int {
	cmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", window), "--", path)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	lines := strings.TrimSpace(string(out))
	if lines == "" {
		return -1
	}

	// Count how many total commits there are, then find position of first commit touching this path
	allCmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", window))
	allCmd.Dir = root
	allOut, err := allCmd.Output()
	if err != nil {
		return -1
	}

	allLines := strings.Split(strings.TrimSpace(string(allOut)), "\n")
	firstPathCommit := strings.SplitN(lines, "\n", 2)[0]
	if firstPathCommit == "" {
		return -1
	}

	// Extract commit hash (first word)
	pathHash := strings.Fields(firstPathCommit)[0]

	for i, line := range allLines {
		if strings.HasPrefix(line, pathHash) {
			return i
		}
	}

	return -1
}

// resolveCodePaths returns code paths for a subsystem. Uses configured paths
// first, then falls back to convention-based guessing.
func resolveCodePaths(subsystemID string, subsystemPaths map[string][]string) []string {
	if paths, ok := subsystemPaths[subsystemID]; ok && len(paths) > 0 {
		// Strip glob suffixes for git log compatibility (watch/** -> watch/)
		var cleaned []string
		for _, p := range paths {
			p = strings.TrimSuffix(p, "**")
			p = strings.TrimSuffix(p, "*")
			if p != "" {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) > 0 {
			return cleaned
		}
	}
	return guessCodePaths(subsystemID)
}

// guessCodePaths returns likely code directory paths for a subsystem ID.
func guessCodePaths(subsystemID string) []string {
	candidates := []string{
		subsystemID + "/",
	}

	aliases := map[string][]string{
		"watching": {"watch/"},
		"scanning": {"scanner/"},
		"hooks":    {"cmd/hooks.go"},
		"handoffs": {"handoff/"},
		"mcp":      {"mcp/"},
		"render":   {"render/"},
		"config":   {"config/"},
	}
	if extra, ok := aliases[subsystemID]; ok {
		candidates = append(candidates, extra...)
	}

	return candidates
}
