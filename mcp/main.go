// MCP Server for codemap - provides codebase analysis tools to LLMs
package codemapmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/internal/buildinfo"
	"codemap/limits"
	"codemap/render"
	"codemap/scanner"
	"codemap/skills"
	"codemap/watch"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Global watcher registry - tracks active watchers per project
var (
	watchers   = make(map[string]*watch.Daemon)
	watchersMu sync.RWMutex

	buildHandoffForMCP    = handoff.BuildContext
	buildHandoffDetailMCP = handoff.BuildFileDetailContext
	writeLatestForMCP     = handoff.WriteLatest
)

const (
	IntegrationClaudeSetup = "claude-setup"
	IntegrationCodexSetup  = "codex-setup"
	IntegrationCodexPlugin = "codex-plugin"
)

type RuntimeOptions struct {
	ConfiguredVersion string
	Integration       string
}

func ParseRuntimeOptions(args []string) (RuntimeOptions, error) {
	var options RuntimeOptions
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfiguredVersion, "configured-version", "", "version recorded by the managed integration")
	flags.StringVar(&options.Integration, "integration", "", "managed integration kind")
	if err := flags.Parse(args); err != nil {
		return RuntimeOptions{}, err
	}
	if flags.NArg() != 0 {
		// Legacy invocations passed stray positional arguments; keep tolerating
		// them so old configured entries don't stop launching the server.
		fmt.Fprintf(os.Stderr, "Ignoring unexpected MCP arguments: %s\n", strings.Join(flags.Args(), " "))
	}
	if options.ConfiguredVersion == "" && options.Integration == "" {
		return options, nil
	}
	if options.ConfiguredVersion == "" || options.Integration == "" {
		return RuntimeOptions{}, fmt.Errorf("--configured-version and --integration must be provided together")
	}
	switch options.Integration {
	case IntegrationClaudeSetup, IntegrationCodexSetup, IntegrationCodexPlugin:
		return options, nil
	default:
		return RuntimeOptions{}, fmt.Errorf("unsupported integration %q", options.Integration)
	}
}

func (options RuntimeOptions) upgradeGuidance() string {
	if options.ConfiguredVersion == "" || options.Integration == "" || buildinfo.Equal(options.ConfiguredVersion, buildinfo.Current()) {
		return ""
	}

	var repairCommand string
	switch options.Integration {
	case IntegrationClaudeSetup:
		repairCommand = "codemap setup --agent claude"
	case IntegrationCodexSetup:
		repairCommand = "codemap setup --agent codex"
	case IntegrationCodexPlugin:
		repairCommand = "codemap plugin install"
	default:
		return ""
	}

	return fmt.Sprintf(
		"Upgrade guidance: this Codemap integration was configured for version %s, but the running server is version %s. Run `%s` to refresh it.",
		options.ConfiguredVersion,
		buildinfo.Current(),
		repairCommand,
	)
}

// Input types for tools
type StructureInput struct {
	Path  string `json:"path" jsonschema:"Path to the project directory to analyze"`
	Depth int    `json:"depth,omitempty" jsonschema:"Optional tree depth; a positive value overrides project configuration"`
}

type PathInput struct {
	Path  string `json:"path" jsonschema:"Path to the project directory to analyze"`
	Depth int    `json:"depth,omitempty" jsonschema:"Deprecated and ignored; retained for compatibility"`
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

type SkillInput struct {
	Path string `json:"path" jsonschema:"Path to the project directory"`
	Name string `json:"name" jsonschema:"Skill name to retrieve"`
}

type HandoffInput struct {
	Path   string `json:"path" jsonschema:"Path to the project directory"`
	Since  string `json:"since,omitempty" jsonschema:"Look back window for recent events (Go duration, e.g. 2h, 30m)"`
	Ref    string `json:"ref,omitempty" jsonschema:"Git base ref for diff (default: main)"`
	Latest bool   `json:"latest,omitempty" jsonschema:"Read latest saved handoff artifact instead of generating a new one"`
	Save   bool   `json:"save,omitempty" jsonschema:"When true, persist generated artifact to .codemap/handoff.latest.json"`
	JSON   bool   `json:"json,omitempty" jsonschema:"Return raw JSON output"`
	Prefix bool   `json:"prefix,omitempty" jsonschema:"Return only the stable prefix snapshot"`
	Delta  bool   `json:"delta,omitempty" jsonschema:"Return only the recent delta snapshot"`
	File   string `json:"file,omitempty" jsonschema:"Load detailed context for one changed file path from handoff delta"`
}

func NewServer(options RuntimeOptions) *mcp.Server {
	guidance := options.upgradeGuidance()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "codemap",
		Version: buildinfo.Current(),
	}, &mcp.ServerOptions{Instructions: guidance})

	// Tool: get_structure - Get project tree view
	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_structure",
		Description:  "Get the project structure as a tree view. Shows files organized by directory with language detection, file sizes, and highlights the top 5 largest source files. Use this to understand how a codebase is organized.",
		OutputSchema: mustSchemaFor[StructureOutput](),
	}, handleGetStructure)

	// Tool: get_dependencies - Get dependency graph
	dependenciesTool := &mcp.Tool{
		Name:         "get_dependencies",
		Description:  "Get the dependency flow of a project. Shows external dependencies by language, internal import chains between files, hub files (most-imported), and function counts. Use this to understand how code connects and which files are most critical.",
		OutputSchema: mustSchemaFor[scanner.DepsProject](),
	}
	mcp.AddTool(server, dependenciesTool, handleGetDependencies)

	// Tool: get_diff - Get changed files with impact analysis
	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_diff",
		Description:  "Get files changed compared to a git branch, with line counts and impact analysis showing which changed files are imported by others. Use this to understand what work has been done and what might break.",
		OutputSchema: mustSchemaFor[DiffOutput](),
	}, handleGetDiff)

	// Tool: find_file - Find files by pattern
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_file",
		Description: "Find files in a project matching a name pattern. Returns file paths with their sizes and languages.",
	}, handleFindFile)

	// Tool: get_importers - Find what imports a file
	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_importers",
		Description:  "Find all files that import/depend on a specific file. Use this to understand the impact of changing a file.",
		OutputSchema: mustSchemaFor[ImportersOutput](),
	}, handleGetImporters)

	// Tool: status - Verify MCP connection
	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Check codemap MCP server status. Returns version and confirms local filesystem access is available.",
	}, statusHandler(guidance))

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

	// Tool: get_handoff - Build/read cross-agent handoff artifact
	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_handoff",
		Description:  "Build or read a layered handoff artifact for agent switching. Prefix = stable project context, delta = recent work. Supports lazy per-file detail via file=<path>. Set save=true to persist generated artifacts.",
		OutputSchema: mustSchemaFor[HandoffOutput](),
	}, handleGetHandoff)

	// Tool: get_working_set - Get current session's working set
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_working_set",
		Description: "Get the current session's working set: files actively being edited, ranked by edit frequency. Shows edit count, net line delta, hub status, and importer count per file. Use this to understand what the user is actively working on right now.",
	}, handleGetWorkingSet)

	// === SKILLS TOOLS ===

	// Tool: list_skills - List available skills (metadata only)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_skills",
		Description: "List all available skills with their names, descriptions, keywords, and source (builtin/project/global). Returns metadata only — use get_skill for full instructions.",
	}, handleListSkills)

	// Tool: get_skill - Get full skill instructions
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_skill",
		Description: "Load the full instructions for a specific skill by name. Returns the complete markdown body with step-by-step guidance. Use after list_skills to get detailed guidance for a task.",
	}, handleGetSkill)

	return server
}

func Run(ctx context.Context, options RuntimeOptions) error {
	return NewServer(options).Run(ctx, &mcp.StdioTransport{})
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

func mustSchemaFor[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("infer MCP schema: %v", err))
	}
	return schema
}

func traversalRoot(path string) (string, *mcp.CallToolResult) {
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return "", errorResult("Invalid path: " + err.Error())
	}
	info, err := os.Stat(absRoot)
	if err != nil || !info.IsDir() {
		return "", errorResult("Invalid project path: path is not an accessible directory")
	}
	return absRoot, nil
}

func cancellationResult(ctx context.Context, operation string) *mcp.CallToolResult {
	if err := ctx.Err(); err != nil {
		return errorResult(operation + " cancelled: " + err.Error())
	}
	return nil
}

func handleGetStructure(ctx context.Context, req *mcp.CallToolRequest, input StructureInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Structure scan"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}

	gitCache := scanner.NewGitIgnoreCache(absRoot)
	files, err := scanner.ScanConfiguredFiles(ctx, absRoot, gitCache)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Structure scan"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Scan error: " + err.Error()), nil, nil
	}
	fileCount := len(files)
	depth := input.Depth
	if depth <= 0 {
		depth = config.Load(absRoot).Depth
	}
	if depth <= 0 {
		depth = limits.AdaptiveDepth(fileCount)
	}

	project := scanner.Project{
		Root:  absRoot,
		Mode:  "tree",
		Depth: depth,
		Files: files,
	}

	var buf bytes.Buffer
	render.Tree(&buf, project)
	output := stripANSI(buf.String())

	// IMPORTANT: MCP tool output contributes directly to context window usage.
	if len(output) > limits.MaxStructureOutputBytes {
		output = limits.TruncateAtLineBoundary(
			output,
			limits.MaxStructureOutputBytes,
			fmt.Sprintf("\n\n... (truncated - repo has %d files, use `codemap --depth N` for full tree)\n", fileCount),
		)
	}

	// Add hub file summary. Prefer daemon cache; avoid expensive graph builds on
	// very large repos.
	var hubs []string
	var importers map[string][]string
	if state := watch.ReadState(absRoot); state != nil && (len(state.Importers) > 0 || len(state.Hubs) > 0) {
		hubs = append(hubs, state.Hubs...)
		importers = state.Importers
	} else if fileCount <= limits.LargeRepoFileCount {
		fg, err := scanner.BuildFileGraph(ctx, absRoot, scanner.ConfiguredFilters(absRoot))
		if cancelled := cancellationResult(ctx, "Structure graph analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
		if err == nil {
			hubs = fg.HubFiles()
			importers = fg.Importers
		}
	} else {
		output += "\nℹ️  Hub analysis skipped for large repo (request get_hubs for explicit analysis)\n"
	}

	if len(hubs) > 0 {
		output += "\n⚠️  HUB FILES (high-impact, 3+ dependents):\n"
		sort.Slice(hubs, func(i, j int) bool {
			return len(importers[hubs[i]]) > len(importers[hubs[j]])
		})
		for i, hub := range hubs {
			if i >= 5 {
				output += fmt.Sprintf("   ... and %d more hubs\n", len(hubs)-5)
				break
			}
			output += fmt.Sprintf("   %s (%d importers)\n", hub, len(importers[hub]))
		}
	}

	return textResult(output), newStructureOutput(project, hubs), nil
}

func handleGetDependencies(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Dependency scan"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}

	filters := scanner.ConfiguredFilters(absRoot)
	outcome, err := scanner.ScanForDeps(ctx, absRoot, filters)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Dependency scan"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Scan error: " + err.Error()), nil, nil
	}
	externalDeps, err := scanner.ReadExternalDeps(ctx, absRoot, scanner.MCPManifestByteBudget)
	if err != nil {
		if cancelled := cancellationResult(ctx, "External dependency scan"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("External dependency scan error: " + err.Error()), nil, nil
	}

	graph, graphErr := scanner.BuildFileGraphFromOutcome(ctx, absRoot, outcome, filters)
	if graphErr != nil {
		if cancelled := cancellationResult(ctx, "Dependency graph"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Dependency graph error: " + graphErr.Error()), nil, nil
	}
	coverage := scanner.CoverageFromSources(graph.Coverage.Sources)
	depsProject := scanner.NewDepsProjectWithCoverage(absRoot, outcome.Analyses, externalDeps, "", coverage)

	var buf bytes.Buffer
	render.Depgraph(ctx, &buf, depsProject)
	output := buf.String()

	return textResult(output), depsProject, nil
}

func handleGetDiff(ctx context.Context, req *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Diff analysis"); cancelled != nil {
		return cancelled, nil, nil
	}
	ref := input.Ref
	if ref == "" {
		ref = "main"
	}

	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}

	diffInfo, err := scanner.GitDiffInfo(ctx, absRoot, ref)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Diff analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Git diff error: " + err.Error() + "\nMake sure '" + ref + "' is a valid branch/ref"), nil, nil
	}

	if len(diffInfo.Changed) == 0 {
		return textResult("No files changed vs " + ref), newDiffOutput("empty", ref, scanner.Project{Root: absRoot, Mode: "tree", DiffRef: ref}), nil
	}

	gitCache := scanner.NewGitIgnoreCache(absRoot)
	files, err := scanner.ScanConfiguredFiles(ctx, absRoot, gitCache)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Diff scan"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	files = scanner.FilterToChangedWithInfo(files, diffInfo)
	impact, err := scanner.AnalyzeImpact(ctx, absRoot, files)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Impact analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Impact scan error: " + err.Error()), nil, nil
	}

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

	return textResult(output), newDiffOutput("diff", ref, project), nil
}

func handleFindFile(ctx context.Context, req *mcp.CallToolRequest, input FindInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "File search"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}
	gitCache := scanner.NewGitIgnoreCache(absRoot)
	files, err := scanner.ScanFiles(ctx, absRoot, gitCache, nil, nil)
	if err != nil {
		if cancelled := cancellationResult(ctx, "File search"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Scan error: " + err.Error()), nil, nil
	}

	// Filter files matching pattern (case-insensitive)
	matches, filteredMatches, hintsEnabled := findConfiguredMatches(absRoot, input.Pattern, files)

	if len(matches) == 0 {
		if hintsEnabled && len(filteredMatches) > 0 {
			return textResult(formatOnlyFilterHint(input.Pattern, filteredMatches)), nil, nil
		}
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

	return textResult(fmt.Sprintf(`codemap MCP server v%s
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
  get_handoff      - Build/read cross-agent handoff summary

Live watch tools:
  start_watch      - Start watching a project for changes
  stop_watch       - Stop watching a project
  get_activity     - See recent coding activity (hot files, edits, timeline)`, buildinfo.Current(), cwd, home, watchStatus)), nil, nil
}

func statusHandler(guidance string) func(context.Context, *mcp.CallToolRequest, EmptyInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
		result, output, err := handleStatus(ctx, req, input)
		if err != nil || guidance == "" {
			return result, output, err
		}
		content, ok := result.Content[0].(*mcp.TextContent)
		if ok {
			content.Text += "\n\n" + guidance
		}
		return result, output, nil
	}
}

func handleListProjects(ctx context.Context, req *mcp.CallToolRequest, input ListProjectsInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Project discovery"); cancelled != nil {
		return cancelled, nil, nil
	}
	// Expand ~ to home directory
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, invalid := traversalRoot(path)
	if invalid != nil {
		return invalid, nil, nil
	}

	dir, err := os.Open(absPath)
	if err != nil {
		return errorResult("Cannot read directory: " + err.Error()), nil, nil
	}
	defer dir.Close()

	pattern := strings.ToLower(input.Pattern)
	candidates, examined, truncatedByResults, truncatedByExamined, err := selectProjectCandidates(ctx, dir, pattern)
	if err != nil {
		if cancelled := cancellationResult(ctx, "Project discovery"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Cannot read directory: " + err.Error()), nil, nil
	}

	projects := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if cancelled := cancellationResult(ctx, "Project discovery"); cancelled != nil {
			return cancelled, nil, nil
		}
		projectPath := filepath.Join(absPath, name)
		stats, scanErr := getProjectStatsContext(ctx, projectPath)
		if scanErr != nil {
			if cancelled := cancellationResult(ctx, "Project discovery"); cancelled != nil {
				return cancelled, nil, nil
			}
			stats = "(error scanning)"
		}
		projects = append(projects, fmt.Sprintf("%-30s %s", name+"/", stats))
	}

	if truncatedByExamined && len(candidates) == 0 {
		return textResult(projectDiscoveryExaminationCap(input.Pattern, absPath, examined)), nil, nil
	}
	if len(projects) == 0 {
		truncation := projectDiscoveryTruncation(truncatedByResults, truncatedByExamined, examined)
		if pattern != "" {
			return textResult(fmt.Sprintf("No projects matching '%s' in %s%s", input.Pattern, absPath, truncation)), nil, nil
		}
		return textResult("No project directories found in " + absPath + truncation), nil, nil
	}

	header := fmt.Sprintf("Projects in %s", absPath)
	if pattern != "" {
		header = fmt.Sprintf("Projects matching '%s' in %s", input.Pattern, absPath)
	}

	output := fmt.Sprintf("%s:\n\n%s", header, strings.Join(projects, "\n"))
	output += projectDiscoveryTruncation(truncatedByResults, truncatedByExamined, examined)
	return textResult(output), nil, nil
}

func projectDiscoveryExaminationCap(pattern, path string, examined int) string {
	if pattern != "" {
		return fmt.Sprintf("Project discovery matching '%s' in %s was truncated after examining %d entries; results are unavailable because the directory exceeds the examination cap.", pattern, path, examined)
	}
	return fmt.Sprintf("Project discovery in %s was truncated after examining %d entries; results are unavailable because the directory exceeds the examination cap.", path, examined)
}

const (
	maxListedProjects         = 50
	maxExaminedProjectEntries = 200
	projectDiscoveryBatchSize = 32
)

type projectDirectoryReader interface {
	ReadDir(int) ([]os.DirEntry, error)
}

// selectProjectCandidates deterministically selects the lexicographically
// first result-cap entries within the bounded examination window.
func selectProjectCandidates(ctx context.Context, dir projectDirectoryReader, pattern string) ([]string, int, bool, bool, error) {
	candidates := make([]string, 0, maxListedProjects+1)
	examined := 0
	truncatedByExamined := false
	discoveryDone := false

	for !discoveryDone {
		if err := ctx.Err(); err != nil {
			return nil, examined, false, truncatedByExamined, err
		}
		entries, readErr := dir.ReadDir(projectDiscoveryBatchSize)
		for _, entry := range entries {
			if examined >= maxExaminedProjectEntries {
				truncatedByExamined = true
				discoveryDone = true
				break
			}
			examined++
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if pattern != "" && !strings.Contains(strings.ToLower(name), pattern) {
				continue
			}
			candidates = append(candidates, name)
		}
		if readErr == io.EOF {
			discoveryDone = true
		} else if readErr != nil {
			return nil, examined, false, truncatedByExamined, readErr
		}
	}

	if truncatedByExamined {
		// The bounded prefix is filesystem-order dependent. Do not emit a
		// partial selection when the examination cap prevents observing the
		// complete directory; only the stable truncation notice is returned.
		return []string{}, examined, false, true, ctx.Err()
	}
	sort.Strings(candidates)
	truncatedByResults := len(candidates) > maxListedProjects
	if truncatedByResults {
		candidates = candidates[:maxListedProjects]
	}
	return candidates, examined, truncatedByResults, truncatedByExamined, ctx.Err()
}

func projectDiscoveryTruncation(byResults, byExamined bool, examined int) string {
	switch {
	case byResults && byExamined:
		return fmt.Sprintf("\n... truncated after %d projects and examining %d entries", maxListedProjects, examined)
	case byResults:
		return fmt.Sprintf("\n... truncated after %d projects", maxListedProjects)
	case byExamined:
		return fmt.Sprintf("\n... truncated after examining %d entries", examined)
	default:
		return ""
	}
}

// getProjectStats returns a brief summary of a project directory
// Uses the same scanner logic as the main codemap command (respects nested .gitignore files)
func getProjectStats(path string) string {
	stats, _ := getProjectStatsContext(context.Background(), path)
	return stats
}

func getProjectStatsContext(ctx context.Context, path string) (string, error) {
	gitCache := scanner.NewGitIgnoreCache(path)
	files, err := scanner.ScanConfiguredFiles(ctx, path, gitCache)
	if err != nil {
		return "(error scanning)", err
	}

	// Count files by language
	langCounts := make(map[string]int)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return "(error scanning)", err
		}
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
		return fmt.Sprintf("(%d files, %s%s)", len(files), lang, isGit), nil
	}
	return fmt.Sprintf("(%d files%s)", len(files), isGit), ctx.Err()
}

func handleGetImporters(ctx context.Context, req *mcp.CallToolRequest, input ImportersInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Importer analysis"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}
	fg, err := scanner.BuildFileGraph(ctx, absRoot, scanner.ConfiguredFilters(absRoot))
	if err != nil {
		if cancelled := cancellationResult(ctx, "Importer analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Failed to build file graph: " + err.Error()), nil, nil
	}

	file := normalizeImporterFile(absRoot, input.File)
	importers := fg.Importers[file]
	structured := newImportersOutput(absRoot, file, fg)
	if len(importers) == 0 {
		return textResult("No files import '" + file + "'" + mcpCoverageText(fg)), structured, nil
	}

	isHub := scanner.CountHubImporters(importers) >= scanner.HubThreshold
	hubNote := ""
	if isHub {
		hubNote = " ⚠️ HUB FILE"
	}

	return textResult(fmt.Sprintf("%d files import '%s':%s\n%s%s", len(importers), file, hubNote, strings.Join(importers, "\n"), mcpCoverageText(fg))), structured, nil
}

func normalizeImporterFile(root, file string) string {
	file = filepath.FromSlash(file)
	if filepath.IsAbs(file) {
		if relative, err := filepath.Rel(root, file); err == nil {
			file = relative
		}
	}
	return filepath.Clean(file)
}

func mcpCoverageText(fg *scanner.FileGraph) string {
	if fg == nil || fg.Coverage.Status == "" {
		return ""
	}
	if len(fg.Coverage.Notes) == 0 {
		return "\n\nCoverage: " + string(fg.Coverage.Status)
	}
	return "\n\nCoverage: " + string(fg.Coverage.Status) + " — " + strings.Join(fg.Coverage.Notes, "; ")
}

func handleGetHandoff(ctx context.Context, req *mcp.CallToolRequest, input HandoffInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "Handoff generation"); cancelled != nil {
		return cancelled, nil, nil
	}
	if input.Prefix && input.Delta {
		return errorResult("prefix and delta options are mutually exclusive"), nil, nil
	}

	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}

	var artifact *handoff.Artifact
	var err error
	if input.Latest {
		artifact, err = handoff.ReadLatest(absRoot)
		if err != nil {
			return errorResult("Failed to read handoff: " + err.Error()), nil, nil
		}
		if artifact == nil {
			message := "No saved handoff found at " + handoff.LatestPath(absRoot)
			return textResult(message), HandoffOutput{Kind: "not_found", Message: message}, nil
		}
	} else {
		baseRef := input.Ref
		if baseRef == "" {
			baseRef = handoff.DefaultBaseRef
		}
		since := handoff.DefaultSince
		if input.Since != "" {
			since, err = time.ParseDuration(input.Since)
			if err != nil {
				return errorResult("Invalid since duration: " + err.Error()), nil, nil
			}
			if since <= 0 {
				return errorResult("Invalid since duration: must be > 0"), nil, nil
			}
		}

		artifact, err = buildHandoffForMCP(ctx, absRoot, handoff.BuildOptions{
			BaseRef: baseRef,
			Since:   since,
		})
		if err != nil {
			if cancelled := cancellationResult(ctx, "Handoff generation"); cancelled != nil {
				return cancelled, nil, nil
			}
			return errorResult("Failed to build handoff: " + err.Error()), nil, nil
		}
		if input.Save {
			// Persistence commit point: cancellation observed before WriteLatest
			// prevents all writes. Once WriteLatest begins, its established
			// non-contextual multi-file semantics remain unchanged.
			if cancelled := cancellationResult(ctx, "Handoff generation"); cancelled != nil {
				return cancelled, nil, nil
			}
			if err := writeLatestForMCP(absRoot, artifact); err != nil {
				return errorResult("Failed to save handoff: " + err.Error()), nil, nil
			}
		}
	}

	if input.File != "" {
		detail, err := buildHandoffDetailMCP(ctx, absRoot, artifact, input.File, nil)
		if err != nil {
			if cancelled := cancellationResult(ctx, "Handoff detail generation"); cancelled != nil {
				return cancelled, nil, nil
			}
			return errorResult("Failed to load handoff detail: " + err.Error()), nil, nil
		}
		structured := boundedFileDetail(detail)
		if input.JSON {
			data, err := json.MarshalIndent(detail, "", "  ")
			if err != nil {
				return errorResult("Failed to serialize handoff detail: " + err.Error()), nil, nil
			}
			return textResult(string(data)), structured, nil
		}
		out := handoff.RenderFileDetailMarkdown(detail)
		out = limits.TruncateAtLineBoundary(out, limits.MaxHandoffDetailBytes, "\n\n... (handoff detail truncated)\n")
		return textResult(out), structured, nil
	}

	kind := "artifact"
	if input.Prefix {
		kind = "prefix"
	} else if input.Delta {
		kind = "delta"
	}
	structured := newHandoffOutput(kind, artifact)

	if input.JSON {
		var payload any = artifact
		if input.Prefix {
			payload = &artifact.Prefix
		} else if input.Delta {
			payload = &artifact.Delta
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return errorResult("Failed to serialize handoff: " + err.Error()), nil, nil
		}
		return textResult(string(data)), structured, nil
	}

	var out string
	switch {
	case input.Prefix:
		out = handoff.RenderPrefixMarkdown(artifact.Prefix)
	case input.Delta:
		out = handoff.RenderDeltaMarkdown(artifact.Delta)
	default:
		out = handoff.RenderMarkdown(artifact)
	}
	out = limits.TruncateAtLineBoundary(out, limits.MaxHandoffMarkdownBytes, "\n\n... (handoff output truncated)\n")
	return textResult(out), structured, nil
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
	if cancelled := cancellationResult(ctx, "Hub analysis"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}
	fg, err := scanner.BuildFileGraph(ctx, absRoot, scanner.ConfiguredFilters(absRoot))
	if err != nil {
		if cancelled := cancellationResult(ctx, "Hub analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
		return errorResult("Failed to build file graph: " + err.Error()), nil, nil
	}

	hubs := fg.HubFiles()
	if len(hubs) == 0 {
		return textResult("No hub files found (no files with 3+ importers)." + mcpCoverageText(fg)), nil, nil
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

	return textResult(sb.String() + mcpCoverageText(fg)), nil, nil
}

func handleGetFileContext(ctx context.Context, req *mcp.CallToolRequest, input ImportersInput) (*mcp.CallToolResult, any, error) {
	if cancelled := cancellationResult(ctx, "File context analysis"); cancelled != nil {
		return cancelled, nil, nil
	}
	absRoot, invalid := traversalRoot(input.Path)
	if invalid != nil {
		return invalid, nil, nil
	}
	fg, err := scanner.BuildFileGraph(ctx, absRoot, scanner.ConfiguredFilters(absRoot))
	if err != nil {
		if cancelled := cancellationResult(ctx, "File context analysis"); cancelled != nil {
			return cancelled, nil, nil
		}
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
	sb.WriteString(mcpCoverageText(fg))

	return textResult(sb.String()), nil, nil
}

// handleGetWorkingSet returns the current session's working set
func handleGetWorkingSet(ctx context.Context, req *mcp.CallToolRequest, input WatchInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	// Try daemon state first (includes working set)
	state := watch.ReadState(absPath)
	if state == nil || state.WorkingSet == nil || state.WorkingSet.Size() == 0 {
		return textResult("No working set available.\n\nThe working set tracks files edited during the current session.\nStart a watch daemon first: use start_watch tool."), nil, nil
	}

	ws := state.WorkingSet
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("WORKING SET: %d files", ws.Size()))
	if ws.HubCount() > 0 {
		sb.WriteString(fmt.Sprintf(" (%d hubs)", ws.HubCount()))
	}
	sb.WriteString(fmt.Sprintf("\nSession started: %s\n\n", ws.StartedAt.Format("15:04:05")))

	hot := ws.HotFiles(10)
	for i, wf := range hot {
		hubStr := ""
		if wf.IsHub {
			hubStr = fmt.Sprintf(" [HUB, %d importers]", wf.Importers)
		}
		sb.WriteString(fmt.Sprintf("%d. %s — %d edits, %+d lines%s\n",
			i+1, wf.Path, wf.EditCount, wf.NetDelta, hubStr))
	}

	return textResult(sb.String()), nil, nil
}

// handleListSkills returns metadata for all available skills
func handleListSkills(ctx context.Context, req *mcp.CallToolRequest, input PathInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	idx, err := skills.LoadSkills(absPath)
	if err != nil {
		return errorResult("Error loading skills: " + err.Error()), nil, nil
	}

	if len(idx.Skills) == 0 {
		return textResult("No skills available.\n\nCreate custom skills in .codemap/skills/ using 'codemap skill init'."), nil, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("AVAILABLE SKILLS: %d\n\n", len(idx.Skills)))
	for _, s := range idx.Skills {
		sb.WriteString(fmt.Sprintf("%-20s [%s] %s\n", s.Meta.Name, s.Source, s.Meta.Description))
		if len(s.Meta.Keywords) > 0 {
			sb.WriteString(fmt.Sprintf("  keywords: %s\n", strings.Join(s.Meta.Keywords, ", ")))
		}
		if len(s.Meta.Languages) > 0 {
			sb.WriteString(fmt.Sprintf("  languages: %s\n", strings.Join(s.Meta.Languages, ", ")))
		}
	}
	return textResult(sb.String()), nil, nil
}

// handleGetSkill returns the full body of a specific skill
func handleGetSkill(ctx context.Context, req *mcp.CallToolRequest, input SkillInput) (*mcp.CallToolResult, any, error) {
	path := input.Path
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}

	idx, err := skills.LoadSkills(absPath)
	if err != nil {
		return errorResult("Error loading skills: " + err.Error()), nil, nil
	}

	skill, ok := idx.ByName[input.Name]
	if !ok {
		available := make([]string, 0, len(idx.Skills))
		for _, s := range idx.Skills {
			available = append(available, s.Meta.Name)
		}
		return errorResult(fmt.Sprintf("Skill %q not found.\nAvailable: %s", input.Name, strings.Join(available, ", "))), nil, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", skill.Meta.Name))
	sb.WriteString(fmt.Sprintf("Source: %s | Priority: %d\n", skill.Source, skill.Meta.Priority))
	if skill.Meta.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", skill.Meta.Description))
	}
	sb.WriteString("\n")
	sb.WriteString(skill.Body)
	return textResult(sb.String()), nil, nil
}
