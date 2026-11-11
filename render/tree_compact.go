package render

import (
	"fmt"
	"io"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"codemap/scanner"
)

// treeMarks carries the plain-tree annotations. compact is false for a diff
// render, whose output stays as it was.
type treeMarks struct {
	compact   bool
	hubFiles  map[string]int
	hubDirs   map[string]int
	untracked map[string]bool
	lines     map[string]int
}

func newTreeMarks(project scanner.Project) *treeMarks {
	m := &treeMarks{compact: project.DiffRef == "", hubFiles: map[string]int{}, hubDirs: map[string]int{}, untracked: project.UntrackedDirs, lines: project.LineCounts}
	for _, h := range project.Hubs {
		if h.IsPackage() {
			m.hubDirs[strings.TrimSuffix(h.Path, "/")] = h.Importers
		} else {
			m.hubFiles[h.Path] = h.Importers
		}
	}
	return m
}

func onlyChild(node *treeNode) *treeNode {
	for _, c := range node.children {
		return c
	}
	return nil
}

// isSingleFileChain reports whether dir holds exactly one file, possibly
// through a chain of single-subdirectory directories.
func isSingleFileChain(dir *treeNode) bool {
	for !dir.isFile && len(dir.children) == 1 {
		dir = onlyChild(dir)
	}
	return dir.isFile
}

func countAndSize(count int, size int64) string {
	if count == 1 {
		return formatSize(size)
	}
	return fmt.Sprintf("%d files, %s", count, formatSize(size))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// loc is " NL" for a file with a line count, else "".
func (m *treeMarks) loc(path string) string { return locLabel(m.lines, path) }

// minLOCShown keeps short files unlabelled: a line count matters when it
// tells a reader to read a range instead of the whole file.
const minLOCShown = 100

// MaxLineCountBytes bounds the files whose lines are counted.
const MaxLineCountBytes = 2 << 20

func locLabel(lines map[string]int, path string) string {
	if n, ok := lines[path]; ok && n >= minLOCShown {
		return fmt.Sprintf(" %dL", n)
	}
	return ""
}

func hubMark(n int) (star, count string) {
	if n > 0 {
		return "⭐️ ", fmt.Sprintf(" ←%d", n)
	}
	return "", ""
}

// printTreeNode recursively prints tree nodes
// currentDepth starts at 1 for the root level, maxDepth 0 means unlimited
func printTreeNode(w io.Writer, node *treeNode, prefix string, isLast bool, marks *treeMarks, currentDepth, maxDepth int) {
	if maxDepth > 0 && currentDepth > maxDepth {
		return
	}
	if marks == nil {
		marks = &treeMarks{}
	}

	var dirs, fileNodes []*treeNode
	for _, child := range node.children {
		if child.isFile {
			fileNodes = append(fileNodes, child)
		} else {
			dirs = append(dirs, child)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(fileNodes, func(i, j int) bool { return fileNodes[i].name < fileNodes[j].name })

	for i, dir := range dirs {
		isLastDir := i == len(dirs)-1 && len(fileNodes) == 0
		connector := "├── "
		newPrefix := prefix + "│   "
		if isLastDir {
			connector = "└── "
			newPrefix = prefix + "    "
		}

		// Flatten single-child directories; an untracked one ends the chain.
		mergedName := dir.name
		current := dir
		for len(current.children) == 1 && !marks.untracked[current.path] {
			c := onlyChild(current)
			if c.isFile {
				break
			}
			mergedName = mergedName + "/" + c.name
			current = c
		}
		fileCount, totalSize := getDirStats(current)

		if marks.compact && marks.untracked[current.path] {
			fmt.Fprintf(w, "%s%s%s  %s/%s %s(untracked, %s)%s\n",
				prefix, connector, BoldBlue, mergedName, Reset, Dim, countAndSize(fileCount, totalSize), Reset)
			continue
		}
		if marks.compact && len(current.children) == 1 {
			f := onlyChild(current)
			star, count := hubMark(marks.hubFiles[f.path])
			fmt.Fprintf(w, "%s%s%s  %s%s/%s%s%s%s%s %s(%s)%s\n",
				prefix, connector, BoldBlue, star, mergedName, Reset, GetFileColor(f.file.Ext), f.name, marks.loc(f.path)+count, Reset, Dim, formatSize(totalSize), Reset)
			continue
		}

		// Check for homogeneous extensions
		var commonExt string
		immediateFiles := make([]*scanner.FileInfo, 0)
		for _, c := range current.children {
			if c.isFile {
				immediateFiles = append(immediateFiles, c.file)
			}
		}
		if len(immediateFiles) > 1 {
			extSet := make(map[string]bool)
			for _, f := range immediateFiles {
				extSet[f.Ext] = true
			}
			if len(extSet) == 1 {
				for ext := range extSet {
					commonExt = ext
				}
			}
		}

		statsParts := []string{countAndSize(fileCount, totalSize)}
		if commonExt != "" {
			statsParts = append(statsParts, fmt.Sprintf("all %s", commonExt))
		}
		star, count := hubMark(marks.hubDirs[current.path])
		fmt.Fprintf(w, "%s%s%s  %s%s/%s%s %s(%s)%s\n",
			prefix, connector, BoldBlue, star, mergedName, Reset, count, Dim, strings.Join(statsParts, ", "), Reset)

		if maxDepth > 0 && currentDepth >= maxDepth {
			// One-line single-file dirs cost no depth, so .claude/skills/ and a
			// deeper plugins/codemap/skills/ render the same shape.
			allChains := marks.compact && len(current.children) > 0
			hiddenDirs, hiddenFiles := 0, 0
			for _, c := range current.children {
				if c.isFile {
					hiddenFiles++
					allChains = false
				} else {
					hiddenDirs++
					allChains = allChains && isSingleFileChain(c)
				}
			}
			if allChains {
				printTreeNode(w, current, newPrefix, isLastDir, marks, currentDepth+1, 0)
				continue
			}
			var parts []string
			if hiddenDirs > 0 {
				parts = append(parts, plural(hiddenDirs, "directory", "directories"))
			}
			if hiddenFiles > 0 {
				parts = append(parts, plural(hiddenFiles, "file", "files"))
			}
			if len(parts) > 0 {
				fmt.Fprintf(w, "%s└── %s... %s%s\n", newPrefix, Dim, strings.Join(parts, ", "), Reset)
			}
		} else {
			printTreeNode(w, current, newPrefix, isLastDir, marks, currentDepth+1, maxDepth)
		}
	}

	if len(fileNodes) > 0 {
		printFileGrid(w, fileNodes, prefix, marks)
	}
}

type fileEntry struct {
	colored string
	width   int
}

// printFileGrid prints a directory's files in column-major order.
func printFileGrid(w io.Writer, fileNodes []*treeNode, prefix string, marks *treeMarks) {
	connector := "└── "
	availableWidth := GetTerminalWidth() - len(prefix) - len(connector)
	if availableWidth < 40 {
		availableWidth = 40
	}

	var items []gridItem
	orphans := 0
	if marks.compact {
		items, orphans = compactFiles(fileNodes, marks)
	} else {
		for _, f := range fileNodes {
			items = append(items, gridItem{node: f})
		}
	}
	var entries []fileEntry
	for _, it := range items {
		entries = append(entries, it.entry(marks))
	}
	if orphans > 0 {
		label := "+" + plural(orphans, "other test", "other tests")
		entries = append(entries, fileEntry{Dim + label + Reset, len(label)})
	}

	widths := make([]int, len(entries))
	for i, e := range entries {
		widths[i] = e.width
	}
	colWidth := gridColumnWidth(widths, availableWidth, marks.compact)
	numCols := max(1, min(availableWidth/colWidth, len(entries)))
	row := 0
	writeRow := func(cells []fileEntry) {
		lead := prefix + "    "
		if row == 0 {
			lead = prefix + connector
		}
		fmt.Fprint(w, lead)
		for _, e := range cells {
			fmt.Fprintf(w, "%s%s", e.colored, strings.Repeat(" ", max(0, colWidth-e.width)))
		}
		fmt.Fprintln(w)
		row++
	}

	if maxInt(widths) < colWidth {
		// Column-major, as before.
		numRows := (len(entries) + numCols - 1) / numCols
		for r := 0; r < numRows; r++ {
			var cells []fileEntry
			for col := 0; col < numCols; col++ {
				if idx := col*numRows + r; idx < len(entries) {
					cells = append(cells, entries[idx])
				}
			}
			writeRow(cells)
		}
		return
	}
	// An entry too wide for a column takes a row of its own in place, so the
	// grid reads in sorted order left to right.
	var cells []fileEntry
	for _, e := range entries {
		if e.width >= colWidth {
			if len(cells) > 0 {
				writeRow(cells)
				cells = nil
			}
			writeRow([]fileEntry{e})
			continue
		}
		if cells = append(cells, e); len(cells) == numCols {
			writeRow(cells)
			cells = nil
		}
	}
	if len(cells) > 0 {
		writeRow(cells)
	}
}

func maxInt(values []int) int {
	m := 0
	for _, v := range values {
		m = max(m, v)
	}
	return m
}

// gridColumnWidth is the widest grid entry plus one; entries at least this
// wide go on their own lines. When the widest entry would force a single
// column, a compact tree moves up to a tenth of the entries (at least one)
// to their own lines, so one long name cannot flatten the whole directory.
func gridColumnWidth(widths []int, available int, allowOutliers bool) int {
	sorted := append([]int(nil), widths...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	if allowOutliers && len(sorted) >= 5 && available/(sorted[0]+1) < 2 {
		limit := max(1, len(sorted)/10)
		for k := 1; k <= limit; k++ {
			if available/(sorted[k]+1) >= 2 {
				return sorted[k] + 1
			}
		}
	}
	return sorted[0] + 1
}

// gridItem is one cell of a file grid: a file with its folded tests, or a
// run of siblings collapsed to their shared name pattern (run > 0).
type gridItem struct {
	node  *treeNode
	tests int
	run   int
	key   string
}

func (it gridItem) entry(marks *treeMarks) fileEntry {
	if it.run > 0 {
		label := fmt.Sprintf("%s ×%d", it.key, it.run)
		return fileEntry{GetFileColor(it.node.file.Ext) + label + Reset, len([]rune(label))}
	}
	f := it.node
	color := GetFileColor(f.file.Ext)
	prefix, prefixWidth := "", 0
	switch {
	case f.file.IsNew:
		prefix, prefixWidth, color = "(new) ", 6, Bold+Green
	case f.file.Added > 0 || f.file.Removed > 0:
		prefix, prefixWidth, color = "✎ ", 3, Bold+Yellow
	case marks.hubFiles[f.path] > 0:
		prefix, prefixWidth, color = "⭐️ ", 3, Bold+color
	}
	suffix := ""
	if f.file.IsNew && f.file.Added > 0 {
		suffix = fmt.Sprintf(" (+%d)", f.file.Added)
	} else if f.file.Added > 0 || f.file.Removed > 0 {
		if f.file.Removed > 0 {
			suffix = fmt.Sprintf(" (+%d -%d)", f.file.Added, f.file.Removed)
		} else {
			suffix = fmt.Sprintf(" (+%d)", f.file.Added)
		}
	}
	_, count := hubMark(marks.hubFiles[f.path])
	suffix += marks.loc(f.path) + count
	if it.tests > 0 {
		suffix += " (+" + plural(it.tests, "test", "tests") + ")"
	}
	width := prefixWidth + len(f.name) + len([]rune(suffix))
	return fileEntry{fmt.Sprintf("%s%s%s%s%s%s", color, prefix, f.name, Reset, Dim, suffix+Reset), width}
}

const minRun = 4

// compactFiles folds test files into their same-directory subject and
// collapses runs of minRun+ siblings that share a runKey. Tests with no
// subject in the directory are only counted, except in a tests tree.
func compactFiles(files []*treeNode, marks *treeMarks) (items []gridItem, orphans int) {
	exists := make(map[string]bool, len(files))
	for _, f := range files {
		exists[f.name] = true
	}
	tests := map[string]int{}
	subjects := map[*treeNode]string{}
	folded := 0
	for _, f := range files {
		if subject, isTest := testSubject(f.name, exists); isTest {
			subjects[f] = subject
			if subject != "" {
				tests[subject]++
				folded++
			} else {
				orphans++
			}
		}
	}
	// A tests tree, not a source dir with stray tests: list the unmatched
	// tests by name.
	listOrphans := folded == 0 || testDirNames[pathpkg.Base(pathpkg.Dir(files[0].path))]
	var kept []*treeNode
	for _, f := range files {
		subject, isTest := subjects[f]
		if !isTest || subject == "" && listOrphans {
			kept = append(kept, f)
		}
	}
	if listOrphans {
		orphans = 0
	}
	keys := map[*treeNode]string{}
	members := map[string]int{}
	for _, f := range kept {
		if key, varied := runKey(f.name); varied && tests[f.name] == 0 && marks.hubFiles[f.path] == 0 {
			keys[f] = key
			members[key]++
		}
	}
	emitted := map[string]bool{}
	for _, f := range kept {
		if key, ok := keys[f]; ok && members[key] >= minRun {
			if !emitted[key] {
				emitted[key] = true
				items = append(items, gridItem{node: f, run: members[key], key: key})
			}
			continue
		}
		items = append(items, gridItem{node: f, tests: tests[f.name]})
	}
	return items, orphans
}

// testDirNames are directory names that conventionally hold only tests.
var testDirNames = map[string]bool{"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true}

var jsTestSubjectExts = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

// testSubject reports whether name is a test file by language convention and,
// when the file it tests is in the same directory, that file's name.
// Go: x_test.go, x_more_test.go -> x.go (longest existing prefix wins).
// JS/TS: x.test.ts, x.spec.tsx -> same extension, then any JS/TS one.
// Python: test_x.py, x_test.py -> x.py.
func testSubject(name string, exists map[string]bool) (string, bool) {
	pick := func(candidate string) string {
		if exists[candidate] {
			return candidate
		}
		return ""
	}
	if stem, ok := strings.CutSuffix(name, "_test.go"); ok {
		for stem != "" {
			if exists[stem+".go"] {
				return stem + ".go", true
			}
			i := strings.LastIndexByte(stem, '_')
			if i <= 0 {
				break
			}
			stem = stem[:i]
		}
		return "", true
	}
	if stem, ok := strings.CutSuffix(name, ".py"); ok {
		if s, ok := strings.CutPrefix(stem, "test_"); ok && s != "" {
			return pick(s + ".py"), true
		}
		if s, ok := strings.CutSuffix(stem, "_test"); ok && s != "" {
			return pick(s + ".py"), true
		}
		return "", false
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	stem, ok := strings.CutSuffix(base, ".test")
	if !ok {
		stem, ok = strings.CutSuffix(base, ".spec")
	}
	if !ok || stem == "" {
		return "", false
	}
	if exists[stem+ext] {
		return stem + ext, true
	}
	for _, e := range jsTestSubjectExts {
		if exists[stem+e] {
			return stem + e, true
		}
	}
	return "", true
}

var (
	runToken      = regexp.MustCompile(`[A-Za-z0-9]+`)
	runWordNumber = regexp.MustCompile(`^([A-Za-z]{2,})[0-9]+$`)
	runStars      = regexp.MustCompile(`\*(?:[-_.:]\*)+`)
)

// runKey replaces every digit-bearing alphanumeric token of name's stem
// (timestamps, hashes, numbers) with "*", keeping a leading word of 2+
// letters (file12 -> file*). Names sharing a key differ only in those tokens.
func runKey(name string) (string, bool) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	varied := false
	key := runToken.ReplaceAllStringFunc(stem, func(tok string) string {
		if !strings.ContainsAny(tok, "0123456789") {
			return tok
		}
		varied = true
		if m := runWordNumber.FindStringSubmatch(tok); m != nil {
			return m[1] + "*"
		}
		return "*"
	})
	return runStars.ReplaceAllString(key, "*") + ext, varied
}

// printHubTrailer prints the hub legend, the hubs one per line with their
// importer counts, and the next commands, all from this run's graph.
func printHubTrailer(w io.Writer, project scanner.Project) {
	if project.Hubs == nil {
		if project.HubNote != "" {
			fmt.Fprintf(w, "%sNo ⭐️ hub marks: %s%s\n", Dim, project.HubNote, Reset)
		}
		return
	}
	legend := fmt.Sprintf("⭐️ ←N = imported by N files, %d+ of them non-test (Go: package count, on its dir)", scanner.HubThreshold)
	if project.LineCounts != nil {
		legend += fmt.Sprintf("; NL = lines, shown at %d+ (text files up to %dMB)", minLOCShown, MaxLineCountBytes>>20)
	}
	if project.HubNote != "" {
		legend += "; " + project.HubNote
	}
	fmt.Fprintf(w, "%s%s%s\n", Dim, legend, Reset)
	if len(project.Hubs) == 0 {
		return
	}
	limit := project.HubListMax
	if limit <= 0 {
		limit = maxTrailerHubs
	}
	shown := project.Hubs[:min(limit, len(project.Hubs))]
	if len(shown) < len(project.Hubs) {
		fmt.Fprintf(w, "Hubs (top %d of %d, by importers):\n", len(shown), len(project.Hubs))
	} else {
		fmt.Fprintf(w, "Hubs (%d, by importers):\n", len(shown))
	}
	width := 0
	for _, h := range shown {
		width = max(width, len(fmt.Sprint(h.Importers)))
	}
	for _, h := range shown {
		fmt.Fprintf(w, "  ←%*d  %s%s\n", width, h.Importers, h.Path, strings.Replace(locLabel(project.LineCounts, h.Path), " ", "  ", 1))
	}
	fmt.Fprintf(w, "Next: codemap --deps · codemap --importers %s\n", project.Hubs[0].Files[0])
}

const maxTrailerHubs = 10
