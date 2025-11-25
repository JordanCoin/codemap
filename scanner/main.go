package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	ignore "github.com/sabhiram/go-gitignore"
)

// FileInfo represents a single file in the codebase.
type FileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Ext  string `json:"ext"`
}

// Project represents the root of the codebase.
type Project struct {
	Root    string     `json:"root"`
	Mode    string     `json:"mode"`
	Animate bool       `json:"animate"`
	Files   []FileInfo `json:"files"`
}

var ignoredDirs = map[string]bool{
	".git":            true,
	"node_modules":    true,
	"Pods":            true,
	"build":           true,
	"DerivedData":     true,
	".idea":           true,
	".vscode":         true,
	"__pycache__":     true,
	".DS_Store":       true,
	"venv":            true,
	".env":            true,
	".pytest_cache":   true,
	"dist":            true,
	".next":           true,
	".nuxt":           true,
	"target":          true,
	".grammar-build":  true,
	"grammars":        true,
}

func loadGitignore(root string) *ignore.GitIgnore {
	gitignorePath := filepath.Join(root, ".gitignore")

	if _, err := os.Stat(gitignorePath); err == nil {
		if gitignore, err := ignore.CompileIgnoreFile(gitignorePath); err == nil {
			return gitignore
		}
	}

	return nil
}

func main() {
	skylineMode := flag.Bool("skyline", false, "Enable skyline visualization mode")
	animateMode := flag.Bool("animate", false, "Enable animation (use with --skyline)")
	depsMode := flag.Bool("deps", false, "Enable dependency graph mode (function/import analysis)")
	flag.Parse()
	root := flag.Arg(0)
	if root == "" {
		root = "."
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute path: %v\n", err)
		os.Exit(1)
	}

	// Load .gitignore if it exists
	gitignore := loadGitignore(root)

	// Handle --deps mode separately
	if *depsMode {
		runDepsMode(absRoot, root, gitignore)
		return
	}

	mode := "tree"
	if *skylineMode {
		mode = "skyline"
	}

	project := Project{
		Root:    absRoot,
		Mode:    mode,
		Animate: *animateMode,
		Files:   []FileInfo{},
	}

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		// Skip if matched by common ignore patterns
		if info.IsDir() {
			if ignoredDirs[info.Name()] {
				return filepath.SkipDir
			}
		} else {
			if ignoredDirs[info.Name()] {
				return nil
			}
		}

		// Skip if matched by .gitignore
		if gitignore != nil && gitignore.MatchesPath(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (we only want files in the output)
		if info.IsDir() {
			return nil
		}

		project.Files = append(project.Files, FileInfo{
			Path: relPath,
			Size: info.Size(),
			Ext:  filepath.Ext(path),
		})

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking tree: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)

	if err := encoder.Encode(project); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

func runDepsMode(absRoot, root string, gitignore *ignore.GitIgnore) {
	loader := NewGrammarLoader()

	depsProject := DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        []FileAnalysis{},
		ExternalDeps: ReadExternalDeps(absRoot),
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(root, path)

		// Skip ignored dirs
		if info.IsDir() {
			if ignoredDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if ignoredDirs[info.Name()] {
			return nil
		}

		// Skip if matched by .gitignore
		if gitignore != nil && gitignore.MatchesPath(relPath) {
			return nil
		}

		// Only analyze supported languages
		if DetectLanguage(path) == "" {
			return nil
		}

		// Analyze file
		analysis, err := loader.AnalyzeFile(path)
		if err != nil || analysis == nil {
			return nil
		}

		// Use relative path in output
		analysis.Path = relPath
		depsProject.Files = append(depsProject.Files, *analysis)

		return nil
	})

	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(depsProject); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}
