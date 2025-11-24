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
	Root  string     `json:"root"`
	Files []FileInfo `json:"files"`
}

var ignoredDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"Pods":          true,
	"build":         true,
	"DerivedData":   true,
	".idea":         true,
	".vscode":       true,
	"__pycache__":   true,
	".DS_Store":     true,
	"venv":          true,
	".env":          true,
	".pytest_cache": true,
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

	project := Project{
		Root:  absRoot,
		Files: []FileInfo{},
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
