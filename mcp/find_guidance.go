package codemapmcp

import (
	"fmt"
	"sort"
	"strings"

	"codemap/config"
	"codemap/scanner"
)

func findConfiguredMatches(root, pattern string, files []scanner.FileInfo) (visible []string, filtered []scanner.FileInfo, hintsEnabled bool) {
	cfg := config.Load(root)
	pattern = strings.ToLower(pattern)
	for _, file := range files {
		if !strings.Contains(strings.ToLower(file.Path), pattern) {
			continue
		}
		if scanner.MatchesFilters(file.Path, file.Ext, cfg.Only, cfg.Exclude) {
			visible = append(visible, file.Path)
			continue
		}
		if !scanner.MatchesFilters(file.Path, file.Ext, cfg.Only, nil) &&
			scanner.MatchesFilters(file.Path, file.Ext, nil, cfg.Exclude) &&
			!cfg.IgnoresGuidanceForExtension(file.Ext) {
			filtered = append(filtered, file)
		}
	}
	return visible, filtered, cfg.MissingExtensionHintsEnabled()
}

func formatOnlyFilterHint(pattern string, matches []scanner.FileInfo) string {
	const maxPaths = 5
	paths := make([]string, 0, min(len(matches), maxPaths))
	extensions := make(map[string]struct{})
	for i, match := range matches {
		if i < maxPaths {
			paths = append(paths, match.Path)
		}
		if ext := strings.TrimPrefix(strings.ToLower(match.Ext), "."); ext != "" {
			extensions[ext] = struct{}{}
		}
	}
	exts := make([]string, 0, len(extensions))
	for ext := range extensions {
		exts = append(exts, ext)
	}
	sort.Strings(exts)

	output := fmt.Sprintf("No configured files found matching '%s'.\n\nMatches excluded by `only` config:\n%s", pattern, strings.Join(paths, "\n"))
	if remaining := len(matches) - len(paths); remaining > 0 {
		output += fmt.Sprintf("\n... and %d more", remaining)
	}
	if len(exts) > 0 {
		extList := strings.Join(exts, ", ")
		output += fmt.Sprintf("\n\nTell your agent: “include suggestions for %s”, “ignore suggestions for %s”, or “disable suggestions for this repo”.", extList, extList)
	} else {
		output += "\n\nTell your agent: “disable suggestions for this repo”."
	}
	return output + "\n\nNo config changed."
}
