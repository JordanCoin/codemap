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

func TestNestedGitignore(t *testing.T) {
	// Create a temp directory structure with nested .gitignore files
	tmpDir := t.TempDir()

	// Create directory structure:
	// root/
	//   .gitignore (ignores *.log)
	//   main.go
	//   debug.log (should be ignored by root)
	//   subproject/
	//     .gitignore (ignores *.tmp)
	//     app.go
	//     cache.tmp (should be ignored by subproject)
	//     data.log (should be ignored by root)

	// Create root .gitignore
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create root files
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "debug.log"), []byte("debug"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create subproject directory
	subDir := filepath.Join(tmpDir, "subproject")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create subproject .gitignore
	if err := os.WriteFile(filepath.Join(subDir, ".gitignore"), []byte("*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create subproject files
	if err := os.WriteFile(filepath.Join(subDir, "app.go"), []byte("package app"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "cache.tmp"), []byte("cache"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "data.log"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Scan with GitIgnoreCache
	cache := NewGitIgnoreCache(tmpDir)
	files, err := ScanFiles(tmpDir, cache)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	// Build set of found paths
	foundPaths := make(map[string]bool)
	for _, f := range files {
		foundPaths[f.Path] = true
	}

	// Should include: main.go, subproject/app.go
	if !foundPaths["main.go"] {
		t.Error("Should include main.go")
	}
	if !foundPaths[filepath.Join("subproject", "app.go")] {
		t.Error("Should include subproject/app.go")
	}

	// Should NOT include: debug.log, subproject/cache.tmp, subproject/data.log
	if foundPaths["debug.log"] {
		t.Error("Should ignore debug.log (matched by root .gitignore)")
	}
	if foundPaths[filepath.Join("subproject", "cache.tmp")] {
		t.Error("Should ignore subproject/cache.tmp (matched by subproject .gitignore)")
	}
	if foundPaths[filepath.Join("subproject", "data.log")] {
		t.Error("Should ignore subproject/data.log (matched by root .gitignore)")
	}
}

func TestGitIgnoreCacheNil(t *testing.T) {
	// Test that ScanFiles works with nil cache (no gitignore)
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "file.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "file.log"), []byte("log"), 0644); err != nil {
		t.Fatal(err)
	}

	// Scan without gitignore cache
	files, err := ScanFiles(tmpDir, nil)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	// Should include both files
	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}
}

// TestNestedGitignoreMonorepo simulates the issue #6 scenario:
// A monorepo with multiple projects, each with their own .gitignore
func TestNestedGitignoreMonorepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Create monorepo structure:
	// root/
	//   .gitignore (*.log, .env)
	//   root.go
	//   debug.log (IGNORED by root)
	//   ProjectA/
	//     .gitignore (build/, *.cache)
	//     app.go
	//     error.log (IGNORED by root *.log cascading down)
	//     data.cache (IGNORED by ProjectA)
	//     build/binary (IGNORED by ProjectA)
	//     src/util.go
	//   ProjectB/
	//     .gitignore (dist/, *.tmp)
	//     main.py
	//     temp.tmp (IGNORED by ProjectB)
	//     dist/bundle.js (IGNORED by ProjectB)
	//     lib/helper.py
	//   ProjectC/
	//     .gitignore (output/, *.bak)
	//     index.ts
	//     backup.bak (IGNORED by ProjectC)
	//     output/result.json (IGNORED by ProjectC)
	//     config/settings.json

	// Create directories
	dirs := []string{
		"ProjectA/build", "ProjectA/src",
		"ProjectB/dist", "ProjectB/lib",
		"ProjectC/output", "ProjectC/config",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create gitignore files
	gitignores := map[string]string{
		".gitignore":          "*.log\n.env\n",
		"ProjectA/.gitignore": "build/\n*.cache\n",
		"ProjectB/.gitignore": "dist/\n*.tmp\n",
		"ProjectC/.gitignore": "output/\n*.bak\n",
	}
	for path, content := range gitignores {
		if err := os.WriteFile(filepath.Join(tmpDir, path), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create files that SHOULD be visible
	visible := []string{
		"root.go",
		"ProjectA/app.go", "ProjectA/src/util.go",
		"ProjectB/main.py", "ProjectB/lib/helper.py",
		"ProjectC/index.ts", "ProjectC/config/settings.json",
	}
	for _, f := range visible {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create files that SHOULD be ignored
	ignored := []string{
		"debug.log",                    // root *.log
		"ProjectA/error.log",           // root *.log cascades to subdirs
		"ProjectA/data.cache",          // ProjectA *.cache
		"ProjectA/build/binary",        // ProjectA build/
		"ProjectB/temp.tmp",            // ProjectB *.tmp
		"ProjectB/dist/bundle.js",      // ProjectB dist/
		"ProjectC/backup.bak",          // ProjectC *.bak
		"ProjectC/output/result.json",  // ProjectC output/
	}
	for _, f := range ignored {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("ignored"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Scan with GitIgnoreCache
	cache := NewGitIgnoreCache(tmpDir)
	files, err := ScanFiles(tmpDir, cache)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	// Build set of found paths (excluding .gitignore files for cleaner test)
	foundPaths := make(map[string]bool)
	for _, f := range files {
		foundPaths[f.Path] = true
	}

	// Verify visible files are included
	for _, f := range visible {
		if !foundPaths[f] {
			t.Errorf("Should include %s but it was ignored", f)
		}
	}

	// Verify ignored files are NOT included
	for _, f := range ignored {
		if foundPaths[f] {
			t.Errorf("Should ignore %s but it was included", f)
		}
	}

	// Verify total count (visible + 4 gitignore files)
	expectedCount := len(visible) + 4 // 4 .gitignore files
	if len(files) != expectedCount {
		t.Errorf("Expected %d files, got %d", expectedCount, len(files))
		t.Logf("Files found: %v", foundPaths)
	}
}

// TestNestedGitignoreDirectoryIgnore verifies that directory patterns
// skip the entire directory (not just pattern-match files inside)
func TestNestedGitignoreDirectoryIgnore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create: root/.gitignore with "ignored_dir/"
	// Create: root/ignored_dir/deeply/nested/file.go
	// The entire ignored_dir should be skipped

	if err := os.MkdirAll(filepath.Join(tmpDir, "ignored_dir/deeply/nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "visible_dir/sub"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored_dir/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ignored_dir/deeply/nested/file.go"), []byte("pkg"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "visible_dir/sub/file.go"), []byte("pkg"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "root.go"), []byte("pkg"), 0644); err != nil {
		t.Fatal(err)
	}

	cache := NewGitIgnoreCache(tmpDir)
	files, err := ScanFiles(tmpDir, cache)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	foundPaths := make(map[string]bool)
	for _, f := range files {
		foundPaths[f.Path] = true
	}

	// Should include visible files
	if !foundPaths["root.go"] {
		t.Error("Should include root.go")
	}
	if !foundPaths[filepath.Join("visible_dir", "sub", "file.go")] {
		t.Error("Should include visible_dir/sub/file.go")
	}

	// Should NOT include anything from ignored_dir
	if foundPaths[filepath.Join("ignored_dir", "deeply", "nested", "file.go")] {
		t.Error("Should ignore ignored_dir/deeply/nested/file.go")
	}

	// Total: .gitignore, root.go, visible_dir/sub/file.go = 3 files
	if len(files) != 3 {
		t.Errorf("Expected 3 files, got %d: %v", len(files), foundPaths)
	}
}
