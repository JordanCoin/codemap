package cmd

import (
	pathpkg "path"
	"regexp"
	"sort"
	"strings"

	"codemap/config"
	"codemap/scanner"
)

var contextRoutingTokenPattern = regexp.MustCompile(`[A-Za-z0-9_@./\\-]*[A-Za-z0-9_@]`)

type contextFileIndex struct {
	caseInsensitive bool
	exact           map[string][]string
	basenames       map[string][]string
	sortedPaths     []contextIndexedPath
}

type contextIndexedPath struct {
	path  string
	key   string
	order int
}

type contextFileResolution struct {
	files         []string
	explicitFiles []string
	inferredFiles []string
}

func resolveContextFilesWithCase(prompt string, files []scanner.FileInfo, cfg config.ProjectConfig, topK int, caseInsensitive bool) []string {
	return resolveContextFileResolutionWithCase(prompt, files, cfg, topK, caseInsensitive).files
}

func resolveContextFileResolutionWithCase(prompt string, files []scanner.FileInfo, cfg config.ProjectConfig, topK int, caseInsensitive bool) contextFileResolution {
	if topK <= 0 || len(files) == 0 {
		return contextFileResolution{}
	}
	index := newContextFileIndex(files, caseInsensitive)
	tokens := contextRoutingTokens(prompt)
	resolution := contextFileResolution{files: make([]string, 0, topK)}
	seen := make(map[string]struct{})
	add := func(path string, inferred bool) bool {
		if path == "" {
			return false
		}
		if _, duplicate := seen[path]; duplicate {
			return false
		}
		seen[path] = struct{}{}
		resolution.files = append(resolution.files, path)
		if inferred {
			resolution.inferredFiles = append(resolution.inferredFiles, path)
		} else {
			resolution.explicitFiles = append(resolution.explicitFiles, path)
		}
		return len(resolution.files) >= topK
	}

	// Exact normalized repository-relative paths always win.
	for _, token := range tokens {
		normalized := normalizeContextPath(token)
		matches := index.exact[index.key(normalized)]
		if normalized != "" && len(matches) == 1 && add(matches[0], false) {
			return resolution
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
		if len(matches) == 1 && add(matches[0], false) {
			return resolution
		}
	}

	// Configured subsystem routes are bounded last-resort file candidates.
	seenPrefixes := make(map[string]struct{})
	for _, subsystemIndex := range contextSubsystemMatches(prompt, cfg, cfg.RoutingTopKOrDefault()) {
		subsystem := cfg.Routing.Subsystems[subsystemIndex]
		for _, prefix := range subsystem.Paths {
			prefix = normalizeContextPath(prefix)
			if prefix == "" {
				continue
			}
			prefixKey := index.key(prefix)
			if _, duplicate := seenPrefixes[prefixKey]; duplicate {
				continue
			}
			seenPrefixes[prefixKey] = struct{}{}
			if index.forPrefix(prefixKey, func(path string) bool {
				return add(path, true)
			}) {
				return resolution
			}
		}
	}
	return resolution
}

func newContextFileIndex(files []scanner.FileInfo, caseInsensitive bool) contextFileIndex {
	index := contextFileIndex{
		caseInsensitive: caseInsensitive,
		exact:           make(map[string][]string, len(files)),
		basenames:       make(map[string][]string),
		sortedPaths:     make([]contextIndexedPath, 0, len(files)),
	}
	for _, file := range files {
		path := normalizeContextInventoryPath(file.Path)
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
		index.exact[pathKey] = append(index.exact[pathKey], path)
		base := pathpkg.Base(path)
		index.basenames[index.key(base)] = append(index.basenames[index.key(base)], path)
		index.sortedPaths = append(index.sortedPaths, contextIndexedPath{path: path, key: pathKey})
	}
	if caseInsensitive {
		sort.Slice(index.sortedPaths, func(i, j int) bool {
			return index.sortedPaths[i].path < index.sortedPaths[j].path
		})
		for order := range index.sortedPaths {
			index.sortedPaths[order].order = order
		}
	}
	sort.Slice(index.sortedPaths, func(i, j int) bool {
		if index.sortedPaths[i].key == index.sortedPaths[j].key {
			return index.sortedPaths[i].path < index.sortedPaths[j].path
		}
		return index.sortedPaths[i].key < index.sortedPaths[j].key
	})
	return index
}

func (i contextFileIndex) forPrefix(prefixKey string, visit func(string) bool) bool {
	exactStart := sort.Search(len(i.sortedPaths), func(index int) bool {
		return i.sortedPaths[index].key >= prefixKey
	})
	if i.caseInsensitive {
		matches := make([]contextIndexedPath, 0)
		for index := exactStart; index < len(i.sortedPaths) && i.sortedPaths[index].key == prefixKey; index++ {
			matches = append(matches, i.sortedPaths[index])
		}
		descendantPrefix := prefixKey + "/"
		descendantStart := sort.Search(len(i.sortedPaths), func(index int) bool {
			return i.sortedPaths[index].key >= descendantPrefix
		})
		for index := descendantStart; index < len(i.sortedPaths) && strings.HasPrefix(i.sortedPaths[index].key, descendantPrefix); index++ {
			matches = append(matches, i.sortedPaths[index])
		}
		sort.Slice(matches, func(left, right int) bool {
			return matches[left].order < matches[right].order
		})
		for _, match := range matches {
			if visit(match.path) {
				return true
			}
		}
		return false
	}
	for index := exactStart; index < len(i.sortedPaths) && i.sortedPaths[index].key == prefixKey; index++ {
		if visit(i.sortedPaths[index].path) {
			return true
		}
	}

	descendantPrefix := prefixKey + "/"
	descendantStart := sort.Search(len(i.sortedPaths), func(index int) bool {
		return i.sortedPaths[index].key >= descendantPrefix
	})
	for index := descendantStart; index < len(i.sortedPaths) && strings.HasPrefix(i.sortedPaths[index].key, descendantPrefix); index++ {
		if visit(i.sortedPaths[index].path) {
			return true
		}
	}
	return false
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

func contextSubsystemMatches(prompt string, cfg config.ProjectConfig, topK int) []int {
	matches := matchSubsystemRoutes(prompt, cfg, topK)
	if len(matches) == 0 {
		return nil
	}

	indices := make([]int, len(matches))
	for i, match := range matches {
		indices[i] = match.Index
	}
	return indices
}
