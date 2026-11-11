package render

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"codemap/scanner"
)

// treeNode represents a node in the file tree
type treeNode struct {
	name       string
	path       string // slash path from the project root
	isFile     bool
	file       *scanner.FileInfo
	children   map[string]*treeNode
	fileCount  int
	totalSize  int64
	statsReady bool
}

// getDirStats recursively calculates file count and total size
func getDirStats(node *treeNode) (int, int64) {
	if node.isFile {
		return 1, node.file.Size
	}
	if node.statsReady {
		return node.fileCount, node.totalSize
	}
	count := 0
	var size int64
	for _, child := range node.children {
		childCount, childSize := getDirStats(child)
		count += childCount
		size += childSize
	}
	return count, size
}

func cacheTreeStats(node *treeNode) (int, int64) {
	if node.isFile {
		return 1, node.file.Size
	}
	node.fileCount, node.totalSize = 0, 0
	for _, child := range node.children {
		count, size := cacheTreeStats(child)
		node.fileCount += count
		node.totalSize += size
	}
	node.statsReady = true
	return node.fileCount, node.totalSize
}

// buildTreeStructure builds a nested tree from flat file list
func buildTreeStructure(files []scanner.FileInfo) *treeNode {
	root := &treeNode{children: make(map[string]*treeNode)}

	for i := range files {
		f := &files[i]
		current := root
		remaining := filepath.ToSlash(f.Path)
		for {
			separator := strings.IndexByte(remaining, '/')
			if separator < 0 {
				// File
				current.children[remaining] = &treeNode{
					name:   remaining,
					path:   filepath.ToSlash(f.Path),
					isFile: true,
					file:   f,
				}
				break
			}
			part := remaining[:separator]
			if current.children[part] == nil {
				current.children[part] = &treeNode{name: part, path: strings.TrimPrefix(current.path+"/"+part, "/"), children: make(map[string]*treeNode)}
			}
			current = current.children[part]
			remaining = remaining[separator+1:]
		}
	}
	cacheTreeStats(root)
	return root
}

// formatSize converts bytes to human readable format
func formatSize(size int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	fsize := float64(size)
	for _, unit := range units[:len(units)-1] {
		if fsize < 1024 {
			return fmt.Sprintf("%.1f%s", fsize, unit)
		}
		fsize /= 1024
	}
	return fmt.Sprintf("%.1f%s", fsize, units[len(units)-1])
}

// Tree renders the file tree to the given writer
func Tree(w io.Writer, project scanner.Project) {
	files := project.Files
	projectName := project.Name
	if projectName == "" {
		projectName = filepath.Base(project.Root)
	}
	isDiffMode := project.DiffRef != ""
	maxDepth := project.Depth // 0 = unlimited

	// Calculate stats
	totalFiles := len(files)
	var totalSize int64 = 0
	var totalAdded, totalRemoved int = 0, 0
	extCount := make(map[string]int)
	for _, f := range files {
		totalSize += f.Size
		totalAdded += f.Added
		totalRemoved += f.Removed
		if f.Ext != "" {
			extCount[f.Ext]++
		}
	}

	// Get top extensions
	type extEntry struct {
		ext   string
		count int
	}
	var exts []extEntry
	for ext, count := range extCount {
		exts = append(exts, extEntry{ext, count})
	}
	sort.Slice(exts, func(i, j int) bool {
		if exts[i].count != exts[j].count {
			return exts[i].count > exts[j].count
		}
		return exts[i].ext < exts[j].ext // alphabetical tiebreaker
	})
	if len(exts) > 5 {
		exts = exts[:5]
	}

	// Build extension line first to calculate width
	var extLine string
	if len(exts) > 0 {
		extParts := make([]string, len(exts))
		for i, e := range exts {
			extParts[i] = fmt.Sprintf("%s (%d)", e.ext, e.count)
		}
		extLine = "Top Extensions: " + strings.Join(extParts, ", ")
	}

	// Print header (match Python rich panel exactly - title in top border)
	innerWidth := 64
	// Expand width if extension line is longer
	if len(extLine)+4 > innerWidth {
		innerWidth = len(extLine) + 4
	}

	// Stats line - different for diff mode
	var statsLine string
	if t := project.DiffTotals; isDiffMode && t != nil && t.Files > totalFiles {
		// The panel holds a subset; the header must still state the whole diff.
		statsLine = fmt.Sprintf("Changed: %d files | +%d -%d lines vs %s | %d shown, ranked by churn (--max-changed-files)",
			t.Files, t.Added, t.Removed, project.DiffRef, totalFiles)
	} else if isDiffMode {
		if totalRemoved > 0 {
			statsLine = fmt.Sprintf("Changed: %d files | +%d -%d lines vs %s", totalFiles, totalAdded, totalRemoved, project.DiffRef)
		} else {
			statsLine = fmt.Sprintf("Changed: %d files | +%d lines vs %s", totalFiles, totalAdded, project.DiffRef)
		}
	} else {
		// Say what the number covers: --deps counts source files in the import
		// graph, --skyline code files, watch status every watched file, and
		// nothing told the reader these are different scopes (#154).
		statsLine = fmt.Sprintf("Files: %d (all types, after ignore rules and filters) | Size: %s", totalFiles, formatSize(totalSize))
	}
	if len(statsLine)+4 > innerWidth {
		innerWidth = len(statsLine) + 4
	}

	// Title in top border line (like rich panel)
	titleLine := fmt.Sprintf(" %s ", projectName)
	padding := innerWidth - len(titleLine)
	leftPad := padding / 2
	rightPad := padding - leftPad
	fmt.Fprintf(w, "╭%s%s%s╮\n", strings.Repeat("─", leftPad), titleLine, strings.Repeat("─", rightPad))
	fmt.Fprintf(w, "│ %-*s │\n", innerWidth-2, statsLine)

	// Extensions line
	if extLine != "" {
		fmt.Fprintf(w, "│ %-*s │\n", innerWidth-2, extLine)
	}

	// Remote URL indicator
	if project.RemoteURL != "" {
		remoteLine := fmt.Sprintf("↳ %s", project.RemoteURL)
		fmt.Fprintf(w, "│ %-*s │\n", innerWidth-2, remoteLine)
	}

	fmt.Fprintf(w, "╰%s╯\n", strings.Repeat("─", innerWidth))

	// Build and render tree
	root := buildTreeStructure(files)
	fmt.Fprintf(w, "%s%s%s\n", Bold, projectName, Reset)
	marks := newTreeMarks(project)
	printTreeNode(w, root, "", true, marks, 1, maxDepth)
	if marks.compact {
		printHubTrailer(w, project)
	}

	// Print impact footer for diff mode
	if isDiffMode && len(project.Impact) > 0 {
		fmt.Fprintln(w)
		for _, imp := range project.Impact {
			files := "files"
			if imp.UsedBy == 1 {
				files = "file"
			}
			fmt.Fprintf(w, "%s⚠ %s is used by %d other %s%s\n", Yellow, imp.File, imp.UsedBy, files, Reset)
		}
	}
}
