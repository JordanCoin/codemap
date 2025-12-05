package scanner

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DiffInfo holds all diff-related data for changed files
type DiffInfo struct {
	Changed   map[string]bool     // all changed files (modified + untracked)
	Untracked map[string]bool     // new/untracked files only
	Stats     map[string]DiffStat // +/- line counts
}

// GitDiffInfo returns comprehensive diff information for the repo
func GitDiffInfo(root, ref string) (*DiffInfo, error) {
	info := &DiffInfo{
		Changed:   make(map[string]bool),
		Untracked: make(map[string]bool),
		Stats:     make(map[string]DiffStat),
	}

	// Get modified files vs ref with stats
	cmd := exec.Command("git", "diff", "--numstat", ref)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
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
	cmd2 := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd2.Dir = root
	output2, _ := cmd2.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(output2)), "\n") {
		if line != "" {
			info.Changed[line] = true
			info.Untracked[line] = true
		}
	}

	return info, nil
}

// GitDiffFiles returns files changed between current HEAD and the given branch/ref
// Also includes untracked files (new files not yet committed)
func GitDiffFiles(root, ref string) (map[string]bool, error) {
	info, err := GitDiffInfo(root, ref)
	if err != nil {
		return nil, err
	}
	return info.Changed, nil
}

// GitDiffStats returns +/- line counts for changed files
type DiffStat struct {
	Added   int
	Removed int
}

func GitDiffStats(root, ref string) (map[string]DiffStat, error) {
	cmd := exec.Command("git", "diff", "--numstat", ref)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
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

// AnalyzeImpact checks which changed files are imported by other files
// Uses ast-grep to extract actual imports for accuracy
func AnalyzeImpact(root string, changedFiles []FileInfo) []ImpactInfo {
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

	// Scan all files to get their imports using ast-grep
	analyses, err := ScanForDeps(root)
	if err != nil {
		return nil
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
