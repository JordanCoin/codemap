package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoredDirs(t *testing.T) {
	// Verify common directories are in the ignored list
	expectedIgnored := []string{
		".git", "node_modules", "vendor", "__pycache__",
		".venv", "dist", "target", ".gradle",
	}

	for _, dir := range expectedIgnored {
		if !IgnoredDirs[dir] {
			t.Errorf("Expected %q to be in IgnoredDirs", dir)
		}
	}
}

func TestLoadGitignore(t *testing.T) {
	// Test loading from current directory (should have .gitignore)
	// Just ensure it doesn't panic
	_ = LoadGitignore("..")

	// Test loading from nonexistent directory
	gitignore := LoadGitignore("/nonexistent/path")
	if gitignore != nil {
		t.Error("Expected nil gitignore for nonexistent path")
	}
}

func TestScanFiles(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir := t.TempDir()

	// Create test files
	files := []string{
		"main.go",
		"README.md",
		"src/app.go",
		"src/util/helper.go",
	}

	for _, f := range files {
		path := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
	}

	// Scan the directory
	result, err := ScanFiles(tmpDir, nil)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	if len(result) != len(files) {
		t.Errorf("Expected %d files, got %d", len(files), len(result))
	}

	// Verify file info
	for _, fi := range result {
		if fi.Size == 0 {
			t.Errorf("File %s has zero size", fi.Path)
		}
	}
}

func TestScanFilesIgnoresDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files including one in an ignored directory
	if err := os.MkdirAll(filepath.Join(tmpDir, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "node_modules", "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanFiles(tmpDir, nil)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	// Should only have main.go, not node_modules/package.json
	if len(result) != 1 {
		t.Errorf("Expected 1 file (main.go), got %d files", len(result))
	}

	if len(result) > 0 && result[0].Path != "main.go" {
		t.Errorf("Expected main.go, got %s", result[0].Path)
	}
}

func TestScanFilesExtensions(t *testing.T) {
	tmpDir := t.TempDir()

	testFiles := map[string]string{
		"main.go":     ".go",
		"app.py":      ".py",
		"index.js":    ".js",
		"style.css":   ".css",
		"Makefile":    "",
		"README":      "",
		"config.json": ".json",
	}

	for name := range testFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := ScanFiles(tmpDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	extMap := make(map[string]string)
	for _, f := range result {
		extMap[filepath.Base(f.Path)] = f.Ext
	}

	for name, expectedExt := range testFiles {
		if got := extMap[name]; got != expectedExt {
			t.Errorf("File %s: expected ext %q, got %q", name, expectedExt, got)
		}
	}
}

func TestFilterToChanged(t *testing.T) {
	files := []FileInfo{
		{Path: "main.go", Size: 100},
		{Path: "util.go", Size: 200},
		{Path: "test.go", Size: 300},
	}

	changed := map[string]bool{
		"main.go": true,
		"test.go": true,
	}

	result := FilterToChanged(files, changed)

	if len(result) != 2 {
		t.Errorf("Expected 2 changed files, got %d", len(result))
	}

	// Verify correct files are included
	resultPaths := make(map[string]bool)
	for _, f := range result {
		resultPaths[f.Path] = true
	}

	if !resultPaths["main.go"] {
		t.Error("Expected main.go in results")
	}
	if !resultPaths["test.go"] {
		t.Error("Expected test.go in results")
	}
	if resultPaths["util.go"] {
		t.Error("Did not expect util.go in results")
	}
}

func TestFilterToChangedWithInfo(t *testing.T) {
	files := []FileInfo{
		{Path: "main.go", Size: 100},
		{Path: "new_file.go", Size: 50},
		{Path: "unchanged.go", Size: 200},
	}

	info := &DiffInfo{
		Changed: map[string]bool{
			"main.go":     true,
			"new_file.go": true,
		},
		Untracked: map[string]bool{
			"new_file.go": true,
		},
		Stats: map[string]DiffStat{
			"main.go":     {Added: 10, Removed: 5},
			"new_file.go": {Added: 50, Removed: 0},
		},
	}

	result := FilterToChangedWithInfo(files, info)

	if len(result) != 2 {
		t.Errorf("Expected 2 files, got %d", len(result))
	}

	// Check annotations
	for _, f := range result {
		switch f.Path {
		case "main.go":
			if f.IsNew {
				t.Error("main.go should not be marked as new")
			}
			if f.Added != 10 || f.Removed != 5 {
				t.Errorf("main.go: expected +10 -5, got +%d -%d", f.Added, f.Removed)
			}
		case "new_file.go":
			if !f.IsNew {
				t.Error("new_file.go should be marked as new")
			}
			if f.Added != 50 {
				t.Errorf("new_file.go: expected +50, got +%d", f.Added)
			}
		}
	}
}

func TestFilterAnalysisToChanged(t *testing.T) {
	analyses := []FileAnalysis{
		{Path: "main.go", Language: "go", Functions: []string{"main"}},
		{Path: "util.go", Language: "go", Functions: []string{"helper"}},
	}

	changed := map[string]bool{"main.go": true}

	result := FilterAnalysisToChanged(analyses, changed)

	if len(result) != 1 {
		t.Errorf("Expected 1 analysis, got %d", len(result))
	}

	if result[0].Path != "main.go" {
		t.Errorf("Expected main.go, got %s", result[0].Path)
	}
}
