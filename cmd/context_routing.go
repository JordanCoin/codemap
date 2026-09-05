package cmd

import (
	pathpkg "path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"codemap/config"
	"codemap/scanner"
)

var contextRoutingTokenPattern = regexp.MustCompile(`[A-Za-z0-9_@./\\-]*[A-Za-z0-9_@]`)

type contextFileIndex struct {
	caseInsensitive              bool
	files                        []scanner.FileInfo
	sortedPaths                  []contextIndexedPath
	useInventory                 bool
	scanInventory                bool
	normalizeInventorySeparators bool
	pathsReady                   bool
}

type contextUniquePathIndex map[string]string

type contextIndexedPath struct {
	path string
	key  string
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
		if normalized == "" {
			continue
		}
		match, unique := index.uniqueExact(index.key(normalized))
		if unique && add(match, false) {
			return resolution
		}
	}

	// Then accept a unique basename that includes its extension.
	basenameKeys := make([]string, 0, len(tokens))
	for _, token := range tokens {
		normalized := normalizeContextPath(token)
		if strings.Contains(normalized, "/") {
			continue
		}
		base := pathpkg.Base(normalized)
		if pathpkg.Ext(base) == "" {
			continue
		}
		basenameKeys = append(basenameKeys, index.key(base))
	}
	basenameMatches := index.uniqueBasenames(basenameKeys)
	for _, baseKey := range basenameKeys {
		match, unique := basenameMatches.unique(baseKey)
		if unique && add(match, false) {
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
			if index.forPrefix(prefixKey, topK, func(path string) bool {
				return add(path, true)
			}) {
				return resolution
			}
		}
	}
	return resolution
}

func newContextFileIndex(files []scanner.FileInfo, caseInsensitive bool) contextFileIndex {
	return contextFileIndex{
		caseInsensitive: caseInsensitive,
		files:           files,
	}
}

func (i *contextFileIndex) uniqueBasenames(keys []string) contextUniquePathIndex {
	if len(keys) == 0 {
		return nil
	}
	matches := make(contextUniquePathIndex, len(keys))
	i.preparePaths()
	if len(keys) > 1 && !i.caseInsensitive {
		requested := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			requested[key] = struct{}{}
		}
		for index := 0; index < i.pathCount(); index++ {
			key := i.basenameKeyAt(index)
			if _, ok := requested[key]; ok {
				matches.add(key, i.pathAt(index))
			}
		}
		return matches
	}
	if len(keys) > 1 {
		return i.uniqueCaseFoldedBasenames(keys, matches)
	}
	for index := 0; index < i.pathCount(); index++ {
		if i.basenameMatches(index, keys[0]) {
			matches.add(keys[0], i.pathAt(index))
		}
	}
	return matches
}

func (i *contextFileIndex) uniqueCaseFoldedBasenames(keys []string, matches contextUniquePathIndex) contextUniquePathIndex {
	requested := make(map[uint64]string, len(keys))
	for _, key := range keys {
		hash, _ := contextASCIIFoldHash(key)
		// Exact comparisons below resolve the unlikely case of a hash collision.
		if _, exists := requested[hash]; !exists {
			requested[hash] = key
		}
	}
	for index := 0; index < i.pathCount(); index++ {
		base := i.basenameAt(index)
		hash, ascii := contextASCIIFoldHash(base)
		if !ascii {
			base = i.key(base)
			hash, _ = contextASCIIFoldHash(base)
		}
		candidate, found := requested[hash]
		if !found {
			continue
		}
		matchesKey := func(key string) bool {
			if ascii {
				return i.compareKeys(base, key) == 0
			}
			return base == key
		}
		if matchesKey(candidate) {
			matches.add(candidate, i.pathAt(index))
			continue
		}
		for _, key := range keys {
			keyHash, _ := contextASCIIFoldHash(key)
			if keyHash == hash && matchesKey(key) {
				matches.add(key, i.pathAt(index))
				break
			}
		}
	}
	return matches
}

func (i contextFileIndex) basenameAt(index int) string {
	if !i.useInventory && !i.scanInventory {
		return pathpkg.Base(i.sortedPaths[index].key)
	}
	path := i.files[index].Path
	if separator := strings.LastIndexAny(path, `/\`); separator >= 0 {
		path = path[separator+1:]
	}
	return path
}

func (i contextFileIndex) basenameKeyAt(index int) string {
	return i.key(i.basenameAt(index))
}

func (i contextUniquePathIndex) add(key, path string) {
	existing, found := i[key]
	if !found {
		i[key] = path
		return
	}
	if existing != path {
		i[key] = ""
	}
}

func (i contextUniquePathIndex) unique(key string) (string, bool) {
	path, found := i[key]
	return path, found && path != ""
}

func (i *contextFileIndex) uniqueExact(key string) (string, bool) {
	i.preparePaths()
	if i.scanInventory {
		match := ""
		for index, file := range i.files {
			if i.compareKeys(file.Path, key) != 0 {
				continue
			}
			path := i.pathAt(index)
			if match != "" && match != path {
				return "", false
			}
			match = path
		}
		return match, match != ""
	}
	start := sort.Search(i.pathCount(), func(index int) bool {
		return i.compareKeyAt(index, key) >= 0
	})
	if start == i.pathCount() || i.compareKeyAt(start, key) != 0 {
		return "", false
	}
	path := i.pathAt(start)
	if start+1 < i.pathCount() && i.compareKeyAt(start+1, key) == 0 && i.pathAt(start+1) != path {
		return "", false
	}
	return path, true
}

func (i *contextFileIndex) preparePaths() {
	if i.pathsReady {
		return
	}
	// Binary-search ordered inventories; scan valid unordered inventories directly.
	inventoryValid := true
	inventoryOrdered := true
	for index, file := range i.files {
		valid, normalizeSeparators := contextInventoryPathState(file.Path)
		i.normalizeInventorySeparators = i.normalizeInventorySeparators || normalizeSeparators
		if !valid {
			inventoryValid = false
			break
		}
		if inventoryOrdered && index > 0 && i.comparePaths(i.files[index-1].Path, file.Path) >= 0 {
			inventoryOrdered = false
		}
	}
	i.useInventory = inventoryValid && inventoryOrdered
	if i.useInventory {
		i.pathsReady = true
		return
	}
	if inventoryValid {
		i.scanInventory = true
		i.pathsReady = true
		return
	}

	i.sortedPaths = make([]contextIndexedPath, 0, len(i.files))
	for _, file := range i.files {
		path := normalizeContextInventoryPath(file.Path)
		if path != "" {
			i.sortedPaths = append(i.sortedPaths, contextIndexedPath{path: path, key: i.key(path)})
		}
	}
	compare := func(left, right contextIndexedPath) int {
		if order := strings.Compare(left.key, right.key); order != 0 {
			return order
		}
		return strings.Compare(left.path, right.path)
	}
	slices.SortFunc(i.sortedPaths, compare)
	write := min(1, len(i.sortedPaths))
	for read := 1; read < len(i.sortedPaths); read++ {
		if i.sortedPaths[read] == i.sortedPaths[write-1] {
			continue
		}
		if write != read {
			i.sortedPaths[write] = i.sortedPaths[read]
		}
		write++
	}
	i.sortedPaths = i.sortedPaths[:write]
	i.pathsReady = true
}

func (i *contextFileIndex) forPrefix(prefixKey string, limit int, visit func(string) bool) bool {
	if limit <= 0 {
		return false
	}
	i.preparePaths()
	matches := make([]int, 0, min(limit, i.pathCount()))
	consider := func(candidate int) {
		position := sort.Search(len(matches), func(index int) bool {
			return i.compareOutputPaths(matches[index], candidate) >= 0
		})
		if position < len(matches) && i.compareOutputPaths(matches[position], candidate) == 0 {
			return
		}
		if len(matches) == limit && position == len(matches) {
			return
		}
		if len(matches) < limit {
			matches = append(matches, 0)
		}
		copy(matches[position+1:], matches[position:len(matches)-1])
		matches[position] = candidate
	}
	visitMatches := func() bool {
		for _, match := range matches {
			if visit(i.pathAt(match)) {
				return true
			}
		}
		return false
	}
	descendantPrefix := prefixKey + "/"
	if i.scanInventory {
		for index, file := range i.files {
			if i.compareKeys(file.Path, prefixKey) == 0 || contextPathPrefix(file.Path, descendantPrefix, i.caseInsensitive) {
				consider(index)
			}
		}
		return visitMatches()
	}
	exactStart := sort.Search(i.pathCount(), func(index int) bool {
		return i.compareKeyAt(index, prefixKey) >= 0
	})
	descendantStart := sort.Search(i.pathCount(), func(index int) bool {
		return i.compareKeyAt(index, descendantPrefix) >= 0
	})
	if i.caseInsensitive {
		for index := exactStart; index < i.pathCount() && i.compareKeyAt(index, prefixKey) == 0; index++ {
			consider(index)
		}
		for index := descendantStart; index < i.pathCount() && i.keyHasPrefixAt(index, descendantPrefix); index++ {
			consider(index)
		}
		return visitMatches()
	}

	visited := 0
	for index := exactStart; index < i.pathCount() && i.compareKeyAt(index, prefixKey) == 0; index++ {
		if visited == limit {
			return false
		}
		visited++
		if visit(i.pathAt(index)) {
			return true
		}
	}

	for index := descendantStart; index < i.pathCount() && i.keyHasPrefixAt(index, descendantPrefix); index++ {
		if visited == limit {
			return false
		}
		visited++
		if visit(i.pathAt(index)) {
			return true
		}
	}
	return false
}

func (i contextFileIndex) pathCount() int {
	if i.useInventory || i.scanInventory {
		return len(i.files)
	}
	return len(i.sortedPaths)
}

func (i contextFileIndex) pathAt(index int) string {
	if i.useInventory || i.scanInventory {
		path := i.files[index].Path
		if i.normalizeInventorySeparators {
			return strings.ReplaceAll(path, `\`, "/")
		}
		return path
	}
	return i.sortedPaths[index].path
}

func (i contextFileIndex) compareOutputPaths(left, right int) int {
	if i.useInventory || i.scanInventory {
		if !i.normalizeInventorySeparators {
			return strings.Compare(i.files[left].Path, i.files[right].Path)
		}
		return compareContextPaths(i.files[left].Path, i.files[right].Path, false)
	}
	return strings.Compare(i.sortedPaths[left].path, i.sortedPaths[right].path)
}

func (i contextFileIndex) compareKeyAt(index int, key string) int {
	if i.useInventory {
		return i.compareKeys(i.files[index].Path, key)
	}
	return strings.Compare(i.sortedPaths[index].key, key)
}

func (i contextFileIndex) keyHasPrefixAt(index int, prefix string) bool {
	if i.useInventory {
		if !i.normalizeInventorySeparators && !i.caseInsensitive {
			return strings.HasPrefix(i.files[index].Path, prefix)
		}
		return contextPathPrefix(i.files[index].Path, prefix, i.caseInsensitive)
	}
	return strings.HasPrefix(i.sortedPaths[index].key, prefix)
}

func (i contextFileIndex) basenameMatches(index int, key string) bool {
	if i.useInventory || i.scanInventory {
		path := i.files[index].Path
		if separator := strings.LastIndexAny(path, `/\`); separator >= 0 {
			path = path[separator+1:]
		}
		return i.compareKeys(path, key) == 0
	}
	return pathpkg.Base(i.sortedPaths[index].key) == key
}

func (i contextFileIndex) comparePaths(left, right string) int {
	if order := i.compareKeys(left, right); order != 0 {
		return order
	}
	return compareContextPaths(left, right, false)
}

func (i contextFileIndex) compareKeys(left, right string) int {
	if !i.normalizeInventorySeparators && !i.caseInsensitive {
		return strings.Compare(left, right)
	}
	return compareContextPaths(left, right, i.caseInsensitive)
}

func compareContextPaths(left, right string, caseInsensitive bool) int {
	limit := min(len(left), len(right))
	for index := 0; index < limit; index++ {
		leftByte, rightByte := left[index], right[index]
		if leftByte == '\\' {
			leftByte = '/'
		}
		if rightByte == '\\' {
			rightByte = '/'
		}
		if leftByte >= 0x80 || rightByte >= 0x80 {
			return strings.Compare(contextComparisonKey(left, caseInsensitive), contextComparisonKey(right, caseInsensitive))
		}
		if leftByte == rightByte {
			continue
		}
		if caseInsensitive && leftByte >= 'A' && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if caseInsensitive && rightByte >= 'A' && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte < rightByte {
			return -1
		}
		if leftByte > rightByte {
			return 1
		}
	}
	return len(left) - len(right)
}

func contextPathPrefix(path, prefix string, caseInsensitive bool) bool {
	if len(path) < len(prefix) {
		return strings.HasPrefix(contextComparisonKey(path, caseInsensitive), contextComparisonKey(prefix, caseInsensitive))
	}
	for index := range prefix {
		pathByte, prefixByte := path[index], prefix[index]
		if pathByte == '\\' {
			pathByte = '/'
		}
		if prefixByte == '\\' {
			prefixByte = '/'
		}
		if pathByte >= 0x80 || prefixByte >= 0x80 {
			return strings.HasPrefix(contextComparisonKey(path, caseInsensitive), contextComparisonKey(prefix, caseInsensitive))
		}
		if caseInsensitive && pathByte >= 'A' && pathByte <= 'Z' {
			pathByte += 'a' - 'A'
		}
		if caseInsensitive && prefixByte >= 'A' && prefixByte <= 'Z' {
			prefixByte += 'a' - 'A'
		}
		if pathByte != prefixByte {
			return false
		}
	}
	return true
}

func contextComparisonKey(path string, caseInsensitive bool) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if caseInsensitive {
		path = strings.ToLower(path)
	}
	return path
}

func contextInventoryPathState(path string) (valid, normalizeSeparators bool) {
	if path == "" || path[0] == '/' || path[0] == '\\' || path[len(path)-1] == '/' || path[len(path)-1] == '\\' {
		return false, false
	}
	segmentStart := 0
	for index := 0; index < len(path); index++ {
		if path[index] != '/' && path[index] != '\\' {
			continue
		}
		normalizeSeparators = normalizeSeparators || path[index] == '\\'
		segment := path[segmentStart:index]
		if segment == "" || segment == "." || segment == ".." {
			return false, normalizeSeparators
		}
		segmentStart = index + 1
	}
	segment := path[segmentStart:]
	return segment != "." && segment != "..", normalizeSeparators
}

func (i contextFileIndex) key(value string) string {
	if i.caseInsensitive {
		return strings.ToLower(value)
	}
	return value
}

func contextASCIIFoldHash(value string) (uint64, bool) {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	hash := uint64(offset64)
	ascii := true
	for index := 0; index < len(value); index++ {
		char := value[index]
		ascii = ascii && char < 0x80
		if char >= 'A' && char <= 'Z' {
			char += 'a' - 'A'
		}
		hash ^= uint64(char)
		hash *= prime64
	}
	return hash, ascii
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
