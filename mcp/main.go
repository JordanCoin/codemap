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
	"strings"

	"codemap/render"
	"codemap/scanner"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	gitignore := scanner.LoadGitignore(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitignore)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	project := scanner.Project{
		Root:  absRoot,
		Mode:  "tree",
		Files: files,
	}

	output := captureOutput(func() {
		render.Tree(project)
	})

	return textResult(output), nil, nil
}

func handleGetDependencies(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	absRoot, err := filepath.Abs(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	gitignore := scanner.LoadGitignore(input.Path)
	loader := scanner.NewGrammarLoader()

	analyses, err := scanner.ScanForDeps(input.Path, gitignore, loader)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	depsProject := scanner.DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        analyses,
		ExternalDeps: scanner.ReadExternalDeps(absRoot),
	}

	output := captureOutput(func() {
		render.Depgraph(depsProject)
	})

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

	gitignore := scanner.LoadGitignore(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitignore)
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

	output := captureOutput(func() {
		render.Tree(project)
	})

	return textResult(output), nil, nil
}

func handleFindFile(ctx context.Context, req *mcp.CallToolRequest, input FindInput) (*mcp.CallToolResult, any, error) {
	gitignore := scanner.LoadGitignore(input.Path)
	files, err := scanner.ScanFiles(input.Path, gitignore)
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

	return textResult(fmt.Sprintf(`codemap MCP server v2.0.0
Status: connected
Local filesystem access: enabled
Working directory: %s
Home directory: %s

Available tools:
  list_projects    - Discover projects in a directory
  get_structure    - Project tree view
  get_dependencies - Import/function analysis
  get_diff         - Changed files vs branch
  find_file        - Search by filename
  get_importers    - Find what imports a file`, cwd, home)), nil, nil
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
// Uses the same scanner logic as the main codemap command (respects .gitignore)
func getProjectStats(path string) string {
	gitignore := scanner.LoadGitignore(path)
	files, err := scanner.ScanFiles(path, gitignore)
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

	if info, ok := scanner.LangDisplay[primaryLang]; ok {
		return fmt.Sprintf("(%d files, %s%s)", len(files), info.Full, isGit)
	}
	return fmt.Sprintf("(%d files%s)", len(files), isGit)
}

func handleGetImporters(ctx context.Context, req *mcp.CallToolRequest, input ImportersInput) (*mcp.CallToolResult, any, error) {
	gitignore := scanner.LoadGitignore(input.Path)
	loader := scanner.NewGrammarLoader()

	analyses, err := scanner.ScanForDeps(input.Path, gitignore, loader)
	if err != nil {
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	targetBase := filepath.Base(input.File)
	targetNoExt := strings.TrimSuffix(targetBase, filepath.Ext(targetBase))
	targetDir := filepath.Dir(input.File)

	var importers []string
	for _, a := range analyses {
		// Skip files in the same directory (same package in Go)
		if filepath.Dir(a.Path) == targetDir {
			continue
		}
		for _, imp := range a.Imports {
			impBase := filepath.Base(imp)
			impNoExt := strings.TrimSuffix(impBase, filepath.Ext(impBase))
			// Match by filename, name without ext, full path, or package/directory
			if impBase == targetBase || impNoExt == targetNoExt ||
				strings.HasSuffix(imp, input.File) ||
				strings.HasSuffix(imp, targetDir) || imp == targetDir {
				importers = append(importers, a.Path)
				break
			}
		}
	}

	if len(importers) == 0 {
		return textResult("No files import '" + input.File + "'"), nil, nil
	}

	return textResult(fmt.Sprintf("%d files import '%s':\n%s", len(importers), input.File, strings.Join(importers, "\n"))), nil, nil
}

// ANSI escape code pattern
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI color codes from a string
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// captureOutput captures stdout from a function and strips ANSI codes
func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return stripANSI(buf.String())
}
