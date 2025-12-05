package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"codemap/render"
	"codemap/scanner"
)

func main() {
	skylineMode := flag.Bool("skyline", false, "Enable skyline visualization mode")
	animateMode := flag.Bool("animate", false, "Enable animation (use with --skyline)")
	depsMode := flag.Bool("deps", false, "Enable dependency graph mode (function/import analysis)")
	diffMode := flag.Bool("diff", false, "Only show files changed vs main (or use --ref to specify branch)")
	diffRef := flag.String("ref", "main", "Branch/ref to compare against (use with --diff)")
	jsonMode := flag.Bool("json", false, "Output JSON (for Python renderer compatibility)")
	debugMode := flag.Bool("debug", false, "Show debug info (gitignore loading, paths, etc.)")
	helpMode := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpMode {
		fmt.Println("codemap - Generate a brain map of your codebase for LLM context")
		fmt.Println()
		fmt.Println("Usage: codemap [options] [path]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --help         Show this help message")
		fmt.Println("  --skyline      City skyline visualization")
		fmt.Println("  --animate      Animated skyline (use with --skyline)")
		fmt.Println("  --deps         Dependency flow map (functions & imports)")
		fmt.Println("  --diff         Only show files changed vs main")
		fmt.Println("  --ref <branch> Branch to compare against (default: main)")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  codemap .                    # Basic tree view")
		fmt.Println("  codemap --skyline .          # Skyline visualization")
		fmt.Println("  codemap --skyline --animate  # Animated skyline")
		fmt.Println("  codemap --deps /path/to/proj # Dependency flow map")
		fmt.Println("  codemap --diff               # Files changed vs main")
		fmt.Println("  codemap --diff --ref develop # Files changed vs develop")
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
