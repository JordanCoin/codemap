package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"codemap/cmd"
	"codemap/render"
	"codemap/scanner"
	"codemap/watch"
)

func main() {
	// Handle "hook" subcommand before flag parsing
	if len(os.Args) >= 2 && os.Args[1] == "hook" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: codemap hook <hookname>")
			fmt.Fprintln(os.Stderr, "Available hooks: session-start, pre-edit, post-edit, prompt-submit, pre-compact, session-stop")
			os.Exit(1)
		}
		hookName := os.Args[2]
		root, _ := os.Getwd()
		if len(os.Args) >= 4 {
			root = os.Args[3]
		}
		if err := cmd.RunHook(hookName, root); err != nil {
			fmt.Fprintf(os.Stderr, "Hook error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	skylineMode := flag.Bool("skyline", false, "Enable skyline visualization mode")
	animateMode := flag.Bool("animate", false, "Enable animation (use with --skyline)")
	depsMode := flag.Bool("deps", false, "Enable dependency graph mode (function/import analysis)")
	diffMode := flag.Bool("diff", false, "Only show files changed vs main (or use --ref to specify branch)")
	diffRef := flag.String("ref", "main", "Branch/ref to compare against (use with --diff)")
	jsonMode := flag.Bool("json", false, "Output JSON (for Python renderer compatibility)")
	debugMode := flag.Bool("debug", false, "Show debug info (gitignore loading, paths, etc.)")
	watchMode := flag.Bool("watch", false, "Live file watcher daemon (experimental)")
	importersMode := flag.String("importers", "", "Check file impact: who imports it, is it a hub?")
	helpMode := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpMode {
		fmt.Println("codemap - Generate a brain map of your codebase for LLM context")
		fmt.Println()
		fmt.Println("Usage: codemap [options] [path]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --help              Show this help message")
		fmt.Println("  --skyline           City skyline visualization")
		fmt.Println("  --animate           Animated skyline (use with --skyline)")
		fmt.Println("  --deps              Dependency flow map (functions & imports)")
		fmt.Println("  --diff              Only show files changed vs main")
		fmt.Println("  --ref <branch>      Branch to compare against (default: main)")
		fmt.Println("  --importers <file>  Check file impact (who imports it, hub status)")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  codemap .                       # Basic tree view")
		fmt.Println("  codemap --skyline .             # Skyline visualization")
		fmt.Println("  codemap --skyline --animate     # Animated skyline")
		fmt.Println("  codemap --deps /path/to/proj    # Dependency flow map")
		fmt.Println("  codemap --diff                  # Files changed vs main")
		fmt.Println("  codemap --diff --ref develop    # Files changed vs develop")
		fmt.Println("  codemap --importers scanner/types.go  # Check file impact")
		fmt.Println()
		fmt.Println("Hooks (for Claude Code integration):")
		fmt.Println("  codemap hook session-start      # Show project context")
		fmt.Println("  codemap hook pre-edit           # Check before editing (stdin)")
		fmt.Println("  codemap hook post-edit          # Check after editing (stdin)")
		fmt.Println("  codemap hook prompt-submit      # Parse user prompt (stdin)")
		fmt.Println("  codemap hook pre-compact        # Save state before compact")
		fmt.Println("  codemap hook session-stop       # Session summary")
		os.Exit(0)
	}

	root := flag.Arg(0)
	if root == "" {
		root = "."
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute path: %v\n", err)
		os.Exit(1)
	}

	// Initialize gitignore cache (supports nested .gitignore files)
	gitCache := scanner.NewGitIgnoreCache(root)

	if *debugMode {
		fmt.Fprintf(os.Stderr, "[debug] Root path: %s\n", root)
		fmt.Fprintf(os.Stderr, "[debug] Absolute path: %s\n", absRoot)
		fmt.Fprintf(os.Stderr, "[debug] GitIgnore cache initialized (supports nested .gitignore files)\n")
	}

	// Watch mode - start daemon
	if *watchMode {
		runWatchMode(absRoot, *debugMode)
		return
	}

	// Importers mode - check file impact
	if *importersMode != "" {
		runImportersMode(absRoot, *importersMode)
		return
	}

	// Get changed files if --diff is specified
	var diffInfo *scanner.DiffInfo
	if *diffMode {
		var err error
		diffInfo, err = scanner.GitDiffInfo(absRoot, *diffRef)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting git diff: %v\n", err)
			fmt.Fprintf(os.Stderr, "Make sure '%s' is a valid branch/ref\n", *diffRef)
			os.Exit(1)
		}
		if len(diffInfo.Changed) == 0 {
			fmt.Printf("No files changed vs %s\n", *diffRef)
			os.Exit(0)
		}
	}

	// Handle --deps mode separately
	if *depsMode {
		var changedFiles map[string]bool
		if diffInfo != nil {
			changedFiles = diffInfo.Changed
		}
		runDepsMode(absRoot, root, *jsonMode, *diffRef, changedFiles)
		return
	}

	mode := "tree"
	if *skylineMode {
		mode = "skyline"
	}

	// Scan files
	files, err := scanner.ScanFiles(root, gitCache)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking tree: %v\n", err)
		os.Exit(1)
	}

	// Filter to changed files if --diff specified (with diff info annotations)
	var impact []scanner.ImpactInfo
	var activeDiffRef string
	if diffInfo != nil {
		files = scanner.FilterToChangedWithInfo(files, diffInfo)
		impact = scanner.AnalyzeImpact(absRoot, files)
		activeDiffRef = *diffRef
	}

	project := scanner.Project{
		Root:    absRoot,
		Mode:    mode,
		Animate: *animateMode,
		Files:   files,
		DiffRef: activeDiffRef,
		Impact:  impact,
	}

	// Render or output JSON
	if *jsonMode {
		json.NewEncoder(os.Stdout).Encode(project)
	} else if *skylineMode {
		render.Skyline(project, *animateMode)
	} else {
		render.Tree(project)
	}
}

func runDepsMode(absRoot, root string, jsonMode bool, diffRef string, changedFiles map[string]bool) {
	analyses, err := scanner.ScanForDeps(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The --deps feature requires ast-grep. Install it with:")
		fmt.Fprintln(os.Stderr, "  brew install ast-grep")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// Filter to changed files if --diff specified
	if changedFiles != nil {
		analyses = scanner.FilterAnalysisToChanged(analyses, changedFiles)
	}

	depsProject := scanner.DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        analyses,
		ExternalDeps: scanner.ReadExternalDeps(absRoot),
		DiffRef:      diffRef,
	}

	// Render or output JSON
	if jsonMode {
		json.NewEncoder(os.Stdout).Encode(depsProject)
	} else {
		render.Depgraph(depsProject)
	}
}

func runWatchMode(root string, verbose bool) {
	fmt.Println("codemap watch - Live code graph daemon")
	fmt.Println()

	daemon, err := watch.NewDaemon(root, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := daemon.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting watch: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Watching: %s\n", root)
	fmt.Printf("Files tracked: %d\n", daemon.FileCount())
	fmt.Println("Event log: .codemap/events.log")
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println()
	fmt.Println("Shutting down...")
	daemon.Stop()

	// Print session summary
	events := daemon.GetEvents(0)
	fmt.Println()
	fmt.Println("Session summary:")
	fmt.Printf("  Files tracked: %d\n", daemon.FileCount())
	fmt.Printf("  Events logged: %d\n", len(events))
}

func runImportersMode(root, file string) {
	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building file graph: %v\n", err)
		os.Exit(1)
	}

	// Handle absolute paths - convert to relative
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(root, file); err == nil {
			file = rel
		}
	}

	importers := fg.Importers[file]
	if len(importers) >= 3 {
		fmt.Printf("⚠️  HUB FILE: %s\n", file)
		fmt.Printf("   Imported by %d files - changes have wide impact!\n", len(importers))
		fmt.Println()
		fmt.Println("   Dependents:")
		for i, imp := range importers {
			if i >= 5 {
				fmt.Printf("   ... and %d more\n", len(importers)-5)
				break
			}
			fmt.Printf("   • %s\n", imp)
		}
	} else if len(importers) > 0 {
		fmt.Printf("📍 File: %s\n", file)
		fmt.Printf("   Imported by %d file(s)\n", len(importers))
		for _, imp := range importers {
			fmt.Printf("   • %s\n", imp)
		}
	}

	// Also check if this file imports any hubs
	imports := fg.Imports[file]
	var hubImports []string
	for _, imp := range imports {
		if fg.IsHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}
	if len(hubImports) > 0 {
		if len(importers) == 0 {
			fmt.Printf("📍 File: %s\n", file)
		}
		fmt.Printf("   Imports %d hub(s): %s\n", len(hubImports), strings.Join(hubImports, ", "))
	}
}
