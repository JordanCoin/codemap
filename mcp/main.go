// MCP Server for codemap - provides codebase analysis tools to LLMs
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"codemap/render"
	"codemap/scanner"
	"codemap/watch"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Global watcher registry - tracks active watchers per project
var (
	watchers   = make(map[string]*watch.Daemon)
	watchersMu sync.RWMutex
)

// Input types for tools
type PathInput struct {
	Path string `json:"path" jsonschema:"Path to the project directory to analyze"`
}

type DiffInput struct {
	Path string `json:"path" jsonschema:"Path to the project directory to analyze"`
	Ref  string `json:"ref,omitempty" jsonschema:"Git branch/ref to compare against (default: main)"`
}

type FindInput struct {
	Path    string `json:"path" jsonschema:"Path to the project directory to search"`
	Pattern string `json:"pattern" jsonschema:"Filename pattern to search for (case-insensitive substring match)"`
}

type ImportersInput struct {
	Path string `json:"path" jsonschema:"Path to the project directory"`
	File string `json:"file" jsonschema:"Relative path to the file to check (e.g. src/utils.ts)"`
}

type ListProjectsInput struct {
	Path    string `json:"path" jsonschema:"Parent directory containing projects (e.g. /Users/name/Code or ~/Code)"`
	Pattern string `json:"pattern,omitempty" jsonschema:"Optional filter to match project names (case-insensitive substring)"`
}

type WatchInput struct {
	Path string `json:"path" jsonschema:"Path to the project directory to watch"`
}

type WatchActivityInput struct {
	Path    string `json:"path" jsonschema:"Path to the project directory"`
	Minutes int    `json:"minutes,omitempty" jsonschema:"Look back this many minutes (default: 30)"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "codemap",
		Version: "2.0.0",
	}, nil)

	// Tool: get_structure - Get project tree view
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_structure",
		Description: "Get the project structure as a tree view. Shows files organized by directory with language detection, file sizes, and highlights the top 5 largest source files. Use this to understand how a codebase is organized.",
	}, handleGetStructure)

	// Tool: get_dependencies - Get dependency graph
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_dependencies",
		Description: "Get the dependency flow of a project. Shows external dependencies by language, internal import chains between files, hub files (most-imported), and function counts. Use this to understand how code connects and which files are most critical.",
	}, handleGetDependencies)

	// Tool: get_diff - Get changed files with impact analysis
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_diff",
		Description: "Get files changed compared to a git branch, with line counts and impact analysis showing which changed files are imported by others. Use this to understand what work has been done and what might break.",
	}, handleGetDiff)

	// Tool: find_file - Find files by pattern
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_file",
		Description: "Find files in a project matching a name pattern. Returns file paths with their sizes and languages.",
	}, handleFindFile)

	// Tool: get_importers - Find what imports a file
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_importers",
		Description: "Find all files that import/depend on a specific file. Use this to understand the impact of changing a file.",
	}, handleGetImporters)

	// Tool: status - Verify MCP connection
	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Check codemap MCP server status. Returns version and confirms local filesystem access is available.",
	}, handleStatus)

	// Tool: list_projects - Discover projects in a directory
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_projects",
		Description: "List project directories under a parent path. Use this to discover projects when you only know the general location (e.g., ~/Code) but not the exact folder name. Optionally filter by pattern to find specific projects. Returns directory names with file counts and primary language.",
	}, handleListProjects)

	// === LIVE WATCH TOOLS ===

	// Tool: start_watch - Start watching a project
	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_watch",
		Description: "Start live file watching for a project. Tracks file changes in real-time with timestamps, line deltas, and git status. The watcher runs in background - use get_activity to see what's happening.",
	}, handleStartWatch)

	// Tool: stop_watch - Stop watching a project
	mcp.AddTool(server, &mcp.Tool{
		Name:        "stop_watch",
		Description: "Stop the live file watcher for a project.",
	}, handleStopWatch)

	// Tool: get_activity - Get recent coding activity
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_activity",
		Description: "Get recent coding activity for a watched project. Shows what files were edited, when, and how much changed. Use this to understand what the user has been working on. Returns hot files, recent changes, and session summary.",
	}, handleGetActivity)

	// === FILE GRAPH TOOLS ===

	// Tool: get_hubs - Get critical hub files
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_hubs",
		Description: "Get all hub files in a project (files imported by 3+ other files). These are the critical files where changes have the most impact. Use this before making changes to understand what's important.",
	}, handleGetHubs)

	// Tool: get_file_context - Get full context for a file
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_file_context",
		Description: "Get complete dependency context for a specific file: what it imports, what imports it, whether it's a hub, and all connected files. Use this before editing a file to understand its role in the codebase.",
	}, handleGetFileContext)

	// Run server on stdio
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("Server error: %v", err)
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
		IsError: true,
	}
}

func handleGetStructure(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	absRoot, err := filepath.Abs(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	gitCache := scanner.NewGitIgnoreCache(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitCache, nil, nil)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	project := scanner.Project{
		Root:  absRoot,
		Mode:  "tree",
		Files: files,
	}

	var buf bytes.Buffer
	render.Tree(&buf, project)
	output := stripANSI(buf.String())

	// Add hub file summary
	fg, err := scanner.BuildFileGraph(input.Path)
	if err == nil {
		hubs := fg.HubFiles()
		if len(hubs) > 0 {
			output += "\n⚠️  HUB FILES (high-impact, 3+ dependents):\n"
			// Sort by importer count
			sort.Slice(hubs, func(i, j int) bool {
				return len(fg.Importers[hubs[i]]) > len(fg.Importers[hubs[j]])
			})
			for i, hub := range hubs {
				if i >= 5 {
					output += fmt.Sprintf("   ... and %d more hubs\n", len(hubs)-5)
					break
				}
				output += fmt.Sprintf("   %s (%d importers)\n", hub, len(fg.Importers[hub]))
			}
		}
	}

	return textResult(output), nil, nil
}

func handleGetDependencies(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	absRoot, err := filepath.Abs(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	analyses, err := scanner.ScanForDeps(input.Path)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	depsProject := scanner.DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        analyses,
		ExternalDeps: scanner.ReadExternalDeps(absRoot),
	}

	var buf bytes.Buffer
	render.Depgraph(&buf, depsProject)
	output := buf.String()

	return textResult(output), nil, nil
}

func handleGetDiff(ctx context.Context, req *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, any, error) {
	ref := input.Ref
	if ref == "" {
		ref = "main"
	}

	absRoot, err := filepath.Abs(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	diffInfo, err := scanner.GitDiffInfo(absRoot, ref)
	if err != nil {
		return errorResult("Git diff error: " + err.Error() + "\nMake sure '" + ref + "' is a valid branch/ref"), nil, nil
	}

	if len(diffInfo.Changed) == 0 {
		return textResult("No files changed vs " + ref), nil, nil
	}

	gitCache := scanner.NewGitIgnoreCache(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitCache, nil, nil)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	files = scanner.FilterToChangedWithInfo(files, diffInfo)
	impact := scanner.AnalyzeImpact(absRoot, files)

	project := scanner.Project{
		Root:    absRoot,
		Mode:    "tree",
		Files:   files,
		DiffRef: ref,
		Impact:  impact,
	}

	var buf bytes.Buffer
	render.Tree(&buf, project)
	output := stripANSI(buf.String())

	return textResult(output), nil, nil
}

func handleFindFile(ctx context.Context, req *mcp.CallToolRequest, input FindInput) (*mcp.CallToolResult, any, error) {
	gitCache := scanner.NewGitIgnoreCache(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitCache, nil, nil)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	// Filter files matching pattern (case-insensitive)
	var matches []string
	pattern := strings.ToLower(input.Pattern)
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Path), pattern) {
			matches = append(matches, f.Path)
		}
	}

	if len(matches) == 0 {
		return textResult("No files found matching '" + input.Pattern + "'"), nil, nil
	}

	return textResult(fmt.Sprintf("Found %d files:\n%s", len(matches), strings.Join(matches, "\n"))), nil, nil
}

// EmptyInput for tools that don't need parameters
type EmptyInput struct{}

func handleStatus(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	cwd, _ := os.Getwd()
	home := os.Getenv("HOME")

	// Check active watchers
	watchersMu.RLock()
	activeWatchers := len(watchers)
	var watchedPaths []string
	for path := range watchers {
		watchedPaths = append(watchedPaths, path)
	}
	watchersMu.RUnlock()

	watchStatus := "none"
	if activeWatchers > 0 {
		watchStatus = fmt.Sprintf("%d active: %s", activeWatchers, strings.Join(watchedPaths, ", "))
	}

	return textResult(fmt.Sprintf(`codemap MCP server v2.1.0
Status: connected
Local filesystem access: enabled
Working directory: %s
Home directory: %s
Active watchers: %s

Available tools:
  list_projects    - Discover projects in a directory
  get_structure    - Project tree view
  get_dependencies - Import/function analysis
  get_diff         - Changed files vs branch
  find_file        - Search by filename
  get_importers    - Find what imports a file

Live watch tools:
  start_watch      - Start watching a project for changes
  stop_watch       - Stop watching a project
  get_activity     - See recent coding activity (hot files, edits, timeline)`, cwd, home, watchStatus)), nil, nil
}

func handleListProjects(ctx context.Context, req *mcp.CallToolRequest, input ListProjectsInput) (*mcp.CallToolResult, any, error) {
	// Expand ~ to home directory
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return errorResult("Cannot read directory: " + err.Error()), nil, nil
	}

	pattern := strings.ToLower(input.Pattern)
	var projects []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Skip hidden directories and common non-project dirs
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Filter by pattern if provided
		if pattern != "" && !strings.Contains(strings.ToLower(name), pattern) {
			continue
		}

		// Get project stats
		projectPath := filepath.Join(absPath, name)
		stats := getProjectStats(projectPath)

		projects = append(projects, fmt.Sprintf("%-30s %s", name+"/", stats))
	}

	if len(projects) == 0 {
		if pattern != "" {
			return textResult(fmt.Sprintf("No projects matching '%s' in %s", input.Pattern, absPath)), nil, nil
		}
		return textResult("No project directories found in " + absPath), nil, nil
	}

	header := fmt.Sprintf("Projects in %s", absPath)
	if pattern != "" {
		header = fmt.Sprintf("Projects matching '%s' in %s", input.Pattern, absPath)
	}

	return textResult(fmt.Sprintf("%s:\n\n%s", header, strings.Join(projects, "\n"))), nil, nil
}

// getProjectStats returns a brief summary of a project directory
// Uses the same scanner logic as the main codemap command (respects nested .gitignore files)
func getProjectStats(path string) string {
	gitCache := scanner.NewGitIgnoreCache(path)
	files, err := scanner.ScanFiles(path, gitCache, nil, nil)
	if err != nil {
		return "(error scanning)"
	}

	// Count files by language
	langCounts := make(map[string]int)
	for _, f := range files {
		lang := scanner.DetectLanguage(f.Path)
		if lang != "" {
			langCounts[lang]++
		}
	}

	// Find primary language
	var primaryLang string
	var maxCount int
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			primaryLang = lang
		}
	}

	// Check if it's a git repo
	isGit := ""
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		isGit = " [git]"
	}

	if lang, ok := scanner.LangDisplay[primaryLang]; ok {
		return fmt.Sprintf("(%d files, %s%s)", len(files), lang, isGit)
	}
	return fmt.Sprintf("(%d files%s)", len(files), isGit)
}

func handleGetImporters(ctx context.Context, req *mcp.CallToolRequest, input ImportersInput) (*mcp.CallToolResult, any, error) {
	fg, err := scanner.BuildFileGraph(input.Path)
	if err != nil {
		return errorResult("Failed to build file graph: " + err.Error()), nil, nil
	}

	importers := fg.Importers[input.File]
	if len(importers) == 0 {
		return textResult("No files import '" + input.File + "'"), nil, nil
	}

	isHub := len(importers) >= 3
	hubNote := ""
	if isHub {
		hubNote = " ⚠️ HUB FILE"
	}

	return textResult(fmt.Sprintf("%d files import '%s':%s\n%s", len(importers), input.File, hubNote, strings.Join(importers, "\n"))), nil, nil
}

// ANSI escape code pattern
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI color codes from a string
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// === WATCH HANDLERS ===

func handleStartWatch(ctx context.Context, req *mcp.CallToolRequest, input WatchInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	watchersMu.Lock()
	defer watchersMu.Unlock()

	// Check if already watching
	if _, exists := watchers[absPath]; exists {
		return textResult(fmt.Sprintf("Already watching: %s\nUse get_activity to see recent changes.", absPath)), nil, nil
	}

	// Start new watcher
	daemon, err := watch.NewDaemon(absPath, false)
	if err != nil {
		return errorResult("Failed to create watcher: " + err.Error()), nil, nil
	}

	if err := daemon.Start(); err != nil {
		return errorResult("Failed to start watcher: " + err.Error()), nil, nil
	}

	watchers[absPath] = daemon

	return textResult(fmt.Sprintf(`Live watcher started for: %s
Tracking %d files

The watcher is now running in background. I can now see:
- When you save files
- How many lines changed (+/-)
- Which files are "hot" (frequently edited)
- What's uncommitted (dirty)

Use get_activity to see what you've been working on.`, absPath, daemon.FileCount())), nil, nil
}

func handleStopWatch(ctx context.Context, req *mcp.CallToolRequest, input WatchInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	watchersMu.Lock()
	defer watchersMu.Unlock()

	daemon, exists := watchers[absPath]
	if !exists {
		return textResult("No active watcher for: " + absPath), nil, nil
	}

	// Get final stats before stopping
	events := daemon.GetEvents(0)
	daemon.Stop()
	delete(watchers, absPath)

	return textResult(fmt.Sprintf("Watcher stopped for: %s\nTotal events captured: %d", absPath, len(events))), nil, nil
}

func handleGetActivity(ctx context.Context, req *mcp.CallToolRequest, input WatchActivityInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	watchersMu.RLock()
	daemon, exists := watchers[absPath]
	watchersMu.RUnlock()

	if !exists {
		return errorResult(fmt.Sprintf("No active watcher for: %s\nUse start_watch first.", absPath)), nil, nil
	}

	minutes := input.Minutes
	if minutes <= 0 {
		minutes = 30
	}

	events := daemon.GetEvents(0)
	cutoff := time.Now().Add(-time.Duration(minutes) * time.Minute)

	// Filter to recent events
	var recent []watch.Event
	for _, e := range events {
		if e.Time.After(cutoff) {
			recent = append(recent, e)
		}
	}

	if len(recent) == 0 {
		return textResult(fmt.Sprintf(`No activity in the last %d minutes.

Watcher is running for: %s
Files tracked: %d
Total events since start: %d

The user may be:
- Reading code
- Thinking/planning
- Working in a different project
- Taking a break`, minutes, absPath, daemon.FileCount(), len(events))), nil, nil
	}

	// Aggregate by file
	type fileStats struct {
		edits    int
		netDelta int
		lastEdit time.Time
		dirty    bool
	}
	byFile := make(map[string]*fileStats)

	for _, e := range recent {
		if e.Op == "WRITE" || e.Op == "CREATE" {
			stats, exists := byFile[e.Path]
			if !exists {
				stats = &fileStats{}
				byFile[e.Path] = stats
			}
			stats.edits++
			stats.netDelta += e.Delta
			if e.Time.After(stats.lastEdit) {
				stats.lastEdit = e.Time
			}
			if e.Dirty {
				stats.dirty = true
			}
		}
	}

	// Sort files by edit count (hot files first)
	type fileSummary struct {
		path     string
		edits    int
		delta    int
		lastEdit time.Time
		dirty    bool
	}
	var summaries []fileSummary
	for path, stats := range byFile {
		summaries = append(summaries, fileSummary{
			path:     path,
			edits:    stats.edits,
			delta:    stats.netDelta,
			lastEdit: stats.lastEdit,
			dirty:    stats.dirty,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].edits > summaries[j].edits
	})

	// Build output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Activity: Last %d minutes ===\n", minutes))
	sb.WriteString(fmt.Sprintf("Project: %s\n\n", absPath))

	// Hot files
	sb.WriteString("HOT FILES (by edit count):\n")
	for i, s := range summaries {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("  ... and %d more files\n", len(summaries)-10))
			break
		}
		deltaStr := ""
		if s.delta > 0 {
			deltaStr = fmt.Sprintf("+%d", s.delta)
		} else if s.delta < 0 {
			deltaStr = fmt.Sprintf("%d", s.delta)
		}
		dirtyStr := ""
		if s.dirty {
			dirtyStr = " [uncommitted]"
		}
		sb.WriteString(fmt.Sprintf("  %-40s %2d edits  %6s lines%s\n",
			s.path, s.edits, deltaStr, dirtyStr))
	}

	// Session summary
	totalEdits := 0
	totalDelta := 0
	dirtyCount := 0
	for _, s := range summaries {
		totalEdits += s.edits
		totalDelta += s.delta
		if s.dirty {
			dirtyCount++
		}
	}

	sb.WriteString("\n")
	sb.WriteString("SESSION SUMMARY:\n")
	sb.WriteString(fmt.Sprintf("  Files touched:  %d\n", len(summaries)))
	sb.WriteString(fmt.Sprintf("  Total edits:    %d\n", totalEdits))
	deltaStr := ""
	if totalDelta >= 0 {
		deltaStr = fmt.Sprintf("+%d", totalDelta)
	} else {
		deltaStr = fmt.Sprintf("%d", totalDelta)
	}
	sb.WriteString(fmt.Sprintf("  Net line change: %s\n", deltaStr))
	sb.WriteString(fmt.Sprintf("  Uncommitted:    %d files\n", dirtyCount))

	// Recent timeline (last 5 events)
	sb.WriteString("\nRECENT TIMELINE:\n")
	start := len(recent) - 5
	if start < 0 {
		start = 0
	}
	for _, e := range recent[start:] {
		deltaStr := ""
		if e.Delta != 0 {
			if e.Delta > 0 {
				deltaStr = fmt.Sprintf(" (+%d)", e.Delta)
			} else {
				deltaStr = fmt.Sprintf(" (%d)", e.Delta)
			}
		}
		sb.WriteString(fmt.Sprintf("  %s  %-6s  %s%s\n",
			e.Time.Format("15:04:05"), e.Op, e.Path, deltaStr))
	}

	return textResult(sb.String()), nil, nil
}

// === FILE GRAPH HANDLERS ===

func handleGetHubs(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	fg, err := scanner.BuildFileGraph(input.Path)
	if err != nil {
		return errorResult("Failed to build file graph: " + err.Error()), nil, nil
	}

	hubs := fg.HubFiles()
	if len(hubs) == 0 {
		return textResult("No hub files found (no files with 3+ importers)."), nil, nil
	}

	// Sort by importer count
	sort.Slice(hubs, func(i, j int) bool {
		return len(fg.Importers[hubs[i]]) > len(fg.Importers[hubs[j]])
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Hub Files (%d total) ===\n", len(hubs)))
	sb.WriteString("These files are imported by 3+ other files. Changes here have wide impact.\n\n")

	for _, hub := range hubs {
		importers := fg.Importers[hub]
		sb.WriteString(fmt.Sprintf("  %s (%d importers)\n", hub, len(importers)))
		// Show first few importers
		for i, imp := range importers {
			if i >= 3 {
				sb.WriteString(fmt.Sprintf("      ... and %d more\n", len(importers)-3))
				break
			}
			sb.WriteString(fmt.Sprintf("      <- %s\n", imp))
		}
	}

	return textResult(sb.String()), nil, nil
}

func handleGetFileContext(ctx context.Context, req *mcp.CallToolRequest, input ImportersInput) (*mcp.CallToolResult, any, error) {
	fg, err := scanner.BuildFileGraph(input.Path)
	if err != nil {
		return errorResult("Failed to build file graph: " + err.Error()), nil, nil
	}

	file := input.File
	imports := fg.Imports[file]
	importers := fg.Importers[file]
	isHub := fg.IsHub(file)
	connected := fg.ConnectedFiles(file)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== File Context: %s ===\n\n", file))

	// Hub status
	if isHub {
		sb.WriteString(fmt.Sprintf("⚠️  HUB FILE - %d files depend on this\n", len(importers)))
		sb.WriteString("    Changes here affect many parts of the codebase.\n\n")
	}

	// What this file imports
	if len(imports) > 0 {
		sb.WriteString(fmt.Sprintf("IMPORTS (%d files):\n", len(imports)))
		for _, imp := range imports {
			sb.WriteString(fmt.Sprintf("  -> %s\n", imp))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("IMPORTS: none (leaf file)\n\n")
	}

	// What imports this file
	if len(importers) > 0 {
		sb.WriteString(fmt.Sprintf("IMPORTED BY (%d files):\n", len(importers)))
		for _, imp := range importers {
			sb.WriteString(fmt.Sprintf("  <- %s\n", imp))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("IMPORTED BY: none (entry point or unused)\n\n")
	}

	// Connected files summary
	sb.WriteString(fmt.Sprintf("CONNECTED: %d files in dependency graph\n", len(connected)))

	return textResult(sb.String()), nil, nil
}
