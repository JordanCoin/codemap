package render

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/scanner"
)

// treeNode represents a node in the file tree
type treeNode struct {
	name     string
	isFile   bool
	file     *scanner.FileInfo
	children map[string]*treeNode
}

// getTopLargeFiles returns paths of top 5 largest source code files
func getTopLargeFiles(files []scanner.FileInfo) map[string]bool {
	// Filter out assets and binaries (no extension = likely binary)
	var sourceFiles []scanner.FileInfo
	for _, f := range files {
		ext := strings.ToLower(f.Ext)
		// Skip if no extension (likely binary) or if it's an asset
		if ext == "" || IsAssetExtension(ext) {
			continue
		}
		sourceFiles = append(sourceFiles, f)
	}

	// Sort by size descending
	sort.Slice(sourceFiles, func(i, j int) bool {
		return sourceFiles[i].Size > sourceFiles[j].Size
	})

	// Return top 5 as set
	result := make(map[string]bool)
	for i := 0; i < len(sourceFiles) && i < 5; i++ {
		result[sourceFiles[i].Path] = true
	}
	return result
}

// getDirStats recursively calculates file count and total size
func getDirStats(node *treeNode) (int, int64) {
	if node.isFile {
		return 1, node.file.Size
	}
	count := 0
	var size int64 = 0
	for _, child := range node.children {
		c, s := getDirStats(child)
		count += c
		size += s
	}
	return count, size
}

// buildTreeStructure builds a nested tree from flat file list
func buildTreeStructure(files []scanner.FileInfo) *treeNode {
	root := &treeNode{children: make(map[string]*treeNode)}

	for _, f := range files {
		parts := strings.Split(f.Path, string(os.PathSeparator))
		current := root
		for i, part := range parts {
			if i == len(parts)-1 {
				// File
				fileCopy := f
				current.children[part] = &treeNode{
					name:   part,
					isFile: true,
					file:   &fileCopy,
				}
			} else {
				// Directory
				if current.children[part] == nil {
					current.children[part] = &treeNode{
						name:     part,
						children: make(map[string]*treeNode),
					}
				}
				current = current.children[part]
			}
		}
	}
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

// Tree renders the file tree to stdout
func Tree(project scanner.Project) {
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

	// Get top large files
	topLarge := getTopLargeFiles(files)

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

	// Title in top border line (like rich panel)
	titleLine := fmt.Sprintf(" %s ", projectName)
	padding := innerWidth - len(titleLine)
	leftPad := padding / 2
	rightPad := padding - leftPad
	fmt.Printf("╭%s%s%s╮\n", strings.Repeat("─", leftPad), titleLine, strings.Repeat("─", rightPad))

	// Stats line - different for diff mode
	var statsLine string
	if isDiffMode {
		if totalRemoved > 0 {
			statsLine = fmt.Sprintf("Changed: %d files | +%d -%d lines vs %s", totalFiles, totalAdded, totalRemoved, project.DiffRef)
		} else {
			statsLine = fmt.Sprintf("Changed: %d files | +%d lines vs %s", totalFiles, totalAdded, project.DiffRef)
		}
	} else {
		statsLine = fmt.Sprintf("Files: %d | Size: %s", totalFiles, formatSize(totalSize))
	}
	fmt.Printf("│ %-*s │\n", innerWidth-2, statsLine)

	// Extensions line
	if extLine != "" {
		fmt.Printf("│ %-*s │\n", innerWidth-2, extLine)
	}

	// Remote URL indicator
	if project.RemoteURL != "" {
		remoteLine := fmt.Sprintf("↳ %s", project.RemoteURL)
		fmt.Printf("│ %-*s │\n", innerWidth-2, remoteLine)
	}

	fmt.Printf("╰%s╯\n", strings.Repeat("─", innerWidth))

	// Build and render tree
	root := buildTreeStructure(files)
	fmt.Printf("%s%s%s\n", Bold, projectName, Reset)
	printTreeNode(root, "", true, topLarge, 1, maxDepth)

	// Print impact footer for diff mode
	if isDiffMode && len(project.Impact) > 0 {
		fmt.Println()
		for _, imp := range project.Impact {
			files := "files"
			if imp.UsedBy == 1 {
				files = "file"
			}
			fmt.Printf("%s⚠ %s is used by %d other %s%s\n", Yellow, imp.File, imp.UsedBy, files, Reset)
		}
	}
}

// printTreeNode recursively prints tree nodes
// currentDepth starts at 1 for the root level, maxDepth 0 means unlimited
func printTreeNode(node *treeNode, prefix string, isLast bool, topLarge map[string]bool, currentDepth, maxDepth int) {
	// Check if we've exceeded depth limit
	if maxDepth > 0 && currentDepth > maxDepth {
		return
	}

	// Separate dirs and files
	var dirs, fileNodes []*treeNode
	for _, child := range node.children {
		if child.isFile {
			fileNodes = append(fileNodes, child)
		} else {
			dirs = append(dirs, child)
		}
	}

	// Sort
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(fileNodes, func(i, j int) bool { return fileNodes[i].name < fileNodes[j].name })

	// Print directories first
	for i, dir := range dirs {
		isLastDir := i == len(dirs)-1 && len(fileNodes) == 0

		// Flatten single-child directories
		mergedName := dir.name
		current := dir
		for len(current.children) == 1 {
			var onlyChild *treeNode
			for _, c := range current.children {
				onlyChild = c
			}
			if onlyChild.isFile {
				break
			}
			mergedName = mergedName + "/" + onlyChild.name
			current = onlyChild
		}

		// Get stats
		fileCount, totalSize := getDirStats(current)

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

		// Format stats
		var statsParts []string
		if fileCount == 1 {
			statsParts = append(statsParts, formatSize(totalSize))
		} else {
			statsParts = append(statsParts, fmt.Sprintf("%d files", fileCount))
			statsParts = append(statsParts, formatSize(totalSize))
		}
		if commonExt != "" {
			statsParts = append(statsParts, fmt.Sprintf("all %s", commonExt))
		}

		connector := "├── "
		if isLastDir {
			connector = "└── "
		}

		fmt.Printf("%s%s%s  %s/%s %s(%s)%s\n",
			prefix, connector, BoldBlue, mergedName, Reset, Dim, strings.Join(statsParts, ", "), Reset)

		newPrefix := prefix + "│   "
		if isLastDir {
			newPrefix = prefix + "    "
		}

		// Check if we're at max depth and there's more content below
		if maxDepth > 0 && currentDepth >= maxDepth {
			// Count hidden content
			hiddenDirs := 0
			hiddenFiles := 0
			for _, c := range current.children {
				if c.isFile {
					hiddenFiles++
				} else {
					hiddenDirs++
				}
			}
			if hiddenDirs > 0 || hiddenFiles > 0 {
				var parts []string
				if hiddenDirs > 0 {
					if hiddenDirs == 1 {
						parts = append(parts, "1 directory")
					} else {
						parts = append(parts, fmt.Sprintf("%d directories", hiddenDirs))
					}
				}
				if hiddenFiles > 0 {
					if hiddenFiles == 1 {
						parts = append(parts, "1 file")
					} else {
						parts = append(parts, fmt.Sprintf("%d files", hiddenFiles))
					}
				}
				fmt.Printf("%s└── %s... %s%s\n", newPrefix, Dim, strings.Join(parts, ", "), Reset)
			}
		} else {
			printTreeNode(current, newPrefix, isLastDir, topLarge, currentDepth+1, maxDepth)
		}
	}

	// Print files as a grid (multi-column layout like Python)
	if len(fileNodes) > 0 {
		connector := "└── "
		termWidth := GetTerminalWidth()
		availableWidth := termWidth - len(prefix) - len(connector)
		if availableWidth < 40 {
			availableWidth = 40
		}

		// Check if all files have the same extension (strip if so, like Python)
		stripExt := ""
		if len(fileNodes) > 1 {
			extSet := make(map[string]bool)
			for _, f := range fileNodes {
				extSet[f.file.Ext] = true
			}
			if len(extSet) == 1 {
				for ext := range extSet {
					stripExt = ext
				}
			}
		}

		// Build file entries with colors
		type fileEntry struct {
			display string
			colored string
			width   int
		}
		var entries []fileEntry
		for _, f := range fileNodes {
			color := GetFileColor(f.file.Ext)
			displayName := f.name
			// Strip extension if all files have same extension
			if stripExt != "" {
				displayName = strings.TrimSuffix(displayName, stripExt)
				if displayName == "" {
					displayName = f.name // Keep original if stripping leaves empty
				}
			}

			// Prefix: diff status indicator OR star for large files
			prefix := ""
			prefixWidth := 0
			if f.file.IsNew {
				prefix = "(new) "
				prefixWidth = 6
				color = Bold + Green
			} else if f.file.Added > 0 || f.file.Removed > 0 {
				prefix = "✎ "
				prefixWidth = 3
				color = Bold + Yellow
			} else if topLarge[f.file.Path] {
				prefix = "⭐️ "
				prefixWidth = 3
				color = Bold + color
			}

			// Suffix: diff stats
			suffix := ""
			suffixWidth := 0
			if f.file.IsNew && f.file.Added > 0 {
				// New file: just show total lines
				suffix = fmt.Sprintf(" (+%d)", f.file.Added)
				suffixWidth = len(suffix)
			} else if f.file.Added > 0 || f.file.Removed > 0 {
				// Modified file: show +/-
				if f.file.Removed > 0 {
					suffix = fmt.Sprintf(" (+%d -%d)", f.file.Added, f.file.Removed)
				} else {
					suffix = fmt.Sprintf(" (+%d)", f.file.Added)
				}
				suffixWidth = len(suffix)
			}

			display := prefix + displayName + suffix
			colored := fmt.Sprintf("%s%s%s%s%s%s", color, prefix, displayName, Reset, Dim, suffix+Reset)
			width := prefixWidth + len(displayName) + suffixWidth
			entries = append(entries, fileEntry{display, colored, width})
		}

		// Calculate columns - find max width and fit columns
		maxWidth := 0
		for _, e := range entries {
			if e.width > maxWidth {
				maxWidth = e.width
			}
		}
		colWidth := maxWidth + 1
		numCols := availableWidth / colWidth
		if numCols < 1 {
			numCols = 1
		}
		if numCols > len(entries) {
			numCols = len(entries)
		}

		// Calculate number of rows
		numRows := (len(entries) + numCols - 1) / numCols

		// Print in column-major order (like Python)
		for row := 0; row < numRows; row++ {
			if row == 0 {
				fmt.Printf("%s%s", prefix, connector)
			} else {
				fmt.Printf("%s    ", prefix)
			}
			for col := 0; col < numCols; col++ {
				idx := col*numRows + row
				if idx < len(entries) {
					e := entries[idx]
					// Pad to column width
					padding := colWidth - e.width
					if padding < 0 {
						padding = 0
					}
					fmt.Printf("%s%s", e.colored, strings.Repeat(" ", padding))
				}
			}
			fmt.Println()
		}
	}
}
