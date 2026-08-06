package scanner

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const gitContextWaitDelay = 100 * time.Millisecond

// DiffInfo holds all diff-related data for changed files
type DiffInfo struct {
	Changed   map[string]bool     // all changed files (modified + untracked)
	Untracked map[string]bool     // new/untracked files only
	Stats     map[string]DiffStat // +/- line counts
}

// GitDiffInfo returns comprehensive diff information for the repo while
// honoring cancellation of Git subprocesses.
func GitDiffInfo(ctx context.Context, root, ref string) (*DiffInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info := &DiffInfo{
		Changed:   make(map[string]bool),
		Untracked: make(map[string]bool),
		Stats:     make(map[string]DiffStat),
	}

	// Get modified files vs ref with stats
	cmd := exec.CommandContext(ctx, "git", "diff", "--numstat", ref)
	cmd.WaitDelay = gitContextWaitDelay
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			var added, removed int
			if parts[0] != "-" {
				fmt.Sscanf(parts[0], "%d", &added)
			}
			if parts[1] != "-" {
				fmt.Sscanf(parts[1], "%d", &removed)
			}
			filename := strings.Join(parts[2:], " ")
			info.Changed[filename] = true
			info.Stats[filename] = DiffStat{Added: added, Removed: removed}
		}
	}

	// Get untracked files (new files)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd2 := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard")
	cmd2.WaitDelay = gitContextWaitDelay
	cmd2.Dir = root
	output2, err := cmd2.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		output2 = nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output2)), "\n") {
		if line != "" {
			info.Changed[line] = true
			info.Untracked[line] = true
		}
	}

	return info, nil
}

// DiffStat returns +/- line counts for changed files
type DiffStat struct {
	Added   int
	Removed int
}

func GitDiffStats(ctx context.Context, root, ref string) (map[string]DiffStat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", "diff", "--numstat", ref)
	cmd.WaitDelay = gitContextWaitDelay
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	stats := make(map[string]DiffStat)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			var added, removed int
			if parts[0] != "-" {
				fmt.Sscanf(parts[0], "%d", &added)
			}
			if parts[1] != "-" {
				fmt.Sscanf(parts[1], "%d", &removed)
			}
			// parts[2] is the filename, but could have spaces - rejoin
			filename := strings.Join(parts[2:], " ")
			stats[filename] = DiffStat{Added: added, Removed: removed}
		}
	}
	return stats, nil
}

// FilterToChanged filters a slice of FileInfo to only include changed files
func FilterToChanged(files []FileInfo, changed map[string]bool) []FileInfo {
	var result []FileInfo
	for _, f := range files {
		if changed[f.Path] || changed[filepath.ToSlash(f.Path)] {
			result = append(result, f)
		}
	}
	return result
}

// FilterToChangedWithInfo filters and annotates files with diff info
func FilterToChangedWithInfo(files []FileInfo, info *DiffInfo) []FileInfo {
	var result []FileInfo
	for _, f := range files {
		path := f.Path
		slashPath := filepath.ToSlash(f.Path)
		if info.Changed[path] || info.Changed[slashPath] {
			// Annotate with diff info
			f.IsNew = info.Untracked[path] || info.Untracked[slashPath]
			if stat, ok := info.Stats[path]; ok {
				f.Added = stat.Added
				f.Removed = stat.Removed
			} else if stat, ok := info.Stats[slashPath]; ok {
				f.Added = stat.Added
				f.Removed = stat.Removed
			}
			result = append(result, f)
		}
	}
	return result
}

// FilterAnalysisToChanged filters FileAnalysis slice to only changed files
func FilterAnalysisToChanged(files []FileAnalysis, changed map[string]bool) []FileAnalysis {
	var result []FileAnalysis
	for _, f := range files {
		if changed[f.Path] || changed[filepath.ToSlash(f.Path)] {
			result = append(result, f)
		}
	}
	return result
}

// ImpactInfo describes which changed files are used by other files
type ImpactInfo struct {
	File   string // the file that changed
	UsedBy int    // number of other files that import/use this file
}

// AnalyzeImpact analyzes which changed files are imported by other files,
// honoring caller cancellation. Uses ast-grep to extract actual imports.
func AnalyzeImpact(ctx context.Context, root string, changedFiles []FileInfo) ([]ImpactInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(changedFiles) == 0 {
		return nil, nil
	}

	// Scan all files to get their imports using ast-grep
	outcome, err := ScanForDeps(ctx, root, ConfiguredFilters(root))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, nil
	}
	return AnalyzeImpactFromAnalyses(changedFiles, outcome.Analyses), ctx.Err()
}

// AnalyzeImpactFromAnalyses computes impact using a pre-computed ast-grep scan,
// so callers that already hold the analyses can avoid a redundant full-repo
// ScanForDeps. AnalyzeImpact is the convenience wrapper that performs the scan.
func AnalyzeImpactFromAnalyses(changedFiles []FileInfo, analyses []FileAnalysis) []ImpactInfo {
	if len(changedFiles) == 0 {
		return nil
	}

	// Build set of changed file base names and directories
	changedBases := make(map[string]string) // base name -> full path
	changedDirs := make(map[string]string)  // dir name -> representative file
	for _, f := range changedFiles {
		base := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
		changedBases[base] = f.Path

		// Also track directories for Go-style package imports
		dir := filepath.Dir(f.Path)
		if dir != "." && dir != "" {
			dirBase := filepath.Base(dir)
			if _, exists := changedDirs[dirBase]; !exists {
				changedDirs[dirBase] = f.Path
			}
		}
	}

	usageCounts := make(map[string]int)
	for _, analysis := range analyses {
		// Check each import to see if it references a changed file
		for _, imp := range analysis.Imports {
			// Extract the last component of the import path
			impBase := filepath.Base(imp)
			impBase = strings.TrimSuffix(impBase, filepath.Ext(impBase))

			// Check if this import matches a changed file (by filename)
			if changedPath, ok := changedBases[impBase]; ok {
				if analysis.Path != changedPath {
					usageCounts[changedPath]++
				}
			}

			// Also check if import matches a changed directory (Go packages)
			if changedPath, ok := changedDirs[impBase]; ok {
				changedDir := filepath.Dir(changedPath)
				if filepath.Dir(analysis.Path) != changedDir {
					usageCounts[changedDir+"/"]++
				}
			}
		}
	}

	// Build impact info
	var impacts []ImpactInfo
	for file, count := range usageCounts {
		if count > 0 {
			impacts = append(impacts, ImpactInfo{
				File:   filepath.Base(file),
				UsedBy: count,
			})
		}
	}

	// Sort by usage count descending
	sort.Slice(impacts, func(i, j int) bool {
		return impacts[i].UsedBy > impacts[j].UsedBy
	})

	return impacts
}
