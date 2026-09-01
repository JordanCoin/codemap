package cmd

import (
	pathpkg "path"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"codemap/config"
	"codemap/scanner"
)

var contextRoutingTokenPattern = regexp.MustCompile(`[A-Za-z0-9_@./\\-]*[A-Za-z0-9_@]`)

type contextFileIndex struct {
	caseInsensitive bool
	paths           []string
	exact           map[string][]string
	basenames       map[string][]string
	stems           map[string][]string
}

func resolveContextFiles(prompt string, files []scanner.FileInfo, cfg config.ProjectConfig, topK int) []string {
	return resolveContextFilesWithCase(prompt, files, cfg, topK, runtime.GOOS == "windows")
}

func resolveContextFilesWithCase(prompt string, files []scanner.FileInfo, cfg config.ProjectConfig, topK int, caseInsensitive bool) []string {
	if topK <= 0 || len(files) == 0 {
		return nil
	}
	index := newContextFileIndex(files, caseInsensitive)
	tokens := contextRoutingTokens(prompt)
	result := make([]string, 0, topK)
	seen := make(map[string]struct{})
	add := func(path string) bool {
		if path == "" {
			return false
		}
		if _, duplicate := seen[path]; duplicate {
			return false
		}
		seen[path] = struct{}{}
		result = append(result, path)
		return len(result) >= topK
	}

	// Exact normalized repository-relative paths always win.
	for _, token := range tokens {
		normalized := normalizeContextPath(token)
		matches := index.exact[index.key(normalized)]
		if normalized != "" && len(matches) == 1 && add(matches[0]) {
			return result
		}
	}

	// Then accept a unique basename that includes its extension.
	for _, token := range tokens {
		normalized := normalizeContextPath(token)
		if strings.Contains(normalized, "/") {
			continue
		}
		base := pathpkg.Base(normalized)
		if pathpkg.Ext(base) == "" {
			continue
		}
		matches := index.basenames[index.key(base)]
		if len(matches) == 1 && add(matches[0]) {
			return result
		}
	}

	// Finally accept unique extensionless file stems.
	for _, token := range tokens {
		normalized := normalizeContextPath(token)
		if normalized == "" || strings.Contains(normalized, "/") || pathpkg.Ext(normalized) != "" {
			continue
		}
		matches := index.stems[index.key(normalized)]
		if len(matches) == 1 && add(matches[0]) {
			return result
		}
	}

	// Configured subsystem routes are bounded last-resort file candidates.
	for _, subsystemIndex := range contextSubsystemMatches(prompt, cfg, len(cfg.Routing.Subsystems)) {
		subsystem := cfg.Routing.Subsystems[subsystemIndex]
		for _, prefix := range subsystem.Paths {
			prefix = normalizeContextPath(prefix)
			if prefix == "" {
				continue
			}
			prefixKey := index.key(prefix)
			for _, path := range index.paths {
				pathKey := index.key(path)
				if (pathKey == prefixKey || strings.HasPrefix(pathKey, prefixKey+"/")) && add(path) {
					return result
				}
			}
		}
	}
	return result
}

func newContextFileIndex(files []scanner.FileInfo, caseInsensitive bool) contextFileIndex {
	index := contextFileIndex{
		caseInsensitive: caseInsensitive,
		exact:           make(map[string][]string, len(files)),
		basenames:       make(map[string][]string),
		stems:           make(map[string][]string),
	}
	for _, file := range files {
		path := normalizeContextPath(file.Path)
		if path == "" {
			continue
		}
		if _, duplicate := index.exact[path]; duplicate && !caseInsensitive {
			continue
		}
		pathKey := index.key(path)
		duplicate := false
		for _, existing := range index.exact[pathKey] {
			if existing == path {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		index.paths = append(index.paths, path)
		index.exact[pathKey] = append(index.exact[pathKey], path)
		base := pathpkg.Base(path)
		index.basenames[index.key(base)] = append(index.basenames[index.key(base)], path)
		stem := strings.TrimSuffix(base, pathpkg.Ext(base))
		if stem != "" {
			index.stems[index.key(stem)] = append(index.stems[index.key(stem)], path)
		}
	}
	sort.Strings(index.paths)
	return index
}

func (i contextFileIndex) key(value string) string {
	if i.caseInsensitive {
		return strings.ToLower(value)
	}
	return value
}

func contextRoutingTokens(prompt string) []string {
	matches := contextRoutingTokenPattern.FindAllString(strings.ReplaceAll(prompt, `\`, "/"), -1)
	tokens := make([]string, 0, len(matches))
	for _, match := range matches {
		if match != "" {
			tokens = append(tokens, match)
		}
	}
	return tokens
}

type contextSubsystemMatch struct {
	index int
	id    string
	score int
}

func contextSubsystemMatches(prompt string, cfg config.ProjectConfig, topK int) []int {
	if topK <= 0 || cfg.RoutingStrategyOrDefault() != "keyword" {
		return nil
	}

	promptLower := strings.ToLower(prompt)
	matches := make([]contextSubsystemMatch, 0, len(cfg.Routing.Subsystems))
	for index, subsystem := range cfg.Routing.Subsystems {
		score := 0
		for _, keyword := range subsystem.Keywords {
			keyword = strings.TrimSpace(strings.ToLower(keyword))
			if keyword != "" && strings.Contains(promptLower, keyword) {
				score++
			}
		}
		for _, pathHint := range subsystem.Paths {
			pathHint = strings.TrimSpace(strings.ToLower(pathHint))
			if pathHint != "" && strings.Contains(promptLower, pathHint) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		id := strings.TrimSpace(subsystem.ID)
		if id == "" {
			id = "(unnamed)"
		}
		matches = append(matches, contextSubsystemMatch{index: index, id: id, score: score})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].id != matches[j].id {
			return matches[i].id < matches[j].id
		}
		return matches[i].index < matches[j].index
	})
	if len(matches) > topK {
		matches = matches[:topK]
	}

	indices := make([]int, len(matches))
	for i, match := range matches {
		indices[i] = match.index
	}
	return indices
}
